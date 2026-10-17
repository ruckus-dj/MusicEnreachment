package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrLibraryReleaseNotFound = errors.New("library release not found")

func IsLibraryReleaseNotFound(err error) bool {
	return errors.Is(err, ErrLibraryReleaseNotFound)
}

type libraryReleaseDetailReader interface {
	ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error)
}

type LibraryReleaseDetail struct {
	ID              uuid.UUID
	Title           string
	Metadata        json.RawMessage
	MetadataVersion int
	Artists         []LibraryArtistCredit
	Tracks          []LibraryReleaseTrackDetail
}

type LibraryArtistCredit struct {
	ArtistID   uuid.UUID
	ArtistName *string
	CreditName string
	JoinPhrase string
}

type LibraryReleaseTrackDetail struct {
	ID              uuid.UUID
	LocalTrackID    uuid.UUID
	RecordingID     *uuid.UUID
	Medium          int
	Position        int
	DisplayedNumber string
	Title           string
	Metadata        json.RawMessage
	MetadataVersion int
	Artists         []LibraryArtistCredit
	Sources         []LibraryTrackSourceDetail
}

type LibraryTrackSourceDetail struct {
	MediaVariantID uuid.UUID
	Selected       bool
	Compatible     bool
	DecisionMethod string
	PolicyVersion  *string
	Metadata       json.RawMessage
	Locations      []LibrarySourceLocationDetail
}

type LibrarySourceLocationDetail struct {
	RootID       uuid.UUID
	LocationID   uuid.UUID
	RelativePath string
	SizeBytes    int64
	Mtime        sql.NullTime
	ProbeStatus  string
	RootStatus   string
	RootEnabled  bool
}

type LibraryReleaseDetails struct {
	repository libraryReleaseDetailReader
}

func NewLibraryReleaseDetails(repository libraryReleaseDetailReader) *LibraryReleaseDetails {
	return &LibraryReleaseDetails{repository: repository}
}

func (service *LibraryReleaseDetails) Read(ctx context.Context, id uuid.UUID) (LibraryReleaseDetail, error) {
	if service == nil || service.repository == nil {
		return LibraryReleaseDetail{}, errors.New("library release detail is unavailable")
	}
	detail, err := service.repository.ReadReleaseDetail(ctx, id)
	if err != nil {
		if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
			return LibraryReleaseDetail{}, fmt.Errorf("read library release detail: %w", ErrLibraryReleaseNotFound)
		}
		return LibraryReleaseDetail{}, fmt.Errorf("read library release detail: %w", err)
	}
	result := LibraryReleaseDetail{
		ID: detail.ID, Title: detail.Title, Metadata: detail.Metadata, MetadataVersion: detail.MetadataVersion,
		Artists: make([]LibraryArtistCredit, 0, len(detail.Artists)), Tracks: make([]LibraryReleaseTrackDetail, 0, len(detail.Tracks)),
	}
	for _, artist := range detail.Artists {
		result.Artists = append(result.Artists, libraryArtistCredit(artist))
	}
	for _, track := range detail.Tracks {
		item := LibraryReleaseTrackDetail{
			ID: track.ID, LocalTrackID: track.LocalTrackID, RecordingID: track.RecordingID,
			Medium: track.Medium, Position: track.Position, DisplayedNumber: track.DisplayedNumber,
			Title: track.Title, Metadata: track.Metadata, MetadataVersion: track.MetadataVersion,
			Artists: make([]LibraryArtistCredit, 0, len(track.Artists)),
			Sources: make([]LibraryTrackSourceDetail, 0, len(track.Sources)),
		}
		for _, artist := range track.Artists {
			item.Artists = append(item.Artists, libraryArtistCredit(artist))
		}
		for _, source := range track.Sources {
			locationList := make([]LibrarySourceLocationDetail, 0, len(source.Locations))
			for _, location := range source.Locations {
				locationList = append(locationList, LibrarySourceLocationDetail{
					RootID: location.RootID, LocationID: location.LocationID, RelativePath: location.RelativePath,
					SizeBytes: location.SizeBytes, Mtime: location.Mtime, ProbeStatus: location.ProbeStatus,
					RootStatus: location.RootStatus, RootEnabled: location.RootEnabled,
				})
			}
			item.Sources = append(item.Sources, LibraryTrackSourceDetail{
				MediaVariantID: source.MediaVariantID, Selected: source.Selected, Compatible: source.Compatible,
				DecisionMethod: source.DecisionMethod, PolicyVersion: source.PolicyVersion, Metadata: source.Metadata, Locations: locationList,
			})
		}
		result.Tracks = append(result.Tracks, item)
	}
	return result, nil
}

func libraryArtistCredit(artist persistence.LibraryArtistCredit) LibraryArtistCredit {
	return LibraryArtistCredit{
		ArtistID: artist.ArtistID, ArtistName: artist.ArtistName, CreditName: artist.CreditName, JoinPhrase: artist.JoinPhrase,
	}
}
