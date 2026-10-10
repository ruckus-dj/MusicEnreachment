//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMatchingEvaluationPostgresCompositionAndProviderFence(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	configurationIdentity := uuid.NewString()
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES
		('musicbrainz_mode','self-hosted'),('musicbrainz_base_url','https://musicbrainz.example/ws/2'),
		('musicbrainz_config_identity',?)
		ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, configurationIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE provider_source SET configuration_identity=? WHERE id=?`, configurationIdentity, fixture.source).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	const recordingQuery = "recording-search:0:1:fixture"
	const recordingKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	ticket, err := cache.BeginRefresh(ctx, fixture.source, recordingQuery, configurationIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(),
		Payload: json.RawMessage(`{"recordings":[{"id":"` + recordingKey + `","score":100}]}`), Graph: fixture.graph,
	}); err != nil {
		t.Fatal(err)
	}

	groupingRepo := persistence.NewIncomingGroupingRepository(db)
	initialCaptures, err := groupingRepo.CurrentCaptures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range initialCaptures {
		if capture.MetadataWinner == nil {
			t.Fatalf("fixture capture has no metadata result: %#v", capture)
		}
		album := "Fixture Album One"
		if capture.WorkID == fixture.workB {
			album = "Fixture Album Two"
		}
		tags, _ := json.Marshal(map[string][]string{"ALBUM": {album}, "ARTIST": {"Fixture Artist"}, "DATE": {"2020"}})
		if _, err := db.NewRaw(`UPDATE media_metadata_result SET observed_tags=?::jsonb WHERE id=?`, string(tags), *capture.MetadataWinner).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	groupsService := service.NewIncomingGroups(groupingRepo)
	if err := groupsService.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	grouping, err := groupsService.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouping.Groups) != 2 {
		t.Fatalf("fixture groups = %#v, want two independently scoped groups", grouping.Groups)
	}
	captures, err := groupingRepo.CurrentCaptures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secondRelease := fixture.releaseKeys[1]
	for _, key := range fixture.releaseKeys {
		if key[0] == 'e' {
			secondRelease = key
		}
	}
	secondReleaseQuery := "release-lookup:" + secondRelease
	missingRecordingQuery := "recording-search:0:1:not-loaded"
	searchKeys := []string{fixture.cacheKey, recordingQuery, secondReleaseQuery, missingRecordingQuery}
	providerReleaseKeys := []string{fixture.releaseKeys[0], fixture.releaseKeys[1]}
	provider, err := cache.GetMusicBrainzMatchingSnapshot(ctx, "https://musicbrainz.example/ws/2", configurationIdentity, providerReleaseKeys, searchKeys)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.Responses[missingRecordingQuery]; !ok || provider.Responses[missingRecordingQuery] != nil {
		t.Fatalf("requested cache-missing query was not retained distinctly: %#v", provider.Responses[missingRecordingQuery])
	}
	request := service.MatchingEvaluationSnapshot{
		Grouping: grouping, Captures: captures, Provider: provider, RequestedGroupIDs: []uuid.UUID{grouping.Groups[0].ID, grouping.Groups[1].ID},
		ReleaseKeysByFile: make(map[uuid.UUID][]string), SearchKeysByFile: make(map[uuid.UUID]service.MatchingSearchKeys),
	}
	scope := service.MatchingEvaluationQueryScope{GroupIDs: request.RequestedGroupIDs, ReleaseKeysByFile: request.ReleaseKeysByFile, SearchKeysByFile: request.SearchKeysByFile}
	for _, group := range grouping.Groups {
		for _, member := range group.Members {
			if member == fixture.memberA {
				request.ReleaseKeysByFile[member] = []string{fixture.releaseKeys[0]}
				request.SearchKeysByFile[member] = service.MatchingSearchKeys{Release: fixture.cacheKey, Recording: recordingQuery}
			} else {
				request.ReleaseKeysByFile[member] = []string{secondRelease}
				request.SearchKeysByFile[member] = service.MatchingSearchKeys{Release: secondReleaseQuery, Recording: missingRecordingQuery}
			}
		}
	}
	// The grouping is split by captured tags, while each per-file lookup uses one
	// exact release key and one exact recording key. The provider fence remains
	// the union across both disjoint groups.
	results, err := service.EvaluateMatchingSnapshot(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(results[0].Result.Groups) != 1 || len(results[1].Result.Groups) != 1 {
		t.Fatalf("evaluated group snapshot = %#v", results)
	}
	if len(results[0].Result.Provider.ReleaseKeys) == len(providerReleaseKeys) || len(results[1].Result.Provider.ReleaseKeys) == len(providerReleaseKeys) {
		t.Fatal("per-group scoring fences should be scoped; the common publication fence is checked below")
	}
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	registry := settings.New(persistence.NewSettingsRepository(db), nil)
	evaluator := service.NewMatchingEvaluationService(groupsService, groupingRepo, cache, repository, registry)
	serviceResults, err := evaluator.EvaluateAndPersist(ctx, scope)
	if err != nil {
		t.Fatalf("evaluate and persist cache-only snapshot with current fences: %v", err)
	}
	if len(serviceResults) != 2 || serviceResults[0].GroupID == serviceResults[1].GroupID {
		t.Fatalf("service returned a misleading requested group scope: %#v", serviceResults)
	}
	priorHeaders := make(map[uuid.UUID]*persistence.IncomingMatchEvaluationHeader)
	for _, group := range grouping.Groups {
		header, _, err := repository.Get(ctx, group.ID)
		if err != nil || header == nil {
			t.Fatalf("group %s was not atomically published: %#v err %v", group.ID, header, err)
		}
		priorHeaders[group.ID] = header
	}
	firstGroup := results[0].Result
	firstGroup.Groups = append(firstGroup.Groups, results[1].Result.Groups[0])
	firstGroup.Provider.ReleaseKeys = providerReleaseKeys
	firstGroup.Provider.CacheRevisions = map[string]*string{}
	for key, response := range provider.Responses {
		if response == nil {
			firstGroup.Provider.CacheRevisions[key] = nil
		} else {
			firstGroup.Provider.CacheRevisions[key] = response.Revision
		}
	}
	queryKeys := []string{recordingQuery, fixture.cacheKey, secondReleaseQuery, missingRecordingQuery}
	// Persistence only requires that CacheKey selects an entry from the full
	// query-revision map; use the sorted first exact request key.
	sort.Strings(queryKeys)
	firstGroup.Provider.CacheKey = queryKeys[0]
	firstGroup.Provider.CacheRevision = firstGroup.Provider.CacheRevisions[queryKeys[0]]
	expected := make(map[uuid.UUID]*string, len(priorHeaders))
	for groupID, header := range priorHeaders {
		revision := header.EvaluationRevision
		expected[groupID] = &revision
	}
	staleExpected := make(map[uuid.UUID]*string, len(expected))
	for groupID, token := range expected {
		staleExpected[groupID] = token
	}
	staleToken := "stale-evaluation-token"
	staleExpected[grouping.Groups[1].ID] = &staleToken
	if err := repository.ReplaceIfCurrent(ctx, firstGroup, staleExpected); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale second-group CAS = %v, want conflict", err)
	}
	for groupID, prior := range priorHeaders {
		current, _, err := repository.Get(ctx, groupID)
		if err != nil || current == nil || current.EvaluationRevision != prior.EvaluationRevision {
			t.Fatalf("stale second-group CAS changed group %s: %#v err %v", groupID, current, err)
		}
	}

	// A query refresh after evaluation must fail the repository's source/cache
	// fences before the evaluation CAS can replace the accepted result.
	refresh, err := cache.BeginRefresh(ctx, fixture.source, recordingQuery, configurationIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: refresh, FetchedAt: time.Now().UTC(),
		Payload: json.RawMessage(`{"recordings":[{"id":"` + recordingKey + `","score":0}]}`), Graph: fixture.graph,
	}); err != nil {
		t.Fatal(err)
	}
	groupID := grouping.Groups[0].ID
	revision := priorHeaders[groupID].EvaluationRevision
	if err := repository.ReplaceIfCurrent(ctx, firstGroup, expected); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale provider snapshot CAS = %v, want conflict", err)
	}
	for groupID, prior := range priorHeaders {
		current, _, err := repository.Get(ctx, groupID)
		if err != nil || current == nil || current.EvaluationRevision != prior.EvaluationRevision || current.EvaluationRevision != revision && groupID == grouping.Groups[0].ID {
			t.Fatalf("stale provider evaluation changed accepted result: %#v err %v", current, err)
		}
	}
	if _, err := db.NewRaw(`UPDATE source_location SET size_bytes=size_bytes+1 WHERE id=(SELECT current_location_id FROM source_analysis_work WHERE id=?)`, fixture.workA).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceIfCurrent(ctx, firstGroup, expected); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale source capture CAS = %v, want conflict", err)
	}
	for groupID, prior := range priorHeaders {
		current, _, err := repository.Get(ctx, groupID)
		if err != nil || current == nil || current.EvaluationRevision != prior.EvaluationRevision {
			t.Fatalf("stale source capture changed accepted group %s: %#v err %v", groupID, current, err)
		}
	}
}
