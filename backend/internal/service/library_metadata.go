package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type LibraryMetadataResult struct {
	Envelope librarymetadata.Envelope `json:"envelope"`
	Revision int64                    `json:"revision"`
}

var (
	ErrLibraryMetadataInvalidRequest = errors.New("library metadata request is invalid")
	ErrLibraryMetadataConflict       = errors.New("library metadata revision conflict")
	ErrLibraryMetadataNotFound       = errors.New("library metadata owner not found")
)

type libraryMetadataRepository interface {
	ReadMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadata, error)
	UpdateMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID, int64, librarymetadata.Envelope) (persistence.LibraryMetadata, error)
}

type LibraryMetadataService struct{ repository libraryMetadataRepository }

func NewLibraryMetadataService(repository libraryMetadataRepository) *LibraryMetadataService {
	return &LibraryMetadataService{repository: repository}
}

func (service *LibraryMetadataService) Read(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID) (LibraryMetadataResult, error) {
	if service == nil || service.repository == nil {
		return LibraryMetadataResult{}, errors.New("library metadata is unavailable")
	}
	result, err := service.repository.ReadMetadata(ctx, scope, releaseID, ownerID)
	if errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		return LibraryMetadataResult{}, fmt.Errorf("read library metadata: %w", ErrLibraryMetadataNotFound)
	}
	if err != nil {
		return LibraryMetadataResult{}, fmt.Errorf("read library metadata: %w", err)
	}
	return LibraryMetadataResult{Envelope: result.Envelope, Revision: result.Revision}, nil
}

func (service *LibraryMetadataService) Update(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID, expected int64, changes map[string]json.RawMessage) (LibraryMetadataResult, error) {
	if service == nil || service.repository == nil {
		return LibraryMetadataResult{}, errors.New("library metadata is unavailable")
	}
	if expected < 1 || len(changes) == 0 {
		return LibraryMetadataResult{}, ErrLibraryMetadataInvalidRequest
	}
	current, err := service.repository.ReadMetadata(ctx, scope, releaseID, ownerID)
	if errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		return LibraryMetadataResult{}, fmt.Errorf("update library metadata: %w", ErrLibraryMetadataNotFound)
	}
	if err != nil {
		return LibraryMetadataResult{}, fmt.Errorf("update library metadata: %w", err)
	}
	for name, raw := range changes {
		definition, ok := librarymetadata.Field(name)
		if !ok {
			return LibraryMetadataResult{}, fmt.Errorf("%w: unknown metadata field %q", ErrLibraryMetadataInvalidRequest, name)
		}
		if definition.Canonical {
			return LibraryMetadataResult{}, fmt.Errorf("%w: canonical field %q is edited through matching", ErrLibraryMetadataInvalidRequest, name)
		}
		var wire struct {
			State librarymetadata.ValueState `json:"state"`
			Value json.RawMessage            `json:"value"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&wire); err != nil {
			return LibraryMetadataResult{}, fmt.Errorf("%w: invalid field %q", ErrLibraryMetadataInvalidRequest, name)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return LibraryMetadataResult{}, fmt.Errorf("%w: invalid field %q", ErrLibraryMetadataInvalidRequest, name)
		}
		var value any
		present := len(wire.Value) > 0
		if present {
			d := json.NewDecoder(bytes.NewReader(wire.Value))
			d.UseNumber()
			if err := d.Decode(&value); err != nil {
				return LibraryMetadataResult{}, fmt.Errorf("%w: invalid field %q value", ErrLibraryMetadataInvalidRequest, name)
			}
		}
		field := librarymetadata.ManualFieldValue(wire.State, value, present)
		if name == "synced_lyrics" && wire.State == librarymetadata.StateValue {
			lyrics, ok := value.(string)
			if !ok {
				return LibraryMetadataResult{}, fmt.Errorf("%w: synced lyrics must be a string", ErrLibraryMetadataInvalidRequest)
			}
			normalized, err := lrclib.ValidateSyncedLyrics(lyrics)
			if err != nil || normalized == "" {
				return LibraryMetadataResult{}, fmt.Errorf("%w: synced lyrics are invalid", ErrLibraryMetadataInvalidRequest)
			}
		}
		probe := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: scope, Desired: map[string]librarymetadata.FieldValue{name: field}}
		if err := probe.Validate(); err != nil {
			return LibraryMetadataResult{}, fmt.Errorf("%w: %v", ErrLibraryMetadataInvalidRequest, err)
		}
		current.Envelope.Desired[name] = field
	}
	result, err := service.repository.UpdateMetadata(ctx, scope, releaseID, ownerID, expected, current.Envelope)
	if errors.Is(err, persistence.ErrLibraryMetadataConflict) {
		return LibraryMetadataResult{}, fmt.Errorf("update library metadata: %w", ErrLibraryMetadataConflict)
	}
	if errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		return LibraryMetadataResult{}, fmt.Errorf("update library metadata: %w", ErrLibraryMetadataNotFound)
	}
	if err != nil {
		return LibraryMetadataResult{}, fmt.Errorf("update library metadata: %w", err)
	}
	return LibraryMetadataResult{Envelope: result.Envelope, Revision: result.Revision}, nil
}
