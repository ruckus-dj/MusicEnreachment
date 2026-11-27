package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type releaseCoverCandidateReader interface {
	ReadReleaseCoverCandidates(context.Context, uuid.UUID) (*persistence.ReleaseCoverCandidates, error)
}

type releaseCoverProvider interface {
	DownloadFront(context.Context, string, io.Writer) (coverart.Result, error)
}

// errReleaseCoverUnavailable marks an optional cover lookup that found no usable
// image. Repository failures and cancellation are deliberately not wrapped in
// this sentinel so callers can still distinguish infrastructure errors.
var errReleaseCoverUnavailable = errors.New("release cover is unavailable")

// ResolvedReleaseCover is detached from the source observations and contains no
// filesystem locator. The original selected bytes and MIME remain unchanged;
// publication embeds a separate bounded derivative.
type ResolvedReleaseCover struct {
	Data            []byte
	MIME            string
	Source          string
	ProviderMBID    string
	CandidateID     uuid.UUID
	WinningResultID uuid.UUID
}

type ReleaseCoverResolver struct {
	repository releaseCoverCandidateReader
	provider   releaseCoverProvider
	// maxBytes bounds the provider original staged for automatic acquisition.
	// It is a field so tests can lower the limit.
	maxBytes int64
}

func NewReleaseCoverResolver(repository releaseCoverCandidateReader, provider releaseCoverProvider) *ReleaseCoverResolver {
	return &ReleaseCoverResolver{repository: repository, provider: provider, maxBytes: releaseCoverCandidateMaxBytes}
}

// Resolve prefers the Cover Art Archive front image for the exact release MBID
// and falls back to the best saved source artwork whenever that optional lookup
// is unavailable, fails, or returns unusable bytes. The provider may be nil.
// Repository errors and cancellation are returned as errors rather than being
// converted into an empty fallback.
func (resolver *ReleaseCoverResolver) Resolve(ctx context.Context, releaseID uuid.UUID) (ResolvedReleaseCover, error) {
	candidates, err := resolver.candidates(ctx, releaseID)
	if err != nil {
		return ResolvedReleaseCover{}, err
	}

	var providerErr error
	if candidates.MusicBrainzMBID != nil && resolver.provider != nil {
		if mbid, ok := exactReleaseMBID(*candidates.MusicBrainzMBID); ok {
			providerCover, found, err := resolveProviderReleaseCover(ctx, resolver.provider, mbid, resolver.providerMaxBytes())
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ResolvedReleaseCover{}, ctxErr
				}
				// A temporary provider failure or unusable front must not hide
				// valid saved artwork.
				providerErr = err
			} else if found {
				return providerCover, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ResolvedReleaseCover{}, err
	}
	localCover, err := resolveLocalReleaseCover(ctx, candidates.Candidates)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ResolvedReleaseCover{}, ctxErr
		}
		return ResolvedReleaseCover{}, fmt.Errorf("%w: %w", errReleaseCoverUnavailable, err)
	}
	if localCover.Source != "" {
		return localCover, nil
	}
	if len(candidates.CoverErrors) > 0 {
		return ResolvedReleaseCover{}, fmt.Errorf("%w: saved source cover could not be inspected: %s", errReleaseCoverUnavailable, candidates.CoverErrors[0])
	}
	if providerErr != nil {
		return ResolvedReleaseCover{}, fmt.Errorf("%w: %w", errReleaseCoverUnavailable, providerErr)
	}
	return ResolvedReleaseCover{}, nil
}

// ResolveSaved resolves only saved source artwork. It is used when an optional
// provider lookup exceeded its own bound but the caller is still live; local
// image work is not subject to the remote provider deadline.
func (resolver *ReleaseCoverResolver) ResolveSaved(ctx context.Context, releaseID uuid.UUID) (ResolvedReleaseCover, error) {
	candidates, err := resolver.candidates(ctx, releaseID)
	if err != nil {
		return ResolvedReleaseCover{}, err
	}
	localCover, err := resolveLocalReleaseCover(ctx, candidates.Candidates)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ResolvedReleaseCover{}, ctxErr
		}
		return ResolvedReleaseCover{}, fmt.Errorf("%w: %w", errReleaseCoverUnavailable, err)
	}
	if localCover.Source != "" {
		return localCover, nil
	}
	if len(candidates.CoverErrors) > 0 {
		return ResolvedReleaseCover{}, fmt.Errorf("%w: saved source cover could not be inspected: %s", errReleaseCoverUnavailable, candidates.CoverErrors[0])
	}
	return ResolvedReleaseCover{}, nil
}

func (resolver *ReleaseCoverResolver) candidates(ctx context.Context, releaseID uuid.UUID) (*persistence.ReleaseCoverCandidates, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resolver == nil || resolver.repository == nil {
		return nil, errors.New("release cover candidate reader is unavailable")
	}
	candidates, err := resolver.repository.ReadReleaseCoverCandidates(ctx, releaseID)
	if err != nil {
		return nil, fmt.Errorf("read release cover candidates: %w", err)
	}
	if candidates == nil {
		return nil, errors.New("release cover candidate snapshot is unavailable")
	}
	return candidates, nil
}

func (resolver *ReleaseCoverResolver) providerMaxBytes() int64 {
	if resolver.maxBytes > 0 {
		return resolver.maxBytes
	}
	return releaseCoverCandidateMaxBytes
}

// exactReleaseMBID accepts only a canonical MusicBrainz release UUID. Invalid
// or non-canonical provider keys must not become outbound Cover Art Archive
// requests.
func exactReleaseMBID(value string) (string, bool) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || !strings.EqualFold(value, parsed.String()) {
		return "", false
	}
	return parsed.String(), true
}

// resolveProviderReleaseCover stages the optional provider download privately so
// partial bytes from a failed stream are never returned. The boolean reports
// whether the provider had a usable front image; errors mean the front was
// unusable but the caller may still use saved artwork.
func resolveProviderReleaseCover(ctx context.Context, provider releaseCoverProvider, mbid string, maxBytes int64) (ResolvedReleaseCover, bool, error) {
	if maxBytes <= 0 {
		maxBytes = releaseCoverCandidateMaxBytes
	}
	downloaded, err := os.CreateTemp("", "melotrove-cover-*")
	if err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("create private cover download: %w", err)
	}
	defer func() { _ = os.Remove(downloaded.Name()) }()
	defer func() { _ = downloaded.Close() }()
	writer := &releaseCoverCandidateWriter{writer: downloaded, remaining: maxBytes}
	result, err := provider.DownloadFront(ctx, mbid, writer)
	if err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("download provider cover: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ResolvedReleaseCover{}, false, err
	}
	if !result.Found {
		return ResolvedReleaseCover{}, false, nil
	}
	info, err := downloaded.Stat()
	if err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("inspect provider cover download: %w", err)
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return ResolvedReleaseCover{}, false, errors.New("provider cover exceeds the maximum size")
	}
	if result.Size > 0 && result.Size != info.Size() {
		return ResolvedReleaseCover{}, false, errors.New("provider cover bytes are incomplete")
	}
	if _, err := downloaded.Seek(0, io.SeekStart); err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("rewind cover download: %w", err)
	}
	format, _, _, err := artwork.InspectReader(downloaded)
	if err != nil || result.MIME != "image/"+format {
		return ResolvedReleaseCover{}, false, fmt.Errorf("decode provider cover: %w", errors.Join(err, errors.New("invalid image or MIME")))
	}
	if _, err := downloaded.Seek(0, io.SeekStart); err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("rewind validated cover download: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(downloaded, maxBytes+1))
	if err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("read cover for database storage: %w", err)
	}
	if int64(len(data)) != info.Size() {
		return ResolvedReleaseCover{}, false, errors.New("provider cover bytes are incomplete")
	}
	if err := artwork.ValidateEncoding(data, format); err != nil {
		return ResolvedReleaseCover{}, false, fmt.Errorf("validate provider cover encoding: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ResolvedReleaseCover{}, false, err
	}
	return ResolvedReleaseCover{Data: data, MIME: result.MIME, Source: "provider", ProviderMBID: mbid}, true, nil
}

// resolveLocalReleaseCover chooses the largest valid saved source image and uses
// the smallest metadata-result UUID to break ties. Invalid observations are
// skipped when another valid candidate exists; an error is returned only when
// no usable saved image remains.
func resolveLocalReleaseCover(ctx context.Context, candidates []persistence.ReleaseCoverCandidate) (ResolvedReleaseCover, error) {
	var winner *persistence.ReleaseCoverCandidate
	var winnerPixels uint64
	var firstErr error
	for index := range candidates {
		if err := ctx.Err(); err != nil {
			return ResolvedReleaseCover{}, err
		}
		candidate := &candidates[index]
		format, width, height, err := artwork.Inspect(candidate.Data)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("inspect saved cover %s resolution: %w", candidate.ID, errors.Join(err, errors.New("invalid image dimensions")))
			}
			continue
		}
		if candidate.MIME != "image/"+format {
			if firstErr == nil {
				firstErr = fmt.Errorf("saved cover %s MIME does not match image bytes", candidate.ID)
			}
			continue
		}
		if err := artwork.ValidateEncoding(candidate.Data, format); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("validate saved cover %s encoding: %w", candidate.ID, err)
			}
			continue
		}
		pixels := uint64(width) * uint64(height)
		if winner == nil || pixels > winnerPixels || pixels == winnerPixels && candidate.ID.String() < winner.ID.String() {
			winner, winnerPixels = candidate, pixels
		}
	}
	if winner == nil {
		if firstErr != nil {
			return ResolvedReleaseCover{}, firstErr
		}
		return ResolvedReleaseCover{}, nil
	}
	result := ResolvedReleaseCover{Data: append([]byte(nil), winner.Data...), MIME: winner.MIME, Source: "local", CandidateID: winner.ID, WinningResultID: winner.WinningResultID}
	if err := ctx.Err(); err != nil {
		return ResolvedReleaseCover{}, err
	}
	return result, nil
}
