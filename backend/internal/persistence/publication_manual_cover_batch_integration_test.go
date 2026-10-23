//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type coverBatchInserter struct {
	RiverInserter
	calls  int
	failAt int
}

func (inserter *coverBatchInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.RiverInserter.InsertTx(ctx, tx, args, opts)
	inserter.calls++
	if err == nil && inserter.calls == inserter.failAt {
		return nil, errors.New("injected failure after real River insert")
	}
	return result, err
}

func TestReleaseCoverBatchAdmissionAtomicFanoutPostgreSQL(t *testing.T) {
	for _, scenario := range []string{"success", "second_insert_failure", "busy", "retry_pending"} {
		t.Run(scenario, func(t *testing.T) {
			db := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			client := openPublicationRetryRiver(t, ctx, db)
			repository := NewSetupManagerRepository(db)
			releaseID := uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'cover batch')`, releaseID); err != nil {
				t.Fatal(err)
			}
			var epoch uuid.UUID
			if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
				t.Fatal(err)
			}
			installationID := uuid.New()
			versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
			verified := time.Now().UTC()
			path := filepath.Join("ffmpeg", "release-1")
			if err := repository.CreateInstallation(ctx, &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: path, State: "ready", ExecutableVersions: versions, ArtifactIdentities: mustJSON(map[string]any{}), VerifiedAt: &verified}); err != nil {
				t.Fatal(err)
			}
			tools := []SourceAnalysisToolSelection{
				{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(path), Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner},
				{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(path), Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner},
			}
			positions := make([]uuid.UUID, 3)
			for index := range positions {
				positionID, trackID := uuid.New(), uuid.New()
				positions[index] = positionID
				if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'cover track')`, trackID); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position,metadata) VALUES (?,?,?,?,'{}'::jsonb)`, positionID, releaseID, trackID, index+1); err != nil {
					t.Fatal(err)
				}
				if index == 2 {
					continue
				}
				input := publicationInputsFixture(t, "rewrite")
				generation := uuid.New()
				relative := fmt.Sprintf("track-%d.mka", index)
				if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, positionID, epoch, generation, relative,
					string(mustJSON(PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership})), string(mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})), string(mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"published title"}}})), `{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`); err != nil {
					t.Fatal(err)
				}
				if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					return reservePublicationNamespace(ctx, tx, PublicationAdmission{PositionID: positionID, ExpectedEpoch: epoch, CandidateGeneration: generation, NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: relative, RelativePath: relative, NodeKind: "file"}}})
				}); err != nil {
					t.Fatal(err)
				}
			}
			library := NewLibraryRepository(db)
			pendingGeneration := uuid.New()
			pending, err := library.SetPendingReleaseCover(ctx, releaseID, 1, pendingGeneration, publicationAdmissionPNG(t), "image/png")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "busy" {
				first, err := repository.AdmitReleaseCoverApply(ctx, ReleaseCoverApplyAdmission{ReleaseID: releaseID, ExpectedEpoch: epoch, ExpectedRevision: pending.Revision, ExpectedPendingGeneration: pendingGeneration, Tools: tools}, client)
				if err != nil || len(first.Operations) != 2 {
					t.Fatalf("establish busy baseline: %+v, %v", first, err)
				}
				pendingGeneration = uuid.New()
				pending, err = library.SetPendingReleaseCover(ctx, releaseID, first.Revision, pendingGeneration, publicationAdmissionPNG(t), "image/png")
				if err != nil {
					t.Fatal(err)
				}
			}
			inserter := &coverBatchInserter{RiverInserter: client}
			if scenario == "second_insert_failure" {
				inserter.failAt = 2
			}
			var beforeJobs int
			if err := db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &beforeJobs); err != nil {
				t.Fatal(err)
			}
			var batch ReleaseCoverBatchAdmission
			if scenario == "retry_pending" {
				selected, selectErr := library.ApplyPendingReleaseCover(ctx, releaseID, pending.Revision, pendingGeneration)
				if selectErr != nil {
					t.Fatal(selectErr)
				}
				pending, err = library.SetPendingReleaseCover(ctx, releaseID, selected.Revision, uuid.New(), publicationAdmissionPNG(t), "image/png")
				if err != nil {
					t.Fatal(err)
				}
				batch, err = repository.AdmitReleaseCoverRetry(ctx, ReleaseCoverRetryAdmission{ReleaseID: releaseID, ExpectedEpoch: epoch, ExpectedRevision: pending.Revision, ExpectedSelectedGeneration: pendingGeneration, Tools: tools}, inserter)
			} else {
				batch, err = repository.AdmitReleaseCoverApply(ctx, ReleaseCoverApplyAdmission{ReleaseID: releaseID, ExpectedEpoch: epoch, ExpectedRevision: pending.Revision, ExpectedPendingGeneration: pendingGeneration, Tools: tools}, inserter)
			}
			current, readErr := library.ReadReleaseCover(ctx, releaseID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var afterJobs int
			if scanErr := db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &afterJobs); scanErr != nil {
				t.Fatal(scanErr)
			}
			if scenario == "second_insert_failure" || scenario == "busy" {
				if err == nil || current.Revision != pending.Revision || current.PendingGeneration == nil || *current.PendingGeneration != pendingGeneration || afterJobs != beforeJobs {
					t.Fatalf("failed batch changed authority/jobs: err=%v cover=%+v jobs=%d->%d", err, current, beforeJobs, afterJobs)
				}
				if scenario == "second_insert_failure" && inserter.calls != 2 {
					t.Fatalf("failure did not follow two real inserts: %d", inserter.calls)
				}
				return
			}
			if err != nil || len(batch.Operations) != 2 || afterJobs-beforeJobs != 2 {
				t.Fatalf("batch fanout=%+v jobs=%d->%d err=%v", batch, beforeJobs, afterJobs, err)
			}
			if scenario == "retry_pending" && !sameOptionalUUID(current.PendingGeneration, pending.PendingGeneration) {
				t.Fatal("retry consumed unrelated pending image")
			}
			for _, operation := range batch.Operations {
				attempt, err := repository.ReadPublicationAttempt(ctx, operation.OperationID)
				if err != nil || attempt == nil || operation.PositionID == positions[2] {
					t.Fatalf("invalid admitted position: %+v, %v", operation, err)
				}
				input, err := DecodePublicationInputs(attempt.FrozenInputs)
				if err != nil || input.ManualCover == nil || input.ManualCover.SelectedGeneration != pendingGeneration || input.Audio != nil {
					t.Fatalf("invalid frozen cover input: %+v, %v", input, err)
				}
				receipt, err := scanPublicationAttemptCoverInput(ctx, db, operation.OperationID)
				if err != nil || receipt.Generation == nil || *receipt.Generation != pendingGeneration {
					t.Fatalf("missing cover receipt: %+v, %v", receipt, err)
				}
			}
			var sourceHolds, toolHolds int
			if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold`).Scan(ctx, &sourceHolds); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold`).Scan(ctx, &toolHolds); err != nil {
				t.Fatal(err)
			}
			if sourceHolds != 0 || toolHolds != 2 {
				t.Fatalf("source-free holds: sources=%d tools=%d", sourceHolds, toolHolds)
			}
		})
	}
}
