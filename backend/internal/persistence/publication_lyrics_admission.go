package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// ManualLyricsAdmission couples a rewrite snapshot to the metadata revision
// that the caller reviewed. FrozenInputs is treated as a claim and is rebuilt
// from the locked publication and metadata rows before it is persisted.
type ManualLyricsAdmission struct {
	PublicationAdmission
	ExpectedReleaseID        uuid.UUID
	ExpectedMetadataRevision int64
}

type publicationCurrentMetadata struct {
	Version int                 `json:"version"`
	Tags    map[string][]string `json:"tags"`
}

// AdmitManualLyricsRewrite atomically admits a source-free lyrics rewrite.
func (repository *SetupManagerRepository) AdmitManualLyricsRewrite(ctx context.Context, request ManualLyricsAdmission, client RiverInserter) (uuid.UUID, error) {
	input, err := DecodePublicationInputs(request.FrozenInputs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("admit manual lyrics rewrite: %w", err)
	}
	if input.Version != PublicationInputsVersion || input.IntentKind != "rewrite" || input.ManualLyrics == nil || request.IntentKind != "rewrite" || request.Source != nil || request.ExpectedReleaseID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("admit manual lyrics rewrite: source-free rewrite input is required")
	}
	if input.ManualLyrics.ExpectedMetadataRevision != request.ExpectedMetadataRevision || input.ManualLyrics.ConfirmedCurrentGeneration != input.Rewrite.CurrentGeneration {
		return uuid.Nil, fmt.Errorf("admit manual lyrics rewrite: request conflicts with frozen metadata receipt")
	}
	request.manualLyrics = input.ManualLyrics
	request.validatedManualLyrics = true
	request.PublicationAdmission.ExpectedReleaseID = request.ExpectedReleaseID
	request.metadataRevision = request.ExpectedMetadataRevision
	return repository.AdmitPublication(ctx, request.PublicationAdmission, client)
}

func validateManualLyricsAdmission(ctx context.Context, tx bun.Tx, request PublicationAdmission, input *PublicationInputs, current TrackPublication) error {
	if input == nil || input.ManualLyrics == nil || input.Version != PublicationInputsVersion {
		return fmt.Errorf("admit manual lyrics rewrite: manual lyrics input is required")
	}
	if input.ManualLyrics.ExpectedMetadataRevision != request.metadataRevision || input.ManualLyrics.ConfirmedCurrentGeneration != current.Generation {
		return fmt.Errorf("admit manual lyrics rewrite: metadata revision or generation changed: %w", ErrPublicationAdmissionConflict)
	}
	var metadata []byte
	var revision int64
	err := tx.NewRaw(`SELECT metadata,metadata_revision FROM release_track WHERE id=? FOR UPDATE`, request.PositionID).Scan(ctx, &metadata, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("admit manual lyrics rewrite: position metadata not found: %w", ErrPublicationAdmissionConflict)
		}
		return fmt.Errorf("admit manual lyrics rewrite: lock current metadata: %w", err)
	}
	if revision != request.metadataRevision {
		return fmt.Errorf("admit manual lyrics rewrite: metadata revision changed: %w", ErrPublicationAdmissionConflict)
	}
	var currentMetadata publicationCurrentMetadata
	if err := decodePublicationCurrentContract(current.ActualMetadata, &currentMetadata); err != nil || currentMetadata.Version != 1 || currentMetadata.Tags == nil {
		return fmt.Errorf("admit manual lyrics rewrite: current publication metadata is invalid")
	}
	desiredTags := make(map[string][]string, len(currentMetadata.Tags))
	for name, values := range currentMetadata.Tags {
		if strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			continue
		}
		desiredTags[name] = append(make([]string, 0, len(values)), values...)
	}
	input.DesiredTags = desiredTags
	if err := input.Validate(); err != nil {
		return fmt.Errorf("admit manual lyrics rewrite: current publication tags are invalid: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &root); err != nil {
		return fmt.Errorf("admit manual lyrics rewrite: decode current metadata: %w", err)
	}
	var envelope librarymetadata.Envelope
	if raw := root["local_metadata"]; len(raw) != 0 {
		envelope, err = librarymetadata.DecodeEnvelope(raw)
		if err != nil || envelope.Scope != librarymetadata.ScopePosition {
			return fmt.Errorf("admit manual lyrics rewrite: current metadata envelope is invalid")
		}
	} else {
		envelope = librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{}}
	}
	field, exists := envelope.Desired["synced_lyrics"]
	if !exists || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
		return fmt.Errorf("admit manual lyrics rewrite: current manual lyrics are missing: %w", ErrPublicationAdmissionConflict)
	}
	manual := input.ManualLyrics
	if manual.State != string(field.State) {
		return fmt.Errorf("admit manual lyrics rewrite: current manual lyrics state changed: %w", ErrPublicationAdmissionConflict)
	}
	switch field.State {
	case librarymetadata.StateValue:
		value, ok := field.Value.(string)
		if !ok || manual.Value != value {
			return fmt.Errorf("admit manual lyrics rewrite: current manual lyrics value changed: %w", ErrPublicationAdmissionConflict)
		}
		manual.Value = value
	case librarymetadata.StateExplicitEmpty:
		manual.Value = ""
	default:
		return fmt.Errorf("admit manual lyrics rewrite: current manual lyrics state is not publishable")
	}
	manual.ExpectedMetadataRevision = revision
	manual.ConfirmedCurrentGeneration = current.Generation
	return nil
}
