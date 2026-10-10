//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestSourceWorkVariantIdentityDigestlessReadinessRetryAndCleanupWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(db)
	root := createInventoryRoot(t, ctx, repository, "/srv/work-variant-digestless")
	location := insertAnalysisLocation(t, ctx, db, root.ID, "track.flac", 4096, probeMtime())
	establishInventory(t, ctx, db, root)
	work := normalizedWork(t, ctx, repository, root, location, false,
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepProbe, State: "pending"},
		persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
	)
	probeTool := insertVerifiedAnalysisTool(t, ctx, db, "ffmpeg", "ffprobe", "7.1.2", "ffprobe version 7.1.2")
	fixture := newAnalysisStepFixture(t, ctx, db, repository, openScanEnqueueRiver(t, db), root, location, work,
		persistence.SourceAnalysisModeBatch, nil, nil, false, []persistence.SourceAnalysisToolSelection{probeTool})
	firstAttempt := fixture.claim(persistence.SourceStepProbe)
	if err := repository.FailSourceAnalysisStep(ctx, persistence.SourceStepFailure{
		WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: firstAttempt, Step: persistence.SourceStepProbe, SafeError: "fixture probe retry",
	}); err != nil {
		t.Fatalf("fail initial probe attempt: %v", err)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, fixture.operation.ID, "failed", "probe", "fixture probe retry"); err != nil {
		t.Fatalf("settle failed initial probe attempt: %v", err)
	}
	probeStep := string(persistence.SourceStepProbe)
	workID := work.ID
	retry := newAnalysisStepFixture(t, ctx, db, repository, openScanEnqueueRiver(t, db), root, location, work,
		persistence.SourceAnalysisModeSingleStep, &workID, &probeStep, false, []persistence.SourceAnalysisToolSelection{probeTool})
	probe := applyIdentityProbe(t, retry, location.SizeBytes, "7.1.2")
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, retry.operation.ID, "succeeded", "probe", ""); err != nil {
		t.Fatalf("settle successful probe retry: %v", err)
	}
	metadataStep := string(persistence.SourceStepMetadata)
	metadataRetry := newAnalysisStepFixture(t, ctx, db, repository, openScanEnqueueRiver(t, db), root, location, work,
		persistence.SourceAnalysisModeSingleStep, &workID, &metadataStep, false, nil)
	applyIdentityMetadata(t, metadataRetry)
	anchor := sourceWorkVariantID(t, ctx, db, work.ID)
	if anchor == uuid.Nil {
		t.Fatal("ready digestless work has no identity anchor")
	}
	assertIdentityLocation(t, ctx, db, location.ID, anchor)
	captures, err := persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil || len(captures) != 1 {
		t.Fatalf("current captures = %#v, %v", captures, err)
	}
	if captures[0].VariantID != work.ID || captures[0].MediaVariantID == nil || *captures[0].MediaVariantID != anchor || !captures[0].Ready {
		t.Fatalf("digestless capture identity/readiness = %#v; want unchanged work member and mapped anchor", captures[0])
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, metadataRetry.operation.ID, "succeeded", "metadata", ""); err != nil {
		t.Fatalf("settle successful metadata retry: %v", err)
	}
	var realForeignKey bool
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_work_variant_identity identity JOIN media_variant variant ON variant.id=identity.media_variant_id WHERE identity.work_id=?)`, work.ID).Scan(ctx, &realForeignKey); err != nil || !realForeignKey {
		t.Fatalf("mapping does not reference a real media variant: %v, %v", realForeignKey, err)
	}

	if got := sourceWorkVariantID(t, ctx, db, work.ID); got != anchor {
		t.Fatalf("probe retry changed mapped anchor from %s to %s", anchor, got)
	}
	assertIdentityLocation(t, ctx, db, location.ID, anchor)
	captures, err = persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil || len(captures) != 1 || captures[0].VariantID != work.ID || captures[0].MediaVariantID == nil || *captures[0].MediaVariantID != anchor || captures[0].ProbeWinner == nil || *captures[0].ProbeWinner != probe.ID {
		t.Fatalf("retry capture member/anchor/probe = %#v, %v", captures, err)
	}

	// The identity FK alone protects the anchor from both targeted cleanup and
	// orphan cleanup, even when no location or analysis step points at it.
	if _, err := db.NewRaw(`UPDATE source_location SET media_variant_id=NULL WHERE id=?`, location.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.CleanupSourceMediaVariants(ctx, []uuid.UUID{anchor}); err != nil {
		t.Fatalf("cleanup mapped digestless anchor: %v", err)
	}
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, anchor).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("mapped anchor count = %d, %v; want retained", count, err)
	}

	for _, selected := range []bool{false, true} {
		trackID := uuid.New()
		if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, trackID, "track").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := persistence.NewCollectionRepository(db).UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
			TrackID: trackID, MediaVariantID: anchor, Selected: selected, Compatible: true, Method: "manual", Metadata: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("attach anchor for selection state %v: %v", selected, err)
		}
	}
	// Model retirement of the mapping while preserving all track attachments.
	if _, err := db.NewRaw(`DELETE FROM source_work_variant_identity WHERE work_id=?`, work.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.CleanupSourceMediaVariants(ctx, []uuid.UUID{anchor}); err != nil {
		t.Fatalf("cleanup track-attached anchor: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM media_variant WHERE id=?`, anchor).Scan(ctx, &count); err != nil || count != 1 {
		t.Fatalf("track-attached anchor count = %d, %v; want retained", count, err)
	}
	captures, err = persistence.NewIncomingGroupingRepository(db).CurrentCaptures(ctx)
	if err != nil || len(captures) != 1 || captures[0].VariantID != work.ID || captures[0].MediaVariantID != nil {
		t.Fatalf("unmapped existing work was implicitly backfilled or changed grouping member: %#v, %v", captures, err)
	}
}

func TestSourceWorkVariantIdentityRequestedSHAReadinessAndCanonicalSharingWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(db)
	root := createInventoryRoot(t, ctx, repository, "/srv/work-variant-sha")
	locationA := insertAnalysisLocation(t, ctx, db, root.ID, "a.flac", 2048, probeMtime())
	locationB := insertAnalysisLocation(t, ctx, db, root.ID, "b.flac", 2048, probeMtime().Add(time.Second))
	establishInventory(t, ctx, db, root)
	steps := []persistence.SourceAnalysisStepInput{
		{Step: persistence.SourceStepSHA256, State: "pending"},
		{Step: persistence.SourceStepMetadata, State: "pending"},
	}
	workA := normalizedWork(t, ctx, repository, root, locationA, true, steps...)
	workB := normalizedWork(t, ctx, repository, root, locationB, true, steps...)
	fixtureA := newAnalysisStepFixture(t, ctx, db, repository, openScanEnqueueRiver(t, db), root, locationA, workA,
		persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
	digest := make([]byte, 32)
	digest[0] = 0x5a

	// Metadata may finish while requested SHA remains pending, but that work is
	// not yet ready and must not receive a digestless placeholder.
	applyIdentityMetadata(t, fixtureA)
	var mappings int
	if err := db.NewRaw(`SELECT count(*) FROM source_work_variant_identity WHERE work_id=?`, workA.ID).Scan(ctx, &mappings); err != nil || mappings != 0 {
		t.Fatalf("pending requested SHA mapped %d times, %v; want no identity", mappings, err)
	}
	shaA := applyIdentitySHA(t, fixtureA, digest)
	anchorA := sourceWorkVariantID(t, ctx, db, workA.ID)
	if anchorA != shaA.ID {
		t.Fatalf("first ready SHA identity = %s, want canonical %s", anchorA, shaA.ID)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, fixtureA.operation.ID, "succeeded", "metadata", ""); err != nil {
		t.Fatalf("settle first SHA analysis: %v", err)
	}

	// A second work finishing SHA before metadata must converge on the same
	// canonical digest row when its final requested step completes.
	fixtureB := newAnalysisStepFixture(t, ctx, db, repository, openScanEnqueueRiver(t, db), root, locationB, workB,
		persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
	shaB := applyIdentitySHA(t, fixtureB, digest)
	if shaB.ID != shaA.ID {
		t.Fatalf("shared SHA selected distinct canonical rows %s and %s", shaA.ID, shaB.ID)
	}
	if err := db.NewRaw(`SELECT count(*) FROM source_work_variant_identity WHERE work_id=?`, workB.ID).Scan(ctx, &mappings); err != nil || mappings != 0 {
		t.Fatalf("unfinished SHA-first work mapped %d times, %v; want no identity before metadata", mappings, err)
	}
	applyIdentityMetadata(t, fixtureB)
	if anchorB := sourceWorkVariantID(t, ctx, db, workB.ID); anchorB != shaA.ID {
		t.Fatalf("second work identity = %s, want shared canonical %s", anchorB, shaA.ID)
	}
	if err := repository.SettleNormalizedSourceAnalysisOperation(ctx, fixtureB.operation.ID, "succeeded", "metadata", ""); err != nil {
		t.Fatalf("settle second SHA analysis: %v", err)
	}
	assertIdentityLocation(t, ctx, db, locationA.ID, shaA.ID)
	assertIdentityLocation(t, ctx, db, locationB.ID, shaA.ID)

	// Same completed delivery with a different digest is rejected and cannot
	// overwrite the immutable work mapping.
	wrong := make([]byte, 32)
	wrong[0] = 0x6b
	// ApplySourceSHA256 recognizes the completed attempt and only accepts an
	// exact replay of its original successful result.
	apply := persistence.SourceSHA256Apply{
		WorkID: workA.ID, OperationID: fixtureA.operation.ID, OperationAttempt: fixtureA.operation.Attempt,
		JobID: *fixtureA.operation.RiverJobID, StepAttempt: 1, SHA256: wrong,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256",
	}
	if _, err := repository.ApplySourceSHA256(ctx, apply); err == nil {
		t.Fatal("completed mapped work accepted a different SHA identity")
	}
	if got := sourceWorkVariantID(t, ctx, db, workA.ID); got != shaA.ID {
		t.Fatalf("rejected SHA replay changed mapping to %s", got)
	}
}

func applyIdentityProbe(t *testing.T, fixture *analysisStepFixture, size int64, version string) *persistence.SourceMediaVariant {
	t.Helper()
	attempt := fixture.claim(persistence.SourceStepProbe)
	result, err := fixture.repository.ApplySourceProbe(fixture.ctx, persistence.SourceProbeApply{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: attempt, SizeBytes: size, AnalysisPolicy: persistence.SourceAnalysisPolicyVersion,
		FFProbeVersion: version, FFProbeJSON: json.RawMessage(`{"format":{"format_name":"flac"}}`),
		ObservedTags: json.RawMessage(`{"ARTIST":["fixture"]}`), InspectedAt: time.Now().UTC().Truncate(time.Microsecond), AudioStreamCount: 1,
	})
	if err != nil {
		t.Fatalf("apply identity probe: %v", err)
	}
	return result
}

func applyIdentityMetadata(t *testing.T, fixture *analysisStepFixture) {
	t.Helper()
	attempt := fixture.claim(persistence.SourceStepMetadata)
	_, err := fixture.repository.ApplySourceMetadata(fixture.ctx, persistence.SourceMetadataApply{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: attempt,
		ObservedTags: json.RawMessage(`{"TITLE":["fixture"]}`), Provenance: json.RawMessage(`{"source":"fixture"}`),
		ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("apply identity metadata: %v", err)
	}
}

func applyIdentitySHA(t *testing.T, fixture *analysisStepFixture, digest []byte) *persistence.SourceMediaVariant {
	t.Helper()
	attempt := fixture.claim(persistence.SourceStepSHA256)
	result, err := fixture.repository.ApplySourceSHA256(fixture.ctx, persistence.SourceSHA256Apply{
		WorkID: fixture.work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
		JobID: *fixture.operation.RiverJobID, StepAttempt: attempt, SHA256: digest,
		CalculatedAt: time.Now().UTC().Truncate(time.Microsecond), Algorithm: "SHA-256",
	})
	if err != nil {
		t.Fatalf("apply identity SHA: %v", err)
	}
	return result
}

func sourceWorkVariantID(t *testing.T, ctx context.Context, db *bun.DB, workID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, workID).Scan(ctx, &id); err != nil {
		t.Fatalf("read work variant identity: %v", err)
	}
	return id
}

func assertIdentityLocation(t *testing.T, ctx context.Context, db *bun.DB, locationID, want uuid.UUID) {
	t.Helper()
	var got *uuid.UUID
	if err := db.NewRaw(`SELECT media_variant_id FROM source_location WHERE id=?`, locationID).Scan(ctx, &got); err != nil || got == nil || *got != want {
		t.Fatalf("location identity = %v, %v; want %s", got, err, want)
	}
}
