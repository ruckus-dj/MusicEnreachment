package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestProviderArtworkDoesNotCacheATruncatedJPEG(t *testing.T) {
	var original bytes.Buffer
	pixels := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := range 96 {
		for x := range 96 {
			pixels.SetRGBA(x, y, color.RGBA{R: uint8(x * y), G: uint8(x + y), B: uint8(2 * y), A: 255})
		}
	}
	if err := jpeg.Encode(&original, pixels, nil); err != nil {
		t.Fatal(err)
	}
	truncated := original.Bytes()[:original.Len()/2]
	if _, _, _, err := artwork.Inspect(truncated); err != nil {
		t.Fatalf("fixture must have a valid header: %v", err)
	}
	mbid := uuid.NewString()
	client := newProviderArtworkClientFixture()
	client.setFront(mbid, providerArtworkDownload{data: truncated, mime: "image/jpeg"})
	store := newProviderArtworkStoreFixture()
	if err := newProviderArtwork(store, client).EnsureFront(context.Background(), mbid); err == nil {
		t.Fatal("header-valid truncated JPEG was cached")
	}
	front, err := store.ReadCoverArtArchiveFront(context.Background(), mbid)
	if err != nil || front.Checked {
		t.Fatalf("truncated JPEG became a terminal cache result: %+v, %v", front, err)
	}
}

func TestProviderArtworkCachesOriginalAbovePublicationDimensions(t *testing.T) {
	var original bytes.Buffer
	if err := png.Encode(&original, image.NewRGBA(image.Rect(0, 0, artwork.MaxOriginalDimension+1, 1))); err != nil {
		t.Fatal(err)
	}
	mbid := uuid.NewString()
	client := newProviderArtworkClientFixture()
	client.setFront(mbid, providerArtworkDownload{data: original.Bytes(), mime: "image/png"})
	store := newProviderArtworkStoreFixture()
	if err := newProviderArtwork(store, client).EnsureFront(context.Background(), mbid); err != nil {
		t.Fatalf("cache original outside publication dimensions: %v", err)
	}
	front, err := store.ReadCoverArtArchiveFront(context.Background(), mbid)
	if err != nil || !front.Found || !bytes.Equal(front.Data, original.Bytes()) {
		t.Fatal("archive original was not kept unchanged")
	}
}

func TestProviderArtworkRechecksAnExpiredDefinitiveMiss(t *testing.T) {
	mbid := uuid.NewString()
	expired := time.Now().Add(-providerArtworkAbsentTTL - time.Hour)
	store := newProviderArtworkStoreFixture()
	store.releases[mbid] = &persistence.CoverArtArchiveRelease{
		ReleaseMBID: mbid, FrontStatus: persistence.CoverArtArchiveFrontAbsent, FrontCheckedAt: &expired,
	}
	client := newProviderArtworkClientFixture()
	client.setFront(mbid, providerArtworkDownload{err: errors.New("archive temporarily unavailable")})
	if err := newProviderArtwork(store, client).EnsureFront(context.Background(), mbid); err == nil {
		t.Fatal("expired 404 was treated as definitive during an outage")
	}
}

type artworkPublicationStoreFixture struct {
	*providerArtworkStoreFixture
	mbid  string
	cover persistence.ReleaseCover
}

func (store *artworkPublicationStoreFixture) ReadReleaseMusicBrainzReleaseID(context.Context, uuid.UUID) (string, bool, error) {
	return store.mbid, true, nil
}

func (store *artworkPublicationStoreFixture) ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error) {
	return store.cover, nil
}

func (store *artworkPublicationStoreFixture) ReadReleaseCoverCandidates(context.Context, uuid.UUID) (*persistence.ReleaseCoverCandidates, error) {
	return &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &store.mbid}, nil
}

func (store *artworkPublicationStoreFixture) RecordAutomaticReleaseCoverSelection(
	_ context.Context, _ uuid.UUID, _ int64, data []byte, mime string, warning *string,
) (persistence.ReleaseCover, error) {
	store.cover.Revision++
	store.cover.Warning = warning
	if len(data) != 0 {
		generation := uuid.New()
		store.cover.SelectedGeneration = &generation
		store.cover.SelectedBytes = bytes.Clone(data)
		store.cover.SelectedMIME = &mime
	}
	return store.cover, nil
}

func TestProviderArtworkPreparesFirstPublicationOnlyAfterDefinitiveArchiveResult(t *testing.T) {
	tests := []struct {
		name      string
		noFront   bool
		frontFail bool
		imageFail bool
	}{
		{name: "front and originals"},
		{name: "confirmed missing front and source", noFront: true},
		{name: "temporary front failure", frontFail: true},
		{name: "temporary gallery failure", imageFail: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mbid, releaseID := uuid.NewString(), uuid.New()
			png := providerArtworkPNG(t, color.Black)
			store := &artworkPublicationStoreFixture{
				providerArtworkStoreFixture: newProviderArtworkStoreFixture(),
				mbid:                        mbid,
				cover:                       persistence.ReleaseCover{ReleaseID: releaseID, Revision: 1, Mode: "automatic"},
			}
			store.eligible[mbid] = true
			client := newProviderArtworkClientFixture()
			front := providerArtworkDownload{data: png, mime: "image/png", notFound: tc.noFront}
			if tc.frontFail {
				front.err = errors.New("archive unavailable")
			}
			client.setFront(mbid, front)
			client.list = []coverart.Image{{ID: "7", Back: true}}
			back := providerArtworkDownload{data: png, mime: "image/png"}
			if tc.imageFail {
				back.err = errors.New("gallery original unavailable")
			}
			client.setImage("7", back)
			service := newProviderArtwork(store, client)
			service.acquisition = NewReleaseCoverAcquisition(store, NewReleaseCoverResolver(store, service))

			err := service.PrepareForPublication(ctx, releaseID)
			if tc.frontFail || tc.imageFail {
				if !errors.Is(err, ErrProviderArtworkPending) || store.cover.SelectedGeneration != nil || store.cover.Revision != 1 {
					t.Fatalf("temporary failure advanced cover authority: %+v, %v", store.cover, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (store.cover.SelectedGeneration == nil) != tc.noFront {
				t.Fatalf("unexpected selected cover: %+v", store.cover)
			}
			if err := service.PrepareForPublication(ctx, releaseID); err != nil {
				t.Fatal(err)
			}
			if calls := client.imageCallsSnapshot(); len(calls) != 1 || calls[0] != "7" {
				t.Fatalf("gallery was downloaded again: %v", calls)
			}
		})
	}
}

type providerArtworkImageErrorRecord struct {
	mbid    string
	imageID string
	message string
}

// providerArtworkStoreFixture embeds the interface with a nil value so that any
// store method a test does not override fails loudly instead of silently
// succeeding. Only the methods exercised by the service are implemented.
type providerArtworkStoreFixture struct {
	providerArtworkStore

	mu sync.Mutex

	releases map[string]*persistence.CoverArtArchiveRelease
	fronts   map[string]*persistence.CoverArtArchiveFront
	images   map[string][]persistence.CoverArtArchiveImage
	eligible map[string]bool

	eligibleErr   error
	eligibleHook  func(call int) (bool, error)
	eligibleCalls int

	readReleaseErr    error
	saveCandidatesErr error
	saveImageErr      error
	completeErr       error

	markAbsentCalls   []string
	frontErrorCalls   []string
	galleryErrorCalls []string
	savedFrontCalls   []string
	savedImageCalls   []string
	candidatesCalls   []string
	completeCalls     []string
	imageErrorCalls   []providerArtworkImageErrorRecord
}

func newProviderArtworkStoreFixture() *providerArtworkStoreFixture {
	return &providerArtworkStoreFixture{
		releases: map[string]*persistence.CoverArtArchiveRelease{},
		fronts:   map[string]*persistence.CoverArtArchiveFront{},
		images:   map[string][]persistence.CoverArtArchiveImage{},
		eligible: map[string]bool{},
	}
}

func (store *providerArtworkStoreFixture) releaseLocked(mbid string) *persistence.CoverArtArchiveRelease {
	release := store.releases[mbid]
	if release == nil {
		release = &persistence.CoverArtArchiveRelease{ReleaseMBID: mbid}
		store.releases[mbid] = release
	}
	if release.FrontStatus == "" {
		release.FrontStatus = persistence.CoverArtArchiveUnchecked
	}
	if release.GalleryStatus == "" {
		release.GalleryStatus = persistence.CoverArtArchiveUnchecked
	}
	return release
}

func (store *providerArtworkStoreFixture) frontLocked(mbid string) *persistence.CoverArtArchiveFront {
	front := store.fronts[mbid]
	if front == nil {
		front = &persistence.CoverArtArchiveFront{Status: persistence.CoverArtArchiveUnchecked}
		store.fronts[mbid] = front
	}
	return front
}

func (store *providerArtworkStoreFixture) ReadCoverArtArchiveRelease(_ context.Context, mbid string) (persistence.CoverArtArchiveRelease, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.readReleaseErr != nil {
		return persistence.CoverArtArchiveRelease{}, store.readReleaseErr
	}
	release := persistence.CoverArtArchiveRelease{
		ReleaseMBID:   mbid,
		FrontStatus:   persistence.CoverArtArchiveUnchecked,
		GalleryStatus: persistence.CoverArtArchiveUnchecked,
		Images:        []persistence.CoverArtArchiveImage{},
	}
	if stored := store.releases[mbid]; stored != nil {
		release = *stored
		release.ReleaseMBID = mbid
		if release.FrontStatus == "" {
			release.FrontStatus = persistence.CoverArtArchiveUnchecked
		}
		if release.GalleryStatus == "" {
			release.GalleryStatus = persistence.CoverArtArchiveUnchecked
		}
	}
	for _, image := range store.images[mbid] {
		if image.Listed {
			release.Images = append(release.Images, copyProviderArtworkImage(image))
		}
	}
	return release, nil
}

func (store *providerArtworkStoreFixture) ReadCoverArtArchiveFront(_ context.Context, mbid string) (persistence.CoverArtArchiveFront, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	front := persistence.CoverArtArchiveFront{Status: persistence.CoverArtArchiveUnchecked}
	if stored := store.fronts[mbid]; stored != nil {
		front = *stored
		front.Data = append([]byte(nil), stored.Data...)
	}
	front.Checked = front.Status != persistence.CoverArtArchiveUnchecked
	front.Found = front.Status == persistence.CoverArtArchiveFrontPresent
	return front, nil
}

func (store *providerArtworkStoreFixture) ReadCoverArtArchiveImage(_ context.Context, mbid, imageID string) (persistence.CoverArtArchiveImage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, image := range store.images[mbid] {
		if image.ID == imageID {
			return copyProviderArtworkImage(image), nil
		}
	}
	return persistence.CoverArtArchiveImage{}, persistence.ErrCoverArtArchiveImageNotFound
}

func (store *providerArtworkStoreFixture) SaveCoverArtArchiveFront(_ context.Context, mbid, _ string, data []byte, mime string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.savedFrontCalls = append(store.savedFrontCalls, mbid)
	digest := sha256.Sum256(data)
	size := int64(len(data))
	now := time.Now()
	mimeCopy := mime
	store.fronts[mbid] = &persistence.CoverArtArchiveFront{
		Status:    persistence.CoverArtArchiveFrontPresent,
		Checked:   true,
		Found:     true,
		Data:      append([]byte(nil), data...),
		MIME:      &mimeCopy,
		SHA256:    append([]byte(nil), digest[:]...),
		SizeBytes: &size,
		CheckedAt: &now,
	}
	release := store.releaseLocked(mbid)
	release.FrontStatus = persistence.CoverArtArchiveFrontPresent
	release.FrontCheckedAt = &now
	release.FrontError = nil
	return nil
}

func (store *providerArtworkStoreFixture) MarkCoverArtArchiveFrontAbsent(_ context.Context, mbid string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.markAbsentCalls = append(store.markAbsentCalls, mbid)
	now := time.Now()
	store.fronts[mbid] = &persistence.CoverArtArchiveFront{
		Status:    persistence.CoverArtArchiveFrontAbsent,
		Checked:   true,
		CheckedAt: &now,
	}
	release := store.releaseLocked(mbid)
	release.FrontStatus = persistence.CoverArtArchiveFrontAbsent
	release.FrontCheckedAt = &now
	release.FrontError = nil
	return nil
}

func (store *providerArtworkStoreFixture) RecordCoverArtArchiveFrontError(_ context.Context, mbid, message string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.frontErrorCalls = append(store.frontErrorCalls, mbid)
	messageCopy := message
	store.frontLocked(mbid).LastError = &messageCopy
	store.releaseLocked(mbid).FrontError = &messageCopy
	return nil
}

func (store *providerArtworkStoreFixture) SaveCoverArtArchiveGalleryCandidates(_ context.Context, mbid string, candidates []persistence.CoverArtArchiveImage) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saveCandidatesErr != nil {
		return store.saveCandidatesErr
	}
	store.candidatesCalls = append(store.candidatesCalls, mbid)

	previous := make(map[string]persistence.CoverArtArchiveImage, len(store.images[mbid]))
	for _, image := range store.images[mbid] {
		previous[image.ID] = image
	}
	next := make([]persistence.CoverArtArchiveImage, 0, len(store.images[mbid])+len(candidates))
	seen := make(map[string]bool, len(candidates))
	for position, candidate := range candidates {
		stored := candidate
		stored.Position = position
		stored.Listed = true
		if cached, ok := previous[stored.ID]; ok && cached.Cached {
			stored.Cached = true
			stored.Data = cached.Data
			stored.MIME = cached.MIME
			stored.SHA256 = cached.SHA256
			stored.SizeBytes = cached.SizeBytes
			stored.DownloadedAt = cached.DownloadedAt
			stored.DownloadError = cached.DownloadError
		}
		next = append(next, stored)
		seen[stored.ID] = true
	}
	for _, cached := range store.images[mbid] {
		if seen[cached.ID] {
			continue
		}
		cached.Listed = false
		next = append(next, cached)
	}
	store.images[mbid] = next

	now := time.Now()
	release := store.releaseLocked(mbid)
	release.GalleryStatus = persistence.CoverArtArchiveUnchecked
	release.GalleryListedAt = &now
	release.GalleryCheckedAt = nil
	release.GalleryError = nil
	return nil
}

func (store *providerArtworkStoreFixture) SaveCoverArtArchiveImageData(_ context.Context, mbid, imageID string, data []byte, mime string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saveImageErr != nil {
		return store.saveImageErr
	}
	store.savedImageCalls = append(store.savedImageCalls, imageID)
	digest := sha256.Sum256(data)
	size := int64(len(data))
	now := time.Now()
	mimeCopy := mime
	for index := range store.images[mbid] {
		image := &store.images[mbid][index]
		if image.ID != imageID {
			continue
		}
		image.Cached = true
		image.Data = append([]byte(nil), data...)
		image.MIME = &mimeCopy
		image.SHA256 = append([]byte(nil), digest[:]...)
		image.SizeBytes = &size
		image.DownloadedAt = &now
		image.DownloadError = nil
		return nil
	}
	store.images[mbid] = append(store.images[mbid], persistence.CoverArtArchiveImage{
		ID:           imageID,
		Listed:       true,
		Cached:       true,
		Data:         append([]byte(nil), data...),
		MIME:         &mimeCopy,
		SHA256:       append([]byte(nil), digest[:]...),
		SizeBytes:    &size,
		DownloadedAt: &now,
	})
	return nil
}

func (store *providerArtworkStoreFixture) CompleteCoverArtArchiveGallery(_ context.Context, mbid string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.completeErr != nil {
		return store.completeErr
	}
	store.completeCalls = append(store.completeCalls, mbid)
	now := time.Now()
	release := store.releaseLocked(mbid)
	release.GalleryStatus = persistence.CoverArtArchiveGalleryComplete
	release.GalleryCheckedAt = &now
	release.GalleryError = nil
	return nil
}

func (store *providerArtworkStoreFixture) RecordCoverArtArchiveGalleryError(_ context.Context, mbid, _ string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.galleryErrorCalls = append(store.galleryErrorCalls, mbid)
	return nil
}

func (store *providerArtworkStoreFixture) RecordCoverArtArchiveImageError(_ context.Context, mbid, imageID, message string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.imageErrorCalls = append(store.imageErrorCalls, providerArtworkImageErrorRecord{mbid: mbid, imageID: imageID, message: message})
	messageCopy := message
	for index := range store.images[mbid] {
		image := &store.images[mbid][index]
		if image.ID == imageID {
			image.DownloadError = &messageCopy
			break
		}
	}
	return nil
}

func (store *providerArtworkStoreFixture) CoverArtArchiveGalleryEligible(_ context.Context, mbid string) (bool, error) {
	store.mu.Lock()
	store.eligibleCalls++
	call := store.eligibleCalls
	if store.eligibleErr != nil {
		err := store.eligibleErr
		store.mu.Unlock()
		return false, err
	}
	hook := store.eligibleHook
	eligible := store.eligible[mbid]
	store.mu.Unlock()
	if hook != nil {
		return hook(call)
	}
	return eligible, nil
}

func (store *providerArtworkStoreFixture) seedCachedImage(mbid, imageID string, data []byte, mime string, listed bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	digest := sha256.Sum256(data)
	size := int64(len(data))
	now := time.Now()
	mimeCopy := mime
	store.images[mbid] = append(store.images[mbid], persistence.CoverArtArchiveImage{
		ID:           imageID,
		Listed:       listed,
		Cached:       true,
		Data:         append([]byte(nil), data...),
		MIME:         &mimeCopy,
		SHA256:       append([]byte(nil), digest[:]...),
		SizeBytes:    &size,
		DownloadedAt: &now,
	})
}

func (store *providerArtworkStoreFixture) cachedImage(mbid, imageID string) (persistence.CoverArtArchiveImage, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, image := range store.images[mbid] {
		if image.ID == imageID && image.Cached {
			return copyProviderArtworkImage(image), true
		}
	}
	return persistence.CoverArtArchiveImage{}, false
}

func (store *providerArtworkStoreFixture) markAbsent() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.markAbsentCalls...)
}

func (store *providerArtworkStoreFixture) frontErrors() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.frontErrorCalls...)
}

func (store *providerArtworkStoreFixture) galleryErrors() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.galleryErrorCalls...)
}

func (store *providerArtworkStoreFixture) savedFront() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.savedFrontCalls...)
}

func (store *providerArtworkStoreFixture) savedImages() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.savedImageCalls...)
}

func (store *providerArtworkStoreFixture) candidates() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.candidatesCalls...)
}

func (store *providerArtworkStoreFixture) completed() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.completeCalls...)
}

func (store *providerArtworkStoreFixture) imageErrors() []providerArtworkImageErrorRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]providerArtworkImageErrorRecord(nil), store.imageErrorCalls...)
}

func copyProviderArtworkImage(image persistence.CoverArtArchiveImage) persistence.CoverArtArchiveImage {
	image.Data = append([]byte(nil), image.Data...)
	image.SHA256 = append([]byte(nil), image.SHA256...)
	image.Types = append([]string(nil), image.Types...)
	return image
}

type providerArtworkDownload struct {
	data     []byte
	mime     string
	err      error
	notFound bool
}

func (download providerArtworkDownload) write(destination io.Writer) (coverart.Result, error) {
	if download.err != nil {
		return coverart.Result{}, download.err
	}
	if download.notFound || len(download.data) == 0 {
		return coverart.Result{}, nil
	}
	if _, err := destination.Write(download.data); err != nil {
		return coverart.Result{}, err
	}
	digest := sha256.Sum256(download.data)
	return coverart.Result{
		Found:  true,
		MIME:   download.mime,
		Size:   int64(len(download.data)),
		SHA256: hex.EncodeToString(digest[:]),
	}, nil
}

// providerArtworkClientFixture records client calls for assertions.
type providerArtworkClientFixture struct {
	mu sync.Mutex

	list      []coverart.Image
	listErr   error
	listCalls []string

	fronts     map[string]providerArtworkDownload
	images     map[string]providerArtworkDownload
	frontCalls []string
	imageCalls []string

	frontHook func(io.Writer) (coverart.Result, error)
}

func newProviderArtworkClientFixture() *providerArtworkClientFixture {
	return &providerArtworkClientFixture{
		fronts: map[string]providerArtworkDownload{},
		images: map[string]providerArtworkDownload{},
	}
}

func (client *providerArtworkClientFixture) setFront(mbid string, download providerArtworkDownload) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.fronts[mbid] = download
}

func (client *providerArtworkClientFixture) setImage(imageID string, download providerArtworkDownload) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.images[imageID] = download
}

func (client *providerArtworkClientFixture) ListImages(_ context.Context, mbid string) ([]coverart.Image, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.listCalls = append(client.listCalls, mbid)
	if client.listErr != nil {
		return nil, client.listErr
	}
	return append([]coverart.Image(nil), client.list...), nil
}

func (client *providerArtworkClientFixture) DownloadFront(_ context.Context, mbid string, destination io.Writer) (coverart.Result, error) {
	client.mu.Lock()
	client.frontCalls = append(client.frontCalls, mbid)
	download := client.fronts[mbid]
	hook := client.frontHook
	client.mu.Unlock()
	if hook != nil {
		return hook(destination)
	}
	return download.write(destination)
}

func (client *providerArtworkClientFixture) DownloadImage(_ context.Context, _ string, imageID string, destination io.Writer) (coverart.Result, error) {
	client.mu.Lock()
	client.imageCalls = append(client.imageCalls, imageID)
	download := client.images[imageID]
	client.mu.Unlock()
	return download.write(destination)
}

func (client *providerArtworkClientFixture) frontCallCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.frontCalls)
}

func (client *providerArtworkClientFixture) listCallCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.listCalls)
}

func (client *providerArtworkClientFixture) imageCallCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.imageCalls)
}

func (client *providerArtworkClientFixture) imageCallsSnapshot() []string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return append([]string(nil), client.imageCalls...)
}

func providerArtworkPNG(t *testing.T, fill color.Color) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			source.Set(x, y, fill)
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return output.Bytes()
}

func providerArtworkStringCount(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func TestProviderArtworkEnsureFrontPersistsAuthoritativeAbsence(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	client := newProviderArtworkClientFixture()
	client.setFront(mbid, providerArtworkDownload{notFound: true})
	service := newProviderArtwork(store, client)

	if err := service.EnsureFront(ctx, mbid); err != nil {
		t.Fatalf("record definitive front absence: %v", err)
	}
	if calls := client.frontCallCount(); calls != 1 {
		t.Fatalf("front downloads = %d, want 1", calls)
	}
	if absences := store.markAbsent(); len(absences) != 1 || absences[0] != mbid {
		t.Fatalf("recorded absences = %v, want [%s]", absences, mbid)
	}
	if recorded := store.frontErrors(); len(recorded) != 0 {
		t.Fatalf("definitive absence recorded as transient error: %v", recorded)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.FrontStatus != persistence.CoverArtArchiveFrontAbsent {
		t.Fatalf("front status = %q, want %q", release.FrontStatus, persistence.CoverArtArchiveFrontAbsent)
	}
	front, err := store.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil {
		t.Fatalf("read front: %v", err)
	}
	if !front.Checked || front.Found {
		t.Fatalf("front checked=%v found=%v, want checked and not found", front.Checked, front.Found)
	}

	if err := service.EnsureFront(ctx, mbid); err != nil {
		t.Fatalf("re-ensure definitive absence: %v", err)
	}
	if calls := client.frontCallCount(); calls != 1 {
		t.Fatalf("front downloads after authoritative absence = %d, want 1", calls)
	}

	var output bytes.Buffer
	result, err := service.DownloadFront(ctx, mbid, &output)
	if err != nil {
		t.Fatalf("read cached absent front: %v", err)
	}
	if result.Found || output.Len() != 0 {
		t.Fatalf("cached absent front returned found=%v bytes=%d, want no content", result.Found, output.Len())
	}
}

func TestProviderArtworkEnsureFrontTransientFailureNeverRecordsAbsence(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	client := newProviderArtworkClientFixture()
	client.setFront(mbid, providerArtworkDownload{err: errors.New("cover art archive unavailable")})
	service := newProviderArtwork(store, client)

	if err := service.EnsureFront(ctx, mbid); err == nil {
		t.Fatal("transient front failure returned no error")
	}
	if absences := store.markAbsent(); len(absences) != 0 {
		t.Fatalf("transient failure recorded a definitive absence: %v", absences)
	}
	if recorded := store.frontErrors(); len(recorded) != 1 {
		t.Fatalf("recorded front errors = %v, want one", recorded)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.FrontStatus != persistence.CoverArtArchiveUnchecked {
		t.Fatalf("front status after transient failure = %q, want %q", release.FrontStatus, persistence.CoverArtArchiveUnchecked)
	}
	if saved := store.savedFront(); len(saved) != 0 {
		t.Fatalf("transient failure saved a front image: %v", saved)
	}

	png := providerArtworkPNG(t, color.White)
	client.setFront(mbid, providerArtworkDownload{data: png, mime: "image/png"})
	if err := service.EnsureFront(ctx, mbid); err != nil {
		t.Fatalf("retry after transient failure: %v", err)
	}
	if calls := client.frontCallCount(); calls != 2 {
		t.Fatalf("front downloads after retry = %d, want 2", calls)
	}
	if saved := store.savedFront(); len(saved) != 1 {
		t.Fatalf("saved fronts after retry = %v, want one", saved)
	}
	front, err := store.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil {
		t.Fatalf("read front: %v", err)
	}
	if !front.Checked || !front.Found || !bytes.Equal(front.Data, png) {
		t.Fatalf("cached front checked=%v found=%v dataMismatch=%v", front.Checked, front.Found, !bytes.Equal(front.Data, png))
	}
}

func TestProviderArtworkEnsureFrontRejectsInvalidImageDataWithoutAbsence(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	client := newProviderArtworkClientFixture()
	client.setFront(mbid, providerArtworkDownload{data: []byte("not an image"), mime: "image/png"})
	service := newProviderArtwork(store, client)

	if err := service.EnsureFront(ctx, mbid); err == nil {
		t.Fatal("invalid provider image data returned no error")
	}
	if absences := store.markAbsent(); len(absences) != 0 {
		t.Fatalf("invalid image data recorded a definitive absence: %v", absences)
	}
	if recorded := store.frontErrors(); len(recorded) != 1 {
		t.Fatalf("recorded front errors = %v, want one", recorded)
	}
	if saved := store.savedFront(); len(saved) != 0 {
		t.Fatalf("invalid image data was saved: %v", saved)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.FrontStatus != persistence.CoverArtArchiveUnchecked {
		t.Fatalf("front status = %q, want unchecked", release.FrontStatus)
	}
}

func TestProviderArtworkEnsureGallerySkipsUnlinkedEdition(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	store.eligible[mbid] = false
	client := newProviderArtworkClientFixture()
	service := newProviderArtwork(store, client)

	if err := service.EnsureGallery(ctx, mbid); err != nil {
		t.Fatalf("skip unlinked edition: %v", err)
	}
	if calls := client.listCallCount(); calls != 0 {
		t.Fatalf("listing calls for unlinked edition = %d, want 0", calls)
	}
	if calls := len(client.imageCallsSnapshot()); calls != 0 {
		t.Fatalf("image downloads for unlinked edition = %d, want 0", calls)
	}
	if candidates := store.candidates(); len(candidates) != 0 {
		t.Fatalf("candidates recorded for unlinked edition: %v", candidates)
	}
	if completed := store.completed(); len(completed) != 0 {
		t.Fatalf("unlinked edition gallery completed: %v", completed)
	}
	if errors := store.galleryErrors(); len(errors) != 0 {
		t.Fatalf("unlinked edition recorded a gallery error: %v", errors)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.GalleryStatus != persistence.CoverArtArchiveUnchecked {
		t.Fatalf("gallery status = %q, want unchecked", release.GalleryStatus)
	}
}

func TestProviderArtworkEnsureGalleryStopsWhenEditionBecomesUnlinked(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	store.eligible[mbid] = true
	// The binding lasts through the listing and the first image, then
	// disappears before the second image is requested.
	store.eligibleHook = func(call int) (bool, error) {
		return call <= 4, nil
	}
	client := newProviderArtworkClientFixture()
	client.list = []coverart.Image{{ID: "1"}, {ID: "2"}}
	png := providerArtworkPNG(t, color.White)
	client.setImage("1", providerArtworkDownload{data: png, mime: "image/png"})
	client.setImage("2", providerArtworkDownload{data: png, mime: "image/png"})
	service := newProviderArtwork(store, client)

	if err := service.EnsureGallery(ctx, mbid); err != nil {
		t.Fatalf("gallery with revoked binding: %v", err)
	}
	if calls := client.imageCallsSnapshot(); len(calls) != 1 || calls[0] != "1" {
		t.Fatalf("image downloads = %v, want only [1]", calls)
	}
	if saved := store.savedImages(); len(saved) != 1 || saved[0] != "1" {
		t.Fatalf("saved images = %v, want only [1]", saved)
	}
	if completed := store.completed(); len(completed) != 0 {
		t.Fatalf("gallery completed after binding was revoked: %v", completed)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.GalleryStatus != persistence.CoverArtArchiveUnchecked {
		t.Fatalf("gallery status = %q, want unchecked", release.GalleryStatus)
	}
}

func TestProviderArtworkEnsureGalleryRetainsEarlierOriginalsAndResumes(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	store.eligible[mbid] = true
	first := providerArtworkPNG(t, color.RGBA{R: 255, A: 255})
	store.seedCachedImage(mbid, "1", first, "image/png", true)

	client := newProviderArtworkClientFixture()
	client.list = []coverart.Image{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	second := providerArtworkPNG(t, color.RGBA{G: 255, A: 255})
	third := providerArtworkPNG(t, color.RGBA{B: 255, A: 255})
	client.setImage("2", providerArtworkDownload{data: second, mime: "image/png"})
	client.setImage("3", providerArtworkDownload{err: errors.New("connection reset")})
	service := newProviderArtwork(store, client)

	if err := service.EnsureGallery(ctx, mbid); err == nil {
		t.Fatal("failed gallery original returned no error")
	}
	calls := client.imageCallsSnapshot()
	if len(calls) != 2 || calls[0] != "2" || calls[1] != "3" {
		t.Fatalf("first attempt image downloads = %v, want [2 3]", calls)
	}
	if saved := store.savedImages(); len(saved) != 1 || saved[0] != "2" {
		t.Fatalf("first attempt saved images = %v, want [2]", saved)
	}
	if completed := store.completed(); len(completed) != 0 {
		t.Fatalf("failed gallery marked complete: %v", completed)
	}
	if errors := store.imageErrors(); len(errors) != 1 || errors[0].imageID != "3" {
		t.Fatalf("recorded image errors = %v, want one for image 3", errors)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.GalleryStatus != persistence.CoverArtArchiveUnchecked {
		t.Fatalf("gallery status after failure = %q, want unchecked", release.GalleryStatus)
	}
	retained, ok := store.cachedImage(mbid, "2")
	if !ok || !bytes.Equal(retained.Data, second) {
		t.Fatal("successfully downloaded original 2 was not retained")
	}
	if retainedFirst, ok := store.cachedImage(mbid, "1"); !ok || !bytes.Equal(retainedFirst.Data, first) {
		t.Fatal("previously cached original 1 was not retained")
	}
	if _, ok := store.cachedImage(mbid, "3"); ok {
		t.Fatal("failed original 3 was cached")
	}

	client.setImage("3", providerArtworkDownload{data: third, mime: "image/png"})
	if err := service.EnsureGallery(ctx, mbid); err != nil {
		t.Fatalf("resume gallery: %v", err)
	}
	calls = client.imageCallsSnapshot()
	if providerArtworkStringCount(calls, "1") != 0 {
		t.Fatalf("resume re-downloaded retained original 1: %v", calls)
	}
	if providerArtworkStringCount(calls, "2") != 1 {
		t.Fatalf("resume re-downloaded retained original 2: %v", calls)
	}
	if providerArtworkStringCount(calls, "3") != 2 {
		t.Fatalf("resume downloads for failed original 3 = %d, want 2", providerArtworkStringCount(calls, "3"))
	}
	if completed := store.completed(); len(completed) != 1 {
		t.Fatalf("gallery completions after resume = %v, want one", completed)
	}
	release, err = store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release after resume: %v", err)
	}
	if release.GalleryStatus != persistence.CoverArtArchiveGalleryComplete {
		t.Fatalf("gallery status after resume = %q, want complete", release.GalleryStatus)
	}
	for _, imageID := range []string{"1", "2", "3"} {
		if _, ok := store.cachedImage(mbid, imageID); !ok {
			t.Fatalf("original %s missing after resume", imageID)
		}
	}
}

func TestProviderArtworkEnsureGalleryRecordsListingFailureWithoutCompletion(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	store.eligible[mbid] = true
	client := newProviderArtworkClientFixture()
	client.listErr = errors.New("cover art archive listing unavailable")
	service := newProviderArtwork(store, client)

	if err := service.EnsureGallery(ctx, mbid); err == nil {
		t.Fatal("gallery listing failure returned no error")
	}
	if recorded := store.galleryErrors(); len(recorded) != 1 {
		t.Fatalf("recorded gallery errors = %v, want one", recorded)
	}
	if candidates := store.candidates(); len(candidates) != 0 {
		t.Fatalf("candidates saved after listing failure: %v", candidates)
	}
	if completed := store.completed(); len(completed) != 0 {
		t.Fatalf("gallery completed after listing failure: %v", completed)
	}

	client.listErr = nil
	client.list = nil
	if err := service.EnsureGallery(ctx, mbid); err != nil {
		t.Fatalf("retry empty gallery listing: %v", err)
	}
	if candidates := store.candidates(); len(candidates) != 1 {
		t.Fatalf("candidate recordings after retry = %v, want one", candidates)
	}
	if completed := store.completed(); len(completed) != 1 {
		t.Fatalf("gallery completions after empty listing = %v, want one", completed)
	}
	release, err := store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatalf("read release: %v", err)
	}
	if release.GalleryStatus != persistence.CoverArtArchiveGalleryComplete {
		t.Fatalf("gallery status after definitive empty listing = %q, want complete", release.GalleryStatus)
	}
}

func TestProviderArtworkListImagesServesLocalCacheOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("cached gallery", func(t *testing.T) {
		mbid := uuid.NewString()
		store := newProviderArtworkStoreFixture()
		store.seedCachedImage(mbid, "7", providerArtworkPNG(t, color.White), "image/png", true)
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		images, err := service.ListImages(ctx, mbid)
		if err != nil {
			t.Fatalf("list cached gallery: %v", err)
		}
		if len(images) != 1 || images[0].ID != "7" {
			t.Fatalf("cached images = %v, want only ID 7", images)
		}
		wantThumbnail := "/api/library/provider-artwork/releases/" + mbid + "/images/7"
		if images[0].ThumbnailURL != wantThumbnail {
			t.Fatalf("thumbnail URL = %q, want %q", images[0].ThumbnailURL, wantThumbnail)
		}
		if calls := client.listCallCount(); calls != 0 {
			t.Fatalf("cached ListImages queried provider %d times", calls)
		}
		if calls := len(client.imageCallsSnapshot()); calls != 0 {
			t.Fatalf("cached ListImages downloaded %d images", calls)
		}
	})

	t.Run("definitive empty gallery", func(t *testing.T) {
		mbid := uuid.NewString()
		store := newProviderArtworkStoreFixture()
		store.releases[mbid] = &persistence.CoverArtArchiveRelease{
			ReleaseMBID:   mbid,
			FrontStatus:   persistence.CoverArtArchiveUnchecked,
			GalleryStatus: persistence.CoverArtArchiveGalleryComplete,
		}
		store.eligible[mbid] = true
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		images, err := service.ListImages(ctx, mbid)
		if err != nil {
			t.Fatalf("list definitive empty gallery: %v", err)
		}
		if len(images) != 0 {
			t.Fatalf("definitive empty gallery returned %v", images)
		}
		if calls := client.listCallCount(); calls != 0 {
			t.Fatalf("definitive empty gallery queried provider %d times", calls)
		}
	})

	t.Run("pending linked gallery", func(t *testing.T) {
		mbid := uuid.NewString()
		store := newProviderArtworkStoreFixture()
		store.eligible[mbid] = true
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		images, err := service.ListImages(ctx, mbid)
		if !errors.Is(err, ErrProviderArtworkPending) {
			t.Fatalf("pending linked gallery error = %v, want ErrProviderArtworkPending", err)
		}
		if images != nil {
			t.Fatalf("pending linked gallery returned images: %v", images)
		}
		if calls := client.listCallCount(); calls != 0 {
			t.Fatalf("pending ListImages queried provider %d times", calls)
		}
	})

	t.Run("unlinked gallery", func(t *testing.T) {
		mbid := uuid.NewString()
		store := newProviderArtworkStoreFixture()
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		images, err := service.ListImages(ctx, mbid)
		if err != nil {
			t.Fatalf("list unlinked gallery: %v", err)
		}
		if len(images) != 0 {
			t.Fatalf("unlinked gallery returned %v", images)
		}
		if calls := client.listCallCount(); calls != 0 {
			t.Fatalf("unlinked ListImages queried provider %d times", calls)
		}
	})
}

func TestProviderArtworkDownloadImageServesLocalCacheOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("cached listed image", func(t *testing.T) {
		mbid := uuid.NewString()
		data := providerArtworkPNG(t, color.White)
		store := newProviderArtworkStoreFixture()
		store.seedCachedImage(mbid, "9", data, "image/png", true)
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		var output bytes.Buffer
		result, err := service.DownloadImage(ctx, mbid, "9", &output)
		if err != nil {
			t.Fatalf("read cached image: %v", err)
		}
		digest := sha256.Sum256(data)
		if !result.Found || result.MIME != "image/png" || result.Size != int64(len(data)) || result.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("cached result = %+v", result)
		}
		if !bytes.Equal(output.Bytes(), data) {
			t.Fatal("cached image bytes differ from stored bytes")
		}
		if calls := client.imageCallCount(); calls != 0 {
			t.Fatalf("cached DownloadImage queried provider %d times", calls)
		}
	})

	t.Run("unlisted cached image", func(t *testing.T) {
		mbid := uuid.NewString()
		store := newProviderArtworkStoreFixture()
		store.seedCachedImage(mbid, "10", providerArtworkPNG(t, color.White), "image/png", false)
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		var output bytes.Buffer
		result, err := service.DownloadImage(ctx, mbid, "10", &output)
		if err != nil {
			t.Fatalf("read unlisted image: %v", err)
		}
		if result.Found || output.Len() != 0 {
			t.Fatalf("unlisted image returned found=%v bytes=%d", result.Found, output.Len())
		}
		if calls := client.imageCallCount(); calls != 0 {
			t.Fatalf("unlisted DownloadImage queried provider %d times", calls)
		}
	})

	t.Run("missing image", func(t *testing.T) {
		mbid := uuid.NewString()
		store := newProviderArtworkStoreFixture()
		client := newProviderArtworkClientFixture()
		service := newProviderArtwork(store, client)

		var output bytes.Buffer
		result, err := service.DownloadImage(ctx, mbid, "11", &output)
		if err != nil {
			t.Fatalf("read missing image: %v", err)
		}
		if result.Found || output.Len() != 0 {
			t.Fatalf("missing image returned found=%v bytes=%d", result.Found, output.Len())
		}
		if calls := client.imageCallCount(); calls != 0 {
			t.Fatalf("missing DownloadImage queried provider %d times", calls)
		}
	})
}

func TestProviderArtworkDownloadFrontPendingUntilChecked(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	client := newProviderArtworkClientFixture()
	service := newProviderArtwork(store, client)

	var output bytes.Buffer
	if _, err := service.DownloadFront(ctx, mbid, &output); !errors.Is(err, ErrProviderArtworkPending) {
		t.Fatalf("unchecked front error = %v, want ErrProviderArtworkPending", err)
	}
	if calls := client.frontCallCount(); calls != 0 {
		t.Fatalf("unchecked DownloadFront queried provider %d times", calls)
	}
}

func TestProviderArtworkRejectsInvalidMBIDBeforeProviderAccess(t *testing.T) {
	ctx := context.Background()
	store := newProviderArtworkStoreFixture()
	client := newProviderArtworkClientFixture()
	service := newProviderArtwork(store, client)

	for _, mbid := range []string{"", "not-a-uuid", uuid.New().String() + "0"} {
		if err := service.EnsureFront(ctx, mbid); !errors.Is(err, ErrReleaseCoverInvalid) {
			t.Fatalf("EnsureFront(%q) error = %v, want ErrReleaseCoverInvalid", mbid, err)
		}
		if err := service.EnsureGallery(ctx, mbid); !errors.Is(err, ErrReleaseCoverInvalid) {
			t.Fatalf("EnsureGallery(%q) error = %v, want ErrReleaseCoverInvalid", mbid, err)
		}
	}
	if calls := client.frontCallCount(); calls != 0 {
		t.Fatalf("invalid MBID triggered %d front downloads", calls)
	}
	if calls := client.listCallCount(); calls != 0 {
		t.Fatalf("invalid MBID triggered %d listings", calls)
	}
}

func TestProviderArtworkEnsureFrontCoalescesConcurrentAttempts(t *testing.T) {
	ctx := context.Background()
	mbid := uuid.NewString()
	store := newProviderArtworkStoreFixture()
	client := newProviderArtworkClientFixture()
	png := providerArtworkPNG(t, color.White)
	started := make(chan struct{})
	release := make(chan struct{})
	client.frontHook = func(destination io.Writer) (coverart.Result, error) {
		close(started)
		<-release
		return providerArtworkDownload{data: png, mime: "image/png"}.write(destination)
	}
	service := newProviderArtwork(store, client)

	const attempts = 8
	var wait sync.WaitGroup
	errs := make(chan error, attempts)
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- service.EnsureFront(ctx, mbid)
		}()
	}
	<-started
	close(release)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent EnsureFront: %v", err)
		}
	}
	if calls := client.frontCallCount(); calls != 1 {
		t.Fatalf("concurrent front downloads = %d, want 1", calls)
	}
	if saved := store.savedFront(); len(saved) != 1 {
		t.Fatalf("concurrent saved fronts = %v, want one", saved)
	}
}
