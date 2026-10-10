//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestFailMatchingDeliveryRequiresCurrentFreshFencePostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name  string
		stale func(context.Context, *bun.DB) error
	}{
		{name: "valid owner"},
		{
			name: "configuration changed after claim",
			stale: func(ctx context.Context, db *bun.DB) error {
				_, err := db.NewRaw(`UPDATE app_setting SET setting_value='new-identity' WHERE setting_name='musicbrainz_config_identity'`).Exec(ctx)
				return err
			},
		},
		{
			name: "grouping refresh required after claim",
			stale: func(ctx context.Context, db *bun.DB) error {
				_, err := db.NewRaw(`UPDATE incoming_grouping_state SET needs_refresh=true WHERE singleton=true`).Exec(ctx)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			fixture := newIncomingMatchFixture(t, ctx, db)
			repository := persistence.NewSetupManagerRepository(db)
			candidateRepository := persistence.NewIncomingMatchCandidateRepository(db)
			priorCandidate := fixture.candidate(fixture.memberA, .73)
			if err := candidateRepository.Replace(ctx, fixture.evaluation([]persistence.IncomingMatchCandidate{priorCandidate})); err != nil {
				t.Fatalf("seed prior matching candidate: %v", err)
			}

			identity := "incoming-match-integration"
			cacheKey := "matching-failure-fence"
			parentID, childID := uuid.New(), uuid.New()
			parent := &persistence.Operation{ID: parentID, Kind: "matching_evaluation_v1", State: "queued", Stage: "waiting_provider", InputSnapshot: json.RawMessage(`{}`)}
			child := &persistence.Operation{
				ID: childID, Kind: "provider_release_lookup", State: "queued", Stage: "fetch", InputSnapshot: json.RawMessage(`{"query":"failure-fence"}`),
				ProviderSourceID: &fixture.source, ProviderConfigurationIdentity: &identity,
				ProviderID: &fixture.provider, ProviderEndpoint: "https://musicbrainz.example/ws/2", ProviderCacheKey: &cacheKey,
			}
			admitted, err := repository.AdmitMatchingRun(ctx, openScanEnqueueRiver(t, db), persistence.MatchingAdmission{
				AdmissionKey: "failure-fence-" + parentID.String(), GroupingEpoch: fixture.groupingEpoch,
				Targets: []persistence.MatchingTarget{{GroupID: fixture.group.ID, Revision: fixture.group.Revision}},
				Plan:    json.RawMessage(`{}`), PolicyVersion: "policy-v1", Parent: parent,
				ParentArgs: persistence.MatchingEvaluationWakeArgs{OperationID: parentID},
				Providers:  []persistence.MatchingProviderRequest{{Operation: child, Args: service.ProviderFetchOperationArgs{OperationID: childID}}},
			})
			if err != nil || admitted.RiverJobID == nil {
				t.Fatalf("admit matching run: operation=%+v err=%v", admitted, err)
			}
			jobID := *admitted.RiverJobID
			if _, err := db.NewRaw(`UPDATE river_job SET state='running', attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
				t.Fatalf("start registered River delivery: %v", err)
			}
			claim, outcome, err := repository.ClaimMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, 1)
			if err != nil || outcome != persistence.MatchingDeliveryClaimed {
				t.Fatalf("claim matching delivery: claim=%+v outcome=%v err=%v", claim, outcome, err)
			}
			if test.stale != nil {
				if err := test.stale(ctx, db); err != nil {
					t.Fatalf("make matching delivery stale: %v", err)
				}
			}
			changed, err := repository.FailMatchingDelivery(ctx, parentID, admitted.Attempt, jobID, claim, "The saved matching evaluation plan is invalid or unsupported.")
			if err != nil {
				t.Fatalf("fail matching delivery: %v", err)
			}
			wantChanged := test.stale == nil
			if changed != wantChanged {
				t.Fatalf("failure changed=%t, want %t", changed, wantChanged)
			}

			operation, err := repository.GetOperation(ctx, parentID)
			if err != nil {
				t.Fatal(err)
			}
			var run struct {
				State              string `bun:"state"`
				ExecutionEpoch     int64  `bun:"execution_epoch"`
				ActiveRiverJobID   *int64 `bun:"active_river_job_id"`
				ActiveRiverAttempt *int   `bun:"active_river_attempt"`
			}
			if err := db.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt FROM matching_run WHERE id=?`, parentID).Scan(ctx, &run); err != nil {
				t.Fatal(err)
			}
			var consumed bool
			if err := db.NewRaw(`SELECT consumed_at IS NOT NULL FROM matching_delivery WHERE run_id=? AND river_job_id=?`, parentID, jobID).Scan(ctx, &consumed); err != nil {
				t.Fatal(err)
			}
			if wantChanged {
				if operation.State != "failed" || operation.SafeError == nil || run.State != "failed" || run.ActiveRiverJobID != nil || run.ActiveRiverAttempt != nil || !consumed {
					t.Fatalf("valid owned failure state: operation=%+v run=%+v consumed=%t", operation, run, consumed)
				}
			} else if operation.State != "running" || operation.SafeError != nil || run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != jobID || run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != 1 || consumed {
				t.Fatalf("stale failure changed ownership: operation=%+v run=%+v consumed=%t", operation, run, consumed)
			}
			_, candidates, err := candidateRepository.Get(ctx, fixture.group.ID)
			if err != nil || len(candidates) != 1 || candidates[0].Score != priorCandidate.Score {
				t.Fatalf("failure changed prior candidates: candidates=%+v err=%v", candidates, err)
			}
		})
	}
}
