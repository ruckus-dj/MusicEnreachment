//go:build integration

package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// TestSourceAnalysisWorkerProbeFailurePostgreSQL proves a real technical probe
// failure is handled distinctly from a missing managed tool and from a failed
// apply. The managed ffprobe still answers its -version query (so the pinned
// installation verifies) but its technical response fails: once with a nonzero
// exit and once with malformed JSON. In both cases the worker fails the analysis
// at the probing stage with the safe probe reason, keeps the previous variant
// linked, commits no new variant, releases both read holds, and really ran the
// probe.
func TestSourceAnalysisWorkerProbeFailurePostgreSQL(t *testing.T) {
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

			// The pinned ffprobe keeps answering -version but fails the technical
			// probe itself.
			configureAnalysisProbe(t, fixture.helperPath, mode)
			failed := fixture.start(t, ctx)
			awaitRiverCompletion(t, ctx, fixture.events, *failed.RiverJobID)

			assertOperationStage(t, ctx, fixture.setup, failed.ID, "failed", service.SourceAnalysisStageProbing)
			requireScanDispatchSafeError(t, fixture.readOperation(t, ctx, failed.ID), analysisSafeProbe)
			requireAnalysisHolds(t, fixture.readOperation(t, ctx, failed.ID), nil, nil)
			if fixture.requireLinkedVariant(t, ctx) != previous {
				t.Fatal("a failed technical probe changed the linked variant")
			}
			if variants := fixture.countVariants(t, ctx); variants != 1 {
				t.Fatalf("media variants after the failed probe = %d, want the previous one alone", variants)
			}
			if probes := scanDispatchProbeCount(t, fixture.probeLog); probes != probesBefore+1 {
				t.Fatalf("probes after the failed probe = %d, want exactly one more than %d", probes, probesBefore)
			}
		})
	}
}
