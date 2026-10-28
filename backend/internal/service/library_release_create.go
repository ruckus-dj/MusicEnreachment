package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrLibraryReleaseCreateInvalidRequest = errors.New("library release create request is invalid")

// LibraryReleaseCreateRequest describes a standalone release without exposing
// persistence-layer request models to callers.
type LibraryReleaseCreateRequest struct {
	ReleaseID     *uuid.UUID                      `json:"release_id,omitempty" nullable:"true"`
	Title         string                          `json:"title,omitempty"`
	ReleaseDate   string                          `json:"release_date,omitempty"`
	Country       string                          `json:"country,omitempty"`
	Label         string                          `json:"label,omitempty"`
	CatalogNumber string                          `json:"catalog_number,omitempty"`
	ReleaseType   string                          `json:"release_type,omitempty"`
	NewArtists    []LibraryReleaseCreateArtist    `json:"new_artists,omitempty"`
	NewRecordings []LibraryReleaseCreateRecording `json:"new_recordings,omitempty"`
	Credits       []LibraryReleaseCreateCredit    `json:"credits,omitempty"`
	Media         []LibraryReleaseCreateMedium    `json:"media,omitempty"`
	Cover         *LibraryReleaseCreateCover      `json:"cover,omitempty"`
}

type LibraryReleaseCreateArtist struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

type LibraryReleaseCreateRecording struct {
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
}

type LibraryReleaseCreateArtistRef struct {
	Kind string     `json:"kind"`
	ID   *uuid.UUID `json:"id,omitempty" nullable:"true"`
	Key  string     `json:"key,omitempty"`
}

type LibraryReleaseCreateCredit struct {
	ArtistRef  LibraryReleaseCreateArtistRef `json:"artist_ref"`
	CreditName string                        `json:"credit_name,omitempty"`
	Role       string                        `json:"role,omitempty"`
	JoinPhrase string                        `json:"join_phrase,omitempty"`
}

type LibraryReleaseCreateMedium struct {
	Key       string                         `json:"key"`
	Title     string                         `json:"title,omitempty"`
	Format    string                         `json:"format,omitempty"`
	Positions []LibraryReleaseCreatePosition `json:"positions,omitempty"`
}

type LibraryReleaseCreatePosition struct {
	Key                    string                            `json:"key"`
	DisplayedNumber        string                            `json:"displayed_number,omitempty"`
	Title                  string                            `json:"title,omitempty"`
	DurationMS             *int64                            `json:"duration_ms,omitempty" nullable:"true"`
	RecordingRef           *LibraryReleaseCreateRecordingRef `json:"recording_ref,omitempty"`
	Sources                []LibraryReleaseCreateSource      `json:"sources,omitempty"`
	SelectedMediaVariantID *uuid.UUID                        `json:"selected_media_variant_id,omitempty" nullable:"true"`
}

type LibraryReleaseCreateRecordingRef struct {
	_                     struct{}   `json:"-" nullable:"true"`
	Kind                  string     `json:"kind"`
	Key                   string     `json:"key,omitempty"`
	ID                    *uuid.UUID `json:"id,omitempty" nullable:"true"`
	ProviderID            *uuid.UUID `json:"provider_id,omitempty" nullable:"true"`
	ProviderSourceID      *uuid.UUID `json:"provider_source_id,omitempty" nullable:"true"`
	ProviderRecordingID   *uuid.UUID `json:"provider_recording_id,omitempty" nullable:"true"`
	ConfigurationIdentity string     `json:"configuration_identity,omitempty"`
}

type LibraryReleaseCreateSource struct {
	MediaVariantID   uuid.UUID  `json:"media_variant_id"`
	MetadataResultID *uuid.UUID `json:"metadata_result_id,omitempty" nullable:"true"`
}

type LibraryReleaseCreateCover struct {
	_                struct{}  `json:"-" nullable:"true"`
	MetadataResultID uuid.UUID `json:"metadata_result_id"`
	ExpectedSHA256   string    `json:"expected_sha256"`
}

type LibraryReleaseCreateResult struct {
	ReleaseID uuid.UUID                            `json:"release_id"`
	Media     []LibraryReleaseCreateMediaResult    `json:"media" nullable:"false"`
	Positions []LibraryReleaseCreatePositionResult `json:"positions" nullable:"false"`
}

type LibraryReleaseCreateMediaResult struct {
	MediaKey string    `json:"media_key"`
	MediaID  uuid.UUID `json:"media_id"`
}

type LibraryReleaseCreatePositionResult struct {
	PositionKey string     `json:"position_key"`
	PositionID  uuid.UUID  `json:"position_id"`
	TrackID     uuid.UUID  `json:"track_id"`
	RecordingID *uuid.UUID `json:"recording_id" nullable:"true"`
}

type libraryReleaseCreateRepository interface {
	CreateStandaloneRelease(context.Context, persistence.LibraryReleaseCreateRequest) (persistence.LibraryReleaseCreateResult, error)
}

type LibraryReleaseCreation struct {
	repository libraryReleaseCreateRepository
}

func IsLibraryReleaseCreateConflict(err error) bool {
	return errors.Is(err, persistence.ErrLibraryReleaseCreateConflict) || errors.Is(err, persistence.ErrLibraryReleaseCreateProviderConflict)
}

func NewLibraryReleaseCreation(repository libraryReleaseCreateRepository) *LibraryReleaseCreation {
	return &LibraryReleaseCreation{repository: repository}
}

func (creation *LibraryReleaseCreation) Create(ctx context.Context, request LibraryReleaseCreateRequest) (LibraryReleaseCreateResult, error) {
	if err := validateLibraryReleaseCreateRequest(request); err != nil {
		return LibraryReleaseCreateResult{}, fmt.Errorf("validate library release create request: %w: %v", ErrLibraryReleaseCreateInvalidRequest, err)
	}
	if creation == nil || creation.repository == nil {
		return LibraryReleaseCreateResult{}, errors.New("library release creation is unavailable")
	}
	created, err := creation.repository.CreateStandaloneRelease(ctx, libraryReleaseCreatePersistenceRequest(request))
	if err != nil {
		return LibraryReleaseCreateResult{}, fmt.Errorf("create library release: %w", err)
	}
	result := LibraryReleaseCreateResult{ReleaseID: created.ReleaseID, Media: make([]LibraryReleaseCreateMediaResult, 0, len(created.Media)), Positions: make([]LibraryReleaseCreatePositionResult, 0, len(created.Positions))}
	for _, medium := range created.Media {
		result.Media = append(result.Media, LibraryReleaseCreateMediaResult{MediaKey: medium.MediaKey, MediaID: medium.MediaID})
	}
	for _, position := range created.Positions {
		result.Positions = append(result.Positions, LibraryReleaseCreatePositionResult{PositionKey: position.PositionKey, PositionID: position.PositionID, TrackID: position.TrackID, RecordingID: position.RecordingID})
	}
	return result, nil
}

func validateLibraryReleaseCreateRequest(request LibraryReleaseCreateRequest) error {
	if request.ReleaseID != nil && *request.ReleaseID == uuid.Nil {
		return errors.New("release ID must be nonzero")
	}
	if request.ReleaseDate != "" {
		envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{
			"release_date": librarymetadata.ManualFieldValue(librarymetadata.StateValue, request.ReleaseDate, true),
		}}
		if err := envelope.Validate(); err != nil {
			return fmt.Errorf("release date is invalid: %w", err)
		}
	}
	artists, recordings, media, positions := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, artist := range request.NewArtists {
		if artist.Key == "" || artists[artist.Key] {
			return errors.New("new artist keys must be nonempty and unique")
		}
		artists[artist.Key] = true
	}
	for _, recording := range request.NewRecordings {
		if recording.Key == "" || recordings[recording.Key] {
			return errors.New("new recording keys must be nonempty and unique")
		}
		recordings[recording.Key] = true
	}
	for _, medium := range request.Media {
		if medium.Key == "" || media[medium.Key] {
			return errors.New("media keys must be nonempty and unique")
		}
		media[medium.Key] = true
		for _, position := range medium.Positions {
			if position.Key == "" || positions[position.Key] {
				return errors.New("position keys must be nonempty and unique")
			}
			positions[position.Key] = true
			if position.DurationMS != nil && *position.DurationMS < 0 {
				return errors.New("duration must not be negative")
			}
			if position.RecordingRef != nil && !validateRecordingRef(*position.RecordingRef, recordings) {
				return errors.New("position recording reference is invalid")
			}
			seen := make(map[uuid.UUID]bool, len(position.Sources))
			for _, source := range position.Sources {
				if source.MediaVariantID == uuid.Nil || seen[source.MediaVariantID] || source.MetadataResultID != nil && *source.MetadataResultID == uuid.Nil {
					return errors.New("source identities must be nonzero and unique per position")
				}
				seen[source.MediaVariantID] = true
			}
			if position.SelectedMediaVariantID != nil && (*position.SelectedMediaVariantID == uuid.Nil || !seen[*position.SelectedMediaVariantID]) {
				return errors.New("selected source must be attached to the position")
			}
		}
	}
	usedArtists, usedRecordings := map[string]bool{}, map[string]bool{}
	for _, credit := range request.Credits {
		switch credit.ArtistRef.Kind {
		case "new":
			if credit.ArtistRef.ID != nil || credit.ArtistRef.Key == "" || !artists[credit.ArtistRef.Key] {
				return errors.New("credit new artist reference is invalid")
			}
			usedArtists[credit.ArtistRef.Key] = true
		case "existing":
			if credit.ArtistRef.Key != "" || credit.ArtistRef.ID == nil || *credit.ArtistRef.ID == uuid.Nil {
				return errors.New("credit existing artist reference is invalid")
			}
		default:
			return errors.New("credit artist reference kind is invalid")
		}
	}
	for _, medium := range request.Media {
		for _, position := range medium.Positions {
			if position.RecordingRef != nil && position.RecordingRef.Kind == "new" {
				usedRecordings[position.RecordingRef.Key] = true
			}
		}
	}
	for key := range artists {
		if !usedArtists[key] {
			return errors.New("new artists must be referenced by a credit")
		}
	}
	for key := range recordings {
		if !usedRecordings[key] {
			return errors.New("new recordings must be referenced by a position")
		}
	}
	if request.Cover != nil {
		if request.Cover.MetadataResultID == uuid.Nil {
			return errors.New("cover reference is invalid")
		}
		if digest, err := hex.DecodeString(strings.TrimSpace(request.Cover.ExpectedSHA256)); err != nil || len(digest) != 32 {
			return errors.New("cover digest is invalid")
		}
	}
	return nil
}

func validateRecordingRef(ref LibraryReleaseCreateRecordingRef, recordings map[string]bool) bool {
	switch ref.Kind {
	case "new":
		return ref.Key != "" && recordings[ref.Key] && ref.ID == nil && ref.ProviderID == nil && ref.ProviderSourceID == nil && ref.ProviderRecordingID == nil && ref.ConfigurationIdentity == ""
	case "existing":
		return ref.Key == "" && ref.ID != nil && *ref.ID != uuid.Nil && ref.ProviderID == nil && ref.ProviderSourceID == nil && ref.ProviderRecordingID == nil && ref.ConfigurationIdentity == ""
	case "provider":
		return ref.Key == "" && ref.ID == nil && ref.ProviderID != nil && *ref.ProviderID != uuid.Nil && ref.ProviderSourceID != nil && *ref.ProviderSourceID != uuid.Nil && ref.ProviderRecordingID != nil && *ref.ProviderRecordingID != uuid.Nil && ref.ConfigurationIdentity != ""
	default:
		return false
	}
}

func libraryReleaseCreatePersistenceRequest(request LibraryReleaseCreateRequest) persistence.LibraryReleaseCreateRequest {
	result := persistence.LibraryReleaseCreateRequest{ReleaseID: request.ReleaseID, Title: request.Title, ReleaseDate: request.ReleaseDate, Country: request.Country, Label: request.Label, CatalogNumber: request.CatalogNumber, ReleaseType: request.ReleaseType, Cover: nil}
	for _, artist := range request.NewArtists {
		result.NewArtists = append(result.NewArtists, persistence.LibraryReleaseCreateArtist{Key: artist.Key, Name: artist.Name})
	}
	for _, recording := range request.NewRecordings {
		result.NewRecordings = append(result.NewRecordings, persistence.LibraryReleaseCreateRecording{Key: recording.Key, Title: recording.Title})
	}
	for _, credit := range request.Credits {
		result.Credits = append(result.Credits, persistence.LibraryReleaseCreateCredit{ArtistRef: persistence.LibraryReleaseCreateArtistRef{Kind: credit.ArtistRef.Kind, ID: uuidValue(credit.ArtistRef.ID), Key: credit.ArtistRef.Key}, CreditName: credit.CreditName, Role: credit.Role, JoinPhrase: credit.JoinPhrase})
	}
	for _, medium := range request.Media {
		mappedMedium := persistence.LibraryReleaseCreateMedium{Key: medium.Key, Title: medium.Title, Format: medium.Format}
		for _, position := range medium.Positions {
			mappedPosition := persistence.LibraryReleaseCreatePosition{Key: position.Key, DisplayedNumber: position.DisplayedNumber, Title: position.Title, DurationMS: position.DurationMS, SelectedMediaVariantID: position.SelectedMediaVariantID}
			if position.RecordingRef != nil {
				ref := position.RecordingRef
				mappedPosition.RecordingRef = &persistence.LibraryReleaseCreateRecordingRef{Kind: ref.Kind, Key: ref.Key, ID: uuidValue(ref.ID), ProviderID: uuidValue(ref.ProviderID), ProviderSourceID: uuidValue(ref.ProviderSourceID), ProviderRecordingID: uuidValue(ref.ProviderRecordingID), ConfigurationIdentity: ref.ConfigurationIdentity}
			}
			for _, source := range position.Sources {
				mappedPosition.Sources = append(mappedPosition.Sources, persistence.LibraryReleaseCreateSource{MediaVariantID: source.MediaVariantID, MetadataResultID: source.MetadataResultID})
			}
			mappedMedium.Positions = append(mappedMedium.Positions, mappedPosition)
		}
		result.Media = append(result.Media, mappedMedium)
	}
	if request.Cover != nil {
		result.Cover = &persistence.LibraryReleaseCreateCover{MetadataResultID: request.Cover.MetadataResultID, ExpectedSHA256: request.Cover.ExpectedSHA256}
	}
	return result
}

func uuidValue(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}
