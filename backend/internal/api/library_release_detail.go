package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type LibraryReleaseDetailInput struct {
	ReleaseID string `path:"release_id"`
}

type LibraryReleaseDetailOutput struct {
	Body LibraryReleaseDetailResponse `json:"body"`
}

type LibraryReleaseDetailResponse struct {
	ID              uuid.UUID                     `json:"id"`
	Title           string                        `json:"title"`
	Metadata        json.RawMessage               `json:"metadata"`
	MetadataVersion int                           `json:"metadata_version"`
	Artists         []LibraryArtistCreditResponse `json:"artists"`
	Tracks          []LibraryReleaseTrackResponse `json:"tracks"`
}

type LibraryArtistCreditResponse struct {
	ArtistID   uuid.UUID `json:"artist_id"`
	ArtistName *string   `json:"artist_name,omitempty"`
	CreditName string    `json:"credit_name"`
	JoinPhrase string    `json:"join_phrase"`
}

type LibraryReleaseTrackResponse struct {
	ID              uuid.UUID                     `json:"id"`
	LocalTrackID    uuid.UUID                     `json:"local_track_id"`
	RecordingID     *uuid.UUID                    `json:"recording_id"`
	Medium          int                           `json:"medium"`
	Position        int                           `json:"position"`
	DisplayedNumber string                        `json:"displayed_number"`
	Title           string                        `json:"title"`
	Metadata        json.RawMessage               `json:"metadata"`
	MetadataVersion int                           `json:"metadata_version"`
	Artists         []LibraryArtistCreditResponse `json:"artists"`
	Sources         []LibraryTrackSourceResponse  `json:"sources"`
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
}

func libraryReleaseDetailResponse(detail service.LibraryReleaseDetail) LibraryReleaseDetailResponse {
	response := LibraryReleaseDetailResponse{
		ID: detail.ID, Title: detail.Title, Metadata: detail.Metadata, MetadataVersion: detail.MetadataVersion,
		Artists: make([]LibraryArtistCreditResponse, 0, len(detail.Artists)),
		Tracks:  make([]LibraryReleaseTrackResponse, 0, len(detail.Tracks)),
	}
	for _, artist := range detail.Artists {
		response.Artists = append(response.Artists, libraryArtistCreditResponse(artist))
	}
	for _, track := range detail.Tracks {
		item := LibraryReleaseTrackResponse{
			ID: track.ID, LocalTrackID: track.LocalTrackID, RecordingID: track.RecordingID,
			Medium: track.Medium, Position: track.Position, DisplayedNumber: track.DisplayedNumber,
			Title: track.Title, Metadata: track.Metadata, MetadataVersion: track.MetadataVersion,
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
