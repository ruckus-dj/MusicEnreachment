package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type matchingRecheckAbandonmentRepository interface {
	PreviewMatchingRecheckAbandonment(context.Context, uuid.UUID) (persistence.MatchingRecheckAbandonmentPreview, error)
	AbandonMatchingRecheckPublications(context.Context, uuid.UUID, string,
		func(context.Context, string, []persistence.MatchingRecheckAbandonmentPredecessor) error) error
}

func (service *LibraryPublications) FinalizeMatchingRecheckMovedOutputs(ctx context.Context) error {
	if service == nil || service.coordinator == nil {
		return errors.New("matching recheck output settlement is unavailable")
	}
	repository, ok := service.repository.(interface {
		FinalizeMatchingRecheckMovedOutputs(context.Context, int, func(context.Context, string, persistence.TrackPublication, uuid.UUID) (*persistence.ObsoletePublicationOutputCleanupAdmission, error)) error
	})
	if !ok {
		return errors.New("matching recheck output settlement is unavailable")
	}
	reader, ok := service.settings.(interface {
		GetOutputDirectory(context.Context) (string, bool, error)
	})
	if !ok {
		return errors.New("matching recheck output settings are unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return err
	}
	defer unlock()
	outputRoot, configured, err := reader.GetOutputDirectory(ctx)
	if err != nil || !configured {
		return err
	}
	return repository.FinalizeMatchingRecheckMovedOutputs(ctx, matchingLocalRecheckRunLimit,
		func(ctx context.Context, _ string, previous persistence.TrackPublication, claimID uuid.UUID) (_ *persistence.ObsoletePublicationOutputCleanupAdmission, resultErr error) {
			root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, outputRoot)
			if err != nil {
				return nil, err
			}
			defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
			return captureObsoletePublicationOutput(ctx, root, outputRoot, previous, claimID)
		})
}

func (service *LibraryPublications) PreviewMatchingRecheckAbandonment(ctx context.Context, operationID uuid.UUID) (persistence.MatchingRecheckAbandonmentPreview, error) {
	if service == nil {
		return persistence.MatchingRecheckAbandonmentPreview{}, persistence.ErrPublicationAdmissionConflict
	}
	repository, ok := service.repository.(matchingRecheckAbandonmentRepository)
	if !ok {
		return persistence.MatchingRecheckAbandonmentPreview{}, errors.New("matching recheck abandonment is unavailable")
	}
	return repository.PreviewMatchingRecheckAbandonment(ctx, operationID)
}

// AbandonMatchingRecheckPublications retires only the unfinished part of the
// previewed recheck, after proving the predecessor files still lie untouched.
// The filesystem callback does not query settings or modify files.
func (service *LibraryPublications) AbandonMatchingRecheckPublications(ctx context.Context, operationID uuid.UUID, revision string) error {
	if service == nil || service.coordinator == nil {
		return errors.New("matching recheck abandonment is unavailable")
	}
	repository, ok := service.repository.(matchingRecheckAbandonmentRepository)
	if !ok {
		return errors.New("matching recheck abandonment is unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return fmt.Errorf("lock matching recheck abandonment: %w", err)
	}
	defer unlock()
	return repository.AbandonMatchingRecheckPublications(ctx, operationID, revision, verifyMatchingRecheckAbandonment)
}

func verifyMatchingRecheckAbandonment(ctx context.Context, outputRoot string, predecessors []persistence.MatchingRecheckAbandonmentPredecessor) (resultErr error) {
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, outputRoot)
	if err != nil {
		return fmt.Errorf("open matching recheck output root: %w", errors.Join(persistence.ErrPublicationAdmissionConflict, err))
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	for _, predecessor := range predecessors {
		if _, err := captureObsoletePublicationOutput(ctx, root, outputRoot, predecessor.Current, predecessor.NamespaceClaimID); err != nil {
			return fmt.Errorf("verify unaffected matching recheck predecessor: %w", errors.Join(persistence.ErrPublicationAdmissionConflict, err))
		}
		if predecessor.AbsentTargetPath != "" {
			if err := verifyMatchingRecheckTargetAbsent(ctx, root, predecessor.AbsentTargetPath); err != nil {
				return fmt.Errorf("verify unused matching recheck destination: %w", errors.Join(persistence.ErrPublicationAdmissionConflict, err))
			}
		}
	}
	return nil
}

func verifyMatchingRecheckTargetAbsent(ctx context.Context, root sourcefs.OutputDirectory, relativePath string) error {
	parent, chain, err := openExistingPublicationParent(ctx, root, relativePath)
	if errors.Is(err, fs.ErrNotExist) {
		return root.Verify(ctx)
	}
	if err != nil {
		return err
	}
	defer closeOutputChain(chain)
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return sourcefs.ErrUnsupported
	}
	file, err := mutation.OpenExistingRegular(ctx, path.Base(relativePath))
	if err == nil {
		return errors.Join(persistence.ErrPublicationAdmissionConflict, file.Close())
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return verifyPublicationParentChain(ctx, root, chain)
}
