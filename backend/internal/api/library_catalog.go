package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryArtistsInput struct {
	Limit       int    `query:"limit" minimum:"1" maximum:"100" required:"true"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Query       string `query:"q"`
	Publication string `query:"publication" enum:"all,published,unpublished" default:"all"`
}

type libraryReleasesInput struct {
	Limit       int    `query:"limit" minimum:"1" maximum:"100" required:"true"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Query       string `query:"q"`
	Publication string `query:"publication" enum:"all,published,unpublished" default:"all"`
	ArtistID    string `query:"artist_id"`
}

type libraryPositionsInput struct {
	Limit       int    `query:"limit" minimum:"1" maximum:"100" required:"true"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Query       string `query:"q"`
	Publication string `query:"publication" enum:"all,published,unpublished" default:"all"`
	ArtistID    string `query:"artist_id"`
	ReleaseID   string `query:"release_id"`
}

type LibraryArtistPage struct {
	Items   []LibraryArtistSummary `json:"items" nullable:"false"`
	Total   int                    `json:"total"`
	Limit   int                    `json:"limit"`
	Offset  int                    `json:"offset"`
	Summary LibraryCatalogSummary  `json:"summary"`
}

type LibraryReleasePage struct {
	Items   []LibraryReleaseSummary `json:"items" nullable:"false"`
	Total   int                     `json:"total"`
	Limit   int                     `json:"limit"`
	Offset  int                     `json:"offset"`
	Summary LibraryCatalogSummary   `json:"summary"`
}

type LibraryPositionPage struct {
	Items   []LibraryPositionSummary `json:"items" nullable:"false"`
	Total   int                      `json:"total"`
	Limit   int                      `json:"limit"`
	Offset  int                      `json:"offset"`
	Summary LibraryCatalogSummary    `json:"summary"`
}

type LibraryCatalogSummary struct {
	ArtistCount            int `json:"artist_count"`
	ReleaseCount           int `json:"release_count"`
	PositionCount          int `json:"position_count"`
	PublishedPositionCount int `json:"published_position_count"`
}

type LibraryArtistSummary struct {
	ID                     uuid.UUID                     `json:"id"`
	Name                   string                        `json:"name"`
	Disambiguation         *string                       `json:"disambiguation" nullable:"true"`
	Genres                 []string                      `json:"genres" nullable:"false"`
	ProviderLinks          []LibraryProviderLinkResponse `json:"provider_links" nullable:"false"`
	ReleaseCount           int                           `json:"release_count"`
	PositionCount          int                           `json:"position_count"`
	PublishedPositionCount int                           `json:"published_position_count"`
}

type LibraryProviderLinkResponse struct {
	ProviderCode string `json:"provider_code"`
	ProviderKey  string `json:"provider_key"`
	Method       string `json:"method"`
}

type LibraryReleaseEditionResponse struct {
	ReleaseDate         *string  `json:"release_date" nullable:"true"`
	OriginalReleaseDate *string  `json:"original_release_date" nullable:"true"`
	Country             *string  `json:"country" nullable:"true"`
	Labels              []string `json:"labels" nullable:"false"`
	CatalogNumbers      []string `json:"catalog_numbers" nullable:"false"`
	PrimaryType         *string  `json:"primary_type" nullable:"true"`
	SecondaryTypes      []string `json:"secondary_types" nullable:"false"`
	Disambiguation      *string  `json:"disambiguation" nullable:"true"`
}

type LibraryReleaseCountsResponse struct {
	PositionCount          int  `json:"position_count"`
	PublishedPositionCount int  `json:"published_position_count"`
	AvailablePositionCount int  `json:"available_position_count"`
	ProviderPositionCount  *int `json:"provider_position_count" nullable:"true"`
}

type LibraryReleaseSummary struct {
	ID            uuid.UUID                     `json:"id"`
	Title         string                        `json:"title"`
	Artists       []LibraryArtistCreditResponse `json:"artists" nullable:"false"`
	Edition       LibraryReleaseEditionResponse `json:"edition"`
	ProviderLinks []LibraryProviderLinkResponse `json:"provider_links" nullable:"false"`
	Cover         service.ReleaseCoverSummary   `json:"cover"`
	Counts        LibraryReleaseCountsResponse  `json:"counts"`
}

type LibraryPositionReleaseResponse struct {
	ID      uuid.UUID                     `json:"id"`
	Title   string                        `json:"title"`
	Artists []LibraryArtistCreditResponse `json:"artists" nullable:"false"`
}

type PublicationAudioResponse = publicationAudioResponse

type LibraryPositionPublicationResponse struct {
	_            struct{}                 `json:"-" nullable:"true"`
	Epoch        uuid.UUID                `json:"epoch"`
	Generation   uuid.UUID                `json:"generation"`
	RelativePath string                   `json:"relative_path"`
	ValidatedAt  string                   `json:"validated_at"`
	Audio        PublicationAudioResponse `json:"audio"`
}

type LibraryPositionSummary struct {
	ID                     uuid.UUID                           `json:"id"`
	LocalTrackID           uuid.UUID                           `json:"local_track_id"`
	RecordingID            *string                             `json:"recording_id" nullable:"true"`
	Release                LibraryPositionReleaseResponse      `json:"release"`
	Medium                 int                                 `json:"medium"`
	Position               int                                 `json:"position"`
	DisplayedNumber        string                              `json:"displayed_number"`
	Title                  string                              `json:"title"`
	Artists                []LibraryArtistCreditResponse       `json:"artists" nullable:"false"`
	ProviderDurationMS     *int64                              `json:"provider_duration_ms" nullable:"true"`
	SourceVariantCount     int                                 `json:"source_variant_count"`
	SelectedMediaVariantID *string                             `json:"selected_media_variant_id" nullable:"true"`
	Publication            *LibraryPositionPublicationResponse `json:"publication"`
}

type libraryArtistsOutput struct {
	Body LibraryArtistPage `json:"body"`
}
type libraryReleasesOutput struct {
	Body LibraryReleasePage `json:"body"`
}
type libraryPositionsOutput struct {
	Body LibraryPositionPage `json:"body"`
}
type libraryArtistOutput struct {
	Body LibraryArtistSummary `json:"body"`
}
type libraryArtistInput struct {
	ArtistID string `path:"artist_id"`
}

func registerLibraryCatalog(api huma.API, catalog *service.LibraryCatalog) {
	huma.Register(api, huma.Operation{OperationID: "list-library-artists", Method: http.MethodGet, Path: "/library/artists", Summary: "Read the current local library catalog", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryArtistsInput) (*libraryArtistsOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("library catalog service is unavailable")
		}
		page, err := catalog.Artists(ctx, service.LibraryCatalogFilter{Limit: input.Limit, Offset: input.Offset, Query: input.Query, Publication: input.Publication})
		if err != nil {
			return nil, libraryCatalogError(err)
		}
		return &libraryArtistsOutput{Body: libraryArtistPage(page)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-library-releases", Method: http.MethodGet, Path: "/library/releases", Summary: "Read the current local library catalog", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryReleasesInput) (*libraryReleasesOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("library catalog service is unavailable")
		}
		artistID, err := parseOptionalLibraryID(input.ArtistID, "artist")
		if err != nil {
			return nil, err
		}
		page, err := catalog.Releases(ctx, service.LibraryCatalogFilter{Limit: input.Limit, Offset: input.Offset, Query: input.Query, Publication: input.Publication, ArtistID: artistID})
		if err != nil {
			return nil, libraryCatalogError(err)
		}
		return &libraryReleasesOutput{Body: libraryReleasePage(page)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-library-positions", Method: http.MethodGet, Path: "/library/positions", Summary: "Read the current local library catalog", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryPositionsInput) (*libraryPositionsOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("library catalog service is unavailable")
		}
		artistID, err := parseOptionalLibraryID(input.ArtistID, "artist")
		if err != nil {
			return nil, err
		}
		releaseID, err := parseOptionalLibraryID(input.ReleaseID, "release")
		if err != nil {
			return nil, err
		}
		page, err := catalog.Positions(ctx, service.LibraryCatalogFilter{Limit: input.Limit, Offset: input.Offset, Query: input.Query, Publication: input.Publication, ArtistID: artistID, ReleaseID: releaseID})
		if err != nil {
			return nil, libraryCatalogError(err)
		}
		return &libraryPositionsOutput{Body: libraryPositionPage(page)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "get-library-artist", Method: http.MethodGet, Path: "/library/artists/{artist_id}", Summary: "Read a local artist", Tags: []string{"Library"}}, func(ctx context.Context, input *libraryArtistInput) (*libraryArtistOutput, error) {
		if catalog == nil {
			return nil, huma.Error503ServiceUnavailable("library catalog service is unavailable")
		}
		id, err := uuid.Parse(input.ArtistID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid artist ID")
		}
		artist, err := catalog.Artist(ctx, id)
		if err != nil {
			return nil, libraryCatalogError(err)
		}
		return &libraryArtistOutput{Body: libraryArtist(artist)}, nil
	})
}

func parseOptionalLibraryID(value, kind string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid " + kind + " ID")
	}
	return &id, nil
}

func libraryCatalogError(err error) error {
	if service.IsLibraryArtistNotFound(err) {
		return huma.Error404NotFound("library artist not found")
	}
	if service.IsLibraryCatalogUnavailable(err) {
		return huma.Error503ServiceUnavailable("library catalog service is unavailable")
	}
	return huma.Error500InternalServerError("failed to read library catalog")
}

func libraryArtistPage(page service.LibraryCatalogPage[service.LibraryCatalogArtist]) LibraryArtistPage {
	items := make([]LibraryArtistSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, libraryArtist(item))
	}
	return LibraryArtistPage{Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset, Summary: libraryCatalogSummary(page.Summary)}
}

func libraryReleasePage(page service.LibraryCatalogPage[service.LibraryCatalogRelease]) LibraryReleasePage {
	items := make([]LibraryReleaseSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, libraryRelease(item))
	}
	return LibraryReleasePage{Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset, Summary: libraryCatalogSummary(page.Summary)}
}

func libraryPositionPage(page service.LibraryCatalogPage[service.LibraryCatalogPosition]) LibraryPositionPage {
	items := make([]LibraryPositionSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, libraryPosition(item))
	}
	return LibraryPositionPage{Items: items, Total: page.Total, Limit: page.Limit, Offset: page.Offset, Summary: libraryCatalogSummary(page.Summary)}
}

func libraryCatalogSummary(value service.LibraryCatalogSummary) LibraryCatalogSummary {
	return LibraryCatalogSummary{ArtistCount: value.ArtistCount, ReleaseCount: value.ReleaseCount, PositionCount: value.PositionCount, PublishedPositionCount: value.PublishedPositionCount}
}

func libraryArtist(value service.LibraryCatalogArtist) LibraryArtistSummary {
	return LibraryArtistSummary{ID: value.ID, Name: value.Name, Disambiguation: value.Disambiguation, Genres: nonNilAPIStrings(value.Genres), ProviderLinks: libraryProviderLinks(value.ProviderLinks), ReleaseCount: value.ReleaseCount, PositionCount: value.PositionCount, PublishedPositionCount: value.PublishedPositionCount}
}

func libraryProviderLinks(values []service.LibraryCatalogProviderLink) []LibraryProviderLinkResponse {
	result := make([]LibraryProviderLinkResponse, 0, len(values))
	for _, value := range values {
		result = append(result, LibraryProviderLinkResponse{ProviderCode: value.ProviderCode, ProviderKey: value.ProviderKey, Method: value.Method})
	}
	return result
}

func libraryCredits(values []service.LibraryCatalogCredit) []LibraryArtistCreditResponse {
	result := make([]LibraryArtistCreditResponse, 0, len(values))
	for _, value := range values {
		id := uuid.Nil
		if value.ArtistID != nil {
			id = *value.ArtistID
		}
		name := value.ArtistName
		var artistName *string
		if name != "" {
			artistName = &name
		}
		result = append(result, LibraryArtistCreditResponse{ArtistID: id, ArtistName: artistName, CreditName: value.CreditName, JoinPhrase: value.JoinPhrase})
	}
	return result
}

func libraryRelease(value service.LibraryCatalogRelease) LibraryReleaseSummary {
	return LibraryReleaseSummary{ID: value.ID, Title: value.Title, Artists: libraryCredits(value.Artists),
		Edition:       LibraryReleaseEditionResponse{ReleaseDate: value.Edition.ReleaseDate, OriginalReleaseDate: value.Edition.OriginalReleaseDate, Country: value.Edition.Country, Labels: nonNilAPIStrings(value.Edition.Labels), CatalogNumbers: nonNilAPIStrings(value.Edition.CatalogNumbers), PrimaryType: value.Edition.PrimaryType, SecondaryTypes: nonNilAPIStrings(value.Edition.SecondaryTypes), Disambiguation: value.Edition.Disambiguation},
		ProviderLinks: libraryProviderLinks(value.ProviderLinks), Cover: value.Cover,
		Counts: LibraryReleaseCountsResponse{PositionCount: value.Counts.PositionCount, PublishedPositionCount: value.Counts.PublishedPositionCount, AvailablePositionCount: value.Counts.AvailablePositionCount, ProviderPositionCount: value.Counts.ProviderPositionCount}}
}

func libraryPosition(value service.LibraryCatalogPosition) LibraryPositionSummary {
	var publication *LibraryPositionPublicationResponse
	if value.Publication != nil {
		publication = &LibraryPositionPublicationResponse{Epoch: value.Publication.Epoch, Generation: value.Publication.Generation, RelativePath: value.Publication.RelativePath, ValidatedAt: value.Publication.ValidatedAt,
			Audio: PublicationAudioResponse{StreamIndex: value.Publication.Audio.StreamIndex, Codec: value.Publication.Audio.Codec, Lossless: value.Publication.Audio.Lossless, Channels: value.Publication.Audio.Channels, Bitrate: value.Publication.Audio.Bitrate, BitDepth: value.Publication.Audio.BitDepth, SampleRate: value.Publication.Audio.SampleRate, Container: value.Publication.Audio.Container, SupportedSourceContainer: value.Publication.Audio.SupportedSourceContainer, ALACCapabilitiesVerified: value.Publication.Audio.ALACCapabilitiesVerified}}
	}
	return LibraryPositionSummary{ID: value.ID, LocalTrackID: value.LocalTrackID, RecordingID: apiUUIDString(value.RecordingID),
		Release: LibraryPositionReleaseResponse{ID: value.Release.ID, Title: value.Release.Title, Artists: libraryCredits(value.Release.Artists)}, Medium: value.Medium, Position: value.Position,
		DisplayedNumber: value.DisplayedNumber, Title: value.Title, Artists: libraryCredits(value.Artists), ProviderDurationMS: value.ProviderDurationMS,
		SourceVariantCount: value.SourceVariantCount, SelectedMediaVariantID: apiUUIDString(value.SelectedMediaVariantID), Publication: publication}
}

func apiUUIDString(value *uuid.UUID) *string {
	if value == nil {
		return nil
	}
	text := value.String()
	return &text
}

func nonNilAPIStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
