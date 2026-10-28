package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryPublicationsListInput struct {
	Query  string `query:"q"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" required:"true"`
	Offset int    `query:"offset" minimum:"0" default:"0"`
}

type LibraryPublicationsListOutput struct {
	Body LibraryPublicationsListResponse `json:"body"`
}

type LibraryPublicationsListResponse struct {
	Items  []LibraryPublicationListItemResponse `json:"items" nullable:"false"`
	Total  int                                  `json:"total"`
	Limit  int                                  `json:"limit"`
	Offset int                                  `json:"offset"`
}

type LibraryPublicationListItemResponse struct {
	ReleaseID       uuid.UUID                     `json:"release_id"`
	PositionID      uuid.UUID                     `json:"position_id"`
	ReleaseTitle    string                        `json:"release_title"`
	Title           string                        `json:"title"`
	Artists         []LibraryArtistCreditResponse `json:"artists" nullable:"false"`
	Medium          int                           `json:"medium"`
	Position        int                           `json:"position"`
	DisplayedNumber string                        `json:"displayed_number"`
	OutputRoot      *string                       `json:"output_root" nullable:"true"`
	OutputEpoch     uuid.UUID                     `json:"output_epoch"`
	RelativePath    string                        `json:"relative_path"`
	Generation      uuid.UUID                     `json:"generation"`
	ValidatedAt     string                        `json:"validated_at"`
	Audio           PublicationAudioResponse      `json:"audio"`
}

func registerLibraryPublicationsList(api huma.API, list *service.LibraryPublicationsList) {
	huma.Register(api, huma.Operation{
		OperationID: "list-library-publications", Method: http.MethodGet,
		Path: "/library/publications", Summary: "Read current library publications", Tags: []string{"Library"},
	}, func(ctx context.Context, input *libraryPublicationsListInput) (*LibraryPublicationsListOutput, error) {
		if list == nil {
			return nil, huma.Error503ServiceUnavailable("library publications list is unavailable")
		}
		page, err := list.List(ctx, service.LibraryPublicationsListRequest{Query: input.Query, Limit: input.Limit, Offset: input.Offset})
		if errors.Is(err, service.ErrLibraryPublicationsListInvalidRequest) {
			return nil, huma.Error400BadRequest("library publications list request is invalid")
		}
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("library publications list is unavailable")
		}
		response := LibraryPublicationsListResponse{Items: make([]LibraryPublicationListItemResponse, 0, len(page.Items)), Total: page.Total, Limit: page.Limit, Offset: page.Offset}
		for _, item := range page.Items {
			artists := make([]LibraryArtistCreditResponse, 0, len(item.Artists))
			for _, credit := range item.Artists {
				artistID := uuid.Nil
				if credit.ArtistID != nil {
					artistID = *credit.ArtistID
				}
				artistName := credit.ArtistName
				artists = append(artists, LibraryArtistCreditResponse{ArtistID: artistID, ArtistName: &artistName, CreditName: credit.CreditName, JoinPhrase: credit.JoinPhrase})
			}
			response.Items = append(response.Items, LibraryPublicationListItemResponse{
				ReleaseID: item.ReleaseID, PositionID: item.PositionID, ReleaseTitle: item.ReleaseTitle,
				Title: item.Title, Artists: artists, Medium: item.Medium, Position: item.Position,
				DisplayedNumber: item.DisplayedNumber, OutputRoot: item.OutputRoot, OutputEpoch: item.OutputEpoch,
				RelativePath: item.RelativePath, Generation: item.Generation, ValidatedAt: item.ValidatedAt,
				Audio: PublicationAudioResponse{
					StreamIndex: item.Audio.StreamIndex, Codec: item.Audio.Codec, Lossless: item.Audio.Lossless,
					Channels: item.Audio.Channels, Bitrate: item.Audio.Bitrate, BitDepth: item.Audio.BitDepth,
					SampleRate: item.Audio.SampleRate, Container: item.Audio.Container,
					SupportedSourceContainer: item.Audio.SupportedSourceContainer,
					ALACCapabilitiesVerified: item.Audio.ALACCapabilitiesVerified,
				},
			})
		}
		return &LibraryPublicationsListOutput{Body: response}, nil
	})
}
