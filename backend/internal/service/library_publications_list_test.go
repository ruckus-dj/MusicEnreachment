package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type libraryPublicationsListRepositoryFixture struct {
	page    persistence.LibraryPublicationListPage
	err     error
	calls   int
	request persistence.LibraryPublicationListRequest
}

func (repository *libraryPublicationsListRepositoryFixture) ReadLibraryPublicationsList(_ context.Context, request persistence.LibraryPublicationListRequest) (persistence.LibraryPublicationListPage, error) {
	repository.calls++
	repository.request = request
	return repository.page, repository.err
}

func TestLibraryPublicationsListValidatesBeforeRepositoryAndNormalizesQuery(t *testing.T) {
	repository := &libraryPublicationsListRepositoryFixture{}
	list := NewLibraryPublicationsList(repository)
	for _, request := range []LibraryPublicationsListRequest{{Limit: 0}, {Limit: 101}, {Limit: 1, Offset: -1}} {
		if _, err := list.List(context.Background(), request); !errors.Is(err, ErrLibraryPublicationsListInvalidRequest) {
			t.Errorf("List(%+v) error = %v, want invalid request", request, err)
		}
	}
	if repository.calls != 0 {
		t.Fatalf("repository called %d times for invalid requests", repository.calls)
	}
	if _, err := list.List(context.Background(), LibraryPublicationsListRequest{Query: "  Artist \n", Limit: 100, Offset: 0}); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 1 || repository.request.Query != "Artist" || repository.request.Limit != 100 {
		t.Fatalf("repository calls/request = %d/%+v", repository.calls, repository.request)
	}
}

func TestLibraryPublicationsListMapsAudioAndEmptyPage(t *testing.T) {
	artistID := uuid.New()
	lossless := true
	channels, bitrate, depth, rate := 2, 320000, 24, 96000
	repository := &libraryPublicationsListRepositoryFixture{page: persistence.LibraryPublicationListPage{
		Items: []persistence.LibraryPublicationListItem{{
			Artists: []persistence.LibraryPublicationListCredit{{ArtistID: &artistID, ArtistName: "Artist", CreditName: "Credit", JoinPhrase: " feat. "}},
			Audio:   persistence.LibraryPublicationListAudio{StreamIndex: 2, Codec: "flac", Lossless: &lossless, Channels: &channels, Bitrate: &bitrate, BitDepth: &depth, SampleRate: &rate, Container: "flac", SupportedSourceContainer: true, ALACCapabilitiesVerified: true},
		}}, Total: 9,
	}}
	page, err := NewLibraryPublicationsList(repository).List(context.Background(), LibraryPublicationsListRequest{Limit: 10, Offset: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 9 || page.Limit != 10 || page.Offset != 20 || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}
	item := page.Items[0]
	if len(item.Artists) != 1 || item.Artists[0].ArtistID == nil || *item.Artists[0].ArtistID != artistID || item.Artists[0].JoinPhrase != " feat. " {
		t.Errorf("artists = %+v", item.Artists)
	}
	if item.Audio.StreamIndex != 2 || item.Audio.Codec != "flac" || item.Audio.Lossless == nil || !*item.Audio.Lossless || item.Audio.Channels == nil || *item.Audio.Channels != channels || item.Audio.Bitrate == nil || *item.Audio.Bitrate != bitrate || item.Audio.BitDepth == nil || *item.Audio.BitDepth != depth || item.Audio.SampleRate == nil || *item.Audio.SampleRate != rate || item.Audio.Container != "flac" || !item.Audio.SupportedSourceContainer || !item.Audio.ALACCapabilitiesVerified {
		t.Errorf("audio = %+v", item.Audio)
	}
	repository.page = persistence.LibraryPublicationListPage{Items: nil, Total: 0}
	empty, err := NewLibraryPublicationsList(repository).List(context.Background(), LibraryPublicationsListRequest{Limit: 1})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty result = %+v, %v; Items must be non-nil empty", empty, err)
	}
}

func TestLibraryPublicationsListWrapsRepositoryError(t *testing.T) {
	want := errors.New("database failure")
	repository := &libraryPublicationsListRepositoryFixture{err: want}
	_, err := NewLibraryPublicationsList(repository).List(context.Background(), LibraryPublicationsListRequest{Limit: 1})
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "read library publications list") {
		t.Fatalf("error = %v, want wrapped repository error", err)
	}
}
