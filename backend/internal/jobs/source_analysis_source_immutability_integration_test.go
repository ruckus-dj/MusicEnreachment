//go:build integration

package jobs

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// The fixture's ffprobe/fpcalc executables are fakes: this verifies filesystem
// immutability across worker orchestration, not behavior of real media tools.
func TestSourceAnalysisWorkerPreservesSourceBytesAndMetadataWithPostgreSQL(t *testing.T) {
	for _, mode := range []string{"in_place", "staged"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := newAnalysisDispatchFixtureWithoutPendingWork(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			fixture.fpcalcID = fixture.installAnalysisDispatchFPCalc(t, ctx)
			workRoot, _, err := fixture.registry.GetWorkDirectory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			workRoot, err = filepath.EvalSymlinks(workRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.settings.Set(ctx, settings.WorkDirectoryKey, workRoot); err != nil {
				t.Fatal(err)
			}

			sourcePath := filepath.Join(fixture.source, "album", "track.flac")
			beforeBytes, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatalf("read source before analysis: %v", err)
			}
			beforeInfo, err := os.Stat(sourcePath)
			if err != nil {
				t.Fatalf("stat source before analysis: %v", err)
			}
			if beforeInfo.Size() != fixture.work.SizeBytes || !beforeInfo.ModTime().Truncate(time.Microsecond).Equal(fixture.work.Mtime) {
				t.Fatalf("source identity = %d/%s, inventory = %d/%s", beforeInfo.Size(), beforeInfo.ModTime(), fixture.work.SizeBytes, fixture.work.Mtime)
			}
			beforeHash := sha256.Sum256(beforeBytes)
			if err := os.Chtimes(sourcePath, beforeInfo.ModTime(), beforeInfo.ModTime()); err != nil {
				t.Fatalf("pin source mtime: %v", err)
			}
			beforeInfo, err = os.Stat(sourcePath)
			if err != nil {
				t.Fatalf("restat source: %v", err)
			}

			operation := fixture.startAllRequestedSteps(t, ctx)
			awaitRiverCompletion(t, ctx, fixture.events, *operation.RiverJobID)
			assertOperationStage(t, ctx, fixture.setup, operation.ID, "succeeded", service.SourceAnalysisStageApplying)
			for _, step := range []persistence.SourceStepName{persistence.SourceStepSHA256, persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
				var state string
				if err := fixture.database.NewRaw(`SELECT state FROM source_analysis_step WHERE work_id=? AND step=?`, fixture.work.ID, string(step)).Scan(ctx, &state); err != nil {
					t.Fatalf("read %s outcome: %v", step, err)
				}
				if state != "succeeded" {
					t.Errorf("%s state = %q, want succeeded", step, state)
				}
			}
			afterBytes, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatalf("read source after analysis: %v", err)
			}
			afterInfo, err := os.Stat(sourcePath)
			if err != nil {
				t.Fatalf("stat source after analysis: %v", err)
			}
			if got := sha256.Sum256(afterBytes); got != beforeHash || string(afterBytes) != string(beforeBytes) || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
				t.Fatalf("source changed during analysis: hash %x/%x size %d/%d mtime %s/%s", got, beforeHash, afterInfo.Size(), beforeInfo.Size(), afterInfo.ModTime(), beforeInfo.ModTime())
			}

			var artifacts int
			if err := fixture.database.NewRaw(`SELECT count(*) FROM source_analysis_artifact WHERE work_id=?`, fixture.work.ID).Scan(ctx, &artifacts); err != nil {
				t.Fatalf("count staged artifacts: %v", err)
			}
			if artifacts != 0 {
				t.Fatalf("%s artifacts = %d, want none", mode, artifacts)
			}
			if mode == "in_place" {
				return
			}
			var processingMode string
			if err := fixture.database.NewRaw(`SELECT processing_mode FROM source_analysis_work_execution
				WHERE work_id=? AND operation_id=? AND operation_attempt=? AND job_id=?`,
				fixture.work.ID, operation.ID, operation.Attempt, *operation.RiverJobID).Scan(ctx, &processingMode); err != nil {
				t.Fatalf("read immutable source analysis execution mode: %v", err)
			}
			if processingMode != service.SourceProcessingModeStaged {
				t.Fatalf("source analysis execution mode = %q, want staged", processingMode)
			}
			var bound bool
			if err := fixture.database.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM source_analysis_work_artifact_binding WHERE work_id=?
			)`, fixture.work.ID).Scan(ctx, &bound); err != nil {
				t.Fatalf("check staged artifact binding after settlement: %v", err)
			}
			if bound {
				t.Fatal("staged artifact remains bound after successful settlement")
			}
			files, err := filepath.Glob(filepath.Join(workRoot, "analysis", "staging", "*", "*", "*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("staged files after successful analysis = %v, error %v", files, err)
			}
		})
	}
}
