//go:build integration

package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun/migrate"
)

func TestPublicationAttemptCoverInputRollbackRefusesRetainedReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	ctx := context.Background()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, migration := range collection.Sorted() {
		if migration.Name < "20261029000003" {
			prior.Add(migration)
		}
		if migration.Name == "20261029000003" {
			target.Add(migration)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatalf("target migration count = %d", len(target.Sorted()))
	}
	priorMigrator := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := priorMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := priorMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	targetMigrator := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := targetMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID, positionID, operationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'cover rollback')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'cover rollback')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, []any{positionID, releaseID, trackID}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, []any{operationID}},
		{`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?,?,'audio','queued','{}'::jsonb,'{}'::jsonb,'[]'::jsonb)`, []any{operationID, positionID, uuid.New(), uuid.New()}},
	} {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	imageBytes := publicationAttemptCoverPNG(t, 12)
	digest := sha256.Sum256(imageBytes)
	generation := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO publication_attempt_cover_input
		(operation_id,position_id,release_id,authority_revision,authority_mode,state,generation,image_bytes,image_mime,image_sha256,image_size)
		VALUES (?,?,?,1,'automatic','value',?,?,'image/png',?,?)`, operationID, positionID, releaseID, generation, imageBytes, digest[:], len(imageBytes)); err != nil {
		t.Fatalf("insert valid frozen cover receipt: %v", err)
	}
	alternateBytes := publicationAttemptCoverPNG(t, 200)
	alternateDigest := sha256.Sum256(alternateBytes)
	immutabilityCases := []struct {
		name, query string
		args        []any
	}{
		{"bytes, digest, and generation", `UPDATE publication_attempt_cover_input SET image_bytes=?,image_sha256=?,generation=? WHERE operation_id=?`, []any{alternateBytes, alternateDigest[:], uuid.New(), operationID}},
		{"authority revision", `UPDATE publication_attempt_cover_input SET authority_revision=authority_revision+1 WHERE operation_id=?`, []any{operationID}},
		{"authority mode", `UPDATE publication_attempt_cover_input SET authority_mode='manual' WHERE operation_id=?`, []any{operationID}},
		{"state", `UPDATE publication_attempt_cover_input SET state='absent' WHERE operation_id=?`, []any{operationID}},
		{"ownership binding", `UPDATE publication_attempt_cover_input SET operation_id=? WHERE operation_id=?`, []any{uuid.New(), operationID}},
	}
	for _, test := range immutabilityCases {
		if _, err := db.ExecContext(ctx, test.query, test.args...); err == nil || !strings.Contains(err.Error(), "publication attempt cover inputs are immutable") {
			t.Errorf("receipt mutation %q error = %v, want immutable refusal", test.name, err)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM publication_attempt_cover_input WHERE operation_id=?`, operationID); err == nil || !strings.Contains(err.Error(), "cannot delete publication attempt cover input while its attempt exists") {
		t.Errorf("live receipt deletion error = %v, want refusal", err)
	}
	if _, err := targetMigrator.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "refusing to drop populated publication_attempt_cover_input") {
		t.Fatalf("rollback with retained receipt = %v, want refusal", err)
	}
	var retained bool
	if err := db.NewRaw(`SELECT to_regclass('public.publication_attempt_cover_input') IS NOT NULL`).Scan(ctx, &retained); err != nil || !retained {
		t.Fatalf("receipt table retained = %t, %v", retained, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM track_publication_attempt WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err := db.NewRaw(`SELECT count(*) FROM publication_attempt_cover_input WHERE operation_id=?`, operationID).Scan(ctx, &receipts); err != nil || receipts != 0 {
		t.Fatalf("attempt deletion cascade left %d cover receipts: %v", receipts, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM operation WHERE id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := targetMigrator.Rollback(ctx); err != nil {
		t.Fatalf("rollback empty receipt table: %v", err)
	}
}

func publicationAttemptCoverPNG(t *testing.T, value uint8) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.SetRGBA(0, 0, color.RGBA{R: value, G: value / 2, B: value / 3, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, imageData); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
