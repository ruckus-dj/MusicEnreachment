//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCommitPublicationResultObsoleteOutputAdmissionPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repository := NewSetupManagerRepository(f.db)
	staged := publicationResultForFixture(f).Observation
	position, oldGeneration := f.guard.ExpectedPositionID, uuid.New()
	oldPath := "album/track-old.flac"
	size, mtime := int64(10), int64(20)
	digest := sha256.Sum256([]byte("old output"))
	previous := TrackPublication{
		PositionID: position, Epoch: f.guard.Epoch, Generation: oldGeneration, RelativePath: oldPath,
		Ownership:   mustPublicationJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}}),
		ActualAudio: staged.ActualAudio, ActualMetadata: staged.ActualMetadata, ActualAssets: staged.ActualAssets,
		QualityPolicyVersion: staged.QualityPolicyVersion, ValidatedAt: time.Now().UTC(),
	}
	if _, err := f.db.NewInsert().Model(&previous).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET expected_generation=? WHERE operation_id=?`, oldGeneration, f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte(oldPath))
	claim := PublicationNamespaceClaim{ID: uuid.New(), Epoch: previous.Epoch, RelativePath: oldPath, CanonicalKey: oldPath, KeyDigest: key[:], NodeKind: "file", OwnerPositionID: &position}
	if _, err := f.db.NewInsert().Model(&claim).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewInsert().Model(&PublicationNamespaceReservation{NamespaceClaimID: claim.ID, PositionID: position, Generation: oldGeneration}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	candidate, err := repository.ReadPublicationObsoleteOutputCandidate(ctx, f.guard, staged.RelativePath)
	if err != nil || candidate == nil || candidate.NamespaceClaimID != claim.ID || candidate.Publication.Generation != oldGeneration {
		t.Fatalf("old candidate = %+v, %v", candidate, err)
	}
	baseProof := &ObsoletePublicationOutputCleanupAdmission{
		Epoch: previous.Epoch, PositionID: position, Generation: oldGeneration, RelativePath: oldPath,
		NativePath: filepath.Join(t.TempDir(), filepath.FromSlash(oldPath)), NamespaceClaimID: claim.ID,
		OutputRootIdentity: PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "root"},
		ParentIdentity:     PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "parent"},
		FileSize:           size, ModifiedAt: time.UnixMicro(mtime).UTC(), SHA256: append([]byte(nil), digest[:]...),
	}
	baseResult := PublicationResult{Observation: staged, InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: staged.PayloadSHA256}}, ObsoleteOutputCleanup: baseProof}
	cloneResult := func() PublicationResult {
		result := baseResult
		proof := *baseProof
		proof.SHA256 = append([]byte(nil), baseProof.SHA256...)
		result.ObsoleteOutputCleanup = &proof
		return result
	}
	var initialJobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, ObsoletePublicationOutputCleanupJobKind).Scan(ctx, &initialJobs); err != nil {
		t.Fatal(err)
	}
	assertUnchanged := func(label string) {
		t.Helper()
		var current TrackPublication
		if err := f.db.NewSelect().Model(&current).Where("position_id=?", position).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		attempt, err := repository.ReadPublicationAttempt(ctx, f.guard.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		var authorities, jobs, cleanupOperations, oldReservations int
		for _, query := range []struct {
			dest  *int
			query string
			args  []any
		}{
			{&authorities, `SELECT count(*) FROM obsolete_publication_output_cleanup`, nil},
			{&cleanupOperations, `SELECT count(*) FROM operation WHERE kind=?`, []any{ObsoletePublicationOutputCleanupOperationKind}},
			{&jobs, `SELECT count(*) FROM river_job WHERE kind=?`, []any{ObsoletePublicationOutputCleanupJobKind}},
			{&oldReservations, `SELECT count(*) FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?`, []any{claim.ID, position, oldGeneration}},
		} {
			if err := f.db.NewRaw(query.query, query.args...).Scan(ctx, query.dest); err != nil {
				t.Fatal(err)
			}
		}
		if current.Generation != oldGeneration || current.RelativePath != oldPath || attempt == nil || authorities != 0 || cleanupOperations != 0 || jobs != initialJobs || oldReservations != 1 {
			t.Fatalf("%s changed rejected result state: current=%+v attempt=%+v authority/operations/jobs/reservations=%d/%d/%d/%d", label, current, attempt, authorities, cleanupOperations, jobs, oldReservations)
		}
	}
	for _, rejection := range []struct {
		name   string
		mutate func(*PublicationResult)
	}{
		{name: "missing proof", mutate: func(result *PublicationResult) { result.ObsoleteOutputCleanup = nil }},
		{name: "ownership mismatch", mutate: func(result *PublicationResult) { result.ObsoleteOutputCleanup.SHA256[0] ^= 1 }},
		{name: "namespace authority mismatch", mutate: func(result *PublicationResult) { result.ObsoleteOutputCleanup.NamespaceClaimID = uuid.New() }},
	} {
		result := cloneResult()
		rejection.mutate(&result)
		if committed, err := repository.CommitPublicationResult(ctx, f.guard, result); committed || err == nil {
			t.Fatalf("%s commit=%t err=%v, want rejected result", rejection.name, committed, err)
		}
		assertUnchanged(rejection.name)
	}

	repository.SetRiverInserter(obsoleteCleanupTestInserter{failAfterInsert: true})
	if committed, err := repository.CommitPublicationResult(ctx, f.guard, cloneResult()); committed || err == nil {
		t.Fatalf("injected enqueue failure commit=%t err=%v", committed, err)
	}
	assertUnchanged("enqueue rollback")

	repository.SetRiverInserter(obsoleteCleanupTestInserter{})
	result := cloneResult()
	committed, err := repository.CommitPublicationResult(ctx, f.guard, result)
	if !committed || err != nil {
		t.Fatalf("successful commit=%t err=%v", committed, err)
	}
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	attempt, err := repository.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var authorities, jobs, cleanupOperations, oldReservations int
	if err := f.db.NewRaw(`SELECT count(*) FROM obsolete_publication_output_cleanup`).Scan(ctx, &authorities); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=?`, ObsoletePublicationOutputCleanupOperationKind).Scan(ctx, &cleanupOperations); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, ObsoletePublicationOutputCleanupJobKind).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?`, claim.ID, position, oldGeneration).Scan(ctx, &oldReservations); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration || current.RelativePath != staged.RelativePath || attempt != nil || authorities != 1 || cleanupOperations != 1 || jobs != initialJobs+1 || oldReservations != 1 {
		t.Fatalf("incomplete atomic result: current=%+v attempt=%+v authority/operations/jobs/reservations=%d/%d/%d/%d", current, attempt, authorities, cleanupOperations, jobs, oldReservations)
	}
	if committed, err := repository.CommitPublicationResult(ctx, f.guard, result); err != nil || committed {
		t.Fatalf("completed redelivery=%t,%v", committed, err)
	}
	var afterJobs int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, ObsoletePublicationOutputCleanupJobKind).Scan(ctx, &afterJobs); err != nil || afterJobs != jobs {
		t.Fatalf("redelivery duplicated cleanup job: %d %v", afterJobs, err)
	}
}
