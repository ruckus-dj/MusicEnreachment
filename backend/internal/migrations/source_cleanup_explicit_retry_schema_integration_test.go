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

func TestExplicitSourceCleanupRetrySchemaRequiresExactSnapshotAndGrantPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	position, epoch, original, claim := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	snapshot := func(extra bool) string {
		value := `{"version":1,"position_id":"` + position.String() + `","output_epoch":"` + epoch.String() + `","original_operation_id":"` + original.String() + `","original_claim_uuid":"` + claim.String() + `"`
		if extra {
			value += `,"unapproved":"value"`
		}
		return value + `}`
	}
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at)
		VALUES (?,'retry_publication_sources_v1','failed','test',?::jsonb,'settled',now())`, uuid.New(), snapshot(true)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "operation_publication_snapshot_check") {
		t.Fatalf("extra recovery snapshot field accepted: %v", err)
	}
	// Establish that the terminal operation shape used by the rejection cases is
	// valid before testing omissions in otherwise identical snapshots.
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at)
		VALUES (?,'retry_publication_sources_v1','failed','test',?::jsonb,'settled',now())`, uuid.New(), snapshot(false)).Exec(ctx); err != nil {
		t.Fatalf("valid terminal recovery operation rejected: %v", err)
	}
	for _, missing := range []string{"original_operation_id", "original_claim_uuid", "both"} {
		invalid := snapshot(false)
		switch missing {
		case "original_operation_id":
			invalid = strings.Replace(invalid, `,"original_operation_id":"`+original.String()+`"`, "", 1)
		case "original_claim_uuid":
			invalid = strings.Replace(invalid, `,"original_claim_uuid":"`+claim.String()+`"`, "", 1)
		case "both":
			invalid = strings.Replace(invalid, `,"original_operation_id":"`+original.String()+`"`, "", 1)
			invalid = strings.Replace(invalid, `,"original_claim_uuid":"`+claim.String()+`"`, "", 1)
		}
		if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,safe_error,finished_at)
			VALUES (?,'retry_publication_sources_v1','failed','test',?::jsonb,'settled',now())`, uuid.New(), invalid).Exec(ctx); err == nil || !strings.Contains(err.Error(), "operation_publication_snapshot_check") {
			t.Errorf("terminal retry snapshot missing %s accepted: %v", missing, err)
		}
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot)
			VALUES (?,'retry_publication_sources_v1','queued','queued',?::jsonb)`, uuid.New(), snapshot(false)).Exec(ctx)
		return err
	}); err == nil || !strings.Contains(err.Error(), "active source cleanup retry requires an exact grant") {
		t.Fatalf("active retry without grant = %v", err)
	}
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM pg_constraint WHERE conname IN (
		'source_cleanup_explicit_retry_original_operation_fkey',
		'source_cleanup_explicit_retry_original_claim_fkey',
		'source_cleanup_explicit_retry_one_outstanding',
		'source_cleanup_explicit_retry_consumed_claim_fkey')`).Scan(ctx, &count); err != nil || count != 4 {
		t.Fatalf("explicit retry restrictive/unique constraints=%d err=%v", count, err)
	}
	var matchType string
	if err := db.NewRaw(`SELECT confmatchtype::text FROM pg_constraint WHERE conname='source_cleanup_explicit_retry_consumed_claim_fkey'`).Scan(ctx, &matchType); err != nil || matchType != "s" {
		t.Fatalf("consumed claim foreign key must permit an unconsumed grant: match=%q err=%v", matchType, err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM pg_constraint WHERE conrelid='public.source_cleanup_explicit_retry_item'::regclass AND contype='f'`).Scan(ctx, &count); err != nil || count != 3 {
		t.Fatalf("explicit retry item foreign keys=%d err=%v", count, err)
	}
}
