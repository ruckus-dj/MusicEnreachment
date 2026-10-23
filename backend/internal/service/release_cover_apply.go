package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type ReleaseCoverApplyRequest struct {
	ExpectedEpoch              uuid.UUID
	ExpectedRevision           int64
	ExpectedPendingGeneration  uuid.UUID
	ExpectedSelectedGeneration uuid.UUID
}

type ReleaseCoverPositionOperation struct {
	PositionID          uuid.UUID `json:"position_id"`
	OperationID         uuid.UUID `json:"operation_id"`
	CandidateGeneration uuid.UUID `json:"candidate_generation"`
}

type ReleaseCoverBatch struct {
	ReleaseID          uuid.UUID                       `json:"release_id"`
	Revision           int64                           `json:"revision"`
	SelectedGeneration uuid.UUID                       `json:"selected_generation"`
	Operations         []ReleaseCoverPositionOperation `json:"operations"`
}

func (service *ReleaseCovers) Apply(ctx context.Context, releaseID uuid.UUID, request ReleaseCoverApplyRequest) (ReleaseCoverBatch, error) {
	return service.admit(ctx, releaseID, request, false)
}

func (service *ReleaseCovers) Retry(ctx context.Context, releaseID uuid.UUID, request ReleaseCoverApplyRequest) (ReleaseCoverBatch, error) {
	return service.admit(ctx, releaseID, request, true)
}

func (service *ReleaseCovers) admit(ctx context.Context, releaseID uuid.UUID, request ReleaseCoverApplyRequest, retry bool) (ReleaseCoverBatch, error) {
	if service == nil || service.repository == nil || service.admission == nil || service.settings == nil || service.installations == nil || service.river == nil {
		return ReleaseCoverBatch{}, errors.New("release cover publication admission dependencies are unavailable")
	}
	if releaseID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.ExpectedRevision < 1 ||
		(!retry && request.ExpectedPendingGeneration == uuid.Nil) || (retry && request.ExpectedSelectedGeneration == uuid.Nil) {
		return ReleaseCoverBatch{}, fmt.Errorf("admit release cover publication: %w", ErrReleaseCoverInvalid)
	}
	if service.coordinator == nil {
		return ReleaseCoverBatch{}, errors.New("release cover publication coordinator is unavailable")
	}
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return ReleaseCoverBatch{}, fmt.Errorf("acquire publication mutation lock: %w", err)
	}
	defer unlock()

	runtimeSettings, err := service.settings.ReadRuntimeSettings(ctx)
	if err != nil {
		return ReleaseCoverBatch{}, fmt.Errorf("read active ffmpeg installation: %w", err)
	}
	installationID, err := uuid.Parse(runtimeSettings.ActiveFFmpegInstallation)
	if err != nil || installationID == uuid.Nil {
		return ReleaseCoverBatch{}, errors.New("active managed ffmpeg installation is unavailable")
	}
	installation, err := service.installations.GetInstallation(ctx, installationID)
	if err != nil {
		return ReleaseCoverBatch{}, fmt.Errorf("read active ffmpeg installation: %w", err)
	}
	if installation == nil || installation.ID != installationID {
		return ReleaseCoverBatch{}, errors.New("active managed ffmpeg installation is not verified for this platform")
	}
	selection, err := selectedFFmpegPairFromInstallation(installation, service.platform)
	if err != nil {
		return ReleaseCoverBatch{}, fmt.Errorf("select managed publication tools: %w", err)
	}
	var batch persistence.ReleaseCoverBatchAdmission
	if retry {
		batch, err = service.admission.AdmitReleaseCoverRetry(ctx, persistence.ReleaseCoverRetryAdmission{
			ReleaseID: releaseID, ExpectedEpoch: request.ExpectedEpoch, ExpectedRevision: request.ExpectedRevision,
			ExpectedSelectedGeneration: request.ExpectedSelectedGeneration, Tools: selection,
		}, service.river)
	} else {
		batch, err = service.admission.AdmitReleaseCoverApply(ctx, persistence.ReleaseCoverApplyAdmission{
			ReleaseID: releaseID, ExpectedEpoch: request.ExpectedEpoch, ExpectedRevision: request.ExpectedRevision,
			ExpectedPendingGeneration: request.ExpectedPendingGeneration, Tools: selection,
		}, service.river)
	}
	if err != nil {
		if errors.Is(err, persistence.ErrPublicationAdmissionConflict) || errors.Is(err, persistence.ErrReleaseCoverConflict) {
			return ReleaseCoverBatch{}, fmt.Errorf("admit release cover publication: %w", ErrReleaseCoverConflict)
		}
		if errors.Is(err, persistence.ErrLibraryReleaseNotFound) || errors.Is(err, persistence.ErrPublicationPositionNotFound) {
			return ReleaseCoverBatch{}, fmt.Errorf("admit release cover publication: %w", ErrReleaseCoverNotFound)
		}
		return ReleaseCoverBatch{}, fmt.Errorf("admit release cover publication: %w", err)
	}
	result := ReleaseCoverBatch{ReleaseID: batch.ReleaseID, Revision: batch.Revision, SelectedGeneration: batch.SelectedGeneration, Operations: make([]ReleaseCoverPositionOperation, 0, len(batch.Operations))}
	for _, operation := range batch.Operations {
		result.Operations = append(result.Operations, ReleaseCoverPositionOperation{PositionID: operation.PositionID, OperationID: operation.OperationID, CandidateGeneration: operation.CandidateGeneration})
	}
	return result, nil
}
