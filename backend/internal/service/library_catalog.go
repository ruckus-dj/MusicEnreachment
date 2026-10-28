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
	ErrLibraryArtistNotFound     = errors.New("library artist not found")
	errLibraryCatalogUnavailable = errors.New("library catalog is unavailable")
)

type LibraryCatalogFilter struct {
	Limit       int
	Offset      int
	Query       string
	Publication string
	ArtistID    *uuid.UUID
	ReleaseID   *uuid.UUID
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

type libraryCatalogRepository interface {
	ReadLibraryArtists(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error)
	ReadLibraryReleases(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogReleasePage, error)
	ReadLibraryPositions(context.Context, persistence.LibraryCatalogRequest) (persistence.LibraryCatalogPositionPage, error)
	ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error)
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
	page, err := repository.ReadLibraryArtists(ctx, catalogRequest(filter))
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogArtist]{}, fmt.Errorf("read library artists: %w", err)
	}
	items := make([]LibraryCatalogArtist, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mapLibraryCatalogArtist(item))
	}
	return LibraryCatalogPage[LibraryCatalogArtist]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset, Summary: mapLibraryCatalogSummary(page.Summary)}, nil
}

func (catalog *LibraryCatalog) Releases(ctx context.Context, filter LibraryCatalogFilter) (LibraryCatalogPage[LibraryCatalogRelease], error) {
	if err := validateLibraryCatalogFilter(&filter); err != nil {
		return LibraryCatalogPage[LibraryCatalogRelease]{}, err
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogRelease]{}, err
	}
	page, err := repository.ReadLibraryReleases(ctx, catalogRequest(filter))
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogRelease]{}, fmt.Errorf("read library releases: %w", err)
	}
	items := make([]LibraryCatalogRelease, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mapLibraryCatalogRelease(item))
	}
	return LibraryCatalogPage[LibraryCatalogRelease]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset, Summary: mapLibraryCatalogSummary(page.Summary)}, nil
}

func (catalog *LibraryCatalog) Positions(ctx context.Context, filter LibraryCatalogFilter) (LibraryCatalogPage[LibraryCatalogPosition], error) {
	if err := validateLibraryCatalogFilter(&filter); err != nil {
		return LibraryCatalogPage[LibraryCatalogPosition]{}, err
	}
	repository, err := catalog.repo()
	if err != nil {
		return LibraryCatalogPage[LibraryCatalogPosition]{}, err
	}
	page, err := repository.ReadLibraryPositions(ctx, catalogRequest(filter))
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
	return mapLibraryCatalogArtist(*artist), nil
}

func (catalog *LibraryCatalog) repo() (libraryCatalogRepository, error) {
	if catalog == nil || catalog.repository == nil {
		return nil, fmt.Errorf("library catalog: %w", errLibraryCatalogUnavailable)
	}
	return catalog.repository, nil
}

func IsLibraryArtistNotFound(err error) bool     { return errors.Is(err, ErrLibraryArtistNotFound) }
func IsLibraryCatalogUnavailable(err error) bool { return errors.Is(err, errLibraryCatalogUnavailable) }

func catalogRequest(filter LibraryCatalogFilter) persistence.LibraryCatalogRequest {
	return persistence.LibraryCatalogRequest{Limit: filter.Limit, Offset: filter.Offset, Query: filter.Query, Publication: filter.Publication, ArtistID: filter.ArtistID, ReleaseID: filter.ReleaseID}
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
	return LibraryCatalogArtist{ID: value.ID, Name: value.Name, Disambiguation: value.Disambiguation, Genres: genres, ProviderLinks: links, ReleaseCount: value.ReleaseCount, PositionCount: value.PositionCount, PublishedPositionCount: value.PublishedPositionCount}
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
		ProviderDurationMS: value.ProviderDurationMS, SourceVariantCount: value.SourceVariantCount, SelectedMediaVariantID: value.SelectedMediaVariantID, Publication: publication}
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
