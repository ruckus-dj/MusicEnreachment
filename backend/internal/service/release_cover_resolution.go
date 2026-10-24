package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

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

// ResolvedReleaseCover is detached from the source observations and contains no
// filesystem locator. The original selected bytes and MIME remain unchanged;
// publication embeds a separate bounded derivative.
type ResolvedReleaseCover struct {
	Data            []byte
	MIME            string
	Source          string
	CandidateID     uuid.UUID
	WinningResultID uuid.UUID
}

type ReleaseCoverResolver struct {
	repository releaseCoverCandidateReader
	provider   releaseCoverProvider
}

func NewReleaseCoverResolver(repository releaseCoverCandidateReader, provider releaseCoverProvider) *ReleaseCoverResolver {
	return &ReleaseCoverResolver{repository: repository, provider: provider}
}

func (resolver *ReleaseCoverResolver) Resolve(ctx context.Context, releaseID uuid.UUID) (ResolvedReleaseCover, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedReleaseCover{}, err
	}
	if resolver == nil || resolver.repository == nil {
		return ResolvedReleaseCover{}, errors.New("release cover candidate reader is unavailable")
	}
	candidates, err := resolver.repository.ReadReleaseCoverCandidates(ctx, releaseID)
	if err != nil {
		return ResolvedReleaseCover{}, fmt.Errorf("read release cover candidates: %w", err)
	}
	if candidates == nil {
		return ResolvedReleaseCover{}, errors.New("release cover candidate snapshot is unavailable")
	}
	if candidates.MusicBrainzMBID != nil {
		if resolver.provider == nil {
			return ResolvedReleaseCover{}, errors.New("release cover provider is unavailable")
		}
		downloaded, err := os.CreateTemp("", "melotrove-cover-*")
		if err != nil {
			return ResolvedReleaseCover{}, fmt.Errorf("create private cover download: %w", err)
		}
		defer func() { _ = os.Remove(downloaded.Name()) }()
		defer func() { _ = downloaded.Close() }()
		result, err := resolver.provider.DownloadFront(ctx, *candidates.MusicBrainzMBID, downloaded)
		if err != nil {
			return ResolvedReleaseCover{}, fmt.Errorf("download provider cover: %w", err)
		}
		if result.Found {
			if _, err := downloaded.Seek(0, io.SeekStart); err != nil {
				return ResolvedReleaseCover{}, fmt.Errorf("rewind cover download: %w", err)
			}
			format, _, _, err := artwork.InspectReader(downloaded)
			if err != nil || result.MIME != "image/"+format {
				return ResolvedReleaseCover{}, fmt.Errorf("decode provider cover: %w", errors.Join(err, errors.New("invalid image or MIME")))
			}
			if _, err := downloaded.Seek(0, io.SeekStart); err != nil {
				return ResolvedReleaseCover{}, fmt.Errorf("rewind validated cover download: %w", err)
			}
			data, err := io.ReadAll(downloaded)
			if err != nil {
				return ResolvedReleaseCover{}, fmt.Errorf("read cover for database storage: %w", err)
			}
			if err := artwork.ValidateEncoding(data, format); err != nil {
				return ResolvedReleaseCover{}, fmt.Errorf("validate provider cover encoding: %w", err)
			}
			return ResolvedReleaseCover{Data: data, MIME: result.MIME, Source: "provider"}, nil
		}
	}
	if len(candidates.CoverErrors) > 0 {
		return ResolvedReleaseCover{}, fmt.Errorf("saved source cover could not be inspected: %s", candidates.CoverErrors[0])
	}
	return resolveLocalReleaseCover(ctx, candidates.Candidates)
}

func resolveLocalReleaseCover(ctx context.Context, candidates []persistence.ReleaseCoverCandidate) (ResolvedReleaseCover, error) {
	var winner *persistence.ReleaseCoverCandidate
	var winnerPixels uint64
	for index := range candidates {
		if err := ctx.Err(); err != nil {
			return ResolvedReleaseCover{}, err
		}
		candidate := &candidates[index]
		_, width, height, err := artwork.Inspect(candidate.Data)
		if err != nil {
			return ResolvedReleaseCover{}, fmt.Errorf("inspect saved cover %s resolution: %w", candidate.ID, errors.Join(err, errors.New("invalid image dimensions")))
		}
		pixels := uint64(width) * uint64(height)
		if winner == nil || pixels > winnerPixels || pixels == winnerPixels && candidate.ID.String() < winner.ID.String() {
			winner, winnerPixels = candidate, pixels
		}
	}
	if winner == nil {
		return ResolvedReleaseCover{}, nil
	}
	result := ResolvedReleaseCover{Data: append([]byte(nil), winner.Data...), MIME: winner.MIME, Source: "local", CandidateID: winner.ID, WinningResultID: winner.WinningResultID}
	format, _, _, err := artwork.Inspect(result.Data)
	if err != nil || result.MIME != "image/"+format {
		return ResolvedReleaseCover{}, errors.New("selected saved cover MIME does not match image bytes")
	}
	if err := artwork.ValidateEncoding(result.Data, format); err != nil {
		return ResolvedReleaseCover{}, fmt.Errorf("validate saved cover encoding: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ResolvedReleaseCover{}, err
	}
	return result, nil
}
