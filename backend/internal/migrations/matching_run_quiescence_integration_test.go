//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestMatchingRunQuiescenceTimestampGuardsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	t.Run("insert rejects preexisting marker", func(t *testing.T) {
		runID := insertUnquiescedMatchingRunTimestampFixture(t, ctx, database)
		if _, err := database.NewRaw(`INSERT INTO matching_run(
			id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version,quiesced_at
		) SELECT ?,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version,transaction_timestamp()-interval '1 day'
		  FROM matching_run WHERE id=?`, uuid.New(), runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "cannot be inserted already quiesced") {
			t.Fatalf("insert of an already-quiesced run error = %v, want insert guard", err)
		}
	})

	t.Run("backdated update is rejected and database timestamp is permanent", func(t *testing.T) {
		runID := insertUnquiescedMatchingRunTimestampFixture(t, ctx, database)
		if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp()-interval '1 day' WHERE id=?`, runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "database-authored") {
			t.Fatalf("backdated quiescence update error = %v, want database-authored guard", err)
		}
		if _, err := database.NewRaw(`UPDATE matching_run SET quiesced_at=transaction_timestamp() WHERE id=?`, runID).Exec(ctx); err != nil {
			t.Fatalf("set database-authored quiescence timestamp: %v", err)
		}
		var markerPresent bool
		if err := database.NewRaw(`SELECT quiesced_at IS NOT NULL FROM matching_run WHERE id=?`, runID).Scan(ctx, &markerPresent); err != nil {
			t.Fatalf("read quiescence timestamp: %v", err)
		}
		if !markerPresent {
			t.Fatal("database-authored quiescence timestamp was not stored")
		}
		for _, update := range []string{
			`UPDATE matching_run SET quiesced_at=NULL WHERE id=?`,
			`UPDATE matching_run SET quiesced_at=quiesced_at+interval '1 second' WHERE id=?`,
		} {
			if _, err := database.NewRaw(update, runID).Exec(ctx); err == nil || !strings.Contains(err.Error(), "write-once") {
				t.Fatalf("change to permanent quiescence timestamp using %q error = %v, want write-once guard", update, err)
			}
		}
	})
}

func insertUnquiescedMatchingRunTimestampFixture(t *testing.T, ctx context.Context, database *bun.DB) uuid.UUID {
	t.Helper()
	runID := uuid.New()
	providerID, sourceID := uuid.New(), uuid.New()
	configuration := "quiescence-timestamp-fixture"
	if _, err := database.NewRaw(`INSERT INTO provider(id,code) VALUES (?,?)`, providerID, "migration-test-"+providerID.String()).Exec(ctx); err != nil {
		t.Fatalf("insert matching provider: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "migration-test", "https://example.invalid", configuration).Exec(ctx); err != nil {
		t.Fatalf("insert matching provider source: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,created_at,updated_at)
		VALUES (?, 'matching_evaluation_v1','running','evaluating','{}'::jsonb,1,now(),now())`, runID).Exec(ctx); err != nil {
		t.Fatalf("insert matching operation: %v", err)
	}
	if _, err := database.NewRaw(`INSERT INTO matching_run(
			id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,
			provider_source_id,provider_configuration_identity,policy_version,state,fence_version
		) VALUES (?,?,?,?, '{}'::jsonb,0,0,?,?,?,'queued',2)`, runID, runID, "migration-test-"+runID.String(),
		make([]byte, 32), sourceID, configuration, "migration-test-policy").Exec(ctx); err != nil {
		t.Fatalf("insert unquiesced version-2 matching run: %v", err)
	}
	return runID
}
