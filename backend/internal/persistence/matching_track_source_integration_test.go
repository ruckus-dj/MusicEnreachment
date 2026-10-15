//go:build integration

package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestAttachTrackSourcePromotesOnlyTheConfirmedPairPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	variantID, trackID, otherTrackID := uuid.New(), uuid.New(), uuid.New()
	associationID := uuid.New()
	policy := "matching-test-v1"
	if _, err := db.NewRaw(`INSERT INTO media_variant(id,size_bytes,ffprobe_version,ffprobe_json,analysis_policy_version,observed_tags,inspected_at,applied_operation_id) VALUES (?,0,'test','{}'::jsonb,1,'{}'::jsonb,now(),?)`, variantID, uuid.New()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{trackID, otherTrackID} {
		if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, id, "fixture").Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?,true,false,'automatic',?,'{"preserve":true}'::jsonb)`, associationID, trackID, variantID, policy).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return attachTrackSource(ctx, tx, trackID, variantID, materializationAuthority{method: "manual"})
	})
	if err != nil {
		t.Fatalf("promote confirmed association: %v", err)
	}
	var association struct {
		ID             uuid.UUID `bun:"id"`
		Selected       bool      `bun:"selected"`
		Compatible     bool      `bun:"compatible"`
		DecisionMethod string    `bun:"decision_method"`
		PolicyVersion  *string   `bun:"policy_version"`
		Metadata       string    `bun:"metadata"`
	}
	if err := db.NewRaw(`SELECT id,selected,compatible,decision_method,policy_version,metadata::text FROM track_source WHERE track_id=? AND media_variant_id=?`, trackID, variantID).Scan(ctx, &association); err != nil {
		t.Fatal(err)
	}
	if association.ID != associationID || !association.Selected || association.Compatible || association.DecisionMethod != "manual" || association.PolicyVersion != nil || association.Metadata != `{"preserve": true}` {
		t.Fatalf("promoted source association did not preserve its identity/state: %+v", association)
	}

	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return attachTrackSource(ctx, tx, otherTrackID, variantID, materializationAuthority{method: "automatic", policyVersion: &policy})
	})
	if !errors.Is(err, ErrMatchingManualProtection) {
		t.Fatalf("automatic reuse of manually protected source error = %v", err)
	}
	var links int
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE media_variant_id=?`, variantID).Scan(ctx, &links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("automatic protected-source attempt left %d associations, want 1", links)
	}
}
