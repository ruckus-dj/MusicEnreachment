package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type automaticReleaseCoverRepository interface {
	ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error)
	RecordAutomaticReleaseCoverSelection(context.Context, uuid.UUID, int64, []byte, string, *string) (persistence.ReleaseCover, error)
}

type ReleaseCoverAcquisition struct {
	repository automaticReleaseCoverRepository
	resolver   *ReleaseCoverResolver
}

func NewReleaseCoverAcquisition(repository automaticReleaseCoverRepository, resolver *ReleaseCoverResolver) *ReleaseCoverAcquisition {
	return &ReleaseCoverAcquisition{repository: repository, resolver: resolver}
}

// Ensure runs inside the existing durable publication-reconciliation job before
// first admission. Network work holds neither a SQL transaction nor the global
// publication coordinator. The final revision CAS protects concurrent manual save.
func (acquisition *ReleaseCoverAcquisition) Ensure(ctx context.Context, releaseID uuid.UUID) error {
	if acquisition == nil || acquisition.repository == nil || acquisition.resolver == nil {
		return errors.New("release cover acquisition is unavailable")
	}
	cover, err := acquisition.repository.ReadReleaseCover(ctx, releaseID)
	if err != nil {
		return fmt.Errorf("read automatic cover authority: %w", err)
	}
	if cover.Mode != "automatic" {
		return nil
	}
	resolved, resolveErr := acquisition.resolver.Resolve(ctx, releaseID)
	if err := ctx.Err(); err != nil {
		return err
	}
	var warning *string
	if resolveErr != nil {
		message := "Не удалось получить обложку релиза."
		warning = &message
		resolved = ResolvedReleaseCover{}
	}
	_, err = acquisition.repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, cover.Revision, resolved.Data, resolved.MIME, warning)
	if errors.Is(err, persistence.ErrReleaseCoverConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("record automatic cover result: %w", err)
	}
	return nil
}
