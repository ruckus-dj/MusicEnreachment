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
	for _, name := range []string{"success", "enqueue rollback", "missing proof", "wrong digest", "wrong mtime", "wrong size", "wrong epoch", "wrong path", "wrong claim"} {
		t.Run(name, func(t *testing.T) {
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
			proof := &ObsoletePublicationOutputCleanupAdmission{
				Epoch: previous.Epoch, PositionID: position, Generation: oldGeneration, RelativePath: oldPath,
				NativePath: filepath.Join(t.TempDir(), filepath.FromSlash(oldPath)), NamespaceClaimID: claim.ID,
				OutputRootIdentity: PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "root"},
				ParentIdentity:     PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "parent"},
				FileSize:           size, ModifiedAt: time.UnixMicro(mtime).UTC(), SHA256: append([]byte(nil), digest[:]...),
			}
			result := PublicationResult{Observation: staged, InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: staged.PayloadSHA256}}, ObsoleteOutputCleanup: proof}
			switch name {
			case "missing proof":
				result.ObsoleteOutputCleanup = nil
			case "wrong digest":
				proof.SHA256[0] ^= 1
			case "wrong mtime":
				proof.ModifiedAt = proof.ModifiedAt.Add(time.Microsecond)
			case "wrong size":
				proof.FileSize++
			case "wrong epoch":
				proof.Epoch = uuid.New()
			case "wrong path":
				proof.RelativePath = "unrelated.flac"
			case "wrong claim":
				proof.NamespaceClaimID = uuid.New()
			}
			repository.SetRiverInserter(obsoleteCleanupTestInserter{failAfterInsert: name == "enqueue rollback"})
			var beforeJobs int
			if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, ObsoletePublicationOutputCleanupJobKind).Scan(ctx, &beforeJobs); err != nil {
				t.Fatal(err)
			}
			committed, err := repository.CommitPublicationResult(ctx, f.guard, result)
			wantSuccess := name == "success"
			if committed != wantSuccess || (err == nil) != wantSuccess {
				t.Fatalf("commit=%t err=%v, success=%t", committed, err, wantSuccess)
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
			if oldReservations != 1 {
				t.Fatalf("old deletion reservation lost: %d", oldReservations)
			}
			if wantSuccess {
				if current.Generation != f.guard.CandidateGeneration || current.RelativePath != staged.RelativePath || attempt != nil || authorities != 1 || cleanupOperations != 1 || jobs != beforeJobs+1 {
					t.Fatalf("incomplete atomic result: current=%+v attempt=%+v authority/operations/jobs=%d/%d/%d", current, attempt, authorities, cleanupOperations, jobs)
				}
				if committed, err := repository.CommitPublicationResult(ctx, f.guard, result); err != nil || committed {
					t.Fatalf("completed redelivery=%t,%v", committed, err)
				}
				var afterJobs int
				if err := f.db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, ObsoletePublicationOutputCleanupJobKind).Scan(ctx, &afterJobs); err != nil || afterJobs != jobs {
					t.Fatalf("redelivery duplicated cleanup job: %d %v", afterJobs, err)
				}
			} else if current.Generation != oldGeneration || current.RelativePath != oldPath || attempt == nil || authorities != 0 || cleanupOperations != 0 || jobs != beforeJobs {
				t.Fatalf("rejected result was not rolled back: current=%+v attempt=%+v authority/operations/jobs=%d/%d/%d", current, attempt, authorities, cleanupOperations, jobs)
			}
		})
	}
}
