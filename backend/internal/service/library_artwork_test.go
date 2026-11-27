package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type libraryArtworkPageFixture struct {
	pages map[int]persistence.LibraryCatalogArtistPage
	calls []int
	err   error
}

func (fixture *libraryArtworkPageFixture) ReadLibraryArtists(_ context.Context, request persistence.LibraryCatalogRequest) (persistence.LibraryCatalogArtistPage, error) {
	fixture.calls = append(fixture.calls, request.Offset)
	if fixture.err != nil {
		return persistence.LibraryCatalogArtistPage{}, fixture.err
	}
	return fixture.pages[request.Offset], nil
}

type libraryArtworkInsertCapture struct {
	args       []river.JobArgs
	options    []*river.InsertOpts
	insertErrs []error
}

func (capture *libraryArtworkInsertCapture) Insert(_ context.Context, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	capture.args = append(capture.args, args)
	capture.options = append(capture.options, options)
	if len(capture.insertErrs) >= len(capture.args) {
		return nil, capture.insertErrs[len(capture.args)-1]
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: int64(len(capture.args))}}, nil
}

func testMusicBrainzArtistLink(key string) persistence.LibraryCatalogProviderLink {
	return persistence.LibraryCatalogProviderLink{ProviderCode: "musicbrainz", ProviderKey: key}
}

func TestLibraryArtworkSchedulerDispatchPageEnqueuesOnlyExactIdentities(t *testing.T) {
	first := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	second := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	third := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	firstMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	secondMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	thirdMBID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	fixture := &libraryArtworkPageFixture{pages: map[int]persistence.LibraryCatalogArtistPage{
		0: {
			Total: 3,
			Items: []persistence.LibraryCatalogArtist{
				{ID: first, ProviderLinks: []persistence.LibraryCatalogProviderLink{testMusicBrainzArtistLink(firstMBID)}},
				{ID: second, ProviderLinks: []persistence.LibraryCatalogProviderLink{{ProviderCode: "local", ProviderKey: secondMBID}}},
				{ID: third, ProviderLinks: []persistence.LibraryCatalogProviderLink{
					testMusicBrainzArtistLink(thirdMBID),
					testMusicBrainzArtistLink("dddddddd-dddd-4ddd-8ddd-dddddddddddd"),
				}},
			},
		},
	}}
	capture := &libraryArtworkInsertCapture{}
	scheduler := NewLibraryArtworkScheduler(fixture)
	scheduler.SetInserter(capture)

	if err := scheduler.DispatchPage(context.Background(), 0); err != nil {
		t.Fatalf("DispatchPage() error = %v", err)
	}
	if len(capture.args) != 1 {
		t.Fatalf("enqueued args = %d, want 1 exact artist", len(capture.args))
	}
	args, ok := capture.args[0].(LibraryArtistArtworkArgs)
	if !ok || args.ArtistID != first || args.ArtistMBID != firstMBID {
		t.Fatalf("enqueued artist args = %#v, want %s/%s", capture.args[0], first, firstMBID)
	}
	options := capture.options[0]
	if options == nil || options.Queue != LibraryArtworkQueue || options.MaxAttempts != 5 {
		t.Fatalf("artist insert options = %+v, want queue %q and 5 attempts", options, LibraryArtworkQueue)
	}
	if !options.UniqueOpts.ByArgs || options.UniqueOpts.ByPeriod != libraryArtistArtworkPeriod {
		t.Fatalf("artist unique options = %+v, want args and %s period", options.UniqueOpts, libraryArtistArtworkPeriod)
	}
}

func TestLibraryArtworkSchedulerDispatchPageContinuesDurably(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	fixture := &libraryArtworkPageFixture{pages: map[int]persistence.LibraryCatalogArtistPage{
		0: {
			Total: 2,
			Items: []persistence.LibraryCatalogArtist{{
				ID: artistID, ProviderLinks: []persistence.LibraryCatalogProviderLink{testMusicBrainzArtistLink(artistMBID)},
			}},
		},
	}}
	capture := &libraryArtworkInsertCapture{}
	scheduler := NewLibraryArtworkScheduler(fixture)
	scheduler.SetInserter(capture)

	if err := scheduler.DispatchPage(context.Background(), 0); err != nil {
		t.Fatalf("DispatchPage() error = %v", err)
	}
	if len(capture.args) != 2 {
		t.Fatalf("enqueued args = %d, want artist and next page", len(capture.args))
	}
	next, ok := capture.args[1].(LibraryArtworkDispatchArgs)
	if !ok || next.Offset != 1 {
		t.Fatalf("next page args = %#v, want offset 1", capture.args[1])
	}
	options := capture.options[1]
	if options == nil || options.Queue != LibraryArtworkQueue || !options.UniqueOpts.ByArgs ||
		options.UniqueOpts.ByPeriod != libraryArtworkDispatchPeriod {
		t.Fatalf("next page insert options = %+v, want unique dispatch page", options)
	}
}

func TestLibraryArtworkSchedulerRejectsInvalidArgsAndRequiresInserter(t *testing.T) {
	scheduler := NewLibraryArtworkScheduler(&libraryArtworkPageFixture{})
	if err := scheduler.EnqueueArtist(context.Background(), uuid.Nil, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"); err == nil {
		t.Fatal("EnqueueArtist() accepted a nil artist ID")
	}
	if err := scheduler.EnqueueEdition(context.Background(), uuid.New(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "not-an-mbid"); err == nil {
		t.Fatal("EnqueueEdition() accepted a malformed release MBID")
	}
	scheduler.SetInserter(&libraryArtworkInsertCapture{})
	if err := scheduler.EnqueueArtist(context.Background(), uuid.New(), "not-an-mbid"); err == nil {
		t.Fatal("EnqueueArtist() accepted a malformed artist MBID")
	}
}

func TestLibraryArtworkSchedulerEnqueueEditionUsesExactArgs(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	releaseMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	capture := &libraryArtworkInsertCapture{}
	scheduler := NewLibraryArtworkScheduler(&libraryArtworkPageFixture{})
	scheduler.SetInserter(capture)
	if err := scheduler.EnqueueEdition(context.Background(), artistID, artistMBID, releaseMBID); err != nil {
		t.Fatalf("EnqueueEdition() error = %v", err)
	}
	args, ok := capture.args[0].(LibraryEditionArtworkArgs)
	if !ok || args.ArtistID != artistID || args.ArtistMBID != artistMBID || args.ReleaseMBID != releaseMBID {
		t.Fatalf("edition args = %#v, want exact identities", capture.args[0])
	}
	options := capture.options[0]
	if options == nil || options.Queue != LibraryArtworkQueue ||
		!options.UniqueOpts.ByArgs || options.UniqueOpts.ByPeriod != libraryEditionArtworkPeriod {
		t.Fatalf("edition insert options = %+v, want unique exact edition", options)
	}
}
