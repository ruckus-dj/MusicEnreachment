package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// ErrProviderArtworkPending means the archive has not provided a complete,
// usable answer yet. A definitive empty listing or missing front is not pending.
var ErrProviderArtworkPending = errors.New("provider artwork has not finished downloading")

// A cached miss is useful during backfill, but an old 404 must not authorize
// a future coverless publication without another exact-provider check.
const providerArtworkAbsentTTL = 7 * 24 * time.Hour

type providerArtworkStore interface {
	ReadCoverArtArchiveRelease(context.Context, string) (persistence.CoverArtArchiveRelease, error)
	ReadCoverArtArchiveFront(context.Context, string) (persistence.CoverArtArchiveFront, error)
	ReadCoverArtArchiveImage(context.Context, string, string) (persistence.CoverArtArchiveImage, error)
	SaveCoverArtArchiveFront(context.Context, string, string, []byte, string) error
	MarkCoverArtArchiveFrontAbsent(context.Context, string) error
	RecordCoverArtArchiveFrontError(context.Context, string, string) error
	SaveCoverArtArchiveGalleryCandidates(context.Context, string, []persistence.CoverArtArchiveImage) error
	SaveCoverArtArchiveImageData(context.Context, string, string, []byte, string) error
	CompleteCoverArtArchiveGallery(context.Context, string) error
	RecordCoverArtArchiveGalleryError(context.Context, string, string) error
	RecordCoverArtArchiveImageError(context.Context, string, string, string) error
	ReadReleaseMusicBrainzReleaseID(context.Context, uuid.UUID) (string, bool, error)
	ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error)
	CoverArtArchiveGalleryEligible(context.Context, string) (bool, error)
}

type providerArtworkClient interface {
	ListImages(context.Context, string) ([]coverart.Image, error)
	DownloadFront(context.Context, string, io.Writer) (coverart.Result, error)
	DownloadImage(context.Context, string, string, io.Writer) (coverart.Result, error)
}

type providerArtworkFlight struct {
	done chan struct{}
	err  error
}

// ProviderArtwork downloads and caches artwork by exact MusicBrainz edition.
// Its reader methods never talk to the network or select publication covers.
type ProviderArtwork struct {
	store       providerArtworkStore
	client      providerArtworkClient
	acquisition *ReleaseCoverAcquisition
	transfers   chan struct{}
	previews    chan struct{}
	mu          sync.Mutex
	flights     map[string]*providerArtworkFlight
}

func NewProviderArtwork(repository *persistence.LibraryRepository, client *coverart.Client) *ProviderArtwork {
	service := newProviderArtwork(repository, client)
	service.acquisition = NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(repository, service))
	return service
}

func newProviderArtwork(store providerArtworkStore, client providerArtworkClient) *ProviderArtwork {
	return &ProviderArtwork{
		store: store, client: client, transfers: make(chan struct{}, 3), previews: make(chan struct{}, 1),
		flights: make(map[string]*providerArtworkFlight),
	}
}

// EnsureFront stores the exact edition's original front or explicit absence.
func (service *ProviderArtwork) EnsureFront(ctx context.Context, mbid string) error {
	mbid, err := artworkReleaseID(mbid)
	if err != nil {
		return err
	}
	return service.once(ctx, "front:"+mbid, func() error {
		state, err := service.store.ReadCoverArtArchiveRelease(ctx, mbid)
		if err != nil || !shouldCheckProviderFront(state, time.Now()) {
			return err
		}
		image, found, err := service.download(ctx, func(destination io.Writer) (coverart.Result, error) {
			return service.client.DownloadFront(ctx, mbid, destination)
		})
		if err != nil {
			if ctx.Err() == nil {
				err = errors.Join(err, service.store.RecordCoverArtArchiveFrontError(ctx, mbid, "Cover Art Archive front download failed"))
			}
			return err
		}
		if !found {
			return service.store.MarkCoverArtArchiveFrontAbsent(ctx, mbid)
		}
		return service.store.SaveCoverArtArchiveFront(ctx, mbid, "", image.Data, image.MIME)
	})
}

func shouldCheckProviderFront(state persistence.CoverArtArchiveRelease, now time.Time) bool {
	switch state.FrontStatus {
	case persistence.CoverArtArchiveUnchecked:
		return true
	case persistence.CoverArtArchiveFrontAbsent:
		return state.FrontCheckedAt != nil && !now.Before(state.FrontCheckedAt.Add(providerArtworkAbsentTTL))
	default:
		return false
	}
}

// EnsureGallery caches originals only for source-linked editions. Successfully
// downloaded images survive a failed or interrupted gallery attempt.
func (service *ProviderArtwork) EnsureGallery(ctx context.Context, mbid string) error {
	mbid, err := artworkReleaseID(mbid)
	if err != nil {
		return err
	}
	return service.once(ctx, "gallery:"+mbid, func() error {
		eligible, err := service.store.CoverArtArchiveGalleryEligible(ctx, mbid)
		if err != nil || !eligible {
			return err
		}
		state, err := service.store.ReadCoverArtArchiveRelease(ctx, mbid)
		if err != nil || state.GalleryStatus == persistence.CoverArtArchiveGalleryComplete {
			return err
		}
		err = service.downloadGallery(ctx, mbid)
		if err != nil && ctx.Err() == nil {
			err = errors.Join(err, service.store.RecordCoverArtArchiveGalleryError(ctx, mbid, "Cover Art Archive gallery download failed"))
		}
		return err
	})
}

func (service *ProviderArtwork) downloadGallery(ctx context.Context, mbid string) error {
	if err := service.takeTransfer(ctx); err != nil {
		return err
	}
	list, err := service.client.ListImages(ctx, mbid)
	<-service.transfers
	if err != nil {
		return fmt.Errorf("read provider artwork gallery: %w", err)
	}
	eligible, err := service.store.CoverArtArchiveGalleryEligible(ctx, mbid)
	if err != nil || !eligible {
		return err
	}
	candidates := make([]persistence.CoverArtArchiveImage, 0, len(list))
	for _, entry := range list {
		candidates = append(candidates, persistence.CoverArtArchiveImage{
			ID: entry.ID, Front: entry.Front, Back: entry.Back, Types: entry.Types,
			Comment: entry.Comment,
		})
	}
	if err := service.store.SaveCoverArtArchiveGalleryCandidates(ctx, mbid, candidates); err != nil {
		return err
	}
	state, err := service.store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		return err
	}
	var firstFailure error
	for _, entry := range state.Images {
		if entry.Cached {
			continue
		}
		eligible, err := service.store.CoverArtArchiveGalleryEligible(ctx, mbid)
		if err != nil || !eligible {
			return err
		}
		image, found, err := service.download(ctx, func(destination io.Writer) (coverart.Result, error) {
			return service.client.DownloadImage(ctx, mbid, entry.ID, destination)
		})
		if err == nil && !found {
			err = errors.New("listed artwork original is not available")
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if writeErr := service.store.RecordCoverArtArchiveImageError(ctx, mbid, entry.ID, "Cover Art Archive original download failed"); writeErr != nil {
				return errors.Join(err, writeErr)
			}
			if firstFailure == nil {
				firstFailure = fmt.Errorf("download gallery image %s: %w", entry.ID, err)
			}
			continue
		}
		eligible, err = service.store.CoverArtArchiveGalleryEligible(ctx, mbid)
		if err != nil || !eligible {
			return err
		}
		if err := service.store.SaveCoverArtArchiveImageData(ctx, mbid, entry.ID, image.Data, image.MIME); err != nil {
			return err
		}
	}
	if firstFailure != nil {
		return firstFailure
	}
	eligible, err = service.store.CoverArtArchiveGalleryEligible(ctx, mbid)
	if err != nil || !eligible {
		return err
	}
	return service.store.CompleteCoverArtArchiveGallery(ctx, mbid)
}

// PrepareForPublication ensures archive decisions and copies the default cover
// into existing publication authority. Network work is outside admission SQL.
func (service *ProviderArtwork) PrepareForPublication(ctx context.Context, releaseID uuid.UUID) error {
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	mbid, linked, err := service.store.ReadReleaseMusicBrainzReleaseID(bounded, releaseID)
	if err != nil {
		return err
	}
	if linked {
		if err := service.EnsureFront(bounded, mbid); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderArtworkPending, err)
		}
		if err := service.EnsureGallery(bounded, mbid); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderArtworkPending, err)
		}
		state, err := service.store.ReadCoverArtArchiveRelease(bounded, mbid)
		if err != nil {
			return err
		}
		eligible, err := service.store.CoverArtArchiveGalleryEligible(bounded, mbid)
		if err != nil {
			return err
		}
		if state.FrontStatus == persistence.CoverArtArchiveUnchecked ||
			eligible && state.GalleryStatus != persistence.CoverArtArchiveGalleryComplete {
			return ErrProviderArtworkPending
		}
	}
	current, stillLinked, err := service.store.ReadReleaseMusicBrainzReleaseID(bounded, releaseID)
	if err != nil {
		return err
	}
	if current != mbid || linked != stillLinked {
		return ErrProviderArtworkPending
	}
	if err := service.acquisition.Ensure(bounded, releaseID); err != nil {
		return err
	}
	if linked {
		state, err := service.store.ReadCoverArtArchiveRelease(bounded, mbid)
		if err != nil {
			return err
		}
		cover, err := service.store.ReadReleaseCover(bounded, releaseID)
		if err != nil {
			return err
		}
		if cover.SelectedGeneration == nil {
			if cover.PendingGeneration != nil {
				return ErrProviderArtworkPending
			}
			if state.FrontStatus == persistence.CoverArtArchiveFrontPresent {
				front, err := service.store.ReadCoverArtArchiveFront(bounded, mbid)
				if err != nil {
					return err
				}
				// An archived original can be larger than publication accepts.
				// The normal resolver already tried saved source artwork and
				// recorded a warning when it could not supply a cover.
				if _, _, _, inspectErr := artwork.Inspect(front.Data); !errors.Is(inspectErr, artwork.ErrDimensionsExceeded) {
					return ErrProviderArtworkPending
				}
			}
		}
	}
	return nil
}

// ListImages implements the editor's archive interface using local data only.
func (service *ProviderArtwork) ListImages(ctx context.Context, mbid string) ([]coverart.Image, error) {
	state, err := service.store.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		return nil, err
	}
	result := make([]coverart.Image, 0, len(state.Images))
	for _, entry := range state.Images {
		if entry.Cached {
			result = append(result, coverart.Image{
				ID: entry.ID, Front: entry.Front, Back: entry.Back, Types: entry.Types, Comment: entry.Comment,
				ThumbnailURL: "/api/library/provider-artwork/releases/" + state.ReleaseMBID + "/images/" + entry.ID,
			})
		}
	}
	if len(result) == 0 && state.GalleryStatus != persistence.CoverArtArchiveGalleryComplete {
		eligible, err := service.store.CoverArtArchiveGalleryEligible(ctx, mbid)
		if err != nil {
			return nil, err
		}
		if eligible {
			return nil, ErrProviderArtworkPending
		}
	}
	return result, nil
}

func (service *ProviderArtwork) DownloadFront(ctx context.Context, mbid string, destination io.Writer) (coverart.Result, error) {
	front, err := service.store.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil {
		return coverart.Result{}, err
	}
	if !front.Checked {
		return coverart.Result{}, ErrProviderArtworkPending
	}
	return writeCachedArtwork(destination, front.Data, front.MIME, front.SHA256)
}

func (service *ProviderArtwork) DownloadImage(ctx context.Context, mbid, id string, destination io.Writer) (coverart.Result, error) {
	image, err := service.store.ReadCoverArtArchiveImage(ctx, mbid, id)
	if err != nil {
		if errors.Is(err, persistence.ErrCoverArtArchiveImageNotFound) {
			return coverart.Result{}, nil
		}
		return coverart.Result{}, err
	}
	if !image.Listed {
		return coverart.Result{}, nil
	}
	return writeCachedArtwork(destination, image.Data, image.MIME, image.SHA256)
}

// Thumbnail serves a bounded local derivative without requesting upstream.
func (service *ProviderArtwork) Thumbnail(ctx context.Context, mbid, imageID string) (ReleaseCoverImage, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseCoverThumbnailTimeout)
	defer cancel()
	select {
	case service.previews <- struct{}{}:
		defer func() { <-service.previews }()
	case <-ctx.Done():
		return ReleaseCoverImage{}, ctx.Err()
	}
	var data []byte
	if imageID == "" {
		front, err := service.store.ReadCoverArtArchiveFront(ctx, mbid)
		if err != nil {
			return ReleaseCoverImage{}, err
		}
		data = front.Data
	} else {
		image, err := service.store.ReadCoverArtArchiveImage(ctx, mbid, imageID)
		if err != nil {
			return ReleaseCoverImage{}, err
		}
		if image.Listed {
			data = image.Data
		}
	}
	if len(data) == 0 {
		return ReleaseCoverImage{}, persistence.ErrCoverArtArchiveImageNotFound
	}
	return releaseCoverThumbnail(ctx, data)
}

func (service *ProviderArtwork) once(ctx context.Context, key string, run func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	if existing := service.flights[key]; existing != nil {
		service.mu.Unlock()
		select {
		case <-existing.done:
			return existing.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	flight := &providerArtworkFlight{done: make(chan struct{})}
	service.flights[key] = flight
	service.mu.Unlock()
	flight.err = run()
	service.mu.Lock()
	delete(service.flights, key)
	close(flight.done)
	service.mu.Unlock()
	return flight.err
}

func (service *ProviderArtwork) takeTransfer(ctx context.Context) error {
	select {
	case service.transfers <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *ProviderArtwork) download(ctx context.Context, fetch func(io.Writer) (coverart.Result, error)) (ReleaseCoverImage, bool, error) {
	if err := service.takeTransfer(ctx); err != nil {
		return ReleaseCoverImage{}, false, err
	}
	defer func() { <-service.transfers }()
	var buffer bytes.Buffer
	result, err := fetch(&releaseCoverCandidateWriter{writer: &buffer, remaining: releaseCoverCandidateMaxBytes})
	if err != nil || !result.Found {
		return ReleaseCoverImage{}, false, err
	}
	data := buffer.Bytes()
	format, _, _, err := artwork.Inspect(data)
	oversized := errors.Is(err, artwork.ErrDimensionsExceeded)
	if oversized {
		// Keep the original in the provider cache, but do not decode or
		// select it for publication above the existing input size limit.
		format = strings.TrimPrefix(result.MIME, "image/")
	}
	if err != nil && !oversized || result.MIME != "image/"+format || result.Size != int64(len(data)) ||
		(format != "png" && format != "jpeg") {
		return ReleaseCoverImage{}, false, errors.New("provider artwork original has invalid image data")
	}
	if err := artwork.ValidateEncoding(data, format); err != nil {
		return ReleaseCoverImage{}, false, fmt.Errorf("validate provider artwork original: %w", err)
	}
	if !oversized {
		// The header/envelope alone does not distinguish a truncated JPEG or
		// corrupt compressed PNG from a usable original.
		if _, err := releaseCoverThumbnail(ctx, data); err != nil {
			return ReleaseCoverImage{}, false, fmt.Errorf("decode provider artwork original: %w", err)
		}
	}
	return ReleaseCoverImage{Data: data, MIME: result.MIME}, true, nil
}

func writeCachedArtwork(destination io.Writer, data []byte, mime *string, sha []byte) (coverart.Result, error) {
	if len(data) == 0 || mime == nil {
		return coverart.Result{}, nil
	}
	count, err := io.Copy(destination, bytes.NewReader(data))
	if err != nil {
		return coverart.Result{}, err
	}
	return coverart.Result{Found: true, MIME: *mime, Size: count, SHA256: hex.EncodeToString(sha)}, nil
}

func artworkReleaseID(mbid string) (string, error) {
	if exact, ok := exactReleaseMBID(mbid); ok {
		return exact, nil
	}
	return "", ErrReleaseCoverInvalid
}
