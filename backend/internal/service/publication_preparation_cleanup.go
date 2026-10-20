package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func (preparer PublicationStagePreparer) cleanupFailedPreInput(ctx context.Context, scope *PublicationExecutionScope, capability *publicationPreInputCleanupCapability, record persistence.PublicationPreparationRecord, directory sourcefs.OutputDirectory) error {
	if scope == nil || !scope.validPreInputCleanup(capability) {
		return errors.New("publication preparation cleanup lacks its live original-owner capability")
	}
	repository, ok := preparer.Repository.(publicationPreparationCleanupRepository)
	cleanup, cleanupOK := preparer.OutputParent.(sourcefs.OutputCleanup)
	if !ok || !cleanupOK {
		return errors.New("publication preparation cleanup capability is unavailable")
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close owned publication directory before cleanup: %w", err)
	}
	witness := persistence.PublicationPreparationCleanupWitness{Preparation: record}
	intent, err := repository.BeginPublicationPreparationCleanup(ctx, scope.guard, witness)
	if err != nil || intent != persistence.PublicationPreparationCleanupRecorded {
		return errors.Join(errors.New("publication preparation cleanup intent was not acknowledged"), err)
	}
	parentIdentity, err := preparer.OutputParent.Identity(ctx)
	if err != nil {
		return fmt.Errorf("identify publication cleanup parent: %w", err)
	}
	expectedParent := sourcefs.DirectoryIdentity{Platform: record.ParentIdentity.Platform, Volume: record.ParentIdentity.Volume, FileID: record.ParentIdentity.FileID}
	if parentIdentity != expectedParent {
		return errors.New("publication cleanup parent identity changed")
	}
	if err := cleanup.RemoveEmptyChildDir(ctx, record.PrivateDirRel, expectedParent, sourcefs.DirectoryIdentity{Platform: record.DirectoryIdentity.Platform, Volume: record.DirectoryIdentity.Volume, FileID: record.DirectoryIdentity.FileID}); err != nil {
		return fmt.Errorf("remove owned empty publication directory: %w", err)
	}
	if syncer, ok := preparer.OutputParent.(sourcefs.OutputDirectorySync); ok {
		if err := syncer.SyncDirectory(ctx); err != nil {
			return fmt.Errorf("sync publication cleanup parent: %w", err)
		}
	} else {
		return errors.New("publication cleanup parent durability is unavailable")
	}
	result, err := repository.RecordPublicationPreparationCleanupResult(ctx, scope.guard, witness)
	if err != nil || result != persistence.PublicationPreparationCleanupRecorded {
		return errors.Join(errors.New("publication preparation cleanup result was not acknowledged"), err)
	}
	settled, err := repository.SettlePublicationPreparationFailure(ctx, scope.guard, witness, true)
	if err != nil || !settled {
		return errors.Join(errors.New("publication preparation failure settlement was not acknowledged"), err)
	}
	if !scope.finishPreInputCleanup(capability, true) {
		return errors.New("publication execution scope changed before cleanup settlement")
	}
	return nil
}
