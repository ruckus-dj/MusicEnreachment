package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type LibraryMetadataResult struct {
	Envelope librarymetadata.Envelope `json:"envelope"`
	Revision int64                    `json:"revision"`
}

type ProviderGenreBaseline struct {
	ProviderCode     string                           `json:"provider_code"`
	ProviderSourceID uuid.UUID                        `json:"provider_source_id"`
	ProviderKey      string                           `json:"provider_key"`
	Evidence         ProviderGenreEvidence            `json:"evidence"`
	Genres           []librarymetadata.GenreReference `json:"genres"`
	Digest           string                           `json:"digest"`
}

type ProviderGenreEvidence struct {
	Authority  int   `json:"authority"`
	FetchOrder int64 `json:"fetch_order"`
}

type ProviderReleaseIdentifierBaseline struct {
	ProviderCode     string                           `json:"provider_code"`
	ProviderSourceID uuid.UUID                        `json:"provider_source_id"`
	ProviderKey      string                           `json:"provider_key"`
	ASIN             *string                          `json:"asin,omitempty"`
	Script           *string                          `json:"script,omitempty"`
	Evidence         map[string]ProviderGenreEvidence `json:"evidence"`
	Digest           string                           `json:"digest"`
}

type LibraryMetadataDisplayResult struct {
	Metadata                          LibraryMetadataResult
	EffectiveKey                      *librarymetadata.ResolvedField
	ProviderGenreBaseline             *ProviderGenreBaseline
	ProviderReleaseIdentifierBaseline *ProviderReleaseIdentifierBaseline
	ArtistCreditBaseline              []LibraryMetadataArtistCredit
	EffectiveArtistCredit             *EffectiveArtistCredit
}

type LibraryMetadataArtistCredit struct {
	ArtistID         string  `json:"artist_id"`
	ArtistName       string  `json:"artist_name"`
	CreditName       string  `json:"credit_name"`
	JoinPhrase       string  `json:"join_phrase"`
	Role             string  `json:"role,omitempty"`
	MBID             *string `json:"mbid,omitempty"`
	SortName         string  `json:"sort_name,omitempty"`
	Disambiguation   string  `json:"disambiguation,omitempty"`
	MetadataRevision int64   `json:"metadata_revision"`
}

type EffectiveArtistCredit struct {
	Credits []LibraryMetadataArtistCredit          `json:"credits"`
	Origin  librarymetadata.CreditOrigin           `json:"origin"`
	State   librarymetadata.ValueState             `json:"state"`
	Status  librarymetadata.CreditResolutionStatus `json:"status"`
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

type libraryMetadataDisplayRepository interface {
	ReadMetadataDisplay(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadataDisplay, error)
}

type libraryMetadataGenreCatalog interface {
	GenreCatalogItems(context.Context) ([]librarymetadata.GenreReference, error)
}

type LibraryMetadataService struct {
	repository  libraryMetadataRepository
	genres      libraryMetadataGenreCatalog
	coordinator *publication.Coordinator
}

func NewLibraryMetadataService(repository libraryMetadataRepository) *LibraryMetadataService {
	service := &LibraryMetadataService{repository: repository}
	if genres, ok := repository.(libraryMetadataGenreCatalog); ok {
		service.genres = genres
	}
	return service
}

func (service *LibraryMetadataService) WithPublicationCoordinator(coordinator *publication.Coordinator) *LibraryMetadataService {
	service.coordinator = coordinator
	return service
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

func (service *LibraryMetadataService) ReadDisplay(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID) (LibraryMetadataDisplayResult, error) {
	if service == nil || service.repository == nil {
		return LibraryMetadataDisplayResult{}, errors.New("library metadata is unavailable")
	}
	displayRepository, ok := service.repository.(libraryMetadataDisplayRepository)
	if !ok {
		metadata, err := service.Read(ctx, scope, releaseID, ownerID)
		return LibraryMetadataDisplayResult{Metadata: metadata}, err
	}
	display, err := displayRepository.ReadMetadataDisplay(ctx, scope, releaseID, ownerID)
	if errors.Is(err, persistence.ErrLibraryMetadataNotFound) {
		return LibraryMetadataDisplayResult{}, fmt.Errorf("read library metadata: %w", ErrLibraryMetadataNotFound)
	}
	if err != nil {
		return LibraryMetadataDisplayResult{}, fmt.Errorf("read library metadata display: %w", err)
	}
	result := LibraryMetadataDisplayResult{
		Metadata:             LibraryMetadataResult{Envelope: display.Metadata.Envelope, Revision: display.Metadata.Revision},
		EffectiveKey:         display.EffectiveKey,
		ArtistCreditBaseline: mapLibraryArtistCredits(display.ArtistCreditBaseline),
	}
	if display.EffectiveArtistCredit != nil {
		result.EffectiveArtistCredit = &EffectiveArtistCredit{
			Credits: mapLibraryArtistCredits(display.EffectiveArtistCredit.Credits),
			Origin:  display.EffectiveArtistCredit.Origin,
			State:   display.EffectiveArtistCredit.State,
			Status:  display.EffectiveArtistCredit.Status,
		}
	}
	if baseline := display.ProviderGenreBaseline; baseline != nil {
		result.ProviderGenreBaseline = &ProviderGenreBaseline{ProviderCode: baseline.ProviderCode, ProviderSourceID: baseline.SourceID, ProviderKey: baseline.ProviderKey, Evidence: ProviderGenreEvidence{Authority: baseline.Authority, FetchOrder: baseline.FetchOrder}, Genres: append([]librarymetadata.GenreReference{}, baseline.Genres...), Digest: baseline.Digest}
	}
	if baseline := display.ProviderReleaseIdentifierBaseline; baseline != nil {
		evidence := make(map[string]ProviderGenreEvidence, len(baseline.Evidence))
		for field, item := range baseline.Evidence {
			evidence[field] = ProviderGenreEvidence{Authority: item.Authority, FetchOrder: item.FetchOrder}
		}
		result.ProviderReleaseIdentifierBaseline = &ProviderReleaseIdentifierBaseline{ProviderCode: baseline.ProviderCode, ProviderSourceID: baseline.SourceID, ProviderKey: baseline.ProviderKey, ASIN: baseline.ASIN, Script: baseline.Script, Evidence: evidence, Digest: baseline.Digest}
	}
	return result, nil
}

func mapLibraryArtistCredits(credits []librarymetadata.ArtistCredit) []LibraryMetadataArtistCredit {
	result := make([]LibraryMetadataArtistCredit, len(credits))
	for index, credit := range credits {
		result[index] = LibraryMetadataArtistCredit{
			ArtistID: credit.ArtistID, ArtistName: credit.ArtistName, CreditName: credit.CreditName,
			JoinPhrase: credit.JoinPhrase, Role: credit.Role, MBID: credit.MBID, SortName: credit.SortName,
			Disambiguation: credit.Disambiguation, MetadataRevision: credit.MetadataRevision,
		}
	}
	return result
}

func (service *LibraryMetadataService) Update(ctx context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID, expected int64, changes map[string]json.RawMessage) (LibraryMetadataResult, error) {
	if service == nil || service.repository == nil {
		return LibraryMetadataResult{}, errors.New("library metadata is unavailable")
	}
	if service.coordinator != nil {
		release, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
		if err != nil {
			return LibraryMetadataResult{}, fmt.Errorf("acquire library metadata mutation resource: %w", err)
		}
		defer release()
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
		if definition.Canonical && name != "title" && name != "artist_credit" && name != "artists" {
			return LibraryMetadataResult{}, fmt.Errorf("%w: canonical field %q is edited through matching", ErrLibraryMetadataInvalidRequest, name)
		}
		if name == "artist_sort_names" || name == "album_artist_sort_names" {
			return LibraryMetadataResult{}, fmt.Errorf("%w: artist sort lists are derived from artist credits and cannot be written independently", ErrLibraryMetadataInvalidRequest)
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
		if name == "genre" && wire.State == librarymetadata.StateValue {
			canonical, err := service.canonicalGenres(ctx, value, current.Envelope)
			if err != nil {
				return LibraryMetadataResult{}, err
			}
			field.Value = canonical
		}
		current.Envelope.Desired[name] = field
	}
	result, err := service.repository.UpdateMetadata(ctx, scope, releaseID, ownerID, expected, current.Envelope)
	if errors.Is(err, persistence.ErrLibraryMetadataArtistNotFound) {
		return LibraryMetadataResult{}, fmt.Errorf("%w: artist_credit references an unknown local artist", ErrLibraryMetadataInvalidRequest)
	}
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

func (service *LibraryMetadataService) canonicalGenres(ctx context.Context, value any, current librarymetadata.Envelope) ([]librarymetadata.GenreReference, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode genre selection: %w", err)
	}
	var selected []librarymetadata.GenreReference
	if err := json.Unmarshal(encoded, &selected); err != nil {
		return nil, fmt.Errorf("%w: genre selection is invalid", ErrLibraryMetadataInvalidRequest)
	}
	retained := make(map[string]librarymetadata.GenreReference)
	if saved, exists := current.Desired["genre"]; exists && saved.State == librarymetadata.StateValue {
		encoded, err := json.Marshal(saved.Value)
		if err != nil {
			return nil, fmt.Errorf("encode saved genre references: %w", err)
		}
		var references []librarymetadata.GenreReference
		if err := json.Unmarshal(encoded, &references); err != nil {
			return nil, fmt.Errorf("decode saved genre references: %w", err)
		}
		for _, genre := range references {
			retained[strings.ToLower(genre.ID)] = genre
		}
	}
	needsCatalog := false
	for _, genre := range selected {
		if _, exists := retained[strings.ToLower(genre.ID)]; !exists {
			needsCatalog = true
			break
		}
	}
	byID := make(map[string]librarymetadata.GenreReference)
	if needsCatalog {
		if service.genres == nil {
			return nil, errors.New("genre catalog is unavailable for metadata validation")
		}
		catalog, err := service.genres.GenreCatalogItems(ctx)
		if err != nil {
			return nil, fmt.Errorf("read genre catalog for metadata validation: %w", err)
		}
		byID = make(map[string]librarymetadata.GenreReference, len(catalog))
		for _, genre := range catalog {
			byID[strings.ToLower(genre.ID)] = genre
		}
	}
	canonical := make([]librarymetadata.GenreReference, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, genre := range selected {
		id := strings.ToLower(genre.ID)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		item, exists := retained[id]
		if !exists {
			item, exists = byID[id]
			if !exists {
				return nil, fmt.Errorf("%w: genre ID is not in the current catalog", ErrLibraryMetadataInvalidRequest)
			}
		}
		canonical = append(canonical, item)
	}
	return canonical, nil
}
