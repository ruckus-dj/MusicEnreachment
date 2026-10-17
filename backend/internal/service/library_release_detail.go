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
	ID               uuid.UUID
	Title            string
	Metadata         json.RawMessage
	MetadataVersion  int
	MetadataRevision int64
	PrimaryArtist    LibraryArtistResolution
	Artists          []LibraryArtistCredit
	Tracks           []LibraryReleaseTrackDetail
}

type LibraryArtistCredit struct {
	ArtistID   uuid.UUID
	ArtistName *string
	CreditName string
	JoinPhrase string
}

type LibraryReleaseTrackDetail struct {
	ID                        uuid.UUID
	LocalTrackID              uuid.UUID
	RecordingID               *uuid.UUID
	Medium                    int
	Position                  int
	DisplayedNumber           string
	Title                     string
	Metadata                  json.RawMessage
	MetadataVersion           int
	MetadataRevision          int64
	TrackMetadataRevision     int64
	RecordingMetadataRevision *int64
	PrimaryArtist             LibraryArtistResolution
	Artists                   []LibraryArtistCredit
	Sources                   []LibraryTrackSourceDetail
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

type LibraryArtistResolutionStatus string

const (
	LibraryArtistMissing   LibraryArtistResolutionStatus = "missing"
	LibraryArtistAutomatic LibraryArtistResolutionStatus = "automatic"
	LibraryArtistRequired  LibraryArtistResolutionStatus = "required"
	LibraryArtistExplicit  LibraryArtistResolutionStatus = "explicit"
	LibraryArtistInvalid   LibraryArtistResolutionStatus = "invalid"
)

type LibraryArtistResolution struct {
	Status              LibraryArtistResolutionStatus `json:"status"`
	SelectedArtistID    *uuid.UUID                    `json:"selected_artist_id"`
	EffectiveArtistID   *uuid.UUID                    `json:"effective_artist_id"`
	EffectiveArtistMBID *uuid.UUID                    `json:"effective_artist_mbid"`
	CandidateArtistIDs  []uuid.UUID                   `json:"candidate_artist_ids"`
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
		ID: detail.ID, Title: detail.Title, Metadata: detail.Metadata, MetadataVersion: detail.MetadataVersion, MetadataRevision: detail.MetadataRevision,
		PrimaryArtist: resolveLibraryArtist(detail.PrimaryArtistID, detail.PrimaryArtistCandidates, detail.PrimaryArtistMBIDs),
		Artists:       make([]LibraryArtistCredit, 0, len(detail.Artists)), Tracks: make([]LibraryReleaseTrackDetail, 0, len(detail.Tracks)),
	}
	for _, artist := range detail.Artists {
		result.Artists = append(result.Artists, libraryArtistCredit(artist))
	}
	for _, track := range detail.Tracks {
		item := LibraryReleaseTrackDetail{
			ID: track.ID, LocalTrackID: track.LocalTrackID, RecordingID: track.RecordingID,
			Medium: track.Medium, Position: track.Position, DisplayedNumber: track.DisplayedNumber,
			Title: track.Title, Metadata: track.Metadata, MetadataVersion: track.MetadataVersion,
			MetadataRevision: track.MetadataRevision, TrackMetadataRevision: track.TrackMetadataRevision,
			RecordingMetadataRevision: track.RecordingMetadataRevision,
			PrimaryArtist:             resolveLibraryArtist(track.PrimaryArtistID, track.PrimaryArtistCandidates, track.PrimaryArtistMBIDs),
			Artists:                   make([]LibraryArtistCredit, 0, len(track.Artists)),
			Sources:                   make([]LibraryTrackSourceDetail, 0, len(track.Sources)),
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

func resolveLibraryArtist(selected *uuid.UUID, candidates []uuid.UUID, mbidMaps ...map[uuid.UUID]*uuid.UUID) LibraryArtistResolution {
	var mbids map[uuid.UUID]*uuid.UUID
	if len(mbidMaps) > 0 {
		mbids = mbidMaps[0]
	}
	result := LibraryArtistResolution{SelectedArtistID: selected, CandidateArtistIDs: append([]uuid.UUID(nil), candidates...)}
	if selected != nil {
		result.Status = LibraryArtistInvalid
		for _, candidate := range candidates {
			if *selected == candidate {
				result.Status, result.EffectiveArtistID = LibraryArtistExplicit, selected
				result.EffectiveArtistMBID = unambiguousArtistMBID(candidate, candidates, mbids)
				return result
			}
		}
		return result
	}
	switch len(candidates) {
	case 0:
		result.Status = LibraryArtistMissing
	case 1:
		result.Status, result.EffectiveArtistID = LibraryArtistAutomatic, &result.CandidateArtistIDs[0]
		result.EffectiveArtistMBID = unambiguousArtistMBID(result.CandidateArtistIDs[0], candidates, mbids)
	default:
		result.Status = LibraryArtistRequired
	}
	return result
}

func unambiguousArtistMBID(artistID uuid.UUID, candidates []uuid.UUID, mbids map[uuid.UUID]*uuid.UUID) *uuid.UUID {
	mbid := mbids[artistID]
	if mbid == nil {
		return nil
	}
	for _, candidate := range candidates {
		if candidate != artistID && mbids[candidate] != nil && *mbids[candidate] == *mbid {
			return nil
		}
	}
	return mbid
}

func libraryArtistCredit(artist persistence.LibraryArtistCredit) LibraryArtistCredit {
	return LibraryArtistCredit{
		ArtistID: artist.ArtistID, ArtistName: artist.ArtistName, CreditName: artist.CreditName, JoinPhrase: artist.JoinPhrase,
	}
}
