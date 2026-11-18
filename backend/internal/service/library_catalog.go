package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrLibraryArtistNotFound              = errors.New("library artist not found")
	ErrLibraryReleaseGroupEditionNotFound = errors.New("library release group edition not found")
	ErrLibrarySelectionInvalid            = errors.New("library selection is invalid")
	ErrLibrarySelectionArtistNotFound     = errors.New("library selection artist not found")
	ErrLibrarySelectionReleaseNotFound    = errors.New("library selection release not found")
	ErrLibrarySelectionTooBroad           = errors.New("library selection is too broad")
	errLibraryCatalogUnavailable          = errors.New("library catalog is unavailable")
)

const (
	// LibrarySelectionMaxSelectors bounds the distinct artist and release parent
	// selectors accepted by one resolution request.
	LibrarySelectionMaxSelectors = 50
	// LibrarySelectionMaxPositions bounds the response. Broader selections are
	// rejected explicitly instead of silently trimming the result.
	LibrarySelectionMaxPositions = 2000
)

type LibraryCatalogFilter struct {
	Limit        int
	Offset       int
	Query        string
	Publication  string
	Availability string
	ArtistID     *uuid.UUID
	ReleaseID    *uuid.UUID
}

// Catalog DTOs deliberately contain no persistence models or query-only fields.
type LibraryCatalogSummary struct {
	ArtistCount            int `json:"artist_count"`
	ReleaseCount           int `json:"release_count"`
	PositionCount          int `json:"position_count"`
	PublishedPositionCount int `json:"published_position_count"`
}

type LibraryCatalogProviderLink struct {
	ProviderCode string `json:"provider_code"`
	ProviderKey  string `json:"provider_key"`
	Method       string `json:"method"`
}

type LibraryCatalogArtist struct {
	ID                     uuid.UUID                    `json:"id"`
	Name                   string                       `json:"name"`
	Disambiguation         *string                      `json:"disambiguation"`
	Genres                 []string                     `json:"genres"`
	ProviderLinks          []LibraryCatalogProviderLink `json:"provider_links"`
	ReleaseCount           int                          `json:"release_count"`
	PositionCount          int                          `json:"position_count"`
	PublishedPositionCount int                          `json:"published_position_count"`
	AvailablePositionCount int                          `json:"available_position_count"`
}

type LibraryCatalogCredit struct {
	ArtistID   *uuid.UUID `json:"artist_id"`
	ArtistName string     `json:"artist_name"`
	CreditName string     `json:"credit_name"`
	JoinPhrase string     `json:"join_phrase"`
}

type LibraryCatalogEdition struct {
	ReleaseDate         *string  `json:"release_date"`
	OriginalReleaseDate *string  `json:"original_release_date"`
	Country             *string  `json:"country"`
	Labels              []string `json:"labels"`
	CatalogNumbers      []string `json:"catalog_numbers"`
	PrimaryType         *string  `json:"primary_type"`
	SecondaryTypes      []string `json:"secondary_types"`
	Disambiguation      *string  `json:"disambiguation"`
}

type LibraryCatalogReleaseCounts struct {
	PositionCount          int  `json:"position_count"`
	PublishedPositionCount int  `json:"published_position_count"`
	AvailablePositionCount int  `json:"available_position_count"`
	ProviderPositionCount  *int `json:"provider_position_count"`
}

type LibraryCatalogRelease struct {
	ID            uuid.UUID                    `json:"id"`
	Title         string                       `json:"title"`
	Artists       []LibraryCatalogCredit       `json:"artists"`
	Edition       LibraryCatalogEdition        `json:"edition"`
	ProviderLinks []LibraryCatalogProviderLink `json:"provider_links"`
	Cover         ReleaseCoverSummary          `json:"cover"`
	Counts        LibraryCatalogReleaseCounts  `json:"counts"`
}

type LibraryCatalogMissingReleaseGroup struct {
	ProviderCode        string                 `json:"provider_code"`
	ProviderKey         string                 `json:"provider_key"`
	Title               string                 `json:"title"`
	Artists             []LibraryCatalogCredit `json:"artists"`
	OriginalReleaseDate *string                `json:"original_release_date"`
}

type LibraryReleaseGroupEdition struct {
	ProviderKey string
	Title       string
	Artists     []LibraryCatalogCredit
	Edition     LibraryCatalogEdition
	TrackCount  *int
}

type LibraryReleaseGroupEditions struct {
	ProviderCode string
	ProviderKey  string
	Editions     []LibraryReleaseGroupEdition
	Total        int
	Limit        int
	Offset       int
}

type LibraryReleaseGroupTrack struct {
	Medium         int
	Position       int
	Number         string
	Title          string
	RecordingTitle *string
	DurationMS     *int64
}

type LibraryReleaseGroupEditionTracks struct {
	ProviderCode       string
	GroupProviderKey   string
	ReleaseProviderKey string
	Complete           *bool
	TrackCount         *int
	Tracks             []LibraryReleaseGroupTrack
}

type LibraryCatalogReleaseItem struct {
	Kind                string                             `json:"kind"`
	LibraryRelease      *LibraryCatalogRelease             `json:"library_release"`
	MissingReleaseGroup *LibraryCatalogMissingReleaseGroup `json:"missing_release_group"`
}

type LibraryCatalogPositionRelease struct {
	ID      uuid.UUID              `json:"id"`
	Title   string                 `json:"title"`
	Artists []LibraryCatalogCredit `json:"artists"`
}

type LibraryCatalogPublicationAudio struct {
	StreamIndex              int    `json:"stream_index"`
	Codec                    string `json:"codec"`
	Lossless                 *bool  `json:"lossless"`
	Channels                 *int   `json:"channels,omitempty"`
	Bitrate                  *int   `json:"bitrate,omitempty"`
	BitDepth                 *int   `json:"bit_depth,omitempty"`
	SampleRate               *int   `json:"sample_rate,omitempty"`
	Container                string `json:"container"`
	SupportedSourceContainer bool   `json:"supported_source_container"`
	ALACCapabilitiesVerified bool   `json:"alac_capabilities_verified"`
}

type LibraryCatalogPublication struct {
	Epoch        uuid.UUID                      `json:"epoch"`
	Generation   uuid.UUID                      `json:"generation"`
	RelativePath string                         `json:"relative_path"`
	ValidatedAt  string                         `json:"validated_at"`
	Audio        LibraryCatalogPublicationAudio `json:"audio"`
}

type LibraryCatalogPosition struct {
	ID                     uuid.UUID                     `json:"id"`
	LocalTrackID           uuid.UUID                     `json:"local_track_id"`
	RecordingID            *uuid.UUID                    `json:"recording_id"`
	Release                LibraryCatalogPositionRelease `json:"release"`
	Medium                 int                           `json:"medium"`
	Position               int                           `json:"position"`
	DisplayedNumber        string                        `json:"displayed_number"`
	Title                  string                        `json:"title"`
	Artists                []LibraryCatalogCredit        `json:"artists"`
	ProviderDurationMS     *int64                        `json:"provider_duration_ms"`
	SourceVariantCount     int                           `json:"source_variant_count"`
	AudioAvailable         bool                          `json:"audio_available"`
	SelectedMediaVariantID *uuid.UUID                    `json:"selected_media_variant_id"`
	Publication            *LibraryCatalogPublication    `json:"publication"`
}

type LibraryCatalogPage[T any] struct {
	Items   []T                   `json:"items"`
	Total   int                   `json:"total"`
	Limit   int                   `json:"limit"`
	Offset  int                   `json:"offset"`
	Summary LibraryCatalogSummary `json:"summary"`
}

// LibrarySelectionPosition is a resolved real local release position. ArtistIDs
// contains only requested artists that credit the release itself.
type LibrarySelectionPosition struct {
	ReleaseID    uuid.UUID   `json:"release_id"`
	PositionID   uuid.UUID   `json:"position_id"`
	Title        string      `json:"title"`
	ReleaseTitle string      `json:"release_title"`
	ArtistIDs    []uuid.UUID `json:"artist_ids"`
}

type LibrarySelection struct {
	Positions []LibrarySelectionPosition `json:"positions"`
}

type libraryCatalogRepository interface {
	ReadLibraryArtists(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error)
	ReadLibraryReleases(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogReleasePage, error)
	ReadLibraryPositions(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogPositionPage, error)
	ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error)
	ReadLibraryAudioCandidates(context.Context, persistence.LibraryCatalogRequest) ([]persistence.LibraryCatalogAudioCandidate, error)
	ReadLibraryReleaseGroupEditions(context.Context, uuid.UUID, int, int) (persistence.LibraryReleaseGroupEditions, error)
	ReadLibraryReleaseGroupEditionTracks(context.Context, uuid.UUID, string) (persistence.LibraryReleaseGroupEditionTracks, error)
	ResolveLibrarySelection(context.Context, persistence.LibrarySelectionRequest) (persistence.LibrarySelectionResolution, error)
}

type LibraryCatalog struct{ repository libraryCatalogRepository }

func NewLibraryCatalog(repository libraryCatalogRepository) *LibraryCatalog {
	return &LibraryCatalog{repository: repository}
}

func (catalog *LibraryCatalog) Artists(ctx context.Context, filter LibraryCatalogFilter) (LibraryCatalogPage[LibraryCatalogArtist], error) {
	if err := validateLibraryCatalogFilter(&filter); err != nil {
		return LibraryCatalogPage[LibraryCatalogArtist]{}, err
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogArtist]{}, err
	}
	request, err := catalog.observedRequest(ctx, filter)
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogArtist]{}, err
	}
	page, err := repository.ReadLibraryArtists(ctx, request)
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogArtist]{}, fmt.Errorf("read library artists: %w", err)
	}
	items := make([]LibraryCatalogArtist, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mapLibraryCatalogArtist(item))
	}
	return LibraryCatalogPage[LibraryCatalogArtist]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset, Summary: mapLibraryCatalogSummary(page.Summary)}, nil
}

func (catalog *LibraryCatalog) Releases(ctx context.Context, filter LibraryCatalogFilter) (LibraryCatalogPage[LibraryCatalogReleaseItem], error) {
	if err := validateLibraryCatalogFilter(&filter); err != nil {
		return LibraryCatalogPage[LibraryCatalogReleaseItem]{}, err
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogReleaseItem]{}, err
	}
	request, err := catalog.observedRequest(ctx, filter)
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogReleaseItem]{}, err
	}
	page, err := repository.ReadLibraryReleases(ctx, request)
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogReleaseItem]{}, fmt.Errorf("read library releases: %w", err)
	}
	items := make([]LibraryCatalogReleaseItem, 0, len(page.Items))
	for _, item := range page.Items {
		mapped := LibraryCatalogReleaseItem{Kind: item.Kind}
		if item.LibraryRelease != nil {
			release := mapLibraryCatalogRelease(*item.LibraryRelease)
			mapped.LibraryRelease = &release
		}
		if item.MissingReleaseGroup != nil {
			group := item.MissingReleaseGroup
			mapped.MissingReleaseGroup = &LibraryCatalogMissingReleaseGroup{ProviderCode: group.ProviderCode, ProviderKey: group.ProviderKey, Title: group.Title, Artists: mapLibraryCatalogCredits(group.Artists), OriginalReleaseDate: group.OriginalReleaseDate}
		}
		items = append(items, mapped)
	}
	return LibraryCatalogPage[LibraryCatalogReleaseItem]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset, Summary: mapLibraryCatalogSummary(page.Summary)}, nil
}

func (catalog *LibraryCatalog) ReleaseGroupEditions(ctx context.Context, groupID uuid.UUID, limit, offset int) (LibraryReleaseGroupEditions, error) {
	if groupID == uuid.Nil || limit < 1 || limit > 100 || offset < 0 {
		return LibraryReleaseGroupEditions{}, errors.New("release group edition pagination is invalid")
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryReleaseGroupEditions{}, err
	}
	page, err := repository.ReadLibraryReleaseGroupEditions(ctx, groupID, limit, offset)
	if err != nil {
		return LibraryReleaseGroupEditions{}, fmt.Errorf("read release group editions: %w", err)
	}
	items := make([]LibraryReleaseGroupEdition, 0, len(page.Items))
	for _, edition := range page.Items {
		items = append(items, LibraryReleaseGroupEdition{ProviderKey: edition.ProviderKey, Title: edition.Title, Artists: mapLibraryCatalogCredits(edition.Artists), Edition: LibraryCatalogEdition{ReleaseDate: edition.Edition.ReleaseDate, OriginalReleaseDate: edition.Edition.OriginalReleaseDate, Country: edition.Edition.Country, Labels: nonNilCatalogStrings(edition.Edition.Labels), CatalogNumbers: nonNilCatalogStrings(edition.Edition.CatalogNumbers), PrimaryType: edition.Edition.PrimaryType, SecondaryTypes: nonNilCatalogStrings(edition.Edition.SecondaryTypes), Disambiguation: edition.Edition.Disambiguation}, TrackCount: edition.TrackCount})
	}
	return LibraryReleaseGroupEditions{ProviderCode: page.ProviderCode, ProviderKey: page.ProviderKey, Editions: items, Total: page.Total, Limit: limit, Offset: offset}, nil
}

func (catalog *LibraryCatalog) ReleaseGroupEditionTracks(ctx context.Context, groupID uuid.UUID, releaseKey string) (LibraryReleaseGroupEditionTracks, error) {
	if groupID == uuid.Nil || releaseKey == "" {
		return LibraryReleaseGroupEditionTracks{}, errors.New("release group edition identity is invalid")
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryReleaseGroupEditionTracks{}, err
	}
	result, err := repository.ReadLibraryReleaseGroupEditionTracks(ctx, groupID, releaseKey)
	if err != nil {
		if errors.Is(err, persistence.ErrLibraryReleaseGroupEditionNotFound) {
			return LibraryReleaseGroupEditionTracks{}, ErrLibraryReleaseGroupEditionNotFound
		}
		return LibraryReleaseGroupEditionTracks{}, fmt.Errorf("read release group edition tracks: %w", err)
	}
	tracks := make([]LibraryReleaseGroupTrack, 0, len(result.Tracks))
	for _, track := range result.Tracks {
		tracks = append(tracks, LibraryReleaseGroupTrack{Medium: track.Medium, Position: track.Position, Number: track.Number, Title: track.Title, RecordingTitle: track.RecordingTitle, DurationMS: track.DurationMS})
	}
	return LibraryReleaseGroupEditionTracks{ProviderCode: result.ProviderCode, GroupProviderKey: result.GroupProviderKey, ReleaseProviderKey: result.ReleaseProviderKey, Complete: result.Complete, TrackCount: result.TrackCount, Tracks: tracks}, nil
}

func (catalog *LibraryCatalog) Positions(ctx context.Context, filter LibraryCatalogFilter) (LibraryCatalogPage[LibraryCatalogPosition], error) {
	if err := validateLibraryCatalogFilter(&filter); err != nil {
		return LibraryCatalogPage[LibraryCatalogPosition]{}, err
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogPosition]{}, err
	}
	request, err := catalog.observedRequest(ctx, filter)
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogPosition]{}, err
	}
	page, err := repository.ReadLibraryPositions(ctx, request)
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogPosition]{}, fmt.Errorf("read library positions: %w", err)
	}
	items := make([]LibraryCatalogPosition, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mapLibraryCatalogPosition(item))
	}
	return LibraryCatalogPage[LibraryCatalogPosition]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset, Summary: mapLibraryCatalogSummary(page.Summary)}, nil
}

func (catalog *LibraryCatalog) Artist(ctx context.Context, id uuid.UUID) (LibraryCatalogArtist, error) {
	repository, err := catalog.repo()
	if err != nil {
		return LibraryCatalogArtist{}, err
	}
	artist, err := repository.ReadLibraryArtist(ctx, id)
	if err != nil {
		return LibraryCatalogArtist{}, fmt.Errorf("read library artist: %w", err)
	}
	if artist == nil {
		return LibraryCatalogArtist{}, ErrLibraryArtistNotFound
	}
	request, err := catalog.observedRequest(ctx, LibraryCatalogFilter{ArtistID: &id})
	if err != nil {
		return LibraryCatalogArtist{}, err
	}
	artist.AvailablePositionCount = len(request.AvailablePositionIDs)
	return mapLibraryCatalogArtist(*artist), nil
}

// ResolveSelection resolves distinct parent selectors to real local release
// positions. Artist selectors match release-level catalog credits only; release
// selectors match concrete local releases exactly. Any unknown selector is
// rejected rather than ignored, and a selection broader than the response bound
// fails explicitly.
func (catalog *LibraryCatalog) ResolveSelection(ctx context.Context, artistIDs, releaseIDs []uuid.UUID) (LibrarySelection, error) {
	artists := distinctLibrarySelectionIDs(artistIDs)
	releases := distinctLibrarySelectionIDs(releaseIDs)
	if len(artists)+len(releases) == 0 {
		return LibrarySelection{}, fmt.Errorf("%w: at least one selector is required", ErrLibrarySelectionInvalid)
	}
	if len(artists)+len(releases) > LibrarySelectionMaxSelectors {
		return LibrarySelection{}, fmt.Errorf("%w: at most %d distinct selectors are accepted", ErrLibrarySelectionInvalid, LibrarySelectionMaxSelectors)
	}
	for _, ids := range [][]uuid.UUID{artists, releases} {
		for _, id := range ids {
			if id == uuid.Nil {
				return LibrarySelection{}, fmt.Errorf("%w: selector identity is empty", ErrLibrarySelectionInvalid)
			}
		}
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibrarySelection{}, err
	}
	resolution, err := repository.ResolveLibrarySelection(ctx, persistence.LibrarySelectionRequest{ArtistIDs: artists, ReleaseIDs: releases, MaxPositions: LibrarySelectionMaxPositions})
	if err != nil {
		switch {
		case errors.Is(err, persistence.ErrLibrarySelectionArtistNotFound):
			return LibrarySelection{}, ErrLibrarySelectionArtistNotFound
		case errors.Is(err, persistence.ErrLibrarySelectionReleaseNotFound):
			return LibrarySelection{}, ErrLibrarySelectionReleaseNotFound
		case errors.Is(err, persistence.ErrLibrarySelectionTooBroad):
			return LibrarySelection{}, ErrLibrarySelectionTooBroad
		}
		return LibrarySelection{}, fmt.Errorf("resolve library selection: %w", err)
	}
	positions := make([]LibrarySelectionPosition, 0, len(resolution.Positions))
	for _, position := range resolution.Positions {
		owners := position.ArtistIDs
		if owners == nil {
			owners = []uuid.UUID{}
		}
		positions = append(positions, LibrarySelectionPosition{ReleaseID: position.ReleaseID, PositionID: position.PositionID, Title: position.Title, ReleaseTitle: position.ReleaseTitle, ArtistIDs: append([]uuid.UUID{}, owners...)})
	}
	return LibrarySelection{Positions: positions}, nil
}

func (catalog *LibraryCatalog) repo() (libraryCatalogRepository, error) {
	if catalog == nil || catalog.repository == nil {
		return nil, fmt.Errorf("library catalog: %w", errLibraryCatalogUnavailable)
	}
	return catalog.repository, nil
}

func IsLibraryArtistNotFound(err error) bool     { return errors.Is(err, ErrLibraryArtistNotFound) }
func IsLibraryCatalogUnavailable(err error) bool { return errors.Is(err, errLibraryCatalogUnavailable) }
func IsLibraryReleaseGroupEditionNotFound(err error) bool {
	return errors.Is(err, ErrLibraryReleaseGroupEditionNotFound)
}
func IsLibrarySelectionInvalid(err error) bool { return errors.Is(err, ErrLibrarySelectionInvalid) }
func IsLibrarySelectionArtistNotFound(err error) bool {
	return errors.Is(err, ErrLibrarySelectionArtistNotFound)
}
func IsLibrarySelectionReleaseNotFound(err error) bool {
	return errors.Is(err, ErrLibrarySelectionReleaseNotFound)
}
func IsLibrarySelectionTooBroad(err error) bool { return errors.Is(err, ErrLibrarySelectionTooBroad) }

func distinctLibrarySelectionIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	distinct := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		distinct = append(distinct, id)
	}
	return distinct
}

func catalogRequest(filter LibraryCatalogFilter) persistence.LibraryCatalogRequest {
	return persistence.LibraryCatalogRequest{Limit: filter.Limit, Offset: filter.Offset, Query: filter.Query, Publication: filter.Publication, Availability: filter.Availability, ArtistID: filter.ArtistID, ReleaseID: filter.ReleaseID}
}

func mapLibraryCatalogSummary(value persistence.LibraryCatalogSummary) LibraryCatalogSummary {
	return LibraryCatalogSummary{ArtistCount: value.ArtistCount, ReleaseCount: value.ReleaseCount, PositionCount: value.PositionCount, PublishedPositionCount: value.PublishedPositionCount}
}

func mapLibraryCatalogArtist(value persistence.LibraryCatalogArtist) LibraryCatalogArtist {
	links := make([]LibraryCatalogProviderLink, 0, len(value.ProviderLinks))
	for _, link := range value.ProviderLinks {
		links = append(links, LibraryCatalogProviderLink{ProviderCode: link.ProviderCode, ProviderKey: link.ProviderKey, Method: link.Method})
	}
	genres := append([]string(nil), value.Genres...)
	if genres == nil {
		genres = []string{}
	}
	return LibraryCatalogArtist{ID: value.ID, Name: value.Name, Disambiguation: value.Disambiguation, Genres: genres, ProviderLinks: links, ReleaseCount: value.ReleaseCount, PositionCount: value.PositionCount, PublishedPositionCount: value.PublishedPositionCount, AvailablePositionCount: value.AvailablePositionCount}
}

func mapLibraryCatalogCredit(value persistence.LibraryCatalogCredit) LibraryCatalogCredit {
	return LibraryCatalogCredit{ArtistID: value.ArtistID, ArtistName: value.ArtistName, CreditName: value.CreditName, JoinPhrase: value.JoinPhrase}
}

func mapLibraryCatalogCredits(values []persistence.LibraryCatalogCredit) []LibraryCatalogCredit {
	result := make([]LibraryCatalogCredit, 0, len(values))
	for _, value := range values {
		result = append(result, mapLibraryCatalogCredit(value))
	}
	return result
}

func mapLibraryCatalogRelease(value persistence.LibraryCatalogRelease) LibraryCatalogRelease {
	links := make([]LibraryCatalogProviderLink, 0, len(value.ProviderLinks))
	for _, link := range value.ProviderLinks {
		links = append(links, LibraryCatalogProviderLink{ProviderCode: link.ProviderCode, ProviderKey: link.ProviderKey, Method: link.Method})
	}
	return LibraryCatalogRelease{
		ID: value.ID, Title: value.Title, Artists: mapLibraryCatalogCredits(value.Artists),
		Edition:       LibraryCatalogEdition{ReleaseDate: value.Edition.ReleaseDate, OriginalReleaseDate: value.Edition.OriginalReleaseDate, Country: value.Edition.Country, Labels: nonNilCatalogStrings(value.Edition.Labels), CatalogNumbers: nonNilCatalogStrings(value.Edition.CatalogNumbers), PrimaryType: value.Edition.PrimaryType, SecondaryTypes: nonNilCatalogStrings(value.Edition.SecondaryTypes), Disambiguation: value.Edition.Disambiguation},
		ProviderLinks: links, Cover: ReleaseCoverSummary{ReleaseID: value.ID, Revision: value.Cover.Revision, Mode: value.Cover.Mode, SelectedGeneration: value.Cover.SelectedGeneration},
		Counts: LibraryCatalogReleaseCounts{PositionCount: value.Counts.PositionCount, PublishedPositionCount: value.Counts.PublishedPositionCount, AvailablePositionCount: value.Counts.AvailablePositionCount, ProviderPositionCount: value.Counts.ProviderPositionCount},
	}
}

func mapLibraryCatalogPosition(value persistence.LibraryCatalogPosition) LibraryCatalogPosition {
	var publication *LibraryCatalogPublication
	if value.Publication != nil {
		publication = &LibraryCatalogPublication{Epoch: value.Publication.Epoch, Generation: value.Publication.Generation, RelativePath: value.Publication.RelativePath, ValidatedAt: value.Publication.ValidatedAt,
			Audio: LibraryCatalogPublicationAudio{StreamIndex: value.Publication.Audio.StreamIndex, Codec: value.Publication.Audio.Codec, Lossless: value.Publication.Audio.Lossless, Channels: value.Publication.Audio.Channels, Bitrate: value.Publication.Audio.Bitrate, BitDepth: value.Publication.Audio.BitDepth, SampleRate: value.Publication.Audio.SampleRate, Container: value.Publication.Audio.Container, SupportedSourceContainer: value.Publication.Audio.SupportedSourceContainer, ALACCapabilitiesVerified: value.Publication.Audio.ALACCapabilitiesVerified}}
	}
	return LibraryCatalogPosition{ID: value.ID, LocalTrackID: value.LocalTrackID, RecordingID: value.RecordingID,
		Release: LibraryCatalogPositionRelease{ID: value.Release.ID, Title: value.Release.Title, Artists: mapLibraryCatalogCredits(value.Release.Artists)},
		Medium:  value.Medium, Position: value.Position, DisplayedNumber: value.DisplayedNumber, Title: value.Title, Artists: mapLibraryCatalogCredits(value.Artists),
		ProviderDurationMS: value.ProviderDurationMS, SourceVariantCount: value.SourceVariantCount, AudioAvailable: value.AudioAvailable, SelectedMediaVariantID: value.SelectedMediaVariantID, Publication: publication}
}

func nonNilCatalogStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func validateLibraryCatalogFilter(filter *LibraryCatalogFilter) error {
	if filter == nil {
		return errors.New("library catalog filter is required")
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return errors.New("library catalog pagination is invalid")
	}
	if filter.Publication == "" {
		filter.Publication = "all"
	}
	if filter.Publication != "all" && filter.Publication != "published" && filter.Publication != "unpublished" {
		return errors.New("library catalog publication filter is invalid")
	}
	if filter.Availability == "" {
		filter.Availability = "all"
	}
	if filter.Availability != "all" && filter.Availability != "available" && filter.Availability != "missing" {
		return errors.New("library catalog availability filter is invalid")
	}
	filter.Query = normalizeLibraryCatalogQuery(filter.Query)
	return nil
}

func normalizeLibraryCatalogQuery(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	var result strings.Builder
	space := true
	for _, r := range value {
		if unicode.IsSpace(r) {
			if !space {
				result.WriteByte(' ')
				space = true
			}
			continue
		}
		result.WriteRune(r)
		space = false
	}
	return strings.TrimSpace(result.String())
}
