//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

func TestFrozenMatchingApplyRequestBindingWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	parentRunID, _ := insertFallbackReceiptMigrationScope(t, ctx, database)
	sealDigest := make([]byte, 32)
	sealDigest[0] = 1
	if _, err := database.NewRaw(`INSERT INTO matching_fallback_authority_seal
		(run_id,wait_revision,schema_version,authority,authority_digest)
		VALUES (?,1,3,'{"schema_version":3}'::jsonb,?)`, parentRunID, sealDigest).Exec(ctx); err != nil {
		t.Fatalf("insert referenced authority seal: %v", err)
	}

	type binding struct {
		schemaVersion          int
		inputSHA256            any
		frozenAuthorityVersion any
		parentExecutionEpoch   any
		sealWaitRevision       any
		sealDigest             any
		targetCaptureSHA256    any
	}
	validV2 := binding{
		schemaVersion: 2, inputSHA256: make([]byte, 32), frozenAuthorityVersion: 3,
		parentExecutionEpoch: int64(1), sealWaitRevision: int64(1), sealDigest: sealDigest,
		targetCaptureSHA256: make([]byte, 32),
	}
	insert := func(name string, value binding) (uuid.UUID, error) {
		t.Helper()
		operationID := uuid.New()
		if _, err := database.NewRaw(`INSERT INTO operation
			(id,kind,state,stage,input_snapshot,finished_at)
			VALUES (?, 'matching_apply_v1', 'succeeded', 'complete', '{}'::jsonb, now())`, operationID).Exec(ctx); err != nil {
			return uuid.Nil, err
		}
		_, err := database.NewRaw(`INSERT INTO matching_apply_request
			(group_id,parent_run_id,evaluation_revision,apply_operation_id,schema_version,input_sha256,
			 frozen_authority_version,parent_execution_epoch,seal_wait_revision,seal_digest,target_capture_sha256)
			VALUES (?,?,?, ?,?,?,?,?,?,?,?)`, uuid.New(), parentRunID, name, operationID, value.schemaVersion,
			value.inputSHA256, value.frozenAuthorityVersion, value.parentExecutionEpoch, value.sealWaitRevision,
			value.sealDigest, value.targetCaptureSHA256).Exec(ctx)
		return operationID, err
	}
	assertRejected := func(name string, value binding) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			if _, err := insert(name+"-"+uuid.NewString(), value); err == nil {
				t.Fatal("invalid frozen matching apply request was accepted")
			}
		})
	}

	if _, err := insert("legacy-v1", binding{schemaVersion: 1}); err != nil {
		t.Fatalf("insert legacy schema-1 request: %v", err)
	}
	frozenRequestID, err := insert("frozen-v2", validV2)
	if err != nil {
		t.Fatalf("insert complete schema-2 request: %v", err)
	}

	for _, field := range []struct {
		name string
		edit func(*binding)
	}{
		{"input_sha256", func(value *binding) { value.inputSHA256 = nil }},
		{"frozen_authority_version", func(value *binding) { value.frozenAuthorityVersion = nil }},
		{"parent_execution_epoch", func(value *binding) { value.parentExecutionEpoch = nil }},
		{"seal_wait_revision", func(value *binding) { value.sealWaitRevision = nil }},
		{"seal_digest", func(value *binding) { value.sealDigest = nil }},
		{"target_capture_sha256", func(value *binding) { value.targetCaptureSHA256 = nil }},
	} {
		invalid := validV2
		field.edit(&invalid)
		assertRejected("schema-2 null "+field.name, invalid)
	}

	assertRejected("unsupported schema version", binding{schemaVersion: 3})
	invalidAuthority := validV2
	invalidAuthority.frozenAuthorityVersion = 4
	assertRejected("unsupported frozen authority version", invalidAuthority)
	for _, field := range []struct {
		name string
		edit func(*binding)
	}{
		{"input_sha256", func(value *binding) { value.inputSHA256 = make([]byte, 31) }},
		{"seal_digest", func(value *binding) { value.sealDigest = make([]byte, 31) }},
		{"target_capture_sha256", func(value *binding) { value.targetCaptureSHA256 = make([]byte, 31) }},
	} {
		invalid := validV2
		field.edit(&invalid)
		assertRejected("invalid "+field.name+" length", invalid)
	}
	wrongSeal := validV2
	wrongSealDigest := make([]byte, 32)
	wrongSealDigest[0] = 2
	wrongSeal.sealDigest = wrongSealDigest
	assertRejected("seal digest foreign key mismatch", wrongSeal)

	if _, err := database.NewRaw(`UPDATE matching_apply_request SET input_sha256=? WHERE apply_operation_id=?`, make([]byte, 32), frozenRequestID).Exec(ctx); err == nil {
		t.Fatal("schema-2 request update was accepted")
	}
	if _, err := database.NewRaw(`DELETE FROM matching_apply_request WHERE apply_operation_id=?`, frozenRequestID).Exec(ctx); err == nil {
		t.Fatal("schema-2 request delete was accepted")
	}

	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migration collection: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if _, err := migrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "cannot roll back frozen matching apply request bindings") {
		t.Fatalf("rollback with populated schema-2 request error = %v, want safety guard", err)
	}
	var retainedSchemaVersion int
	if err := database.NewRaw(`SELECT schema_version FROM matching_apply_request WHERE apply_operation_id=?`, frozenRequestID).Scan(ctx, &retainedSchemaVersion); err != nil {
		t.Fatalf("read request after refused rollback: %v", err)
	}
	if retainedSchemaVersion != 2 {
		t.Fatalf("request after refused rollback has schema version %d, want 2", retainedSchemaVersion)
	}
	for _, column := range []string{"schema_version", "input_sha256", "frozen_authority_version", "parent_execution_epoch", "seal_wait_revision", "seal_digest", "target_capture_sha256"} {
		if !columnExists(t, database, "matching_apply_request", column) {
			t.Fatalf("refused rollback removed matching_apply_request.%s", column)
		}
	}
}
