package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// automaticReleaseCoverWarning is deliberately generic: the UI needs a safe
	// signal that no publication cover was acquired, not provider diagnostics.
	automaticReleaseCoverWarning = "Не удалось получить обложку релиза."
	// releaseCoverAcquisitionTimeout bounds the optional Cover Art Archive
	// lookup. Saved source artwork remains eligible afterwards while the caller
	// is still live. It is a field so tests can shorten the bound.
	releaseCoverAcquisitionTimeout = releaseCoverCandidateTimeout
)

type automaticReleaseCoverRepository interface {
	ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error)
	RecordAutomaticReleaseCoverSelection(context.Context, uuid.UUID, int64, []byte, string, *string) (persistence.ReleaseCover, error)
}

// automaticReleaseCoverFencedRepository is implemented by the real repository.
// The optional interface keeps older and test doubles working while allowing the
// production write to reject a provider image whose exact MBID was rebound
// during the lookup.
type automaticReleaseCoverFencedRepository interface {
	RecordAutomaticReleaseCoverSelectionForMBID(context.Context, uuid.UUID, int64, []byte, string, *string, string) (persistence.ReleaseCover, error)
}

type releaseCoverEnsureFlight struct {
	done chan struct{}
	err  error
}

type ReleaseCoverAcquisition struct {
	repository automaticReleaseCoverRepository
	resolver   *ReleaseCoverResolver
	// timeout bounds the optional provider lookup.
	timeout time.Duration

	mu      sync.Mutex
	flights map[uuid.UUID]*releaseCoverEnsureFlight
}

func NewReleaseCoverAcquisition(repository automaticReleaseCoverRepository, resolver *ReleaseCoverResolver) *ReleaseCoverAcquisition {
	return &ReleaseCoverAcquisition{
		repository: repository,
		resolver:   resolver,
		timeout:    releaseCoverAcquisitionTimeout,
		flights:    make(map[uuid.UUID]*releaseCoverEnsureFlight),
	}
}

// Ensure runs inside the existing durable publication-reconciliation job before
// first admission. Network work holds neither a SQL transaction nor the global
// publication coordinator. Identical concurrent calls are coalesced by release.
// The final revision CAS protects concurrent manual save and the fenced MBID
// write rejects a provider image after a release rebind.
func (acquisition *ReleaseCoverAcquisition) Ensure(ctx context.Context, releaseID uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if acquisition == nil || acquisition.repository == nil || acquisition.resolver == nil {
		return errors.New("release cover acquisition is unavailable")
	}
	if releaseID == uuid.Nil {
		return errors.New("release cover acquisition identity is required")
	}
	leader, err := acquisition.join(ctx, releaseID)
	if err != nil || !leader {
		return err
	}
	err = acquisition.ensure(ctx, releaseID)
	acquisition.finish(releaseID, err)
	return err
}

func (acquisition *ReleaseCoverAcquisition) join(ctx context.Context, releaseID uuid.UUID) (bool, error) {
	acquisition.mu.Lock()
	if flight, ok := acquisition.flights[releaseID]; ok {
		acquisition.mu.Unlock()
		select {
		case <-flight.done:
			if err := ctx.Err(); err != nil {
				return false, err
			}
			return false, flight.err
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	acquisition.flights[releaseID] = &releaseCoverEnsureFlight{done: make(chan struct{})}
	acquisition.mu.Unlock()
	return true, nil
}

func (acquisition *ReleaseCoverAcquisition) finish(releaseID uuid.UUID, err error) {
	acquisition.mu.Lock()
	defer acquisition.mu.Unlock()
	flight := acquisition.flights[releaseID]
	if flight == nil {
		return
	}
	flight.err = err
	delete(acquisition.flights, releaseID)
	close(flight.done)
}

func (acquisition *ReleaseCoverAcquisition) ensure(ctx context.Context, releaseID uuid.UUID) error {
	cover, err := acquisition.repository.ReadReleaseCover(ctx, releaseID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("read automatic cover authority: %w", err)
	}
	if cover.Mode != "automatic" {
		return nil
	}
	// An existing selected image already has authority. In particular, a manual
	// selection that returned to automatic mode must not trigger CAA work.
	if cover.SelectedGeneration != nil {
		return nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, acquisition.lookupTimeout())
	defer cancel()
	resolved, resolveErr := acquisition.resolver.Resolve(lookupCtx, releaseID)
	if err := ctx.Err(); err != nil {
		return err
	}
	// Our optional provider bound may have expired while the caller is still
	// live. Saved artwork is local and remains worth trying after that bound.
	boundedFailure := lookupCtx.Err() != nil
	if boundedFailure && resolveErr != nil {
		saved, savedErr := acquisition.resolver.ResolveSaved(ctx, releaseID)
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case savedErr == nil && saved.Source != "":
			resolved, resolveErr = saved, nil
		case savedErr != nil && !errors.Is(savedErr, errReleaseCoverUnavailable):
			return fmt.Errorf("resolve saved release cover: %w", savedErr)
		}
	}
	if resolveErr != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !errors.Is(resolveErr, errReleaseCoverUnavailable) && !boundedFailure {
			return fmt.Errorf("resolve automatic cover: %w", resolveErr)
		}
		// Optional provider and unusable saved-image failures become a safe
		// warning; publication itself remains possible without a cover.
		resolved = ResolvedReleaseCover{}
	}

	var warning *string
	if len(resolved.Data) == 0 {
		message := automaticReleaseCoverWarning
		warning = &message
	}
	// An identical absent/failure warning is already durable. Do not churn the
	// revision on repeated previews; a later call can still select artwork if the
	// provider recovers or new saved artwork appears.
	if warning != nil && cover.Warning != nil && *cover.Warning == *warning {
		return nil
	}

	if resolved.ProviderMBID != "" {
		if fenced, ok := acquisition.repository.(automaticReleaseCoverFencedRepository); ok {
			_, err = fenced.RecordAutomaticReleaseCoverSelectionForMBID(ctx, releaseID, cover.Revision, resolved.Data, resolved.MIME, warning, resolved.ProviderMBID)
		} else {
			_, err = acquisition.repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, cover.Revision, resolved.Data, resolved.MIME, warning)
		}
	} else {
		_, err = acquisition.repository.RecordAutomaticReleaseCoverSelection(ctx, releaseID, cover.Revision, resolved.Data, resolved.MIME, warning)
	}
	if errors.Is(err, persistence.ErrReleaseCoverConflict) {
		return nil
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("record automatic cover result: %w", err)
	}
	return nil
}

func (acquisition *ReleaseCoverAcquisition) lookupTimeout() time.Duration {
	if acquisition.timeout > 0 {
		return acquisition.timeout
	}
	return releaseCoverAcquisitionTimeout
}
