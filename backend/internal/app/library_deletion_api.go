package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryDeletionAPI struct {
	deletion *service.LibraryDeletionService
	river    persistence.RiverInserter
}

func (api libraryDeletionAPI) Preview(ctx context.Context, kind string, releaseID, positionID uuid.UUID) (*service.LibraryDeletionPreview, error) {
	return api.deletion.Preview(ctx, kind, releaseID, positionID)
}

func (api libraryDeletionAPI) Confirm(ctx context.Context, digest string) (uuid.UUID, error) {
	operation, err := api.deletion.Confirm(ctx, digest, api.river, nil)
	if err != nil {
		return uuid.Nil, err
	}
	return operation.ID, nil
}

func (api libraryDeletionAPI) Retry(ctx context.Context, operationID uuid.UUID) (uuid.UUID, error) {
	operation, err := api.deletion.Retry(ctx, operationID, api.river)
	if err != nil {
		return uuid.Nil, err
	}
	return operation.ID, nil
}
