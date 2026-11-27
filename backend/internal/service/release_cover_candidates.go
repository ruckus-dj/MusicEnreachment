package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// releaseCoverCandidateTimeout bounds the whole remote listing and image
	// download. The persistence write that follows has its own revision CAS.
	releaseCoverCandidateTimeout = 30 * time.Second
	// releaseCoverCandidateMaxBytes bounds bytes staged from Cover Art Archive
	// before the downloaded image is inspected and saved.
	releaseCoverCandidateMaxBytes = 64 << 20
)

var errReleaseCoverCandidateTooLarge = errors.New("release cover candidate exceeds the maximum size")

// ReleaseCoverCandidate is one selectable Cover Art Archive image for the
// release's exact MusicBrainz MBID. It never contains image bytes.
type ReleaseCoverCandidate struct {
	ID           string   `json:"id"`
	Front        bool     `json:"front"`
	Back         bool     `json:"back"`
	Types        []string `json:"types"`
	Comment      string   `json:"comment"`
	ThumbnailURL string   `json:"thumbnail_url"`
}

type releaseCoverCandidateRepository interface {
	ReadReleaseMusicBrainzReleaseID(context.Context, uuid.UUID) (string, bool, error)
}

type releaseCoverCandidateProvider interface {
	ListImages(context.Context, string) ([]coverart.Image, error)
	DownloadImage(context.Context, string, string, io.Writer) (coverart.Result, error)
}

type releaseCoverCandidateSaver interface {
	Save(context.Context, uuid.UUID, int64, []byte, string) (ReleaseCoverSummary, error)
}

// releaseCoverCandidateBoundSaver fences the pending write on the exact
// MusicBrainz release binding used for the listing.
type releaseCoverCandidateBoundSaver interface {
	SaveFromMusicBrainz(context.Context, uuid.UUID, int64, string, []byte, string) (ReleaseCoverSummary, error)
}

// ReleaseCoverCandidates lists Cover Art Archive alternatives for a release
// and turns one selected alternative into the existing pending cover authority.
type ReleaseCoverCandidates struct {
	repository releaseCoverCandidateRepository
	provider   releaseCoverCandidateProvider
	covers     releaseCoverCandidateSaver

	// timeout and maxBytes are fields so tests can shorten the network bound.
	timeout  time.Duration
	maxBytes int64
}

func NewReleaseCoverCandidates(repository releaseCoverCandidateRepository, provider releaseCoverCandidateProvider, covers releaseCoverCandidateSaver) *ReleaseCoverCandidates {
	return &ReleaseCoverCandidates{
		repository: repository,
		provider:   provider,
		covers:     covers,
		timeout:    releaseCoverCandidateTimeout,
		maxBytes:   releaseCoverCandidateMaxBytes,
	}
}

// List returns the approved provider images for the release's exact
// MusicBrainz release MBID. A release without a linked MBID has no candidates;
// no release-group or name fallback is attempted.
func (service *ReleaseCoverCandidates) List(ctx context.Context, releaseID uuid.UUID) ([]ReleaseCoverCandidate, error) {
	if service == nil || service.repository == nil {
		return nil, errors.New("release cover candidate service is unavailable")
	}
	if releaseID == uuid.Nil {
		return nil, fmt.Errorf("list release cover candidates: %w", ErrReleaseCoverInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, service.networkTimeout())
	defer cancel()
	_, images, err := service.images(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	result := make([]ReleaseCoverCandidate, 0, len(images))
	for _, image := range images {
		types := append([]string(nil), image.Types...)
		if types == nil {
			types = []string{}
		}
		result = append(result, ReleaseCoverCandidate{
			ID:           image.ID,
			Front:        image.Front,
			Back:         image.Back,
			Types:        types,
			Comment:      image.Comment,
			ThumbnailURL: image.ThumbnailURL,
		})
	}
	return result, nil
}

// Select downloads one image that is present in the release's current provider
// listing, validates it, and records it through the existing Save workflow. The
// expected revision is the caller's CAS guard; Save re-reads the release and
// never changes the effective selected image.
func (service *ReleaseCoverCandidates) Select(ctx context.Context, releaseID uuid.UUID, imageID string, expectedRevision int64) (ReleaseCoverSummary, error) {
	if service == nil || service.repository == nil || service.provider == nil || service.covers == nil {
		return ReleaseCoverSummary{}, errors.New("release cover candidate service is unavailable")
	}
	if releaseID == uuid.Nil || expectedRevision < 1 || imageID == "" || strings.TrimSpace(imageID) != imageID {
		return ReleaseCoverSummary{}, fmt.Errorf("select release cover candidate: %w", ErrReleaseCoverInvalid)
	}
	networkCtx, cancel := context.WithTimeout(ctx, service.networkTimeout())
	defer cancel()
	mbid, images, err := service.images(networkCtx, releaseID)
	if err != nil {
		return ReleaseCoverSummary{}, err
	}
	var eligible *coverart.Image
	for index := range images {
		if images[index].ID == imageID {
			eligible = &images[index]
			break
		}
	}
	if eligible == nil {
		return ReleaseCoverSummary{}, fmt.Errorf("select release cover candidate: %w", ErrReleaseCoverNotFound)
	}
	data, mime, err := service.download(networkCtx, mbid, eligible.ID)
	if err != nil {
		return ReleaseCoverSummary{}, err
	}
	if bound, ok := service.covers.(releaseCoverCandidateBoundSaver); ok {
		return bound.SaveFromMusicBrainz(ctx, releaseID, expectedRevision, mbid, data, mime)
	}
	// Narrow savers cannot fence the binding transactionally; at least reject a
	// binding change detected while the image was downloaded before the Save CAS.
	current, linked, err := service.repository.ReadReleaseMusicBrainzReleaseID(ctx, releaseID)
	if err != nil {
		if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
			return ReleaseCoverSummary{}, fmt.Errorf("select release cover candidate: %w", ErrReleaseCoverNotFound)
		}
		return ReleaseCoverSummary{}, fmt.Errorf("select release cover candidate: %w", err)
	}
	if !linked || current != mbid {
		return ReleaseCoverSummary{}, fmt.Errorf("select release cover candidate: %w", ErrReleaseCoverConflict)
	}
	return service.covers.Save(ctx, releaseID, expectedRevision, data, mime)
}

func (service *ReleaseCoverCandidates) images(ctx context.Context, releaseID uuid.UUID) (string, []coverart.Image, error) {
	mbid, linked, err := service.repository.ReadReleaseMusicBrainzReleaseID(ctx, releaseID)
	if err != nil {
		if errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
			return "", nil, fmt.Errorf("read release cover candidates: %w", ErrReleaseCoverNotFound)
		}
		return "", nil, fmt.Errorf("read release cover candidates: %w", err)
	}
	if !linked {
		return "", []coverart.Image{}, nil
	}
	if service.provider == nil {
		return "", nil, errors.New("release cover candidate provider is unavailable")
	}
	images, err := service.provider.ListImages(ctx, mbid)
	if err != nil {
		return "", nil, fmt.Errorf("read release cover candidates: %w", err)
	}
	return mbid, images, nil
}

func (service *ReleaseCoverCandidates) download(ctx context.Context, mbid, imageID string) ([]byte, string, error) {
	maxBytes := service.maxBytes
	if maxBytes <= 0 {
		maxBytes = releaseCoverCandidateMaxBytes
	}
	file, err := os.CreateTemp("", "melotrove-cover-candidate-*")
	if err != nil {
		return nil, "", fmt.Errorf("create private release cover candidate download: %w", err)
	}
	name := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(name)
	}()
	result, err := service.provider.DownloadImage(ctx, mbid, imageID, &releaseCoverCandidateWriter{writer: file, remaining: maxBytes})
	if err != nil {
		if errors.Is(err, errReleaseCoverCandidateTooLarge) {
			return nil, "", fmt.Errorf("download release cover candidate: %w", ErrReleaseCoverInvalid)
		}
		return nil, "", fmt.Errorf("download release cover candidate: %w", err)
	}
	if !result.Found {
		return nil, "", fmt.Errorf("download release cover candidate: %w", ErrReleaseCoverNotFound)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("inspect private release cover candidate download: %w", err)
	}
	if info.Size() <= 0 || info.Size() > maxBytes || result.Size != info.Size() {
		return nil, "", fmt.Errorf("download release cover candidate: %w", ErrReleaseCoverInvalid)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("rewind release cover candidate download: %w", err)
	}
	format, _, _, err := artwork.InspectReader(file)
	if err != nil || result.MIME != "image/"+format {
		return nil, "", fmt.Errorf("download release cover candidate: %w", ErrReleaseCoverInvalid)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("rewind validated release cover candidate download: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read release cover candidate download: %w", err)
	}
	if int64(len(data)) != info.Size() {
		return nil, "", fmt.Errorf("read release cover candidate download: %w", ErrReleaseCoverInvalid)
	}
	if err := artwork.ValidateEncoding(data, format); err != nil {
		return nil, "", fmt.Errorf("download release cover candidate: %w", ErrReleaseCoverInvalid)
	}
	return data, result.MIME, nil
}

func (service *ReleaseCoverCandidates) networkTimeout() time.Duration {
	if service.timeout > 0 {
		return service.timeout
	}
	return releaseCoverCandidateTimeout
}

type releaseCoverCandidateWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *releaseCoverCandidateWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.remaining {
		return 0, errReleaseCoverCandidateTooLarge
	}
	written, err := writer.writer.Write(data)
	writer.remaining -= int64(written)
	return written, err
}
