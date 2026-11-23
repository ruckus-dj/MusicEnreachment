package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingReceiptPublicationSource selects the immutable confirmation receipt
// and the exact confirmed association that publication intends to bind.
type MatchingReceiptPublicationSource struct {
	Kind          string    `json:"kind"`
	RequestID     uuid.UUID `json:"request_id"`
	TrackSourceID uuid.UUID `json:"track_source_id"`
}

const (
	MatchingReceiptKindManual = "manual"
	MatchingReceiptKindLocal  = "local"
)

// ReadMatchingReceiptPublicationSource proves that one publication position is
// still bound to the exact association recorded by a saved confirmation receipt.
// It does not read group membership or grant any source beyond that association.
func (repository *SetupManagerRepository) ReadMatchingReceiptPublicationSource(ctx context.Context, releaseID, positionID, variantID uuid.UUID, receipt MatchingReceiptPublicationSource) error {
	return readMatchingReceiptPublicationSource(ctx, repository.db, releaseID, positionID, variantID, receipt)
}

// readMatchingReceiptPublicationSource is tx-compatible so admission can verify
// the same receipt tuple inside its own transaction. Every check is one
// statement: the receipt and the live library rows share a snapshot. A caller
// admitting publication must hold both the output admission and source
// fingerprint mutation gates in its transaction to keep the binding current.
func readMatchingReceiptPublicationSource(ctx context.Context, db bun.IDB, releaseID, positionID, variantID uuid.UUID, receipt MatchingReceiptPublicationSource) error {
	if releaseID == uuid.Nil || positionID == uuid.Nil || variantID == uuid.Nil ||
		receipt.RequestID == uuid.Nil || receipt.TrackSourceID == uuid.Nil {
		return fmt.Errorf("read matching receipt publication source: invalid selector: %w", ErrPublicationAdmissionConflict)
	}
	var receiptQuery string
	var receiptArgs []any
	switch receipt.Kind {
	case MatchingReceiptKindManual:
		// The column is the manual receipt's immutable release binding; a retired
		// receipt has no live release and cannot authorize publication.
		receiptQuery = `SELECT response FROM manual_matching_receipt
			WHERE request_id=? AND local_release_id=? AND original_local_release_id=?
				AND retired_by_operation_id IS NULL`
		receiptArgs = []any{receipt.RequestID, releaseID, releaseID}
	case MatchingReceiptKindLocal:
		// Local confirmation has no release column: its immutable response names
		// the materialized release that the caller must match exactly.
		receiptQuery = `SELECT response FROM local_matching_receipt
			WHERE request_id=? AND response->>'local_release_id'=?`
		receiptArgs = []any{receipt.RequestID, releaseID.String()}
	default:
		return fmt.Errorf("read matching receipt publication source: invalid kind: %w", ErrPublicationAdmissionConflict)
	}
	args := append(receiptArgs, positionID, releaseID, receipt.TrackSourceID, variantID)
	var proof struct {
		Response json.RawMessage `bun:"response,type:jsonb"`
		Live     bool            `bun:"live"`
	}
	err := db.NewRaw(`WITH receipt AS (`+receiptQuery+`),
		live AS (
			SELECT 1
			FROM release_track position
			JOIN local_release live_release ON live_release.id=position.release_id
			JOIN track_source association ON association.track_id=position.track_id
			JOIN media_variant variant ON variant.id=association.media_variant_id
			WHERE position.id=? AND position.release_id=?
				AND association.id=? AND association.media_variant_id=?
				AND association.compatible IS TRUE
		)
		SELECT (SELECT response FROM receipt) AS response, EXISTS(SELECT 1 FROM live) AS live`,
		args...).Scan(ctx, &proof)
	if err != nil {
		return fmt.Errorf("read matching receipt publication source: %w", err)
	}
	var saved struct {
		LocalReleaseID   uuid.UUID                 `json:"local_release_id"`
		ConfirmedSources []MatchingConfirmedSource `json:"confirmed_sources"`
	}
	if proof.Live && json.Unmarshal(proof.Response, &saved) == nil && saved.LocalReleaseID == releaseID {
		for _, source := range saved.ConfirmedSources {
			if source.FileMemberID != uuid.Nil && source.PositionID == positionID &&
				source.TrackSourceID == receipt.TrackSourceID && source.MediaVariantID == variantID {
				return nil
			}
		}
	}
	return fmt.Errorf("read matching receipt publication source: receipt does not bind the live association: %w", ErrPublicationAdmissionConflict)
}
