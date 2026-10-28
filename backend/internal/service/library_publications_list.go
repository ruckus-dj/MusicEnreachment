package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

var ErrLibraryPublicationsListInvalidRequest = errors.New("library publications list request is invalid")

type LibraryPublicationsListRequest struct {
	Query  string
	Limit  int
	Offset int
}

type LibraryPublicationsListCredit struct {
	ArtistID   *uuid.UUID
	ArtistName string
	CreditName string
	JoinPhrase string
}

type LibraryPublicationsListAudio struct {
	StreamIndex              int
	Codec                    string
	Lossless                 *bool
	Channels                 *int
	Bitrate                  *int
	BitDepth                 *int
	SampleRate               *int
	Container                string
	SupportedSourceContainer bool
	ALACCapabilitiesVerified bool
}

type LibraryPublicationsListItem struct {
	ReleaseID       uuid.UUID
	PositionID      uuid.UUID
	ReleaseTitle    string
	Title           string
	Artists         []LibraryPublicationsListCredit
	Medium          int
	Position        int
	DisplayedNumber string
	OutputRoot      *string
	OutputEpoch     uuid.UUID
	RelativePath    string
	Generation      uuid.UUID
	ValidatedAt     string
	Audio           LibraryPublicationsListAudio
}

type LibraryPublicationsListPage struct {
	Items  []LibraryPublicationsListItem
	Total  int
	Limit  int
	Offset int
}

type libraryPublicationsListRepository interface {
	ReadLibraryPublicationsList(context.Context, persistence.LibraryPublicationListRequest) (persistence.LibraryPublicationListPage, error)
}

type LibraryPublicationsList struct {
	repository libraryPublicationsListRepository
}

func NewLibraryPublicationsList(repository libraryPublicationsListRepository) *LibraryPublicationsList {
	return &LibraryPublicationsList{repository: repository}
}

func (list *LibraryPublicationsList) List(ctx context.Context, request LibraryPublicationsListRequest) (LibraryPublicationsListPage, error) {
	if list == nil || list.repository == nil {
		return LibraryPublicationsListPage{}, fmt.Errorf("library publications list repository is unavailable")
	}
	if request.Limit < 1 || request.Limit > 100 || request.Offset < 0 {
		return LibraryPublicationsListPage{}, fmt.Errorf("library publications list: %w", ErrLibraryPublicationsListInvalidRequest)
	}
	page, err := list.repository.ReadLibraryPublicationsList(ctx, persistence.LibraryPublicationListRequest{
		Query: strings.TrimSpace(request.Query), Limit: request.Limit, Offset: request.Offset,
	})
	if err != nil {
		return LibraryPublicationsListPage{}, fmt.Errorf("read library publications list: %w", err)
	}
	items := make([]LibraryPublicationsListItem, 0, len(page.Items))
	for _, item := range page.Items {
		artists := make([]LibraryPublicationsListCredit, 0, len(item.Artists))
		for _, credit := range item.Artists {
			artists = append(artists, LibraryPublicationsListCredit{
				ArtistID: credit.ArtistID, ArtistName: credit.ArtistName, CreditName: credit.CreditName, JoinPhrase: credit.JoinPhrase,
			})
		}
		items = append(items, LibraryPublicationsListItem{
			ReleaseID: item.ReleaseID, PositionID: item.PositionID, ReleaseTitle: item.ReleaseTitle,
			Title: item.Title, Artists: artists, Medium: item.Medium, Position: item.Position,
			DisplayedNumber: item.DisplayedNumber, OutputRoot: item.OutputRoot, OutputEpoch: item.OutputEpoch,
			RelativePath: item.RelativePath, Generation: item.Generation, ValidatedAt: item.ValidatedAt,
			Audio: LibraryPublicationsListAudio{
				StreamIndex: item.Audio.StreamIndex, Codec: item.Audio.Codec, Lossless: item.Audio.Lossless,
				Channels: item.Audio.Channels, Bitrate: item.Audio.Bitrate, BitDepth: item.Audio.BitDepth,
				SampleRate: item.Audio.SampleRate, Container: item.Audio.Container,
				SupportedSourceContainer: item.Audio.SupportedSourceContainer,
				ALACCapabilitiesVerified: item.Audio.ALACCapabilitiesVerified,
			},
		})
	}
	return LibraryPublicationsListPage{Items: items, Total: page.Total, Limit: request.Limit, Offset: request.Offset}, nil
}
