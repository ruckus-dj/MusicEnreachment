//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestIncomingMatchCandidatesPersistAndFenceWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)

	assertIncomingMatchResult(t, ctx, repository, fixture.group.ID, false, nil)
	if err := repository.Replace(ctx, fixture.evaluation(nil)); err != nil {
		t.Fatalf("publish evaluated-empty result: %v", err)
	}
	header, candidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	var providerFences persistence.IncomingMatchProviderFence
	if err := json.Unmarshal(header.ProviderFences, &providerFences); err != nil {
		t.Fatalf("decode persisted provider fences: %v", err)
	}
	if len(candidates) != 0 || len(providerFences.ReleaseKeys) != 2 || providerFences.ReleaseKeys[0] != fixture.releaseKeys[0] || providerFences.ReleaseKeys[1] != fixture.releaseKeys[1] {
		t.Fatalf("evaluated-empty result did not preserve exact inspected keys: candidates=%#v fences=%#v", candidates, providerFences)
	}
	emptySnapshot, err := persistence.NewProviderCacheRepository(db).GetMusicBrainzMatchingSnapshot(ctx,
		"https://musicbrainz.example/ws/2", "incoming-match-integration", nil, nil)
	if err != nil || len(emptySnapshot.Releases) != 0 || len(emptySnapshot.Responses) != 0 || emptySnapshot.AcceptedProjectionRevision == "" {
		t.Fatalf("empty cache-only search snapshot = %#v, err=%v", emptySnapshot, err)
	}

	first := fixture.candidate(fixture.memberA, 0.12345678901234567)
	second := fixture.candidate(fixture.memberB, 0.91)
	position := persistence.IncomingMatchPosition{TrackID: fixture.trackID, Medium: 0, Position: 0}
	first.Positions = []persistence.IncomingMatchPosition{position}
	second.Positions = []persistence.IncomingMatchPosition{position}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first, second})); err != nil {
		t.Fatalf("persist candidates: %v", err)
	}
	_, got := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	scoresByMember := make(map[uuid.UUID]float64, len(got))
	for _, candidate := range got {
		scoresByMember[candidate.FileMemberID] = candidate.Score
	}
	if len(got) != 2 || scoresByMember[first.FileMemberID] != first.Score || scoresByMember[second.FileMemberID] != second.Score {
		t.Fatalf("persisted scores/candidates = %#v, want exact full-precision scores", got)
	}
	if got[0].FileMemberID == got[1].FileMemberID || len(got[0].Positions) != 1 || len(got[1].Positions) != 1 || got[0].Positions[0] != position || got[1].Positions[0] != position {
		t.Fatalf("same provider position was not retained for distinct file members: %#v", got)
	}

	// A failed insert after the old rows have been deleted must roll back the
	// previous accepted result. The candidate is otherwise a valid provider hit.
	bad := fixture.candidate(fixture.memberA, .5)
	bad.Positions = []persistence.IncomingMatchPosition{{TrackID: uuid.New(), Medium: 0, Position: 0}}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{bad})); err == nil {
		t.Fatal("candidate with a track outside the accepted release unexpectedly replaced prior result")
	}
	_, after := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	afterScores := make(map[uuid.UUID]float64, len(after))
	for _, candidate := range after {
		afterScores[candidate.FileMemberID] = candidate.Score
	}
	if len(after) != 2 || afterScores[first.FileMemberID] != first.Score || afterScores[second.FileMemberID] != second.Score {
		t.Fatalf("failed replacement partially wiped previous data: %#v", after)
	}

	// A successful refresh of the accepted query changes the source projection
	// revision. Existing candidates remain readable, but the old query result can
	// no longer publish over them.
	cache := persistence.NewProviderCacheRepository(db)
	ticket, err := cache.BeginRefresh(ctx, fixture.source, fixture.cacheKey, "incoming-match-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(),
		Payload: json.RawMessage(`{"releases":[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","score":99}]}`), Graph: fixture.graph,
	}); err != nil {
		t.Fatalf("refresh accepted provider projection: %v", err)
	}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first})); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale provider projection error = %v, want conflict", err)
	}
	_, after = mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	afterScores = make(map[uuid.UUID]float64, len(after))
	for _, candidate := range after {
		afterScores[candidate.FileMemberID] = candidate.Score
	}
	if len(after) != 2 || afterScores[first.FileMemberID] != first.Score || afterScores[second.FileMemberID] != second.Score {
		t.Fatalf("stale provider projection changed previous result: %#v", after)
	}

	if _, err := db.ExecContext(ctx, `UPDATE incoming_grouping_state SET revision=revision+1 WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if err := repository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{first})); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("stale epoch error = %v, want conflict", err)
	}
	_, after = mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if len(after) != 2 {
		t.Fatalf("stale epoch changed previous result: %#v", after)
	}
}

func assertIncomingMatchResult(t *testing.T, ctx context.Context, repository *persistence.IncomingMatchCandidateRepository, groupID uuid.UUID, evaluated bool, want []persistence.IncomingMatchCandidate) {
	t.Helper()
	header, candidates, err := repository.Get(ctx, groupID)
	if err != nil {
		t.Fatalf("read incoming match result: %v", err)
	}
	if (header != nil) != evaluated || len(candidates) != len(want) {
		t.Fatalf("result header=%#v candidates=%#v, evaluated=%v want=%#v", header, candidates, evaluated, want)
	}
}

func mustIncomingMatchResult(t *testing.T, ctx context.Context, repository *persistence.IncomingMatchCandidateRepository, groupID uuid.UUID) (*persistence.IncomingMatchEvaluationHeader, []persistence.IncomingMatchCandidate) {
	t.Helper()
	header, candidates, err := repository.Get(ctx, groupID)
	if err != nil || header == nil {
		t.Fatalf("read evaluated result: header=%#v err=%v", header, err)
	}
	return header, candidates
}

type incomingMatchFixture struct {
	group                 persistence.IncomingGroupingGroup
	memberA, memberB      uuid.UUID
	workA, workB          uuid.UUID
	provider, source      uuid.UUID
	releaseID, trackID    uuid.UUID
	groupingEpoch         int64
	groupsRevision        string
	acceptedProjectionRev string
	cacheKey              string
	releaseKeys           []string
	graph                 persistence.ProviderGraph
}

func newIncomingMatchFixture(t *testing.T, ctx context.Context, db *bun.DB) *incomingMatchFixture {
	t.Helper()
	inventory := persistence.NewSourceInventoryRepository(db)
	root := createInventoryRoot(t, ctx, inventory, "/srv/incoming-match-candidates")
	root.ProcessingMode = service.SourceProcessingModeStaged
	if _, err := db.NewUpdate().Model((*persistence.SourceRoot)(nil)).Set("processing_mode = ?", root.ProcessingMode).Where("id = ?", root.ID).Exec(ctx); err != nil {
		t.Fatalf("set staged fixture processing mode: %v", err)
	}
	establishInventory(t, ctx, db, root)
	var works []*persistence.SourceAnalysisWork
	for _, path := range []string{"album/one.flac", "album/two.flac"} {
		location := insertAnalysisLocation(t, ctx, db, root.ID, path, 4096, probeMtime())
		work := normalizedWork(t, ctx, inventory, root, location, false,
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
		)
		operation := normalizedOperation(t, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, false, nil, true)
		if err := inventory.CreateNormalizedSourceAnalysisOperationAndEnqueue(ctx, operation, openScanEnqueueRiver(t, db), service.SourceAnalysisJobArgs{OperationID: operation.ID}, nil); err != nil {
			t.Fatalf("admit metadata-only source analysis: %v", err)
		}
		delivery := persistence.SourceAnalysisOperationDelivery{Attempt: operation.Attempt, JobID: *operation.RiverJobID}
		started, _, mode, err := inventory.StartNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "darwin", "arm64")
		if err != nil {
			t.Fatalf("start metadata-only source analysis: %v", err)
		}
		if started.State != "running" || mode != service.SourceProcessingModeStaged {
			t.Fatalf("metadata-only source analysis delivery = state %q mode %q", started.State, mode)
		}
		attempt, err := inventory.ClaimSourceAnalysisStep(ctx, persistence.SourceStepClaim{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: *operation.RiverJobID, Step: persistence.SourceStepMetadata,
		})
		if err != nil {
			t.Fatalf("claim metadata analysis step: %v", err)
		}
		fence := persistence.SourceAnalysisArtifactFence{WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt, JobID: *operation.RiverJobID}
		artifactRepository := persistence.NewSourceAnalysisArtifactRepository(db)
		artifactID := uuid.New()
		if _, err := artifactRepository.Acquire(ctx, artifactID, fence); err != nil {
			t.Fatalf("acquire staged metadata artifact: %v", err)
		}
		if _, err := artifactRepository.MarkReady(ctx, artifactID, fence, location.SizeBytes); err != nil {
			t.Fatalf("mark staged metadata artifact ready: %v", err)
		}
		if _, err := inventory.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
			WorkID: work.ID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			JobID: *operation.RiverJobID, StepAttempt: attempt,
			ObservedTags: json.RawMessage(`{"ARTIST":["Fixture Artist"],"ALBUM":["Fixture Album"]}`),
			Provenance:   json.RawMessage(`{"source":"integration-test"}`), ObservedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("apply current metadata capture: %v", err)
		}
		if err := inventory.SettleNormalizedSourceAnalysisDelivery(ctx, operation.ID, delivery, "succeeded", "complete", ""); err != nil {
			t.Fatalf("settle completed metadata analysis: %v", err)
		}
		works = append(works, work)
	}
	grouping := persistence.NewIncomingGroupingRepository(db)
	captures, err := grouping.CurrentCaptures(ctx)
	if err != nil {
		t.Fatalf("read actual incoming captures before grouping: %v", err)
	}
	if len(captures) != len(works) {
		t.Fatalf("current incoming captures = %d, want %d", len(captures), len(works))
	}
	for _, capture := range captures {
		if !capture.Ready {
			t.Fatalf("analysis fixture for work %s is not ready: %+v", capture.WorkID, capture)
		}
	}
	if err := service.NewIncomingGroups(grouping).Refresh(ctx); err != nil {
		t.Fatalf("refresh groups from persisted captures: %v", err)
	}
	state, err := grouping.State(ctx)
	if err != nil {
		t.Fatalf("read grouping state: %v", err)
	}
	groups, err := grouping.List(ctx)
	if err != nil {
		t.Fatalf("read persisted groups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatalf("real captures grouped as %#v, want one group with both files", groups)
	}
	groupSnapshot, err := service.NewIncomingGroups(grouping).Snapshot(ctx)
	if err != nil {
		t.Fatalf("read service grouping snapshot: %v", err)
	}
	if len(groupSnapshot.Groups) != 1 || len(groupSnapshot.Groups[0].Members) != 2 {
		t.Fatalf("service grouping snapshot = %+v, want one group with both files", groupSnapshot.Groups)
	}
	groupRevision, err := service.IncomingGroupsRevision(groupSnapshot.Groups)
	if err != nil {
		t.Fatalf("calculate accepted groups revision: %v", err)
	}

	const identity = "incoming-match-integration"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name, setting_value) VALUES('musicbrainz_config_identity', ?) ON CONFLICT (setting_name) DO UPDATE SET setting_value=EXCLUDED.setting_value`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	provider := persistence.Provider{ID: uuid.New(), Code: "musicbrainz"}
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := persistence.ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "incoming-match", Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const trackKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	rawRelease := json.RawMessage(`{"id":"` + releaseKey + `","title":"Fixture Album","artist-credit":[{"name":"Fixture Artist","artist":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Fixture Artist"}}],"media":[{"position":1,"tracks":[{"id":"` + trackKey + `","number":"1","recording":{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","title":"Fixture Track"}}]}]}`)
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: rawRelease}})
	if err != nil {
		t.Fatalf("normalize real provider release fixture: %v", err)
	}
	const cacheKey = "release-search:0:1:fixture"
	ticket, err := cache.BeginRefresh(ctx, source.ID, cacheKey, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: time.Now().UTC(),
		Payload: json.RawMessage(`{"releases":[{"id":"` + releaseKey + `","score":100}]}`), Graph: graph,
	}); err != nil {
		t.Fatalf("persist accepted provider response and projection: %v", err)
	}
	const secondReleaseKey = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	secondRaw := json.RawMessage(`{"id":"` + secondReleaseKey + `","title":"Other Fixture Album"}`)
	secondGraph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: secondRaw}})
	if err != nil {
		t.Fatalf("normalize second inspected release fixture: %v", err)
	}
	secondTicket, err := cache.BeginRefresh(ctx, source.ID, "release-lookup:"+secondReleaseKey, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: secondTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: secondGraph,
	}); err != nil {
		t.Fatalf("persist second inspected release projection: %v", err)
	}
	releaseKeys := []string{releaseKey, secondReleaseKey}
	snapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, identity, releaseKeys, []string{cacheKey})
	if err != nil || len(snapshot.Releases) != 2 || len(snapshot.Releases[0].Tracks) != 1 {
		t.Fatalf("read accepted provider projection: snapshot=%#v err=%v", snapshot, err)
	}
	// Candidate ReleaseID is the persistence identity, not the provider MBID
	// exposed in MatchingRelease.ProviderKey. Resolve the same ID used by
	// persisted candidates and their release/track relations.
	var releaseID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, provider.ID, snapshot.Releases[0].ProviderKey).Scan(ctx, &releaseID); err != nil {
		t.Fatalf("resolve persisted release ID for projected key %q: %v", snapshot.Releases[0].ProviderKey, err)
	}
	trackID := snapshot.Releases[0].Tracks[0].ID
	return &incomingMatchFixture{
		group: persistence.IncomingGroupingGroup{
			ID: groupSnapshot.Groups[0].ID, Revision: groupSnapshot.Groups[0].Revision,
			Members: append([]uuid.UUID(nil), groupSnapshot.Groups[0].Members...),
		},
		memberA: works[0].ID, memberB: works[1].ID, workA: works[0].ID, workB: works[1].ID,
		provider: provider.ID, source: source.ID, releaseID: releaseID, trackID: trackID, releaseKeys: releaseKeys,
		groupingEpoch: state.Revision, groupsRevision: groupRevision,
		acceptedProjectionRev: snapshot.AcceptedProjectionRevision, cacheKey: cacheKey, graph: graph,
	}
}

func (f *incomingMatchFixture) evaluation(candidates []persistence.IncomingMatchCandidate) persistence.IncomingMatchEvaluation {
	return persistence.IncomingMatchEvaluation{
		ExpectedGroupsRevision: f.groupsRevision, ExpectedGroupingEpoch: f.groupingEpoch, PolicyVersion: "policy-v1",
		Provider: persistence.IncomingMatchProviderFence{
			ProviderID: f.provider, ProviderSourceID: f.source, Namespace: "incoming-match",
			Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: "incoming-match-integration",
			CacheKey: f.cacheKey, AcceptedProjectionRevision: f.acceptedProjectionRev,
			ReleaseKeys: append([]string(nil), f.releaseKeys...),
		},
		Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: f.group.ID, GroupRevision: f.group.Revision,
			Members:    []persistence.IncomingMatchMemberFence{{FileMemberID: f.memberA, WorkID: f.workA}, {FileMemberID: f.memberB, WorkID: f.workB}},
			Candidates: candidates,
		}},
	}
}

func (f *incomingMatchFixture) candidate(member uuid.UUID, score float64) persistence.IncomingMatchCandidate {
	return persistence.IncomingMatchCandidate{
		FileMemberID: member, ProviderID: f.provider, ProviderSourceID: f.source,
		ReleaseID: f.releaseID, Score: score, Evidence: json.RawMessage(`{"kind":"test"}`),
	}
}
