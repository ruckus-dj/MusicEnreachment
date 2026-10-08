//go:build integration

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisWorkerProbeFailurePostgreSQL proves a real technical probe
// failure is handled distinctly from a missing managed tool and from a failed
// apply. The managed ffprobe still answers its -version query (so the pinned
// installation verifies) but its technical response fails: once with a nonzero
// exit and once with malformed JSON. In both cases the worker fails the analysis
// at the applying stage with the safe probe reason, keeps the previous variant
// linked, commits no new probe result, releases its work and managed-tool read
// holds, and really ran the probe.
func TestSourceAnalysisWorkerProbeFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"nonzero", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newAnalysisDispatchFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()

			// Analyze once successfully so the failing probe has a previous result
			// to preserve.
			baseline := fixture.start(t, ctx)
			awaitRiverCompletion(t, ctx, fixture.events, *baseline.RiverJobID)
			assertOperationStage(t, ctx, fixture.setup, baseline.ID, "succeeded", service.SourceAnalysisStageApplying)
			previous := fixture.requireLinkedVariant(t, ctx)
			probesBefore := scanDispatchProbeCount(t, fixture.probeLog)
			// A successful prior analysis populated the probe cache. Use a new
			// source identity so this delivery must invoke ffprobe rather than
			// correctly reusing that cached result.
			fixture.changeSourceBytes(t, ctx, "new bytes for uncached probe")

			// The pinned ffprobe keeps answering -version but fails the technical
			// probe itself.
			configureAnalysisProbe(t, fixture.helperPath, mode)
			failed := fixture.start(t, ctx)
			awaitRiverCompletion(t, ctx, fixture.events, *failed.RiverJobID)

			// The batch continues applying sibling steps, then reports failure
			// because its probe step failed.
			assertOperationStage(t, ctx, fixture.setup, failed.ID, "failed", service.SourceAnalysisStageApplying)
			requireAnalysisStepSafeError(t, ctx, fixture, failed.ID, persistence.SourceStepProbe, "managed ffprobe could not analyze the source file")
			requireAnalysisHolds(t, ctx, fixture, failed.ID, nil, nil)
			linked := fixture.requireLinkedVariant(t, ctx)
			var shaVariantID uuid.UUID
			if err := fixture.database.NewRaw(`SELECT success_sha_variant_id FROM source_analysis_step WHERE work_id=? AND step='sha256'`, fixture.work.ID).Scan(ctx, &shaVariantID); err != nil {
				t.Fatalf("read new canonical SHA identity: %v", err)
			}
			if linked != shaVariantID || linked == previous {
				t.Fatalf("linked variant = %s, want the fresh SHA identity %s rather than old result %s", linked, shaVariantID, previous)
			}
			if variants := fixture.countVariants(t, ctx); variants != 2 {
				t.Fatalf("media variants after the failed probe = %d, want the prior probe result and new SHA identity", variants)
			}
			if probes := scanDispatchProbeCount(t, fixture.probeLog); probes != probesBefore+1 {
				t.Fatalf("probes after the failed probe = %d, want exactly one more than %d", probes, probesBefore)
			}
		})
	}
}

func (fixture *analysisDispatchFixture) changeSourceBytes(t *testing.T, ctx context.Context, contents string) {
	t.Helper()
	path := filepath.Join(fixture.source, filepath.FromSlash(fixture.work.RelativePath))
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("change source bytes for cache miss: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat changed source: %v", err)
	}
	mtime := info.ModTime().UTC().Truncate(time.Microsecond)
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_analysis_work SET size_bytes=?,mtime=? WHERE id=?`, info.Size(), mtime, fixture.work.ID); err != nil {
		t.Fatalf("update normalized work for changed source: %v", err)
	}
	if _, err := fixture.database.ExecContext(ctx, `UPDATE source_location SET size_bytes=?,mtime=? WHERE id=?`, info.Size(), mtime, fixture.track.ID); err != nil {
		t.Fatalf("update inventory location for changed source: %v", err)
	}
	fixture.work.SizeBytes, fixture.work.Mtime = info.Size(), mtime
	fixture.track.SizeBytes, fixture.track.Mtime = info.Size(), mtime
}
