package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type ManualLyricsDesiredState string

const (
	ManualLyricsValue         ManualLyricsDesiredState = "value"
	ManualLyricsExplicitEmpty ManualLyricsDesiredState = "explicit_empty"
)

// PublicationManualLyricsReceipt is bounded confirmation state, not source provenance.
type PublicationManualLyricsReceipt struct {
	bun.BaseModel       `bun:"table:publication_manual_lyrics_receipt"`
	PositionID          uuid.UUID                `bun:"position_id,pk,type:uuid"`
	Epoch               uuid.UUID                `bun:"epoch,type:uuid"`
	DesiredRevision     int64                    `bun:"desired_revision"`
	ConfirmedGeneration uuid.UUID                `bun:"confirmed_generation,type:uuid"`
	PublishedGeneration uuid.UUID                `bun:"published_generation,type:uuid"`
	DesiredState        ManualLyricsDesiredState `bun:"desired_state"`
	DesiredSHA256       *string                  `bun:"desired_sha256,nullzero"`
	WarningCode         *string                  `bun:"warning_code,nullzero"`
	RecordedAt          time.Time                `bun:"recorded_at"`
}

type ManualLyricsObserved struct {
	MetadataPresent bool
	MetadataValue   string
	Assets          PublicationAssetOutcomes
}

var manualLyricsReceiptHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateManualLyricsObservation(state ManualLyricsDesiredState, digest *string, observed ManualLyricsObserved) error {
	if state != ManualLyricsValue && state != ManualLyricsExplicitEmpty {
		return errors.New("unsupported manual lyrics desired state")
	}
	if err := observed.Assets.Validate(); err != nil {
		return fmt.Errorf("observed publication assets are invalid: %w", err)
	}
	if state == ManualLyricsValue {
		if digest == nil || !manualLyricsReceiptHash.MatchString(*digest) {
			return errors.New("manual lyrics desired digest is invalid")
		}
		hash := sha256.Sum256([]byte(observed.MetadataValue))
		if !observed.MetadataPresent || observed.MetadataValue == "" || hex.EncodeToString(hash[:]) != *digest {
			return errors.New("observed LYRICS metadata does not match manual lyrics digest")
		}
		if err := observed.Assets.ValidateAgainstMetadata(map[string][]string{"LYRICS": {observed.MetadataValue}}, nil); err != nil {
			return fmt.Errorf("observed lyrics assets are inconsistent: %w", err)
		}
		return nil
	}
	if digest != nil || observed.MetadataPresent && observed.MetadataValue != "" || observed.Assets.Lyrics.State != PublicationAssetAbsent {
		return errors.New("explicit-empty lyrics require observed absence")
	}
	return nil
}

// ManualLyricsReceiptProjection deliberately distinguishes current publication facts
// from receipt confirmation facts. A confirmed G0 does not confirm current G1.
type ManualLyricsReceiptProjection struct {
	MetadataRevision  int64
	ActiveEpoch       uuid.UUID
	CurrentGeneration *uuid.UUID
	Receipt           *PublicationManualLyricsReceipt
	Pending           bool
}

// ReadManualLyricsReceiptProjection reads the desired revision, active epoch,
// current publication and receipt in one SQL statement/snapshot.
func (repository *SetupManagerRepository) ReadManualLyricsReceiptProjection(ctx context.Context, positionID uuid.UUID) (ManualLyricsReceiptProjection, error) {
	var row struct {
		MetadataRevision    int64           `bun:"metadata_revision"`
		ActiveEpoch         uuid.UUID       `bun:"active_epoch"`
		Generation          *uuid.UUID      `bun:"generation,type:uuid"`
		ReceiptEpoch        *uuid.UUID      `bun:"receipt_epoch,type:uuid"`
		DesiredRevision     *int64          `bun:"desired_revision"`
		ConfirmedGeneration *uuid.UUID      `bun:"confirmed_generation,type:uuid"`
		PublishedGeneration *uuid.UUID      `bun:"published_generation,type:uuid"`
		DesiredState        *string         `bun:"desired_state"`
		DesiredSHA256       *string         `bun:"desired_sha256"`
		WarningCode         *string         `bun:"warning_code"`
		RecordedAt          *time.Time      `bun:"recorded_at"`
		ActualMetadata      json.RawMessage `bun:"actual_metadata,type:jsonb"`
		ActualAssets        json.RawMessage `bun:"actual_assets,type:jsonb"`
	}
	err := repository.db.NewRaw(`SELECT rt.metadata_revision, ns.output_epoch AS active_epoch,
		tp.generation, r.epoch AS receipt_epoch, r.desired_revision,
		r.confirmed_generation, r.published_generation, r.desired_state,
		r.desired_sha256, r.warning_code, r.recorded_at,
		tp.actual_metadata, tp.actual_assets
		FROM release_track rt CROSS JOIN publication_namespace_state ns
		LEFT JOIN track_publication tp ON tp.position_id=rt.id
		LEFT JOIN publication_manual_lyrics_receipt r ON r.position_id=rt.id
		WHERE rt.id=? AND ns.singleton=true`, positionID).Scan(ctx, &row)
	if err != nil {
		return ManualLyricsReceiptProjection{}, fmt.Errorf("read manual lyrics receipt projection: %w", err)
	}
	out := ManualLyricsReceiptProjection{MetadataRevision: row.MetadataRevision, ActiveEpoch: row.ActiveEpoch, CurrentGeneration: row.Generation}
	if row.DesiredRevision != nil {
		out.Receipt = &PublicationManualLyricsReceipt{PositionID: positionID, Epoch: *row.ReceiptEpoch, DesiredRevision: *row.DesiredRevision, ConfirmedGeneration: *row.ConfirmedGeneration, PublishedGeneration: *row.PublishedGeneration, DesiredState: ManualLyricsDesiredState(*row.DesiredState), DesiredSHA256: row.DesiredSHA256, WarningCode: row.WarningCode, RecordedAt: *row.RecordedAt}
		out.Pending = row.ReceiptEpoch == nil || *row.ReceiptEpoch != row.ActiveEpoch || *row.DesiredRevision != row.MetadataRevision || row.Generation == nil || *row.PublishedGeneration != *row.Generation
		if !out.Pending {
			assets, assetErr := DecodePublicationAssetOutcomes(row.ActualAssets)
			lyrics, present, metadataErr := EncodeObservedManualLyricsMetadata(row.ActualMetadata)
			out.Pending = assetErr != nil || metadataErr != nil || validateManualLyricsObservation(out.Receipt.DesiredState, out.Receipt.DesiredSHA256, ManualLyricsObserved{MetadataPresent: present, MetadataValue: lyrics, Assets: assets}) != nil
		}
	}
	if out.Receipt == nil {
		out.Pending = true
	}
	return out, nil
}

// EncodeObservedManualLyricsMetadata extracts the factual single-value LYRICS field.
func EncodeObservedManualLyricsMetadata(raw json.RawMessage) (string, bool, error) {
	var metadata PublicationCommitMetadata
	if err := strictMetadata(raw, &metadata); err != nil {
		return "", false, err
	}
	if metadata.Version != 1 {
		return "", false, fmt.Errorf("unsupported publication metadata version %d", metadata.Version)
	}
	values, ok := metadata.Tags["LYRICS"]
	if !ok {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, errors.New("observed LYRICS metadata must contain exactly one value")
	}
	return values[0], true, nil
}
