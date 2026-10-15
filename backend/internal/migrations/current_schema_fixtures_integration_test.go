//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// insertMatchingCurrentSchemaScope creates a minimal current-format matching
// receipt scope for catalog constraint tests. It deliberately writes only the
// current schema shape; historical migrations and policy versions are not
// loaded to manufacture this fixture.
func insertMatchingCurrentSchemaScope(t *testing.T, ctx context.Context, database *bun.DB, configurationIdentities ...string) (uuid.UUID, int64) {
	t.Helper()
	configurationIdentity := "current-config"
	if len(configurationIdentities) > 0 {
		configurationIdentity = configurationIdentities[0]
	}
	runID, sourceID, providerID := uuid.New(), uuid.New(), uuid.New()
	if _, err := database.NewRaw(`INSERT INTO provider(id,code) VALUES (?,?)`, providerID, "current-schema-test-"+providerID.String()).Exec(ctx); err != nil {
		t.Fatalf("create current-schema receipt provider: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "current-schema-test", "https://example.invalid", configurationIdentity).Exec(ctx); err != nil {
		t.Fatalf("create current-schema receipt provider source: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at)
		VALUES (?, 'matching_evaluation_v1', 'succeeded', 'complete', '{}'::jsonb, now())`, runID).Exec(ctx); err != nil {
		t.Fatalf("create current-schema matching operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_run(
			id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state)
		VALUES (?,?,?,?, '{}'::jsonb,0,0,?,?,?,'succeeded')`, runID, runID, "current-schema-"+runID.String(),
		make([]byte, 32), sourceID, configurationIdentity, "current-schema-test-policy").Exec(ctx); err != nil {
		t.Fatalf("create current-schema matching run: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_acquisition(run_id,intent_version,intent,intent_digest,current_phase,revision)
		VALUES (?,3,'{}'::jsonb,?,'fallback_applicability',1)`, runID, make([]byte, 32)).Exec(ctx); err != nil {
		t.Fatalf("create current-schema acquisition: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance)
		VALUES (?,1,'fallback_applicability','{"stage_index":0}'::jsonb,'{"stage_index":0}'::jsonb,'[]'::jsonb)`, runID).Exec(ctx); err != nil {
		t.Fatalf("create current-schema applicability phase: %v", err)
	}
	return runID, 1
}
