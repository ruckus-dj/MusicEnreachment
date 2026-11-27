package jobs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryArtworkProjectionFixture struct {
	artist        *persistence.LibraryCatalogArtist
	artistErr     error
	releaseMBIDs  []string
	releaseErr    error
	releaseStates map[string]persistence.CoverArtArchiveRelease
	stateErr      error
	gallery       map[string]bool
	galleryCalls  []string
	galleryErr    error
}

func (fixture *libraryArtworkProjectionFixture) ReadLibraryArtist(context.Context, uuid.UUID) (*persistence.LibraryCatalogArtist, error) {
	return fixture.artist, fixture.artistErr
}

func (fixture *libraryArtworkProjectionFixture) ReadLibraryArtistCoverArtReleaseMBIDs(context.Context, uuid.UUID) ([]string, error) {
	return fixture.releaseMBIDs, fixture.releaseErr
}

func (fixture *libraryArtworkProjectionFixture) ReadCoverArtArchiveRelease(_ context.Context, mbid string) (persistence.CoverArtArchiveRelease, error) {
	if fixture.stateErr != nil {
		return persistence.CoverArtArchiveRelease{}, fixture.stateErr
	}
	return fixture.releaseStates[mbid], nil
}

func (fixture *libraryArtworkProjectionFixture) ReadCoverArtArchiveReleaseStatuses(_ context.Context, mbids []string) (map[string]persistence.CoverArtArchiveRelease, error) {
	if fixture.stateErr != nil {
		return nil, fixture.stateErr
	}
	result := make(map[string]persistence.CoverArtArchiveRelease, len(mbids))
	for _, mbid := range mbids {
		state, ok := fixture.releaseStates[mbid]
		if !ok {
			state = persistence.CoverArtArchiveRelease{
				ReleaseMBID: mbid, FrontStatus: persistence.CoverArtArchiveUnchecked,
				GalleryStatus: persistence.CoverArtArchiveUnchecked,
			}
		}
		result[mbid] = state
	}
	return result, nil
}

func (fixture *libraryArtworkProjectionFixture) CoverArtArchiveGalleryEligible(_ context.Context, mbid string) (bool, error) {
	fixture.galleryCalls = append(fixture.galleryCalls, mbid)
	return fixture.gallery[mbid], fixture.galleryErr
}

type libraryArtworkDiscographyFixture struct {
	mbids []string
	err   error
}

func (fixture *libraryArtworkDiscographyFixture) EnsureArtistDiscography(_ context.Context, mbid string) error {
	fixture.mbids = append(fixture.mbids, mbid)
	return fixture.err
}

type libraryArtworkPhotoFixture struct {
	artistIDs []uuid.UUID
	err       error
}

func (fixture *libraryArtworkPhotoFixture) Read(_ context.Context, artistID uuid.UUID) (service.LibraryArtistPhotoResponse, error) {
	fixture.artistIDs = append(fixture.artistIDs, artistID)
	return service.LibraryArtistPhotoResponse{}, fixture.err
}

type libraryArtworkEditionEnqueuerFixture struct {
	args []service.LibraryEditionArtworkArgs
	err  error
}

func (fixture *libraryArtworkEditionEnqueuerFixture) EnqueueEdition(_ context.Context, artistID uuid.UUID, artistMBID, releaseMBID string) error {
	fixture.args = append(fixture.args, service.LibraryEditionArtworkArgs{
		ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID,
	})
	return fixture.err
}

type libraryArtworkProviderFixture struct {
	front   []string
	gallery []string
	err     error
}

func (fixture *libraryArtworkProviderFixture) EnsureFront(_ context.Context, mbid string) error {
	fixture.front = append(fixture.front, mbid)
	return fixture.err
}

func (fixture *libraryArtworkProviderFixture) EnsureGallery(_ context.Context, mbid string) error {
	fixture.gallery = append(fixture.gallery, mbid)
	return fixture.err
}

func visibleArtworkArtist(id uuid.UUID, mbid string) *persistence.LibraryCatalogArtist {
	return &persistence.LibraryCatalogArtist{
		ID: id,
		ProviderLinks: []persistence.LibraryCatalogProviderLink{{
			ProviderCode: "musicbrainz", ProviderKey: mbid,
		}},
	}
}

func TestLibraryArtistArtworkWorkerWarmsDiscographyPhotoAndNeededEditions(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	frontNeeded := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	frontDone := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	galleryNeeded := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	projection := &libraryArtworkProjectionFixture{
		artist:       visibleArtworkArtist(artistID, artistMBID),
		releaseMBIDs: []string{frontNeeded, frontDone, galleryNeeded, "not-an-mbid"},
		releaseStates: map[string]persistence.CoverArtArchiveRelease{
			frontNeeded:   {FrontStatus: persistence.CoverArtArchiveUnchecked},
			frontDone:     {FrontStatus: persistence.CoverArtArchiveFrontPresent, GalleryStatus: persistence.CoverArtArchiveGalleryComplete},
			galleryNeeded: {FrontStatus: persistence.CoverArtArchiveFrontPresent, GalleryStatus: persistence.CoverArtArchiveUnchecked},
		},
		gallery: map[string]bool{galleryNeeded: true},
	}
	discography := &libraryArtworkDiscographyFixture{}
	photos := &libraryArtworkPhotoFixture{}
	editions := &libraryArtworkEditionEnqueuerFixture{}
	worker := NewLibraryArtistArtworkWorker(projection, discography, photos, editions)

	job := &river.Job[service.LibraryArtistArtworkArgs]{Args: service.LibraryArtistArtworkArgs{
		ArtistID: artistID, ArtistMBID: artistMBID,
	}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	if !reflect.DeepEqual(discography.mbids, []string{artistMBID}) {
		t.Fatalf("discography calls = %v, want %s", discography.mbids, artistMBID)
	}
	if !reflect.DeepEqual(photos.artistIDs, []uuid.UUID{artistID}) {
		t.Fatalf("photo calls = %v, want %s", photos.artistIDs, artistID)
	}
	if len(editions.args) != 2 || editions.args[0].ReleaseMBID != frontNeeded || editions.args[1].ReleaseMBID != galleryNeeded {
		t.Fatalf("edition enqueues = %+v, want front-needed and gallery-needed only", editions.args)
	}
	if !reflect.DeepEqual(projection.galleryCalls, []string{galleryNeeded}) {
		t.Fatalf("gallery eligibility calls = %v, want only the incomplete eligible edition", projection.galleryCalls)
	}
	for _, args := range editions.args {
		if args.ArtistID != artistID || args.ArtistMBID != artistMBID {
			t.Fatalf("edition args lost exact artist identity: %+v", args)
		}
	}
}

func TestLibraryArtistArtworkWorkerSkipsRemovedOrRelinkedArtist(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for name, projection := range map[string]*libraryArtworkProjectionFixture{
		"removed":  {artist: nil},
		"relinked": {artist: visibleArtworkArtist(artistID, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")},
		"hidden":   {artist: &persistence.LibraryCatalogArtist{ID: artistID}},
	} {
		t.Run(name, func(t *testing.T) {
			discography := &libraryArtworkDiscographyFixture{}
			photos := &libraryArtworkPhotoFixture{}
			editions := &libraryArtworkEditionEnqueuerFixture{}
			worker := NewLibraryArtistArtworkWorker(projection, discography, photos, editions)
			job := &river.Job[service.LibraryArtistArtworkArgs]{Args: service.LibraryArtistArtworkArgs{
				ArtistID: artistID, ArtistMBID: artistMBID,
			}}
			if err := worker.Work(context.Background(), job); err != nil {
				t.Fatalf("Work() error = %v", err)
			}
			if len(discography.mbids) != 0 || len(photos.artistIDs) != 0 || len(editions.args) != 0 {
				t.Fatalf("ineligible artist performed work: discography=%v photos=%v editions=%v",
					discography.mbids, photos.artistIDs, editions.args)
			}
		})
	}
}

func TestLibraryArtistArtworkWorkerKeepsEditionWorkAfterTransientPhotoFailure(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	releaseMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	projection := &libraryArtworkProjectionFixture{
		artist:       visibleArtworkArtist(artistID, artistMBID),
		releaseMBIDs: []string{releaseMBID},
		releaseStates: map[string]persistence.CoverArtArchiveRelease{
			releaseMBID: {FrontStatus: persistence.CoverArtArchiveUnchecked},
		},
	}
	photos := &libraryArtworkPhotoFixture{err: errors.Join(service.ErrLibraryArtistPhotoUnavailable, errors.New("temporary"))}
	editions := &libraryArtworkEditionEnqueuerFixture{}
	worker := NewLibraryArtistArtworkWorker(projection, &libraryArtworkDiscographyFixture{}, photos, editions)
	job := &river.Job[service.LibraryArtistArtworkArgs]{Args: service.LibraryArtistArtworkArgs{
		ArtistID: artistID, ArtistMBID: artistMBID,
	}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("Work() hid a transient photo failure")
	}
	if len(editions.args) != 1 || editions.args[0].ReleaseMBID != releaseMBID {
		t.Fatalf("edition enqueues = %+v, want front-needed edition despite photo failure", editions.args)
	}
}

func TestLibraryArtistArtworkWorkerKeepsLocalEditionWorkAfterDiscographyFailure(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	releaseMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	projection := &libraryArtworkProjectionFixture{
		artist:       visibleArtworkArtist(artistID, artistMBID),
		releaseMBIDs: []string{releaseMBID},
		releaseStates: map[string]persistence.CoverArtArchiveRelease{
			releaseMBID: {FrontStatus: persistence.CoverArtArchiveUnchecked},
		},
	}
	discography := &libraryArtworkDiscographyFixture{err: errors.New("temporary provider failure")}
	photos := &libraryArtworkPhotoFixture{}
	editions := &libraryArtworkEditionEnqueuerFixture{}
	worker := NewLibraryArtistArtworkWorker(projection, discography, photos, editions)
	job := &river.Job[service.LibraryArtistArtworkArgs]{Args: service.LibraryArtistArtworkArgs{
		ArtistID: artistID, ArtistMBID: artistMBID,
	}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("Work() hid the discography failure")
	}
	if len(photos.artistIDs) != 1 {
		t.Fatalf("photo calls = %v, want the artist even after discography failure", photos.artistIDs)
	}
	if len(editions.args) != 1 || editions.args[0].ReleaseMBID != releaseMBID {
		t.Fatalf("edition enqueues = %+v, want known local edition", editions.args)
	}
}

func TestLibraryEditionArtworkWorkerRechecksEligibilityAndIndependentStatus(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	releaseMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	projection := &libraryArtworkProjectionFixture{
		artist: visibleArtworkArtist(artistID, artistMBID),
		releaseStates: map[string]persistence.CoverArtArchiveRelease{
			releaseMBID: {FrontStatus: persistence.CoverArtArchiveUnchecked, GalleryStatus: persistence.CoverArtArchiveUnchecked},
		},
		gallery: map[string]bool{releaseMBID: true},
	}
	artwork := &libraryArtworkProviderFixture{}
	worker := NewLibraryEditionArtworkWorker(projection, artwork)
	job := &river.Job[service.LibraryEditionArtworkArgs]{Args: service.LibraryEditionArtworkArgs{
		ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID,
	}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() error = %v", err)
	}
	if !reflect.DeepEqual(artwork.front, []string{releaseMBID}) || !reflect.DeepEqual(artwork.gallery, []string{releaseMBID}) {
		t.Fatalf("provider work front=%v gallery=%v, want both exact edition", artwork.front, artwork.gallery)
	}
}

func TestLibraryEditionArtworkWorkerSkipsCompletedOrIneligibleEditions(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	releaseMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	for name, test := range map[string]struct {
		projection *libraryArtworkProjectionFixture
		args       service.LibraryEditionArtworkArgs
	}{
		"removed artist": {
			projection: &libraryArtworkProjectionFixture{artist: nil},
			args:       service.LibraryEditionArtworkArgs{ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID},
		},
		"relinked artist": {
			projection: &libraryArtworkProjectionFixture{artist: visibleArtworkArtist(artistID, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")},
			args:       service.LibraryEditionArtworkArgs{ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID},
		},
		"front and gallery complete": {
			projection: &libraryArtworkProjectionFixture{
				artist: visibleArtworkArtist(artistID, artistMBID),
				releaseStates: map[string]persistence.CoverArtArchiveRelease{
					releaseMBID: {FrontStatus: persistence.CoverArtArchiveFrontPresent, GalleryStatus: persistence.CoverArtArchiveGalleryComplete},
				},
				gallery: map[string]bool{releaseMBID: true},
			},
			args: service.LibraryEditionArtworkArgs{ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID},
		},
		"gallery ineligible": {
			projection: &libraryArtworkProjectionFixture{
				artist: visibleArtworkArtist(artistID, artistMBID),
				releaseStates: map[string]persistence.CoverArtArchiveRelease{
					releaseMBID: {FrontStatus: persistence.CoverArtArchiveFrontPresent, GalleryStatus: persistence.CoverArtArchiveUnchecked},
				},
				gallery: map[string]bool{releaseMBID: false},
			},
			args: service.LibraryEditionArtworkArgs{ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID},
		},
	} {
		t.Run(name, func(t *testing.T) {
			artwork := &libraryArtworkProviderFixture{}
			worker := NewLibraryEditionArtworkWorker(test.projection, artwork)
			job := &river.Job[service.LibraryEditionArtworkArgs]{Args: test.args}
			if err := worker.Work(context.Background(), job); err != nil {
				t.Fatalf("Work() error = %v", err)
			}
			if len(artwork.front) != 0 || len(artwork.gallery) != 0 {
				t.Fatalf("ineligible edition performed provider work: front=%v gallery=%v", artwork.front, artwork.gallery)
			}
		})
	}
}

func TestLibraryEditionArtworkWorkerStillCachesGalleryAfterFrontFailure(t *testing.T) {
	artistID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	artistMBID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	releaseMBID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	projection := &libraryArtworkProjectionFixture{
		artist: visibleArtworkArtist(artistID, artistMBID),
		releaseStates: map[string]persistence.CoverArtArchiveRelease{
			releaseMBID: {FrontStatus: persistence.CoverArtArchiveUnchecked, GalleryStatus: persistence.CoverArtArchiveUnchecked},
		},
		gallery: map[string]bool{releaseMBID: true},
	}
	artwork := &libraryArtworkProviderFixture{err: errors.New("temporary")}
	worker := NewLibraryEditionArtworkWorker(projection, artwork)
	job := &river.Job[service.LibraryEditionArtworkArgs]{Args: service.LibraryEditionArtworkArgs{
		ArtistID: artistID, ArtistMBID: artistMBID, ReleaseMBID: releaseMBID,
	}}
	if err := worker.Work(context.Background(), job); err == nil {
		t.Fatal("Work() hid provider errors")
	}
	if !reflect.DeepEqual(artwork.front, []string{releaseMBID}) || !reflect.DeepEqual(artwork.gallery, []string{releaseMBID}) {
		t.Fatalf("provider work front=%v gallery=%v, want independent attempts", artwork.front, artwork.gallery)
	}
}

func TestLibraryArtworkDispatchPeriodicJobSchedule(t *testing.T) {
	periodic := NewLibraryArtworkDispatchPeriodicJob()
	value := reflect.ValueOf(periodic)
	interval := time.Duration(periodicJobField(t, value, "scheduleFunc", "interval").Int())
	if interval != time.Minute {
		t.Fatalf("library artwork dispatch interval = %s, want %s", interval, time.Minute)
	}
	if id := periodicJobField(t, value, "opts", "ID").String(); id != "library-artwork-dispatch" {
		t.Fatalf("library artwork dispatch periodic job ID = %q", id)
	}
	if !periodicJobField(t, value, "opts", "RunOnStart").Bool() {
		t.Fatal("library artwork dispatch periodic job does not run on start")
	}
	constructor := periodicJobField(t, value, "constructorFunc")
	if constructor.IsNil() || constructor.Pointer() != reflect.ValueOf(service.LibraryArtworkDispatchInsert).Pointer() {
		t.Fatal("library artwork dispatch periodic job does not insert the durable sweep root")
	}
	args, opts := service.LibraryArtworkDispatchInsert()
	if _, ok := args.(service.LibraryArtworkDispatchArgs); !ok || opts == nil ||
		opts.Queue != service.LibraryArtworkQueue || !opts.UniqueOpts.ByArgs {
		t.Fatalf("dispatch root args/options = %#v/%+v, want unique dedicated queue page", args, opts)
	}
	if !reflect.DeepEqual(opts.UniqueOpts.ByState, []rivertype.JobState{
		rivertype.JobStateAvailable, rivertype.JobStateCompleted, rivertype.JobStatePending,
		rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled,
	}) {
		t.Fatalf("dispatch unique states = %v", opts.UniqueOpts.ByState)
	}
}

var _ river.JobArgs = service.LibraryArtworkDispatchArgs{}
var _ river.JobArgs = service.LibraryArtistArtworkArgs{}
var _ river.JobArgs = service.LibraryEditionArtworkArgs{}
