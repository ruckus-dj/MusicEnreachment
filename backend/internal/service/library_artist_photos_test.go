package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type artistPhotoRepositoryFixture struct {
	mu      sync.Mutex
	binding persistence.LibraryArtistPhotoBinding
	byID    map[uuid.UUID]persistence.LibraryArtistPhotoBinding
	err     error
	reads   int
}

func (repository *artistPhotoRepositoryFixture) ReadLibraryArtistPhotoBinding(_ context.Context, id uuid.UUID) (persistence.LibraryArtistPhotoBinding, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.reads++
	if repository.err != nil {
		return persistence.LibraryArtistPhotoBinding{}, repository.err
	}
	if repository.byID != nil {
		return repository.byID[id], nil
	}
	return repository.binding, nil
}

type artistPhotoStoreFixture struct {
	mu      sync.Mutex
	records map[string]persistence.LibraryArtistPhotoRecord
	reads   int
	saves   int
	err     error
}

func artistPhotoStoreKey(identity, mbid string) string {
	return identity + "\x00" + mbid
}

func (store *artistPhotoStoreFixture) ReadLibraryArtistPhoto(_ context.Context, identity, mbid string) (persistence.LibraryArtistPhotoRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads++
	if store.err != nil {
		return persistence.LibraryArtistPhotoRecord{}, store.err
	}
	record, ok := store.records[artistPhotoStoreKey(identity, mbid)]
	if !ok {
		return persistence.LibraryArtistPhotoRecord{}, persistence.ErrLibraryArtistPhotoNotFound
	}
	return record, nil
}

func (store *artistPhotoStoreFixture) SaveLibraryArtistPhoto(_ context.Context, record persistence.LibraryArtistPhotoRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saves++
	if store.err != nil {
		return store.err
	}
	if store.records == nil {
		store.records = make(map[string]persistence.LibraryArtistPhotoRecord)
	}
	store.records[artistPhotoStoreKey(record.ConfigIdentity, record.ArtistMBID.String())] = record
	return nil
}

func (store *artistPhotoStoreFixture) record(identity, mbid string) (persistence.LibraryArtistPhotoRecord, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[artistPhotoStoreKey(identity, mbid)]
	return record, ok
}

type artistPhotoDurableRepositoryFixture struct {
	artistPhotoRepositoryFixture
	store artistPhotoStoreFixture
}

func (repository *artistPhotoDurableRepositoryFixture) ReadLibraryArtistPhoto(ctx context.Context, identity, mbid string) (persistence.LibraryArtistPhotoRecord, error) {
	return repository.store.ReadLibraryArtistPhoto(ctx, identity, mbid)
}

func (repository *artistPhotoDurableRepositoryFixture) SaveLibraryArtistPhoto(ctx context.Context, record persistence.LibraryArtistPhotoRecord) error {
	return repository.store.SaveLibraryArtistPhoto(ctx, record)
}

type artistPhotoDurableResolverFixture struct {
	artistPhotoResolverFixture
	download artistPhotoDownloaderFixture
}

func (fixture *artistPhotoDurableResolverFixture) DownloadImage(ctx context.Context, imageURL string) ([]byte, string, error) {
	return fixture.download.DownloadImage(ctx, imageURL)
}

type artistPhotoDownloaderFixture struct {
	mu   sync.Mutex
	data []byte
	mime string
	err  error
	urls []string
}

func (fixture *artistPhotoDownloaderFixture) DownloadImage(_ context.Context, imageURL string) ([]byte, string, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.urls = append(fixture.urls, imageURL)
	return append([]byte(nil), fixture.data...), fixture.mime, fixture.err
}

func (fixture *artistPhotoDownloaderFixture) callCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return len(fixture.urls)
}

type artistPhotoSettingsFixture struct {
	config settings.MusicBrainzConfig
	err    error
	reads  atomic.Int64
}

func (fixture *artistPhotoSettingsFixture) GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error) {
	fixture.reads.Add(1)
	return fixture.config, fixture.err
}

type artistPhotoProviderFactoryFixture struct {
	provider *musicbrainz.Provider
	err      error
	calls    atomic.Int64
}

func (fixture *artistPhotoProviderFactoryFixture) MusicBrainzForConfig(context.Context, settings.MusicBrainzConfig) (*musicbrainz.Provider, error) {
	fixture.calls.Add(1)
	return fixture.provider, fixture.err
}

type artistPhotoResolverFixture struct {
	mu        sync.Mutex
	photos    []*LibraryArtistPhoto
	errs      []error
	calls     []string
	active    int
	maxActive int
	delay     time.Duration
	block     chan struct{}
}

func (fixture *artistPhotoResolverFixture) Resolve(ctx context.Context, _ *musicbrainz.Provider, mbid string) (*LibraryArtistPhoto, error) {
	fixture.mu.Lock()
	fixture.calls = append(fixture.calls, mbid)
	fixture.active++
	if fixture.active > fixture.maxActive {
		fixture.maxActive = fixture.active
	}
	index := len(fixture.calls) - 1
	fixture.mu.Unlock()

	if fixture.block != nil {
		select {
		case <-fixture.block:
		case <-ctx.Done():
		}
	}
	if fixture.delay > 0 {
		time.Sleep(fixture.delay)
	}

	fixture.mu.Lock()
	fixture.active--
	photo := fixture.photos[min(index, len(fixture.photos)-1)]
	var err error
	if len(fixture.errs) > 0 {
		err = fixture.errs[min(index, len(fixture.errs)-1)]
	}
	fixture.mu.Unlock()
	return photo, err
}

func (fixture *artistPhotoResolverFixture) callCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return len(fixture.calls)
}

func testArtistPhotoProvider(t *testing.T) *musicbrainz.Provider {
	t.Helper()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: musicbrainz.OfficialEndpoint})
	if err != nil {
		t.Fatalf("create MusicBrainz provider: %v", err)
	}
	return provider
}

func artistPhotoBinding(keys ...string) persistence.LibraryArtistPhotoBinding {
	return persistence.LibraryArtistPhotoBinding{ArtistExists: true, ProviderKeys: keys}
}

func photoFixture() *LibraryArtistPhoto {
	licenseURL := "https://creativecommons.org/licenses/by-sa/4.0/"
	return &LibraryArtistPhoto{
		ImageURL:   "https://upload.wikimedia.org/photo.jpg",
		SourceURL:  "https://commons.wikimedia.org/wiki/File:Photo.jpg",
		Author:     "Photographer",
		License:    "CC BY-SA 4.0",
		LicenseURL: &licenseURL,
	}
}

func TestValidatedMusicBrainzArtistMBID(t *testing.T) {
	valid := uuid.NewString()
	other := uuid.NewString()
	tests := []struct {
		name string
		keys []string
		want string
	}{
		{name: "none"},
		{name: "invalid mbid", keys: []string{"not-an-mbid"}},
		{name: "nil mbid", keys: []string{uuid.Nil.String()}},
		{name: "uppercase mbid", keys: []string{uppercaseMBID(valid)}, want: valid},
		{name: "valid", keys: []string{valid}, want: valid},
		{name: "duplicate", keys: []string{valid, valid}, want: valid},
		{name: "ambiguous", keys: []string{valid, other}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validatedMusicBrainzArtistMBID(test.keys); got != test.want {
				t.Fatalf("validated MBID = %q, want %q", got, test.want)
			}
		})
	}
}

func uppercaseMBID(value string) string {
	upper := make([]byte, len(value))
	for i := range value {
		c := value[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper[i] = c
	}
	return string(upper)
}

func TestLibraryArtistPhotosCachesSuccessfulPhoto(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)}
	configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
	factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}}
	photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)

	first, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if first.Photo == nil || first.Photo.ImageURL != "https://upload.wikimedia.org/photo.jpg" {
		t.Fatalf("first photo = %#v", first.Photo)
	}
	first.Photo.Author = "mutated"
	second, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second.Photo == nil || second.Photo.Author != "Photographer" {
		t.Fatalf("cached photo was mutated: %#v", second.Photo)
	}
	if resolver.callCount() != 1 || factory.calls.Load() != 1 || configuration.reads.Load() != 2 {
		t.Fatalf("resolver calls = %d, factory calls = %d, config reads = %d", resolver.callCount(), factory.calls.Load(), configuration.reads.Load())
	}
}

func TestLibraryArtistPhotosCachesNoPhoto(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)}
	configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
	factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{nil}}
	photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)

	for range 2 {
		result, err := photos.Read(context.Background(), artistID)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if result.Photo != nil {
			t.Fatalf("photo = %#v, want none", result.Photo)
		}
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.callCount())
	}
}

func TestLibraryArtistPhotosNeverResolvesWithoutValidatedMBID(t *testing.T) {
	valid := uuid.NewString()
	other := uuid.NewString()
	tests := []struct {
		name  string
		keys  []string
		exist bool
	}{
		{name: "no links", exist: true},
		{name: "invalid key", keys: []string{"broken"}, exist: true},
		{name: "ambiguous keys", keys: []string{valid, other}, exist: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artistID := uuid.New()
			repository := &artistPhotoRepositoryFixture{binding: persistence.LibraryArtistPhotoBinding{ArtistExists: test.exist, ProviderKeys: test.keys}}
			configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
			factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
			resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}}
			photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)

			result, err := photos.Read(context.Background(), artistID)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if result.Photo != nil {
				t.Fatalf("photo = %#v, want none", result.Photo)
			}
			if resolver.callCount() != 0 || factory.calls.Load() != 0 || configuration.reads.Load() != 0 {
				t.Fatalf("resolver calls = %d, factory calls = %d, config reads = %d, want none",
					resolver.callCount(), factory.calls.Load(), configuration.reads.Load())
			}
		})
	}
}

func TestLibraryArtistPhotosReturnsNotFound(t *testing.T) {
	photos := NewLibraryArtistPhotos(
		&artistPhotoRepositoryFixture{},
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		&artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}},
	)
	_, err := photos.Read(context.Background(), uuid.New())
	if !errors.Is(err, ErrLibraryArtistNotFound) {
		t.Fatalf("error = %v, want artist not found", err)
	}
}

func TestLibraryArtistPhotosSingleFlightPerMBID(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)}
	configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
	factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}, block: make(chan struct{})}
	photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)

	var wait sync.WaitGroup
	results := make(chan LibraryArtistPhotoResponse, 2)
	errors := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := photos.Read(context.Background(), artistID)
			results <- result
			errors <- err
		}()
	}
	for resolver.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(resolver.block)
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	photosSeen := make([]LibraryArtistPhotoResponse, 0, 2)
	for result := range results {
		if result.Photo == nil {
			t.Fatal("response photo is missing")
		}
		photosSeen = append(photosSeen, result)
	}
	photosSeen[0].Photo.Author = "mutated"
	if photosSeen[1].Photo.Author != "Photographer" {
		t.Fatalf("single-flight callers share a mutable photo: %#v", photosSeen[1].Photo)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.callCount())
	}
}

func TestLibraryArtistPhotosBoundsSlotWait(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)}
	configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
	factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}}
	photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)
	photos.slots = make(chan struct{}, 1)
	photos.slots <- struct{}{}
	photos.timeout = 25 * time.Millisecond
	defer func() { <-photos.slots }()

	started := time.Now()
	_, err := photos.Read(context.Background(), artistID)
	if !errors.Is(err, ErrLibraryArtistPhotoUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("slot wait took %s, want the bounded timeout", elapsed)
	}
	if resolver.callCount() != 0 {
		t.Fatalf("resolver calls = %d, want none while the slot was occupied", resolver.callCount())
	}
}

func TestLibraryArtistPhotosCachesTransientFailure(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)}
	configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
	factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
	resolver := &artistPhotoResolverFixture{
		photos: []*LibraryArtistPhoto{photoFixture(), photoFixture()},
		errs:   []error{errors.New("provider outage"), nil},
	}
	photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	photos.now = func() time.Time { return now }

	_, err := photos.Read(context.Background(), artistID)
	if !errors.Is(err, ErrLibraryArtistPhotoUnavailable) {
		t.Fatalf("first error = %v, want unavailable", err)
	}
	now = now.Add(libraryArtistPhotoFailurePause / 2)
	if _, err := photos.Read(context.Background(), artistID); !errors.Is(err, ErrLibraryArtistPhotoUnavailable) {
		t.Fatalf("cooldown error = %v, want unavailable", err)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver calls during cooldown = %d, want 1", resolver.callCount())
	}
	now = now.Add(libraryArtistPhotoFailurePause)
	result, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result.Photo == nil {
		t.Fatal("retry photo is missing")
	}
	if resolver.callCount() != 2 {
		t.Fatalf("resolver calls after cooldown = %d, want 2", resolver.callCount())
	}
}

func TestLibraryArtistPhotosEvaluatesConfigurationIdentity(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)}
	configuration := &artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}}
	factory := &artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture(), photoFixture()}}
	photos := NewLibraryArtistPhotos(repository, configuration, factory, resolver)

	if _, err := photos.Read(context.Background(), artistID); err != nil {
		t.Fatalf("first read: %v", err)
	}
	configuration.config.Identity = "config-2"
	if _, err := photos.Read(context.Background(), artistID); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if resolver.callCount() != 2 {
		t.Fatalf("resolver calls = %d, want 2 after configuration change", resolver.callCount())
	}
}

func TestLibraryArtistPhotosBoundsConcurrency(t *testing.T) {
	const artistCount = 4
	repository := &artistPhotoRepositoryFixture{byID: make(map[uuid.UUID]persistence.LibraryArtistPhotoBinding)}
	artistIDs := make([]uuid.UUID, 0, artistCount)
	for range artistCount {
		artistID := uuid.New()
		artistIDs = append(artistIDs, artistID)
		repository.byID[artistID] = artistPhotoBinding(uuid.NewString())
	}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}, delay: 20 * time.Millisecond}
	photos := NewLibraryArtistPhotos(
		repository,
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		resolver,
	)
	photos.slots = make(chan struct{}, 1)

	var wait sync.WaitGroup
	for _, artistID := range artistIDs {
		wait.Add(1)
		go func(id uuid.UUID) {
			defer wait.Done()
			_, _ = photos.Read(context.Background(), id)
		}(artistID)
	}
	wait.Wait()
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if resolver.maxActive > 1 {
		t.Fatalf("max concurrent resolver calls = %d, want at most 1", resolver.maxActive)
	}
}

func TestLibraryArtistPhotosCacheIsBounded(t *testing.T) {
	const artistCount = 2
	repository := &artistPhotoRepositoryFixture{byID: make(map[uuid.UUID]persistence.LibraryArtistPhotoBinding)}
	artistIDs := make([]uuid.UUID, 0, artistCount)
	for range artistCount {
		artistID := uuid.New()
		artistIDs = append(artistIDs, artistID)
		repository.byID[artistID] = artistPhotoBinding(uuid.NewString())
	}
	resolver := &artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}}
	photos := NewLibraryArtistPhotos(
		repository,
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		resolver,
	)
	photos.cache = newLibraryArtistPhotoCache(1)

	for _, artistID := range artistIDs {
		if _, err := photos.Read(context.Background(), artistID); err != nil {
			t.Fatalf("read %s: %v", artistID, err)
		}
	}
	if _, err := photos.Read(context.Background(), artistIDs[0]); err != nil {
		t.Fatalf("re-read first artist: %v", err)
	}
	if resolver.callCount() != 3 {
		t.Fatalf("resolver calls = %d, want 3 after eviction", resolver.callCount())
	}
}

func artistPhotoPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	imageValue := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			imageValue.Set(x, y, color.RGBA{R: 40, G: 80, B: 120, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageValue); err != nil {
		t.Fatalf("encode artist photo: %v", err)
	}
	return encoded.Bytes()
}

func artistPhotoRecord(mbid string, data []byte, width, height int) persistence.LibraryArtistPhotoRecord {
	digest := sha256.Sum256(data)
	licenseURL := "https://creativecommons.org/licenses/by-sa/4.0/"
	return persistence.LibraryArtistPhotoRecord{
		ConfigIdentity: "config-1", ArtistMBID: uuid.MustParse(mbid),
		ImageURL:  "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.png/330px-Photo.png",
		SourceURL: "https://commons.wikimedia.org/wiki/File:Photo.png", Author: "Photographer",
		License: "CC BY-SA 4.0", LicenseURL: &licenseURL,
		Data: data, MIME: "image/png", SHA256: digest[:], SizeBytes: int64(len(data)),
		Width: width, Height: height, FetchedAt: time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC),
	}
}

func TestLibraryArtistPhotosServesDurablePhotoWithoutResolving(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	data := artistPhotoPNG(t, 16, 12)
	repository := &artistPhotoDurableRepositoryFixture{
		artistPhotoRepositoryFixture: artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)},
		store: artistPhotoStoreFixture{records: map[string]persistence.LibraryArtistPhotoRecord{
			artistPhotoStoreKey("config-1", mbid): artistPhotoRecord(mbid, data, 16, 12),
		}},
	}
	resolver := &artistPhotoDurableResolverFixture{
		artistPhotoResolverFixture: artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}},
	}
	photos := NewLibraryArtistPhotos(
		repository,
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		resolver,
	)

	result, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result.Photo == nil || result.Photo.Author != "Photographer" ||
		result.Photo.SourceURL != "https://commons.wikimedia.org/wiki/File:Photo.png" {
		t.Fatalf("photo = %#v", result.Photo)
	}
	if resolver.callCount() != 0 || resolver.download.callCount() != 0 {
		t.Fatalf("resolver calls = %d, download calls = %d, want none", resolver.callCount(), resolver.download.callCount())
	}
	imageResult, err := photos.Image(context.Background(), artistID)
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	if imageResult.MIME != "image/png" || !bytes.Equal(imageResult.Data, data) {
		t.Fatalf("image = %q %d bytes", imageResult.MIME, len(imageResult.Data))
	}
}

func TestLibraryArtistPhotosPersistsResolvedPhotoByConfigIdentity(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	data := artistPhotoPNG(t, 16, 12)
	repository := &artistPhotoDurableRepositoryFixture{
		artistPhotoRepositoryFixture: artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)},
	}
	resolver := &artistPhotoDurableResolverFixture{
		artistPhotoResolverFixture: artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}},
		download:                   artistPhotoDownloaderFixture{data: data, mime: "image/png"},
	}
	photos := NewLibraryArtistPhotos(
		repository,
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		resolver,
	)

	first, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if first.Photo == nil || first.Photo.ImageURL != "https://upload.wikimedia.org/photo.jpg" {
		t.Fatalf("photo = %#v", first.Photo)
	}
	if resolver.callCount() != 1 || resolver.download.callCount() != 1 {
		t.Fatalf("resolver calls = %d, download calls = %d, want one each", resolver.callCount(), resolver.download.callCount())
	}
	record, ok := repository.store.record("config-1", mbid)
	if !ok {
		t.Fatal("resolved photo was not persisted")
	}
	if record.ImageURL != first.Photo.ImageURL || record.SourceURL != first.Photo.SourceURL ||
		record.Author != first.Photo.Author || record.License != first.Photo.License ||
		!bytes.Equal(record.Data, data) ||
		record.Width != 16 || record.Height != 12 || record.MIME != "image/png" {
		t.Fatalf("stored record = %#v", record)
	}

	second, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second.Photo == nil || second.Photo.Author != "Photographer" {
		t.Fatalf("second photo = %#v", second.Photo)
	}
	if resolver.callCount() != 1 || resolver.download.callCount() != 1 {
		t.Fatalf("durable read repeated provider work: resolver = %d, download = %d", resolver.callCount(), resolver.download.callCount())
	}
}

func TestLibraryArtistPhotosDoesNotReusePhotoAcrossConfigIdentities(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	data := artistPhotoPNG(t, 8, 8)
	repository := &artistPhotoDurableRepositoryFixture{
		artistPhotoRepositoryFixture: artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)},
		store: artistPhotoStoreFixture{records: map[string]persistence.LibraryArtistPhotoRecord{
			artistPhotoStoreKey("config-1", mbid): artistPhotoRecord(mbid, data, 8, 8),
		}},
	}
	resolver := &artistPhotoDurableResolverFixture{
		artistPhotoResolverFixture: artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{nil}},
	}
	photos := NewLibraryArtistPhotos(
		repository,
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-2"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		resolver,
	)

	result, err := photos.Read(context.Background(), artistID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result.Photo != nil {
		t.Fatalf("photo = %#v, want no photo for another configuration", result.Photo)
	}
	if resolver.callCount() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.callCount())
	}
	if _, err := photos.Image(context.Background(), artistID); !errors.Is(err, ErrLibraryArtistPhotoNotFound) {
		t.Fatalf("image error = %v, want not found", err)
	}
}

func TestLibraryArtistPhotosFailsClosedWhenDownloadFails(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	repository := &artistPhotoDurableRepositoryFixture{
		artistPhotoRepositoryFixture: artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)},
	}
	resolver := &artistPhotoDurableResolverFixture{
		artistPhotoResolverFixture: artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}},
		download:                   artistPhotoDownloaderFixture{err: errors.New("download failed")},
	}
	photos := NewLibraryArtistPhotos(
		repository,
		&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
		resolver,
	)

	result, err := photos.Read(context.Background(), artistID)
	if !errors.Is(err, ErrLibraryArtistPhotoUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if result.Photo != nil {
		t.Fatalf("photo = %#v, want no fallback to the external URL", result.Photo)
	}
	if repository.store.saves != 0 {
		t.Fatalf("saves = %d, want none", repository.store.saves)
	}
}

func TestLibraryArtistPhotosRejectsOversizedOrMismatchedDownload(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		mime string
	}{
		{name: "oversized", data: make([]byte, persistence.MaxLibraryArtistPhotoBytes+1), mime: "image/png"},
		{name: "mime mismatch", data: artistPhotoPNG(t, 4, 4), mime: "image/jpeg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artistID, mbid := uuid.New(), uuid.NewString()
			repository := &artistPhotoDurableRepositoryFixture{
				artistPhotoRepositoryFixture: artistPhotoRepositoryFixture{binding: artistPhotoBinding(mbid)},
			}
			resolver := &artistPhotoDurableResolverFixture{
				artistPhotoResolverFixture: artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}},
				download:                   artistPhotoDownloaderFixture{data: test.data, mime: test.mime},
			}
			photos := NewLibraryArtistPhotos(
				repository,
				&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
				&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
				resolver,
			)
			if _, err := photos.Read(context.Background(), artistID); !errors.Is(err, ErrLibraryArtistPhotoUnavailable) {
				t.Fatalf("error = %v, want unavailable", err)
			}
			if repository.store.saves != 0 {
				t.Fatalf("saves = %d, want none", repository.store.saves)
			}
		})
	}
}

func TestLibraryArtistPhotosImageRequiresArtistAndStoredData(t *testing.T) {
	tests := []struct {
		name     string
		binding  persistence.LibraryArtistPhotoBinding
		identity string
		want     error
	}{
		{name: "missing artist", binding: persistence.LibraryArtistPhotoBinding{}, identity: "config-1", want: ErrLibraryArtistNotFound},
		{name: "unlinked artist", binding: artistPhotoBinding(), identity: "config-1", want: ErrLibraryArtistPhotoNotFound},
		{name: "no saved photo", binding: artistPhotoBinding(uuid.NewString()), identity: "config-1", want: ErrLibraryArtistPhotoNotFound},
		{name: "no config identity", binding: artistPhotoBinding(uuid.NewString()), identity: "", want: ErrLibraryArtistPhotoUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &artistPhotoDurableRepositoryFixture{
				artistPhotoRepositoryFixture: artistPhotoRepositoryFixture{binding: test.binding},
			}
			photos := NewLibraryArtistPhotos(
				repository,
				&artistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: test.identity}},
				&artistPhotoProviderFactoryFixture{provider: testArtistPhotoProvider(t)},
				&artistPhotoResolverFixture{photos: []*LibraryArtistPhoto{photoFixture()}},
			)
			if _, err := photos.Image(context.Background(), uuid.New()); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
