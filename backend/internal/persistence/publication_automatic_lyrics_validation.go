package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

func validateAutomaticLyricsRetryCurrentAuthority(ctx context.Context, tx bun.Tx, positionID uuid.UUID, authority PublicationAutomaticLyricsRetryInput) (bool, error) {
	var raw []byte
	var revision int64
	if err := tx.NewRaw(`SELECT metadata,metadata_revision FROM release_track WHERE id=? FOR UPDATE`, positionID).Scan(ctx, &raw, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock current automatic lyrics retry authority: %w", err)
	}
	if revision != authority.ExpectedMetadataRevision {
		return false, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return false, nil
	}
	var envelope librarymetadata.Envelope
	if local := root["local_metadata"]; len(local) == 0 {
		envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	} else {
		var err error
		envelope, err = librarymetadata.DecodeEnvelope(local)
		if err != nil {
			return false, nil
		}
	}
	if envelope.Scope != librarymetadata.ScopePosition {
		return false, nil
	}
	var current *librarymetadata.FieldValue
	if field, exists := envelope.Desired["synced_lyrics"]; exists {
		copy := field
		copy.Value = clonePublicationJSONValue(field.Value)
		current = &copy
	}
	return sameJSONValue(json.RawMessage(mustJSON(current)), json.RawMessage(mustJSON(authority.Desired))), nil
}

func validateAutomaticLyricsRetryAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission, input *PublicationInputs, current TrackPublication) error {
	retry := input.AutomaticLyricsRetry
	if retry == nil || retry.ExpectedMetadataRevision != request.metadataRevision || retry.ConfirmedCurrentGeneration != current.Generation {
		return fmt.Errorf("admit automatic lyrics retry: metadata revision or generation changed: %w", ErrPublicationAdmissionConflict)
	}
	var raw []byte
	var revision int64
	if err := tx.NewRaw(`SELECT metadata,metadata_revision FROM release_track WHERE id=? FOR UPDATE`, request.PositionID).Scan(ctx, &raw, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("admit automatic lyrics retry: position metadata not found: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("admit automatic lyrics retry: lock position metadata: %w", err)
	}
	if revision != request.metadataRevision {
		return fmt.Errorf("admit automatic lyrics retry: metadata revision changed: %w", ErrPublicationAdmissionConflict)
	}
	var currentMetadata publicationCurrentMetadata
	if err := decodePublicationCurrentContract(current.ActualMetadata, &currentMetadata); err != nil || currentMetadata.Version != 1 || currentMetadata.Tags == nil {
		return errors.New("admit automatic lyrics retry: current publication metadata is invalid")
	}
	mandatory := make(map[string][]string, len(currentMetadata.Tags))
	for name, values := range currentMetadata.Tags {
		if !strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			mandatory[name] = append(make([]string, 0, len(values)), values...)
		}
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		return fmt.Errorf("admit automatic lyrics retry: current publication assets are invalid: %w", err)
	}
	var metadataRoot map[string]json.RawMessage
	if err := json.Unmarshal(raw, &metadataRoot); err != nil || metadataRoot == nil {
		return errors.New("admit automatic lyrics retry: current metadata is invalid")
	}
	var envelope librarymetadata.Envelope
	if local := metadataRoot["local_metadata"]; len(local) != 0 {
		envelope, err = librarymetadata.DecodeEnvelope(local)
		if err != nil || envelope.Scope != librarymetadata.ScopePosition {
			return errors.New("admit automatic lyrics retry: current lyrics authority is invalid")
		}
	} else {
		envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	}
	var desired *librarymetadata.FieldValue
	if field, exists := envelope.Desired["synced_lyrics"]; exists {
		copy := field
		copy.Value = clonePublicationJSONValue(field.Value)
		desired = &copy
	}
	input.DesiredTags = mandatory
	retry.ExpectedMetadataRevision = revision
	retry.ConfirmedCurrentGeneration = current.Generation
	retry.Desired = desired
	retry.CurrentAssets = assets
	if err := input.Validate(); err != nil {
		return fmt.Errorf("admit automatic lyrics retry: current frozen contract is invalid: %w", err)
	}
	return nil
}
