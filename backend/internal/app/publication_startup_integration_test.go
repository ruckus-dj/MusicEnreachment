package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestRunStopsBeforePlatformInitializationWhenPublicationRecoveryIsRequired(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	operationID, positionID, generation := uuid.New(), uuid.New(), uuid.New()
	releaseID, trackID := uuid.New(), uuid.New()
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton = true`).Scan(ctx, &epoch); err != nil {
		t.Fatalf("read publication epoch: %v", err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id) VALUES (?)`, releaseID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id) VALUES (?)`, trackID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_track(id, release_id, "position", track_id) VALUES (?, ?, 0, ?)`, positionID, releaseID, trackID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation
			(id, kind, state, stage, input_snapshot)
			VALUES (?, 'publish_track_v1', 'running', 'preparing', ?::jsonb)`, operationID,
			fmt.Sprintf(`{"version":1,"position_id":%q,"output_epoch":%q,"intent_kind":"audio"}`, positionID, epoch)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO track_publication_attempt
			(operation_id, position_id, epoch, candidate_generation, intent_kind, phase,
			 frozen_inputs, namespace_manifest, mutation_journal)
			VALUES (?, ?, ?, ?, 'audio', 'preparing', '{}'::jsonb, '{}'::jsonb, '[]'::jsonb)`,
			operationID, positionID, epoch, generation)
		return err
	}); err != nil {
		t.Fatalf("seed retained publication state: %v", err)
	}

	var settingsBefore, operationsBefore, attemptsBefore int
	for _, snapshot := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM app_setting`, &settingsBefore},
		{`SELECT count(*) FROM operation`, &operationsBefore},
		{`SELECT count(*) FROM track_publication_attempt`, &attemptsBefore},
	} {
		if err := db.NewRaw(snapshot.query).Scan(ctx, snapshot.into); err != nil {
			t.Fatalf("snapshot startup rows: %v", err)
		}
	}

	err := Run(ctx, Config{DatabaseURL: testpostgres.URL(t, db)})
	const want = "publication recovery is required before startup; publication state is inconsistent"
	if err == nil || err.Error() != want {
		t.Fatalf("Run error = %v, want safe publication recovery error %q", err, want)
	}
	var settingsAfter, operationsAfter, attemptsAfter, resetJournals int
	for _, snapshot := range []struct {
		query string
		into  *int
	}{
		{`SELECT count(*) FROM app_setting`, &settingsAfter},
		{`SELECT count(*) FROM operation`, &operationsAfter},
		{`SELECT count(*) FROM track_publication_attempt`, &attemptsAfter},
		{`SELECT count(*) FROM output_reset_journal`, &resetJournals},
	} {
		if err := db.NewRaw(snapshot.query).Scan(ctx, snapshot.into); err != nil {
			t.Fatalf("read startup rows after Run: %v", err)
		}
	}
	if settingsAfter != settingsBefore {
		t.Errorf("platform initialization wrote %d app settings; before=%d after=%d", settingsAfter-settingsBefore, settingsBefore, settingsAfter)
	}
	if operationsAfter != operationsBefore || attemptsAfter != attemptsBefore {
		t.Errorf("Run changed retained rows: operations %d->%d, attempts %d->%d", operationsBefore, operationsAfter, attemptsBefore, attemptsAfter)
	}
	if resetJournals != 0 {
		t.Errorf("Run created %d output-reset recovery journal rows before rejecting publication state", resetJournals)
	}
}
