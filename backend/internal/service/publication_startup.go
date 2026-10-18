package service

import (
	"context"
	"errors"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type publicationStartupSnapshotLoader interface {
	LoadPublicationStartupSnapshot(context.Context) (persistence.PublicationStartupSnapshot, error)
}

type PublicationStartup struct {
	loader publicationStartupSnapshotLoader
}

func NewPublicationStartup(loader publicationStartupSnapshotLoader) *PublicationStartup {
	return &PublicationStartup{loader: loader}
}

// CheckPublicationStartup is a read-only fail-closed barrier. It does not
// settle claims, repair rows, or authorize worker registration.
func (startup *PublicationStartup) CheckPublicationStartup(ctx context.Context) error {
	if startup == nil || startup.loader == nil {
		return errors.New("publication recovery is required before startup; recovery status is unavailable")
	}
	snapshot, err := startup.loader.LoadPublicationStartupSnapshot(ctx)
	if err != nil {
		return errors.New("publication recovery is required before startup; recovery status could not be established")
	}
	for _, record := range snapshot.Records {
		if record.HasClaim || !record.EpochKnown || !record.EpochMatches || record.OperationState == "" {
			return errors.New("publication recovery is required before startup; retained execution state needs settlement")
		}
		if record.Phase != "queued" || record.OperationState != "queued" || !record.JobAvailable || !record.QueuedContractValid {
			return errors.New("publication recovery is required before startup; publication state is inconsistent")
		}
	}
	return nil
}
