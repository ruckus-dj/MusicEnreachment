package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var ErrLibraryArtistPhotoUnavailable = errors.New("library artist photo is temporarily unavailable")

var ErrLibraryArtistPhotoNotFound = errors.New("library artist photo not found")

func IsLibraryArtistPhotoUnavailable(err error) bool {
	return errors.Is(err, ErrLibraryArtistPhotoUnavailable)
}

func IsLibraryArtistPhotoNotFound(err error) bool {
	return errors.Is(err, ErrLibraryArtistPhotoNotFound)
}

const (
	// Library artist photos are optional enrichment. Successful and absent
	// results are cached for hours, while a transient provider failure only
	// pauses new outbound requests briefly.
	libraryArtistPhotoSuccessTTL    = 24 * time.Hour
	libraryArtistPhotoNoPhotoTTL    = 6 * time.Hour
	libraryArtistPhotoFailurePause  = time.Minute
	libraryArtistPhotoCacheCapacity = 512
	libraryArtistPhotoConcurrency   = 2
	libraryArtistPhotoTimeout       = 15 * time.Second
)

// LibraryArtistPhoto is the detached photo projection returned to clients. It
// is deliberately independent of the resolver implementation so the service can
// be tested without outbound HTTP.
type LibraryArtistPhoto struct {
	ImageURL   string  `json:"image_url"`
	SourceURL  string  `json:"source_url"`
	Author     string  `json:"author"`
	License    string  `json:"license"`
	LicenseURL *string `json:"license_url,omitempty"`
}

// LibraryArtistPhotoResponse is empty when no verified photo exists. The photo
// key is omitted instead of being null so clients can distinguish "looked up,
// nothing found" from a failed lookup.
type LibraryArtistPhotoResponse struct {
	Photo *LibraryArtistPhoto `json:"photo,omitempty"`
}

// ArtistPhotoResolver resolves one MusicBrainz artist into a photo. A nil photo
// with a nil error means the provider has no usable photo.
type ArtistPhotoResolver interface {
	Resolve(context.Context, *musicbrainz.Provider, string) (*LibraryArtistPhoto, error)
}

// ArtistPhotoImageDownloader fetches the bounded provider thumbnail for an
// already-resolved photo. Implementations must re-validate the URL instead of
// following an arbitrary client-supplied address.
type ArtistPhotoImageDownloader interface {
	DownloadImage(context.Context, string) ([]byte, string, error)
}

// LibraryArtistPhotoImage is one durable thumbnail served from the same origin.
type LibraryArtistPhotoImage struct {
	MIME string
	Data []byte
}

type libraryArtistPhotoStore interface {
	ReadLibraryArtistPhoto(context.Context, string, string) (persistence.LibraryArtistPhotoRecord, error)
	SaveLibraryArtistPhoto(context.Context, persistence.LibraryArtistPhotoRecord) error
}

type libraryArtistPhotoRepository interface {
	ReadLibraryArtistPhotoBinding(context.Context, uuid.UUID) (persistence.LibraryArtistPhotoBinding, error)
}

type libraryArtistPhotoSettings interface {
	GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error)
}

type libraryArtistPhotoProviderFactory interface {
	MusicBrainzForConfig(context.Context, settings.MusicBrainzConfig) (*musicbrainz.Provider, error)
}

type libraryArtistPhotoResult struct {
	photo *LibraryArtistPhoto
	err   error
}

type libraryArtistPhotoCacheEntry struct {
	photo     *LibraryArtistPhoto
	err       error
	expiresAt time.Time
}

type libraryArtistPhotoFlight struct {
	done   chan struct{}
	result libraryArtistPhotoResult
}

type libraryArtistPhotoCache struct {
	mu      sync.Mutex
	entries map[string]libraryArtistPhotoCacheEntry
	flights map[string]*libraryArtistPhotoFlight
	limit   int
}

func newLibraryArtistPhotoCache(limit int) *libraryArtistPhotoCache {
	if limit < 1 {
		limit = libraryArtistPhotoCacheCapacity
	}
	return &libraryArtistPhotoCache{
		entries: make(map[string]libraryArtistPhotoCacheEntry),
		flights: make(map[string]*libraryArtistPhotoFlight),
		limit:   limit,
	}
}

func (cache *libraryArtistPhotoCache) get(key string, now time.Time) (libraryArtistPhotoResult, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.getLocked(key, now)
}

func (cache *libraryArtistPhotoCache) getLocked(key string, now time.Time) (libraryArtistPhotoResult, bool) {
	entry, ok := cache.entries[key]
	if !ok {
		return libraryArtistPhotoResult{}, false
	}
	if !now.Before(entry.expiresAt) {
		delete(cache.entries, key)
		return libraryArtistPhotoResult{}, false
	}
	return libraryArtistPhotoResult{photo: cloneLibraryArtistPhoto(entry.photo), err: entry.err}, true
}

func (cache *libraryArtistPhotoCache) put(key string, photo *LibraryArtistPhoto, err error, now time.Time, ttl time.Duration) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, exists := cache.entries[key]; !exists && len(cache.entries) >= cache.limit {
		cache.evictLocked(now)
	}
	cache.entries[key] = libraryArtistPhotoCacheEntry{
		photo: cloneLibraryArtistPhoto(photo), err: err, expiresAt: now.Add(ttl),
	}
}

// evictLocked removes expired entries first, then the entry closest to expiry.
// The bounded map is intentionally small and eviction runs only on insertion.
func (cache *libraryArtistPhotoCache) evictLocked(now time.Time) {
	for key, entry := range cache.entries {
		if !now.Before(entry.expiresAt) {
			delete(cache.entries, key)
		}
	}
	for len(cache.entries) >= cache.limit {
		var oldestKey string
		var oldest time.Time
		for key, entry := range cache.entries {
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey, oldest = key, entry.expiresAt
			}
		}
		if oldestKey == "" {
			return
		}
		delete(cache.entries, oldestKey)
	}
}

// LibraryArtistPhotos reads the validated MusicBrainz link of a local artist and
// resolves an optional photo. It never sends outbound requests for artists
// without one unambiguous, valid MusicBrainz id.
type LibraryArtistPhotos struct {
	repository    libraryArtistPhotoRepository
	configuration libraryArtistPhotoSettings
	providers     libraryArtistPhotoProviderFactory
	resolver      ArtistPhotoResolver

	now   func() time.Time
	cache *libraryArtistPhotoCache
	slots chan struct{}
	// timeout bounds the whole in-memory resolution, including single-flight
	// and semaphore waits. It is a field so tests can shorten it.
	timeout time.Duration
}

func NewLibraryArtistPhotos(
	repository libraryArtistPhotoRepository,
	configuration libraryArtistPhotoSettings,
	providers libraryArtistPhotoProviderFactory,
	resolver ArtistPhotoResolver,
) *LibraryArtistPhotos {
	return &LibraryArtistPhotos{
		repository: repository, configuration: configuration, providers: providers, resolver: resolver,
		now: time.Now, cache: newLibraryArtistPhotoCache(libraryArtistPhotoCacheCapacity),
		slots: make(chan struct{}, libraryArtistPhotoConcurrency), timeout: libraryArtistPhotoTimeout,
	}
}

func (service *LibraryArtistPhotos) Read(ctx context.Context, artistID uuid.UUID) (LibraryArtistPhotoResponse, error) {
	if err := ctx.Err(); err != nil {
		return LibraryArtistPhotoResponse{}, err
	}
	if service == nil || service.repository == nil || service.configuration == nil ||
		service.providers == nil || service.resolver == nil ||
		service.cache == nil || service.slots == nil || service.now == nil {
		return LibraryArtistPhotoResponse{}, errors.New("library artist photos are unavailable")
	}
	if artistID == uuid.Nil {
		return LibraryArtistPhotoResponse{}, ErrLibraryArtistNotFound
	}
	artist, err := service.repository.ReadLibraryArtistPhotoBinding(ctx, artistID)
	if err != nil {
		return LibraryArtistPhotoResponse{}, fmt.Errorf("read library artist photo: %w", err)
	}
	if !artist.ArtistExists {
		return LibraryArtistPhotoResponse{}, ErrLibraryArtistNotFound
	}
	mbid := validatedMusicBrainzArtistMBID(artist.ProviderKeys)
	if mbid == "" {
		return LibraryArtistPhotoResponse{}, nil
	}
	config, err := service.configuration.GetMusicBrainzConfig(ctx)
	if err != nil {
		return LibraryArtistPhotoResponse{}, fmt.Errorf("%w: read MusicBrainz configuration: %w", ErrLibraryArtistPhotoUnavailable, err)
	}
	if config.Identity == "" {
		return LibraryArtistPhotoResponse{}, fmt.Errorf("%w: MusicBrainz configuration is not saved", ErrLibraryArtistPhotoUnavailable)
	}
	if photo, found, err := service.readStoredPhoto(ctx, config.Identity, mbid); err != nil {
		return LibraryArtistPhotoResponse{}, err
	} else if found {
		return LibraryArtistPhotoResponse{Photo: photo}, nil
	}
	result := service.resolve(ctx, config, mbid)
	if result.err != nil {
		return LibraryArtistPhotoResponse{}, fmt.Errorf("%w: %w", ErrLibraryArtistPhotoUnavailable, result.err)
	}
	if result.photo == nil {
		return LibraryArtistPhotoResponse{}, nil
	}
	return LibraryArtistPhotoResponse{Photo: cloneLibraryArtistPhoto(result.photo)}, nil
}

// Image reads only the durable thumbnail for one local artist. It never resolves
// provider metadata or downloads an image.
func (service *LibraryArtistPhotos) Image(ctx context.Context, artistID uuid.UUID) (LibraryArtistPhotoImage, error) {
	if err := ctx.Err(); err != nil {
		return LibraryArtistPhotoImage{}, err
	}
	if service == nil || service.repository == nil || service.configuration == nil ||
		service.cache == nil || service.now == nil {
		return LibraryArtistPhotoImage{}, fmt.Errorf("%w: library artist photos are unavailable", ErrLibraryArtistPhotoUnavailable)
	}
	if artistID == uuid.Nil {
		return LibraryArtistPhotoImage{}, ErrLibraryArtistNotFound
	}
	artist, err := service.repository.ReadLibraryArtistPhotoBinding(ctx, artistID)
	if err != nil {
		return LibraryArtistPhotoImage{}, fmt.Errorf("read library artist photo image: %w", err)
	}
	if !artist.ArtistExists {
		return LibraryArtistPhotoImage{}, ErrLibraryArtistNotFound
	}
	mbid := validatedMusicBrainzArtistMBID(artist.ProviderKeys)
	if mbid == "" {
		return LibraryArtistPhotoImage{}, ErrLibraryArtistPhotoNotFound
	}
	config, err := service.configuration.GetMusicBrainzConfig(ctx)
	if err != nil {
		return LibraryArtistPhotoImage{}, fmt.Errorf("%w: read MusicBrainz configuration: %w", ErrLibraryArtistPhotoUnavailable, err)
	}
	if config.Identity == "" {
		return LibraryArtistPhotoImage{}, fmt.Errorf("%w: MusicBrainz configuration is not saved", ErrLibraryArtistPhotoUnavailable)
	}
	store, ok := service.repository.(libraryArtistPhotoStore)
	if !ok {
		return LibraryArtistPhotoImage{}, fmt.Errorf("%w: durable artist photos are unavailable", ErrLibraryArtistPhotoUnavailable)
	}
	record, err := store.ReadLibraryArtistPhoto(ctx, config.Identity, mbid)
	if errors.Is(err, persistence.ErrLibraryArtistPhotoNotFound) {
		return LibraryArtistPhotoImage{}, ErrLibraryArtistPhotoNotFound
	}
	if err != nil {
		return LibraryArtistPhotoImage{}, fmt.Errorf("%w: %w", ErrLibraryArtistPhotoUnavailable, err)
	}
	return LibraryArtistPhotoImage{MIME: record.MIME, Data: append([]byte(nil), record.Data...)}, nil
}

func (service *LibraryArtistPhotos) readStoredPhoto(ctx context.Context, configIdentity, mbid string) (*LibraryArtistPhoto, bool, error) {
	store, ok := service.repository.(libraryArtistPhotoStore)
	if !ok {
		return nil, false, nil
	}
	record, err := store.ReadLibraryArtistPhoto(ctx, configIdentity, mbid)
	if errors.Is(err, persistence.ErrLibraryArtistPhotoNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrLibraryArtistPhotoUnavailable, err)
	}
	return libraryArtistPhotoFromRecord(record), true, nil
}

func libraryArtistPhotoFromRecord(record persistence.LibraryArtistPhotoRecord) *LibraryArtistPhoto {
	result := &LibraryArtistPhoto{
		ImageURL: record.ImageURL, SourceURL: record.SourceURL,
		Author: record.Author, License: record.License,
	}
	if record.LicenseURL != nil {
		value := *record.LicenseURL
		result.LicenseURL = &value
	}
	return result
}

// validatedMusicBrainzArtistMBID returns a canonical MBID only when the local
// artist has one unambiguous, valid MusicBrainz binding. Invalid provider keys
// are ignored; two distinct valid keys make the identity ambiguous.
func validatedMusicBrainzArtistMBID(keys []string) string {
	mbid := ""
	for _, key := range keys {
		parsed, err := uuid.Parse(key)
		if err != nil || parsed == uuid.Nil || !strings.EqualFold(key, parsed.String()) {
			continue
		}
		canonical := parsed.String()
		if mbid != "" && mbid != canonical {
			return ""
		}
		mbid = canonical
	}
	return mbid
}

func (service *LibraryArtistPhotos) resolve(ctx context.Context, config settings.MusicBrainzConfig, mbid string) libraryArtistPhotoResult {
	// The deadline covers the whole resolution, including waiting behind the
	// single-flight entry and the bounded concurrency slot. A caller context
	// without its own deadline therefore cannot wait indefinitely.
	timeout := service.timeout
	if timeout <= 0 {
		timeout = libraryArtistPhotoTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	key := config.Identity + "\x00" + mbid
	if result, ok := service.cache.get(key, service.now()); ok {
		return result
	}
	service.cache.mu.Lock()
	if result, ok := service.cache.getLocked(key, service.now()); ok {
		service.cache.mu.Unlock()
		return result
	}
	if flight, ok := service.cache.flights[key]; ok {
		service.cache.mu.Unlock()
		select {
		case <-flight.done:
			result := flight.result
			result.photo = cloneLibraryArtistPhoto(result.photo)
			return result
		case <-ctx.Done():
			return libraryArtistPhotoResult{err: ctx.Err()}
		}
	}
	// Bound pending single-flight entries independently of the TTL cache so a
	// burst of distinct artists cannot grow memory without limit.
	if len(service.cache.flights) >= service.cache.limit {
		service.cache.mu.Unlock()
		return libraryArtistPhotoResult{err: errors.New("library artist photo requests are saturated")}
	}
	flight := &libraryArtistPhotoFlight{done: make(chan struct{})}
	service.cache.flights[key] = flight
	service.cache.mu.Unlock()

	result := service.load(ctx, config, mbid, key)
	service.cache.mu.Lock()
	delete(service.cache.flights, key)
	flight.result = result
	close(flight.done)
	service.cache.mu.Unlock()
	return result
}

func (service *LibraryArtistPhotos) load(ctx context.Context, config settings.MusicBrainzConfig, mbid, key string) libraryArtistPhotoResult {
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	case <-ctx.Done():
		return libraryArtistPhotoResult{err: ctx.Err()}
	}
	// Another MBID may have filled the cache while this request waited for a
	// concurrency slot.
	if result, ok := service.cache.get(key, service.now()); ok {
		return result
	}
	provider, err := service.providers.MusicBrainzForConfig(ctx, config)
	if err == nil && provider == nil {
		err = errors.New("MusicBrainz provider is unavailable")
	}
	var photo *LibraryArtistPhoto
	if err == nil {
		photo, err = service.resolver.Resolve(ctx, provider, mbid)
	}
	now := service.now()
	if err != nil {
		if cacheableArtistPhotoFailure(ctx) {
			service.cache.put(key, nil, err, now, libraryArtistPhotoFailurePause)
		}
		return libraryArtistPhotoResult{err: err}
	}
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		timeoutErr := fmt.Errorf("resolve artist photo: %w", context.DeadlineExceeded)
		service.cache.put(key, nil, timeoutErr, now, libraryArtistPhotoFailurePause)
		return libraryArtistPhotoResult{err: timeoutErr}
	}
	if err := ctx.Err(); err != nil {
		return libraryArtistPhotoResult{err: err}
	}
	if photo == nil || strings.TrimSpace(photo.ImageURL) == "" {
		service.cache.put(key, nil, nil, now, libraryArtistPhotoNoPhotoTTL)
		return libraryArtistPhotoResult{}
	}
	normalized := normalizedLibraryArtistPhoto(photo)
	if err := service.persist(ctx, config, mbid, normalized); err != nil {
		if cacheableArtistPhotoFailure(ctx) {
			service.cache.put(key, nil, err, now, libraryArtistPhotoFailurePause)
		}
		return libraryArtistPhotoResult{err: err}
	}
	service.cache.put(key, normalized, nil, now, libraryArtistPhotoSuccessTTL)
	return libraryArtistPhotoResult{photo: normalized}
}

// persist downloads the bounded provider thumbnail and records it with the
// verified attribution under the exact MusicBrainz/config identity. A request
// that cannot be persisted is not exposed as a photo, because the JSON contract
// now points clients at the durable same-origin image route.
func (service *LibraryArtistPhotos) persist(ctx context.Context, config settings.MusicBrainzConfig, mbid string, photo *LibraryArtistPhoto) error {
	store, ok := service.repository.(libraryArtistPhotoStore)
	if !ok {
		return nil
	}
	downloader, ok := service.resolver.(ArtistPhotoImageDownloader)
	if !ok {
		return nil
	}
	data, mime, err := downloader.DownloadImage(ctx, photo.ImageURL)
	if err != nil {
		return fmt.Errorf("download artist photo: %w", err)
	}
	if len(data) == 0 || len(data) > persistence.MaxLibraryArtistPhotoBytes {
		return errors.New("artist photo exceeds the size limit")
	}
	format, width, height, err := artwork.Inspect(data)
	if err != nil {
		return fmt.Errorf("inspect artist photo: %w", err)
	}
	if width > persistence.MaxLibraryArtistPhotoDimension || height > persistence.MaxLibraryArtistPhotoDimension {
		return errors.New("artist photo dimensions exceed the limit")
	}
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime != "image/"+format {
		return errors.New("artist photo MIME type does not match its bytes")
	}
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return errors.New("artist photo MIME type is unsupported")
	}
	if err := artwork.ValidateEncoding(data, format); err != nil {
		return fmt.Errorf("validate artist photo encoding: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	artistMBID, err := uuid.Parse(mbid)
	if err != nil || artistMBID == uuid.Nil {
		return errors.New("artist MBID is invalid")
	}
	return store.SaveLibraryArtistPhoto(ctx, persistence.LibraryArtistPhotoRecord{
		ConfigIdentity: config.Identity, ArtistMBID: artistMBID,
		ImageURL: photo.ImageURL, SourceURL: photo.SourceURL,
		Author: photo.Author, License: photo.License, LicenseURL: photo.LicenseURL,
		Data: data, MIME: mime, Width: width, Height: height,
		FetchedAt: service.now(),
	})
}

// cacheableArtistPhotoFailure stores a transient failure cooldown unless the
// caller itself canceled the request. A missing cause means the resolver failed
// while the context was still active; an exceeded deadline is our own bounded
// timeout and remains cacheable.
func cacheableArtistPhotoFailure(ctx context.Context) bool {
	cause := context.Cause(ctx)
	return cause == nil || errors.Is(cause, context.DeadlineExceeded)
}

func normalizedLibraryArtistPhoto(photo *LibraryArtistPhoto) *LibraryArtistPhoto {
	if photo == nil {
		return nil
	}
	result := *photo
	result.ImageURL = strings.TrimSpace(result.ImageURL)
	result.SourceURL = strings.TrimSpace(result.SourceURL)
	result.Author = strings.TrimSpace(result.Author)
	result.License = strings.TrimSpace(result.License)
	if result.LicenseURL != nil {
		value := strings.TrimSpace(*result.LicenseURL)
		result.LicenseURL = &value
	}
	return &result
}

func cloneLibraryArtistPhoto(photo *LibraryArtistPhoto) *LibraryArtistPhoto {
	if photo == nil {
		return nil
	}
	result := *photo
	if photo.LicenseURL != nil {
		value := *photo.LicenseURL
		result.LicenseURL = &value
	}
	return &result
}
