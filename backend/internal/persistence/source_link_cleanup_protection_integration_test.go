//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestSourceLinkCleanupOwnerProtectsTrackSourceMutationsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, variantID := publicationSourceCleanupReadyFixture(t)
	collection := NewCollectionRepository(f.db)
	var trackID uuid.UUID
	if err := f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}

	manual := TrackSourceDecision{TrackID: trackID, MediaVariantID: variantID, Compatible: true, Method: "manual", Metadata: json.RawMessage(`{}`)}
	if err := collection.UpsertTrackSourceDecision(ctx, manual); err != nil {
		t.Fatalf("baseline manual track-source upsert: %v", err)
	}
	attach := func(method string, policy *string) error {
		return f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
				return err
			}
			return attachTrackSource(ctx, tx, trackID, variantID, materializationAuthority{method: method, policyVersion: policy})
		})
	}
	if err := attach("manual", nil); err != nil {
		t.Fatalf("baseline manual attach: %v", err)
	}

	// Make the existing association automatic so the post-owner manual attach
	// also proves that cleanup ownership prevents promotion.
	policy := "source-cleanup-protection-test-v1"
	if _, err := f.db.NewRaw(`UPDATE track_source SET decision_method='automatic',policy_version=? WHERE track_id=? AND media_variant_id=?`, policy, trackID, variantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := attach("automatic", &policy); err != nil {
		t.Fatalf("baseline automatic attach: %v", err)
	}

	request := PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}
	admitted, err := repo.AdmitPublicationSourceCleanup(ctx, request, &publicationSourceCleanupTestInserter{})
	if err != nil || admitted.OperationID == uuid.Nil || len(admitted.Blockers) != 0 {
		t.Fatalf("admit cleanup owner = %+v, %v", admitted, err)
	}
	var owners int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Scan(ctx, &owners); err != nil || owners == 0 {
		t.Fatalf("cleanup owner rows = %d, %v; want owner for operation %s", owners, err, admitted.OperationID)
	}
	if _, err := f.db.NewRaw(`UPDATE operation SET state='failed',safe_error='fixture cleanup failure',finished_at=now() WHERE id=?`, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatalf("fail cleanup operation: %v", err)
	}

	if err := collection.UpsertTrackSourceDecision(ctx, manual); !errors.Is(err, ErrSourceCleanupOwned) {
		t.Fatalf("owned manual track-source upsert error = %v, want ErrSourceCleanupOwned", err)
	}
	if err := attach("automatic", &policy); !errors.Is(err, ErrSourceCleanupOwned) {
		t.Fatalf("owned automatic attach error = %v, want ErrSourceCleanupOwned", err)
	}
	if err := attach("manual", nil); !errors.Is(err, ErrSourceCleanupOwned) {
		t.Fatalf("owned manual promotion error = %v, want ErrSourceCleanupOwned", err)
	}
	var method string
	var storedPolicy *string
	if err := f.db.NewRaw(`SELECT decision_method,policy_version FROM track_source WHERE track_id=? AND media_variant_id=?`, trackID, variantID).Scan(ctx, &method, &storedPolicy); err != nil {
		t.Fatal(err)
	}
	if method != "automatic" || storedPolicy == nil || *storedPolicy != policy {
		t.Fatalf("rejected mutations changed association: method=%q policy=%v", method, storedPolicy)
	}

	if _, err := f.db.NewRaw(`DELETE FROM source_cleanup_owner WHERE operation_id=?`, admitted.OperationID).Exec(ctx); err != nil {
		t.Fatalf("remove cleanup owner: %v", err)
	}
	if err := attach("manual", nil); err != nil {
		t.Fatalf("manual attach after owner removal: %v", err)
	}
	if err := collection.UpsertTrackSourceDecision(ctx, manual); err != nil {
		t.Fatalf("manual upsert after owner removal: %v", err)
	}
	if err := f.db.NewRaw(`SELECT decision_method FROM track_source WHERE track_id=? AND media_variant_id=?`, trackID, variantID).Scan(ctx, &method); err != nil {
		t.Fatal(err)
	}
	if method != "manual" {
		t.Fatalf("association method after owner removal = %q, want manual", method)
	}
}
