//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func matchingApplyEvidence(t *testing.T, fixture *incomingMatchFixture, trackID uuid.UUID) json.RawMessage {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(fixture.evaluationEvidence(), &envelope); err != nil {
		t.Fatal(err)
	}
	decision, err := json.Marshal(struct {
		Version         int       `json:"version"`
		GroupID         uuid.UUID `json:"group_id"`
		PolicyVersion   string    `json:"policy_version"`
		Outcome         string    `json:"outcome"`
		SelectedRelease struct {
			ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
			ProviderReleaseKey string    `json:"provider_release_key"`
		} `json:"selected_release"`
		Assignments []struct {
			FileMemberID    uuid.UUID `json:"file_member_id"`
			ProviderTrackID uuid.UUID `json:"provider_track_id"`
			Medium          int       `json:"medium"`
			Position        int       `json:"position"`
		} `json:"assignments"`
	}{
		Version: 1, GroupID: fixture.group.ID, PolicyVersion: "matching-rapidfuzz-v1", Outcome: "unique",
		SelectedRelease: struct {
			ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
			ProviderReleaseKey string    `json:"provider_release_key"`
		}{ProviderReleaseID: fixture.releaseID, ProviderReleaseKey: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		Assignments: []struct {
			FileMemberID    uuid.UUID `json:"file_member_id"`
			ProviderTrackID uuid.UUID `json:"provider_track_id"`
			Medium          int       `json:"medium"`
			Position        int       `json:"position"`
		}{
			{FileMemberID: fixture.memberA, ProviderTrackID: trackID, Medium: 1, Position: 1},
			{FileMemberID: fixture.memberB, ProviderTrackID: trackID, Medium: 1, Position: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope["evaluation_decision"] = decision
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func assertMatchingPublicationStillRunning(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, parentID uuid.UUID, jobID int64, groupID uuid.UUID) {
	t.Helper()
	parent, err := repository.GetOperation(ctx, parentID)
	if err != nil || parent.State != "running" {
		t.Fatalf("rejected publication parent = %+v err=%v, want running", parent, err)
	}
	stored, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, groupID)
	if err != nil || stored != nil || len(candidates) != 0 {
		t.Fatalf("rejected publication changed evaluated result: header=%+v candidates=%+v err=%v", stored, candidates, err)
	}
	var state string
	var consumed bool
	if err := db.NewRaw(`SELECT run.state, delivery.consumed_at IS NOT NULL FROM matching_run run JOIN matching_delivery delivery ON delivery.run_id=run.id WHERE run.id=? AND delivery.river_job_id=?`, parentID, jobID).Scan(ctx, &state, &consumed); err != nil || state != "running" || consumed {
		t.Fatalf("rejected publication run/delivery state=%q consumed=%v err=%v, want running/unconsumed", state, consumed, err)
	}
}

func assertMatchingPublicationWaiting(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, parentID, groupID uuid.UUID) {
	t.Helper()
	parent, err := repository.GetOperation(ctx, parentID)
	if err != nil || parent.State != "running" || parent.Stage != "waiting_provider" {
		t.Fatalf("waiting parent = %+v err=%v", parent, err)
	}
	var state string
	if err := db.NewRaw(`SELECT state FROM matching_run WHERE id=?`, parentID).Scan(ctx, &state); err != nil || state != "waiting_provider" {
		t.Fatalf("waiting run state=%q err=%v", state, err)
	}
	stored, candidates, err := persistence.NewIncomingMatchCandidateRepository(db).Get(ctx, groupID)
	if err != nil || stored != nil || len(candidates) != 0 {
		t.Fatalf("waiting publication changed candidates: header=%+v candidates=%+v err=%v", stored, candidates, err)
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
	sourceSHA256A         []byte
	sourceSHA256B         []byte
	groupingEpoch         int64
	groupsRevision        string
	acceptedProjectionRev string
	cacheKey              string
	cacheRevision         *string
	releaseKeys           []string
	graph                 persistence.ProviderGraph
}

type localMatchingPublicationReconcileInserter struct {
	args []persistence.PublicationReconciliationJobArgs
	ids  []int64
}

func (inserter *localMatchingPublicationReconcileInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	jobArgs, ok := args.(persistence.PublicationReconciliationJobArgs)
	if !ok {
		return nil, errors.New("unexpected publication reconciliation job arguments")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var jobID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), encoded).Scan(&jobID); err != nil {
		return nil, err
	}
	inserter.args = append(inserter.args, jobArgs)
	inserter.ids = append(inserter.ids, jobID)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: jobID}}, nil
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
	sourceSHA256ByWork := make(map[uuid.UUID][]byte, len(captures))
	for _, capture := range captures {
		sourceSHA256ByWork[capture.WorkID] = append([]byte(nil), capture.SourceSHA256...)
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
	rawRelease := json.RawMessage(`{"id":"` + releaseKey + `","title":"Fixture Album","artist-credit":[{"name":"Fixture Artist","artist":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Fixture Artist"}}],"media":[{"position":1,"tracks":[{"id":"` + trackKey + `","number":"1","recording":{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","title":"Fixture Track","length":201000,"artist-credit":[{"name":"Recording Credit","joinphrase":" feat. ","artist":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","name":"Canonical Recording Artist"}}]}}]}]}`)
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
	queryKeys := []string{cacheKey, "recording-search:a", "recording-search:b", "release-search:b"}
	snapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, source.Endpoint, identity, releaseKeys, queryKeys)
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
	cacheResponse, err := cache.GetCachedResponse(ctx, source.ID, cacheKey)
	if err != nil || cacheResponse == nil {
		t.Fatalf("read accepted cache revision: %v", err)
	}
	cacheRevision := cacheResponse.Revision
	return &incomingMatchFixture{
		group: persistence.IncomingGroupingGroup{
			ID: groupSnapshot.Groups[0].ID, Revision: groupSnapshot.Groups[0].Revision,
			Members: append([]uuid.UUID(nil), groupSnapshot.Groups[0].Members...),
		},
		memberA: works[0].ID, memberB: works[1].ID, workA: works[0].ID, workB: works[1].ID,
		provider: provider.ID, source: source.ID, releaseID: releaseID, trackID: trackID, releaseKeys: releaseKeys,
		sourceSHA256A: sourceSHA256ByWork[works[0].ID], sourceSHA256B: sourceSHA256ByWork[works[1].ID],
		groupingEpoch: state.Revision, groupsRevision: groupRevision,
		acceptedProjectionRev: snapshot.AcceptedProjectionRevision, cacheKey: cacheKey, cacheRevision: cacheRevision, graph: graph,
	}
}

func (f *incomingMatchFixture) evaluation(candidates []persistence.IncomingMatchCandidate) persistence.IncomingMatchEvaluation {
	return persistence.IncomingMatchEvaluation{
		ExpectedGroupsRevision: f.groupsRevision, ExpectedGroupingEpoch: f.groupingEpoch, PolicyVersion: "matching-rapidfuzz-v1",
		Provider: persistence.IncomingMatchProviderFence{
			ProviderID: f.provider, ProviderSourceID: f.source, Namespace: "incoming-match",
			Endpoint: "https://musicbrainz.example/ws/2", ConfigurationIdentity: "incoming-match-integration",
			CacheKey: f.cacheKey, CacheRevision: f.cacheRevision, AcceptedProjectionRevision: f.acceptedProjectionRev,
			CacheRevisions: map[string]*string{
				f.cacheKey:           f.cacheRevision,
				"release-search:b":   nil,
				"recording-search:a": nil, "recording-search:b": nil,
			},
			ReleaseKeys: append([]string(nil), f.releaseKeys...),
		},
		Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: f.group.ID, GroupRevision: f.group.Revision,
			Members: []persistence.IncomingMatchMemberFence{
				{FileMemberID: f.memberA, WorkID: f.workA, SourceSHA256: append([]byte(nil), f.sourceSHA256A...)},
				{FileMemberID: f.memberB, WorkID: f.workB, SourceSHA256: append([]byte(nil), f.sourceSHA256B...)},
			},
			Candidates: candidates,
			Evidence:   f.evaluationEvidence(),
		}},
	}
}

func (f *incomingMatchFixture) evaluationEvidence() json.RawMessage {
	type queryEvidence struct {
		FileMemberID      uuid.UUID `json:"file_member_id"`
		ReleaseQueryKey   string    `json:"release_query_key"`
		RecordingQueryKey string    `json:"recording_query_key"`
	}
	evidence, _ := json.Marshal(struct {
		Files []queryEvidence `json:"files"`
	}{Files: []queryEvidence{
		{FileMemberID: f.memberA, ReleaseQueryKey: f.cacheKey, RecordingQueryKey: "recording-search:a"},
		{FileMemberID: f.memberB, ReleaseQueryKey: "release-search:b", RecordingQueryKey: "recording-search:b"},
	}})
	return evidence
}

func (f *incomingMatchFixture) candidate(member uuid.UUID, score float64) persistence.IncomingMatchCandidate {
	return persistence.IncomingMatchCandidate{
		FileMemberID: member, ProviderID: f.provider, ProviderSourceID: f.source,
		ReleaseID: f.releaseID, Score: score, Evidence: json.RawMessage(`{"kind":"test"}`),
	}
}

func TestLocalProviderReleaseMatchingMaterializesAndReplaysPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)

	var artistID, recordingID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? ORDER BY id LIMIT 1`, fixture.provider).Scan(ctx, &artistID); err != nil {
		t.Fatalf("read cross-provider artist: %v", err)
	}
	if err := db.NewRaw(`SELECT recording_id FROM provider_release_track WHERE provider_id=? AND release_id=? ORDER BY id LIMIT 1`, fixture.provider, fixture.releaseID).Scan(ctx, &recordingID); err != nil {
		t.Fatalf("read cross-provider recording: %v", err)
	}
	var publicationRequestsBefore int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &publicationRequestsBefore); err != nil {
		t.Fatalf("count publication requests before local CRUD: %v", err)
	}

	titleOverride, durationOverride := "Local title", int64(123456)
	catalog := service.NewLocalProviderCatalog(persistence.NewLocalProviderRepository(db), nil)
	created, err := catalog.Create(ctx, "release", service.LocalProviderItem{
		Title: "Local matching edition", ReleaseGroupTitle: "Local matching group",
		Credits: []service.LocalProviderCredit{{ArtistID: artistID, Name: "Local artist credit", JoinPhrase: " & "}},
		Tracks:  []service.LocalProviderTrack{{RecordingID: recordingID, TitleOverride: &titleOverride, DurationOverrideMS: &durationOverride}},
	})
	if err != nil {
		t.Fatalf("create local provider release: %v", err)
	}
	var publicationRequestsAfterCRUD int
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &publicationRequestsAfterCRUD); err != nil || publicationRequestsAfterCRUD != publicationRequestsBefore {
		t.Fatalf("local provider CRUD created publication requests: before=%d after=%d err=%v", publicationRequestsBefore, publicationRequestsAfterCRUD, err)
	}
	if created.ProviderCode != "local" || len(created.Tracks) != 1 || created.Tracks[0].ProviderID != fixture.provider || created.Tracks[0].Position != 0 {
		t.Fatalf("created local provider release/track = %+v", created)
	}

	matching := service.NewMatchingLocalService(persistence.NewIncomingMatchCandidateRepository(db))
	intent := service.MatchingLocalIntent{
		ProviderReleaseID: created.ID, ExpectedGroupRevision: fixture.group.Revision,
		ExpectedAuthorityRevision: 0,
		Assignments:               []service.MatchingLocalAssignment{{FileMemberID: fixture.memberA, ProviderTrackID: created.Tracks[0].ID}},
	}
	preview, err := matching.Preview(ctx, fixture.group.ID, intent)
	if err != nil {
		t.Fatalf("preview local provider matching: %v", err)
	}
	if len(preview.Tracks) != 1 || preview.Tracks[0].Position != 1 || preview.Tracks[0].Medium != 1 || preview.Tracks[0].TitleOverride == nil || *preview.Tracks[0].TitleOverride != titleOverride || preview.Tracks[0].RecordingDurationMS == nil || *preview.Tracks[0].RecordingDurationMS != 201000 || preview.Tracks[0].DurationOverrideMS == nil || *preview.Tracks[0].DurationOverrideMS != durationOverride {
		t.Fatalf("preview lost provider track coordinates or overrides: %+v", preview.Tracks)
	}
	requestID := uuid.New()
	confirmed, err := matching.Confirm(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil {
		t.Fatalf("confirm local provider matching: %v", err)
	}
	if confirmed.LocalReleaseID == nil {
		t.Fatalf("confirmation did not return materialized local release: %+v", confirmed)
	}

	// Consume the reconciliation created by the real local-confirmation writer.
	// An unpublished matched release is not authorization to publish its first
	// audio; reconciliation must acknowledge it without creating an attempt.
	var positionID, outputEpoch uuid.UUID
	if err := db.NewRaw(`SELECT id FROM release_track WHERE release_id=? ORDER BY position LIMIT 1`, *confirmed.LocalReleaseID).Scan(ctx, &positionID); err != nil {
		t.Fatalf("read confirmed local release position: %v", err)
	}
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &outputEpoch); err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSetupManagerRepository(db)
	inserter := new(localMatchingPublicationReconcileInserter)
	if dispatched, err := repository.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || dispatched != 1 {
		t.Fatalf("dispatch local-confirmation reconciliation=%d err=%v", dispatched, err)
	}
	operationID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, claim, err := repository.ClaimPublicationReconciliationDelivery(ctx, operationID, jobID, 1)
	if err != nil || claim != persistence.PublicationReconciliationClaimed {
		t.Fatalf("claim local-confirmation reconciliation=%s err=%v", claim, err)
	}
	reconciler := service.NewLibraryPublications(repository, settings.New(persistence.NewSettingsRepository(db), nil), inserter, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}).
		WithPublicationCoordinator(publication.NewCoordinator())
	if err := reconciler.ReconcilePublication(ctx, guard); err != nil {
		t.Fatalf("consume local-confirmation reconciliation: %v", err)
	}
	var current, attempts, dirty int
	var eventState, operationState string
	for _, query := range []string{
		`SELECT count(*) FROM track_publication WHERE position_id=?`,
		`SELECT count(*) FROM track_publication_attempt WHERE position_id=?`,
	} {
		target := &current
		if query == `SELECT count(*) FROM track_publication_attempt WHERE position_id=?` {
			target = &attempts
		}
		if err := db.NewRaw(query, positionID).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_dirty_scope WHERE output_epoch=? AND position_id=? AND generation<=?`, outputEpoch, positionID, guard.ObservedGeneration).Scan(ctx, &dirty); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT state FROM publication_reconciliation_event WHERE operation_id=?`, operationID).Scan(ctx, &eventState); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT state FROM operation WHERE id=?`, operationID).Scan(ctx, &operationState); err != nil {
		t.Fatal(err)
	}
	if current != 0 || attempts != 0 || dirty != 0 || eventState != "completed" || operationState != "succeeded" {
		t.Fatalf("local-confirmation reconciliation created publication authority: current=%d attempts=%d observed dirty=%d event=%s operation=%s", current, attempts, dirty, eventState, operationState)
	}

	var releaseLinks, recordingLinks, trackLinks, artistLinks int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM provider_local_release WHERE provider_id=(SELECT id FROM provider WHERE code='local') AND provider_release_id=?`: &releaseLinks,
		`SELECT count(*) FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=?`:                                        &recordingLinks,
		`SELECT count(*) FROM provider_local_track WHERE provider_id=(SELECT id FROM provider WHERE code='local') AND provider_track_id=?`:     &trackLinks,
		`SELECT count(*) FROM provider_local_artist WHERE provider_id=? AND provider_artist_id=?`:                                              &artistLinks,
	} {
		var args []any
		switch target {
		case &releaseLinks:
			args = []any{created.ID}
		case &recordingLinks:
			args = []any{fixture.provider, recordingID}
		case &trackLinks:
			args = []any{created.Tracks[0].ID}
		case &artistLinks:
			args = []any{fixture.provider, artistID}
		}
		if err := db.NewRaw(query, args...).Scan(ctx, target); err != nil || *target != 1 {
			t.Fatalf("provider link count for %q = %d, err=%v; want one", query, *target, err)
		}
	}
	var localMedium, localPosition int
	var localTitle string
	var localDuration int64
	if err := db.NewRaw(`SELECT position.medium,position.position,track.title,position.duration_ms
		FROM provider_local_track link JOIN release_track position ON position.track_id=link.local_track_id
		JOIN local_track track ON track.id=position.track_id
		WHERE link.provider_id=(SELECT id FROM provider WHERE code='local') AND link.provider_track_id=?`, created.Tracks[0].ID).Scan(ctx, &localMedium, &localPosition, &localTitle, &localDuration); err != nil {
		t.Fatalf("read materialized local track: %v", err)
	}
	if localMedium != 0 || localPosition != 0 || localTitle != titleOverride || localDuration != durationOverride {
		t.Fatalf("materialized track coordinates/title/duration = %d/%d %q %d", localMedium, localPosition, localTitle, localDuration)
	}
	var recordingDuration int64
	var recordingArtistCredit, trackArtistCredit string
	if err := db.NewRaw(`SELECT (recording.metadata->>'duration_ms')::bigint,recording_artist.name,track_artist.name
		FROM provider_local_recording link JOIN local_recording recording ON recording.id=link.local_recording_id
		JOIN recording_artist ON recording_artist.recording_id=recording.id
		JOIN provider_local_track track_link ON track_link.provider_id=(SELECT id FROM provider WHERE code='local') AND track_link.provider_track_id=?
		JOIN track_artist ON track_artist.track_id=track_link.local_track_id
		WHERE link.provider_id=? AND link.provider_recording_id=?`, created.Tracks[0].ID, fixture.provider, recordingID).Scan(ctx, &recordingDuration, &recordingArtistCredit, &trackArtistCredit); err != nil {
		t.Fatalf("read materialized recording credits and duration: %v", err)
	}
	if recordingDuration != 201000 || recordingArtistCredit != "Recording Credit" || trackArtistCredit != "Recording Credit" {
		t.Fatalf("materialized recording duration/credits = %d/%q/%q", recordingDuration, recordingArtistCredit, trackArtistCredit)
	}
	var associated int
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=(SELECT id FROM provider WHERE code='local') AND provider_track_id=?) AND media_variant_id IN (SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?)`, created.Tracks[0].ID, fixture.memberA).Scan(ctx, &associated); err != nil || associated == 0 {
		t.Fatalf("confirmed source association count=%d err=%v", associated, err)
	}
	const expectedGenreID = "00000000-0000-4000-8000-000000000099"
	const expectedGenreName = "Expected Local Genre"
	if _, err := db.NewRaw(`UPDATE provider_release SET metadata=jsonb_set(metadata,'{genres}',?::jsonb,true) WHERE id=? AND provider_id=(SELECT id FROM provider WHERE code='local')`,
		`[{"id":"`+expectedGenreID+`","source_name":"local","name":"`+expectedGenreName+`"}]`, created.ID).Exec(ctx); err != nil {
		t.Fatalf("seed saved Local provider release genres: %v", err)
	}
	if err := db.NewRaw(`SELECT id FROM release_track WHERE release_id=? AND track_id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=(SELECT id FROM provider WHERE code='local') AND provider_track_id=?)`, *confirmed.LocalReleaseID, created.Tracks[0].ID).Scan(ctx, &positionID); err != nil {
		t.Fatalf("resolve materialized Local release position: %v", err)
	}
	firstPublication, err := persistence.NewSetupManagerRepository(db).ReadLibraryFirstPublicationContext(ctx, *confirmed.LocalReleaseID, positionID)
	if err != nil {
		t.Fatalf("read first-publication context for Local matching result: %v", err)
	}
	if len(firstPublication.ProviderGenres) != 1 || firstPublication.ProviderGenres[0].ID != expectedGenreID || firstPublication.ProviderGenres[0].Name != expectedGenreName {
		t.Fatalf("first-publication Local provider genres = %+v, want saved genre %q without catalog lookup", firstPublication.ProviderGenres, expectedGenreName)
	}

	var countsBeforeReplay int
	if err := db.NewRaw(`SELECT (SELECT count(*) FROM local_release)+(SELECT count(*) FROM local_recording)+(SELECT count(*) FROM local_track)+(SELECT count(*) FROM release_artist)+(SELECT count(*) FROM track_artist)+(SELECT count(*) FROM track_source)`).Scan(ctx, &countsBeforeReplay); err != nil {
		t.Fatal(err)
	}
	replayed, err := matching.Confirm(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil {
		t.Fatalf("replay local matching receipt: %v", err)
	}
	var countsAfterReplay int
	if err := db.NewRaw(`SELECT (SELECT count(*) FROM local_release)+(SELECT count(*) FROM local_recording)+(SELECT count(*) FROM local_track)+(SELECT count(*) FROM release_artist)+(SELECT count(*) FROM track_artist)+(SELECT count(*) FROM track_source)`).Scan(ctx, &countsAfterReplay); err != nil {
		t.Fatal(err)
	}
	if replayed.LocalReleaseID == nil || *replayed.LocalReleaseID != *confirmed.LocalReleaseID || countsAfterReplay != countsBeforeReplay {
		t.Fatalf("receipt replay changed materialization: confirmed=%+v replayed=%+v counts=%d/%d", confirmed.LocalReleaseID, replayed.LocalReleaseID, countsBeforeReplay, countsAfterReplay)
	}
}
