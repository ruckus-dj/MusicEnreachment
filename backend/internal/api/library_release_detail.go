package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type LibraryReleaseDetailInput struct {
	ReleaseID string `path:"release_id"`
}

type libraryPrimaryArtistReleaseInput struct {
	ReleaseID string                   `path:"release_id"`
	Body      libraryPrimaryArtistBody `json:"body"`
}
type libraryPrimaryArtistPositionInput struct {
	ReleaseID  string                   `path:"release_id"`
	PositionID string                   `path:"position_id"`
	Body       libraryPrimaryArtistBody `json:"body"`
}
type libraryPrimaryArtistBody struct {
	ExpectedRevision      int64      `json:"expected_revision"`
	ExpectedTrackRevision *int64     `json:"expected_track_revision,omitempty"`
	ArtistID              *uuid.UUID `json:"artist_id" nullable:"true" required:"true"`
}
type libraryPrimaryArtistOutput struct {
	Body libraryPrimaryArtistResponse `json:"body"`
}
type libraryPrimaryArtistResponse struct {
	Revision           int64 `json:"revision"`
	PublicationApplied bool  `json:"publication_applied"`
}

type LibraryReleaseDetailOutput struct {
	Body LibraryReleaseDetailResponse `json:"body"`
}

type LibraryReleaseDetailResponse struct {
	ID                    uuid.UUID                       `json:"id"`
	Title                 string                          `json:"title"`
	Metadata              json.RawMessage                 `json:"metadata"`
	MetadataVersion       int                             `json:"metadata_version"`
	MetadataRevision      int64                           `json:"metadata_revision"`
	CanRefreshMusicBrainz bool                            `json:"can_refresh_musicbrainz"`
	PrimaryArtist         service.LibraryArtistResolution `json:"primary_artist"`
	Artists               []LibraryArtistCreditResponse   `json:"artists"`
	Tracks                []LibraryReleaseTrackResponse   `json:"tracks"`
}

type LibraryArtistCreditResponse struct {
	ArtistID   uuid.UUID `json:"artist_id"`
	ArtistName *string   `json:"artist_name,omitempty"`
	CreditName string    `json:"credit_name"`
	JoinPhrase string    `json:"join_phrase"`
}

type LibraryReleaseTrackResponse struct {
	ID                        uuid.UUID                       `json:"id"`
	LocalTrackID              uuid.UUID                       `json:"local_track_id"`
	RecordingID               *uuid.UUID                      `json:"recording_id"`
	Medium                    int                             `json:"medium"`
	Position                  int                             `json:"position"`
	DisplayedNumber           string                          `json:"displayed_number"`
	Title                     string                          `json:"title"`
	Metadata                  json.RawMessage                 `json:"metadata"`
	MetadataVersion           int                             `json:"metadata_version"`
	MetadataRevision          int64                           `json:"metadata_revision"`
	TrackMetadataRevision     int64                           `json:"track_metadata_revision"`
	RecordingMetadataRevision *int64                          `json:"recording_metadata_revision,omitempty"`
	PrimaryArtist             service.LibraryArtistResolution `json:"primary_artist"`
	Artists                   []LibraryArtistCreditResponse   `json:"artists"`
	Sources                   []LibraryTrackSourceResponse    `json:"sources"`
}

type LibraryTrackSourceResponse struct {
	MediaVariantID uuid.UUID                       `json:"media_variant_id"`
	Selected       bool                            `json:"selected"`
	Compatible     bool                            `json:"compatible"`
	DecisionMethod string                          `json:"decision_method"`
	PolicyVersion  *string                         `json:"policy_version,omitempty"`
	Metadata       json.RawMessage                 `json:"metadata"`
	Locations      []LibrarySourceLocationResponse `json:"locations"`
}

type LibrarySourceLocationResponse struct {
	RootID       uuid.UUID  `json:"root_id"`
	LocationID   uuid.UUID  `json:"location_id"`
	RelativePath string     `json:"relative_path"`
	SizeBytes    int64      `json:"size_bytes"`
	Mtime        *time.Time `json:"mtime,omitempty"`
	ProbeStatus  string     `json:"probe_status"`
	RootStatus   string     `json:"root_status"`
	RootEnabled  bool       `json:"root_enabled"`
}

func registerLibraryReleaseDetail(api huma.API, dependencies Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "get-library-release", Method: http.MethodGet,
		Path: "/library/releases/{release_id}", Summary: "Read local release detail", Tags: []string{"Library"},
	}, func(ctx context.Context, input *LibraryReleaseDetailInput) (*LibraryReleaseDetailOutput, error) {
		releaseID, err := uuid.Parse(input.ReleaseID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid release ID")
		}
		if dependencies.LibraryReleaseDetails == nil {
			return nil, huma.Error503ServiceUnavailable("library release detail is unavailable")
		}
		detail, err := dependencies.LibraryReleaseDetails.Read(ctx, releaseID)
		if err != nil {
			if service.IsLibraryReleaseNotFound(err) {
				return nil, huma.Error404NotFound("library release not found")
			}
			return nil, huma.Error503ServiceUnavailable("library release detail is unavailable")
		}
		return &LibraryReleaseDetailOutput{Body: libraryReleaseDetailResponse(detail)}, nil
	})
	registerLibraryPrimaryArtist(api, dependencies.LibraryPrimaryArtists)
}

func registerLibraryPrimaryArtist(api huma.API, selection *service.LibraryPrimaryArtists) {
	write := func(ctx context.Context, releaseID, positionID uuid.UUID, body libraryPrimaryArtistBody) (*libraryPrimaryArtistOutput, error) {
		if selection == nil {
			return nil, huma.Error503ServiceUnavailable("library primary artist selection is unavailable")
		}
		revision, err := selection.Set(ctx, releaseID, positionID, body.ArtistID, body.ExpectedRevision, body.ExpectedTrackRevision)
		switch {
		case errors.Is(err, service.ErrLibraryPrimaryArtistInvalidRequest):
			return nil, huma.Error400BadRequest("library primary artist request is invalid")
		case errors.Is(err, service.ErrLibraryReleaseNotFound), errors.Is(err, service.ErrLibraryPositionNotFound):
			return nil, huma.Error404NotFound("library release or position not found")
		case errors.Is(err, service.ErrLibraryPrimaryArtistConflict):
			return nil, huma.Error409Conflict("library metadata revision is stale")
		case errors.Is(err, service.ErrLibraryPrimaryArtistInvalid):
			return nil, huma.Error400BadRequest("artist is not a current credit member")
		case err != nil:
			return nil, huma.Error503ServiceUnavailable("library primary artist selection is unavailable")
		default:
			return &libraryPrimaryArtistOutput{Body: libraryPrimaryArtistResponse{Revision: revision, PublicationApplied: false}}, nil
		}
	}
	huma.Register(api, huma.Operation{OperationID: "set-library-release-primary-artist", Method: http.MethodPut,
		Path: "/library/releases/{release_id}/primary-artist", Summary: "Select a local release primary artist", Tags: []string{"Library"}},
		func(ctx context.Context, input *libraryPrimaryArtistReleaseInput) (*libraryPrimaryArtistOutput, error) {
			releaseID, err := uuid.Parse(input.ReleaseID)
			if err != nil || releaseID == uuid.Nil {
				return nil, huma.Error400BadRequest("library release ID is invalid")
			}
			return write(ctx, releaseID, uuid.Nil, input.Body)
		})
	huma.Register(api, huma.Operation{OperationID: "set-library-position-primary-artist", Method: http.MethodPut,
		Path: "/library/releases/{release_id}/positions/{position_id}/primary-artist", Summary: "Select a local position primary artist", Tags: []string{"Library"}},
		func(ctx context.Context, input *libraryPrimaryArtistPositionInput) (*libraryPrimaryArtistOutput, error) {
			releaseID, err := uuid.Parse(input.ReleaseID)
			if err != nil || releaseID == uuid.Nil {
				return nil, huma.Error400BadRequest("library release ID is invalid")
			}
			positionID, err := uuid.Parse(input.PositionID)
			if err != nil || positionID == uuid.Nil {
				return nil, huma.Error400BadRequest("library position ID is invalid")
			}
			return write(ctx, releaseID, positionID, input.Body)
		})
}

func libraryReleaseDetailResponse(detail service.LibraryReleaseDetail) LibraryReleaseDetailResponse {
	response := LibraryReleaseDetailResponse{
		ID: detail.ID, Title: detail.Title, Metadata: detail.Metadata, MetadataVersion: detail.MetadataVersion,
		MetadataRevision: detail.MetadataRevision, PrimaryArtist: detail.PrimaryArtist,
		CanRefreshMusicBrainz: detail.CanRefreshMusicBrainz,
		Artists:               make([]LibraryArtistCreditResponse, 0, len(detail.Artists)),
		Tracks:                make([]LibraryReleaseTrackResponse, 0, len(detail.Tracks)),
	}
	for _, artist := range detail.Artists {
		response.Artists = append(response.Artists, libraryArtistCreditResponse(artist))
	}
	for _, track := range detail.Tracks {
		item := LibraryReleaseTrackResponse{
			ID: track.ID, LocalTrackID: track.LocalTrackID, RecordingID: track.RecordingID,
			Medium: track.Medium, Position: track.Position, DisplayedNumber: track.DisplayedNumber,
			Title: track.Title, Metadata: track.Metadata, MetadataVersion: track.MetadataVersion,
			MetadataRevision: track.MetadataRevision, TrackMetadataRevision: track.TrackMetadataRevision,
			RecordingMetadataRevision: track.RecordingMetadataRevision, PrimaryArtist: track.PrimaryArtist,
			Artists: make([]LibraryArtistCreditResponse, 0, len(track.Artists)),
			Sources: make([]LibraryTrackSourceResponse, 0, len(track.Sources)),
		}
		for _, artist := range track.Artists {
			item.Artists = append(item.Artists, libraryArtistCreditResponse(artist))
		}
		for _, source := range track.Sources {
			locations := make([]LibrarySourceLocationResponse, 0, len(source.Locations))
			for _, location := range source.Locations {
				var mtime *time.Time
				if location.Mtime.Valid {
					value := location.Mtime.Time
					mtime = &value
				}
				locations = append(locations, LibrarySourceLocationResponse{
					RootID: location.RootID, LocationID: location.LocationID, RelativePath: location.RelativePath,
					SizeBytes: location.SizeBytes, Mtime: mtime, ProbeStatus: location.ProbeStatus,
					RootStatus: location.RootStatus, RootEnabled: location.RootEnabled,
				})
			}
			item.Sources = append(item.Sources, LibraryTrackSourceResponse{
				MediaVariantID: source.MediaVariantID, Selected: source.Selected, Compatible: source.Compatible,
				DecisionMethod: source.DecisionMethod, PolicyVersion: source.PolicyVersion, Metadata: source.Metadata, Locations: locations,
			})
		}
		response.Tracks = append(response.Tracks, item)
	}
	return response
}

func libraryArtistCreditResponse(artist service.LibraryArtistCredit) LibraryArtistCreditResponse {
	return LibraryArtistCreditResponse{
		ArtistID: artist.ArtistID, ArtistName: artist.ArtistName, CreditName: artist.CreditName, JoinPhrase: artist.JoinPhrase,
	}
}
