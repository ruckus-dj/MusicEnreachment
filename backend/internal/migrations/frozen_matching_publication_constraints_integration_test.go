//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestFrozenMatchingPublicationHeaderConstraintsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	providerID, sourceID := uuid.New(), uuid.New()
	if _, err := database.NewRaw(`INSERT INTO provider (id,code) VALUES (?,?)`, providerID, "frozen-publication-constraint-test").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.NewRaw(`INSERT INTO provider_source (id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,?,?,?)`,
		sourceID, providerID, "frozen-publication-constraint-test", "https://example.invalid", "test").Exec(ctx); err != nil {
		t.Fatal(err)
	}

	insert := func(groupID uuid.UUID, authorityVersion any, runID any, waitRevision any, digest any, targetRunID any, fenceVersion int, targetCapture any, targetDigest any, authorityMode string) error {
		_, err := database.NewRaw(`INSERT INTO incoming_match_evaluation
			(group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,
			provider_source_id,provider_id,provider_configuration_identity,provider_fences,matching_authority_mode,
			fence_version,target_run_id,target_capture,target_capture_digest,
			frozen_authority_version,frozen_run_id,frozen_wait_revision,frozen_seal_digest)
			VALUES (?,?,'{}'::jsonb,'group','groups',0,'frozen-test',?,?,?,'{}'::jsonb,?,?,?,?,?,?,?,?,?)`,
			groupID, uuid.NewString(), sourceID, providerID, "test", authorityMode, fenceVersion, targetRunID, targetCapture, targetDigest,
			authorityVersion, runID, waitRevision, digest).Exec(ctx)
		return err
	}
	assertRejected := func(name string, args ...any) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := insert(uuid.New(), args[0], args[1], args[2], args[3], args[4], args[5].(int), args[6], args[7], args[8].(string)); err == nil {
				t.Fatal("malformed frozen header was accepted")
			}
		})
	}
	wait := int64(1)
	captureDigest := make([]byte, 32)
	capture := `{"schema_version":2}`

	// The referenced tuple includes the exact digest, not merely run and wait.
	sealRunID, _ := insertMatchingCurrentSchemaScope(t, ctx, database)
	sealDigest := make([]byte, 32)
	sealDigest[0] = 1
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_authority_seal
		(run_id,wait_revision,schema_version,authority,authority_digest) VALUES (?,1,1,'{"schema_version":1}'::jsonb,?)`, sealRunID, sealDigest).Exec(ctx); err != nil {
		t.Fatalf("insert referenced authority seal: %v", err)
	}
	if err := insert(uuid.New(), 1, sealRunID, wait, sealDigest, sealRunID, 2, capture, captureDigest, "automatic"); err != nil {
		t.Fatalf("insert valid frozen v1 header: %v", err)
	}
	assertRejected("partial frozen authority nulls", 1, sealRunID, wait, nil, sealRunID, 2, capture, captureDigest, "automatic")
	assertRejected("frozen header target run is null", 1, sealRunID, wait, sealDigest, nil, 2, capture, captureDigest, "automatic")
	assertRejected("unsupported provider authority version", 4, sealRunID, wait, sealDigest, sealRunID, 2, capture, captureDigest, "automatic")
	assertRejected("legacy target fence", 1, sealRunID, wait, sealDigest, sealRunID, 1, nil, nil, "automatic")
	assertRejected("non-automatic authority", 1, sealRunID, wait, sealDigest, sealRunID, 2, capture, captureDigest, "manual_review")

	formerAuthority := insert(uuid.New(), 3, sealRunID, wait, sealDigest, sealRunID, 2, capture, captureDigest, "automatic")
	var pgErr *pgconn.PgError
	if !errors.As(formerAuthority, &pgErr) || pgErr.SQLState() != "23514" || pgErr.ConstraintName != "incoming_match_evaluation_frozen_authority_all_or_none" {
		t.Fatalf("former frozen authority version 3 error = %v, want incoming_match_evaluation_frozen_authority_all_or_none check violation", formerAuthority)
	}

	wrongDigest := append([]byte(nil), sealDigest...)
	wrongDigest[0] = 2
	assertRejected("seal digest mismatch", 1, sealRunID, wait, wrongDigest, sealRunID, 2, capture, captureDigest, "automatic")
}
