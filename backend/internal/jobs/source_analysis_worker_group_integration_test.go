//go:build integration

package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestSourceAnalysisWorkerRunsSHAThenCombinedGroupPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		probeFails bool
	}{
		{name: "digest is shared by probe and fingerprint"},
		{name: "probe failure does not prevent fingerprint apply", probeFails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAnalysisDispatchFixtureWithPreparer(t, &recordingAnalysisPreparer{probeFails: test.probeFails})
			fixture.installAnalysisDispatchFPCalc(t, context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()

			if _, err := fixture.database.ExecContext(ctx,
				"UPDATE source_analysis_step SET state='pending', step_attempt=0 WHERE work_id=? AND step='fingerprint'", fixture.work.ID); err != nil {
				t.Fatalf("make fingerprint work pending: %v", err)
			}
			operation := fixture.start(t, ctx)
			awaitRiverCompletion(t, ctx, fixture.events, *operation.RiverJobID)

			assertOperationStage(t, ctx, fixture.setup, operation.ID, "succeeded", service.SourceAnalysisStageApplying)
			requireAnalysisHolds(t, ctx, fixture, operation.ID, nil, nil)
			var snapshot persistence.SourceAnalysisOperationSnapshot
			if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
				t.Fatalf("decode admitted tool pins: %v", err)
			}
			if len(snapshot.Tools) != 2 {
				t.Fatalf("pinned analysis tools = %+v, want exactly ffprobe and fpcalc", snapshot.Tools)
			}
			pinned := map[string]uuid.UUID{}
			for _, tool := range snapshot.Tools {
				pinned[tool.Executable] = tool.InstallationID
			}
			if pinned["ffprobe"] != fixture.installationID || pinned["fpcalc"] != fixture.fpcalcID {
				t.Fatalf("pinned tool installations = %v, want verified ffprobe %s and fpcalc %s", pinned, fixture.installationID, fixture.fpcalcID)
			}
			assertAnalysisGroupSteps(t, ctx, fixture, test.probeFails)
			preparer := fixture.worker.preparer.(*recordingAnalysisPreparer)
			preparer.assertRequests(t)
			var savedDigest []byte
			if err := fixture.database.NewRaw(`SELECT variant.source_sha256
				FROM source_analysis_step AS step JOIN media_variant AS variant ON variant.id=step.success_sha_variant_id
				WHERE step.work_id=? AND step.step='sha256'`, fixture.work.ID).Scan(ctx, &savedDigest); err != nil {
				t.Fatalf("read applied SHA digest: %v", err)
			}
			wantDigest := sha256.Sum256([]byte("audio bytes"))
			if got, want := hex.EncodeToString(savedDigest), hex.EncodeToString(wantDigest[:]); got != want {
				t.Fatalf("applied SHA digest = %s, want %s", got, want)
			}
			var shaVariantID, probeVariantID uuid.UUID
			if err := fixture.database.NewRaw(`SELECT success_sha_variant_id FROM source_analysis_step WHERE work_id=? AND step='sha256'`, fixture.work.ID).Scan(ctx, &shaVariantID); err != nil {
				t.Fatalf("read canonical SHA variant identity: %v", err)
			}
			if !test.probeFails {
				if err := fixture.database.NewRaw(`SELECT success_probe_variant_id FROM source_analysis_step WHERE work_id=? AND step='probe'`, fixture.work.ID).Scan(ctx, &probeVariantID); err != nil {
					t.Fatalf("read selected probe variant identity: %v", err)
				}
				if probeVariantID != shaVariantID {
					t.Fatalf("probe variant identity = %s, want canonical SHA identity %s", probeVariantID, shaVariantID)
				}
				if variants := fixture.countVariants(t, ctx); variants != 1 {
					t.Fatalf("canonical SHA/probe rows = %d, want one shared identity", variants)
				}
			}
		})
	}
}

type recordingAnalysisPreparer struct {
	probeFails bool
	requests   []service.SourceAnalysisPrepareRequest
}

func (preparer *recordingAnalysisPreparer) Prepare(_ context.Context, request service.SourceAnalysisPrepareRequest) service.SourceAnalysisPreparation {
	preparer.requests = append(preparer.requests, request)
	result := service.SourceAnalysisPreparation{
		SHA256:      service.SourceAnalysisSHA256Outcome{SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisNotRequested}},
		Probe:       service.SourceAnalysisProbeOutcome{SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisNotRequested}},
		Fingerprint: service.SourceAnalysisFingerprintOutcome{SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisNotRequested}},
	}
	if request.Targets&service.SourceAnalysisTargetSHA256 != 0 {
		result.SHA256 = service.SourceAnalysisSHA256Outcome{
			SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded},
			Digest:                    sha256.Sum256([]byte("audio bytes")),
		}
	}
	if request.Targets&service.SourceAnalysisTargetProbe != 0 {
		if preparer.probeFails {
			result.Probe = service.SourceAnalysisProbeOutcome{
				SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisFailed, SafeError: "probe failed in group"},
			}
		} else {
			policy, version, inspected := persistence.SourceAnalysisPolicyVersion, analysisDispatchRelease, time.Now().UTC()
			digest := sha256.Sum256([]byte("audio bytes"))
			result.Probe = service.SourceAnalysisProbeOutcome{
				SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded},
				Result: &persistence.SourceMediaVariant{
					ID: uuid.New(), AnalysisPolicyVersion: &policy, FFProbeVersion: &version,
					InspectedAt: &inspected, FFProbeJSON: []byte(`{}`), ObservedTags: []byte(`{}`),
					SourceSHA256: digest[:],
				},
			}
		}
	}
	if request.Targets&service.SourceAnalysisTargetFingerprint != 0 {
		result.Fingerprint = service.SourceAnalysisFingerprintOutcome{
			SourceAnalysisStepOutcome: service.SourceAnalysisStepOutcome{State: service.SourceAnalysisSucceeded},
			Result: &persistence.SourceFingerprintResult{
				ID: uuid.New(), FPCalcVersion: analysisDispatchRelease, VersionBanner: "fpcalc version " + analysisDispatchRelease,
				AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: "AQID",
				ReportedDuration: 1, CalculatedAt: time.Now().UTC(), ParserContractVersion: 1,
			},
		}
	}
	return result
}

func (preparer *recordingAnalysisPreparer) assertRequests(t *testing.T) {
	t.Helper()
	if len(preparer.requests) != 2 {
		t.Fatalf("preparer calls = %d, want SHA first and one combined probe/fingerprint call", len(preparer.requests))
	}
	if got := preparer.requests[0].Targets; got != service.SourceAnalysisTargetSHA256 || preparer.requests[0].ExistingSHA256 != nil {
		t.Fatalf("first preparation request = targets %b, existing digest %v; want SHA only and no digest", got, preparer.requests[0].ExistingSHA256)
	}
	combined := preparer.requests[1]
	wantTargets := service.SourceAnalysisTargetProbe | service.SourceAnalysisTargetFingerprint
	if combined.Targets != wantTargets {
		t.Fatalf("second preparation targets = %b, want probe|fingerprint %b", combined.Targets, wantTargets)
	}
	wantDigest := sha256.Sum256([]byte("audio bytes"))
	if combined.ExistingSHA256 == nil || hex.EncodeToString(combined.ExistingSHA256[:]) != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("second preparation digest = %v, want SHA-256 of analyzed source", combined.ExistingSHA256)
	}
	if combined.BypassFingerprintCache {
		t.Fatal("ordinary batch fingerprint preparation unexpectedly bypassed the cache")
	}
}

func assertAnalysisGroupSteps(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, probeFails bool) {
	t.Helper()
	var steps []persistence.SourceAnalysisStep
	if err := fixture.database.NewSelect().Model(&steps).Where("work_id = ?", fixture.work.ID).Order("step").Scan(ctx); err != nil {
		t.Fatalf("read persisted analysis steps: %v", err)
	}
	want := map[string]string{"sha256": "succeeded", "probe": "succeeded", "fingerprint": "succeeded"}
	if probeFails {
		want["probe"] = "failed"
	}
	for _, step := range steps {
		if expected, ok := want[step.Step]; ok {
			if step.State != expected || step.StepAttempt != 1 {
				t.Errorf("%s step = state %q, attempt %d; want %q, attempt 1", step.Step, step.State, step.StepAttempt, expected)
			}
			switch step.Step {
			case string(persistence.SourceStepSHA256):
				if step.SuccessSHAVariantID == nil {
					t.Error("successful SHA step has no selected digest variant")
				}
			case string(persistence.SourceStepProbe):
				if probeFails && step.SuccessProbeVariantID != nil {
					t.Error("failed probe step selected a probe result")
				}
				if !probeFails && step.SuccessProbeVariantID == nil {
					t.Error("successful probe step has no selected probe result")
				}
			case string(persistence.SourceStepFingerprint):
				if step.SuccessFingerprintResultID == nil {
					t.Error("successful fingerprint step has no saved result")
				}
			}
			delete(want, step.Step)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing persisted steps: %v", want)
	}
}
