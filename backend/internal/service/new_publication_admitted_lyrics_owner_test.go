package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type admittedLyricsOwnerRepository struct {
	*validatedLyricsOwnerRepository
	attempt      *persistence.TrackPublicationAttempt
	beginCalls   int
	failExposure bool
}

func (repository *admittedLyricsOwnerRepository) ReadPublicationAttempt(_ context.Context, operationID uuid.UUID) (*persistence.TrackPublicationAttempt, error) {
	if repository.attempt.OperationID != operationID {
		return nil, errors.New("unexpected publication attempt operation")
	}
	copy := *repository.attempt
	return &copy, nil
}

func (repository *admittedLyricsOwnerRepository) BeginManualLyricsExposure(_ context.Context, guard persistence.PublicationDeliveryGuard, intent persistence.PublicationExposureIntent, observation persistence.PublicationStageObservation) (persistence.PublicationExposureOutcome, error) {
	repository.beginCalls++
	if repository.scope.privateManualLyricsWork != repository.work || !repository.ownsWork() || repository.stageCalls != 2 || repository.phase != "validated" {
		return persistence.PublicationExposureStale, errors.New("exposure acknowledgement did not retain validated private work")
	}
	if guard != repository.scope.guard || intent.Version != persistence.PublicationExposureVersion ||
		intent.StagedPath != repository.stage.preparationRecord.OutputRel ||
		intent.TargetRelativePath != repository.stage.privateInputs.Rewrite.RelativePath ||
		intent.TargetCanonicalKey != intent.TargetRelativePath || observation.RelativePath != intent.StagedPath {
		return persistence.PublicationExposureStale, errors.New("exposure acknowledgement did not match the frozen publication target")
	}
	if repository.failExposure {
		return persistence.PublicationExposureStale, errors.New("injected exposure acknowledgement error")
	}
	return persistence.PublicationExposureRecorded, nil
}

func TestAdmittedManualLyricsExposureRetainsOneOwner(t *testing.T) {
	for _, test := range []struct {
		name         string
		failExposure bool
	}{
		{name: "recorded"},
		{name: "exposure error", failExposure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stage, input, _, scope, reservation, proof := manualLyricsObservationFixture(t)
			stage.privateInputs = input.Clone()
			base := stage.repository.(*optionalLyricsV3Repository)
			base.outcome = persistence.PublicationOptionalLyricsRecorded
			owner := &validatedLyricsOwnerRepository{optionalLyricsV3Repository: base, stage: stage, scope: scope, reservation: reservation, phase: "preparing"}
			frozenInputs, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			target := input.Rewrite.RelativePath
			namespace, err := json.Marshal(frozenPublicationNamespace{Version: 1, Nodes: []persistence.PublicationNamespaceNode{{CanonicalKey: target, RelativePath: target, NodeKind: "file"}}})
			if err != nil {
				t.Fatal(err)
			}
			admission := &admittedLyricsOwnerRepository{
				validatedLyricsOwnerRepository: owner,
				attempt:                        &persistence.TrackPublicationAttempt{OperationID: scope.guard.OperationID, Phase: "validated", FrozenInputs: frozenInputs, NamespaceManifest: namespace},
				failExposure:                   test.failExposure,
			}
			stage.repository = admission
			scope.repository = &validatedLyricsScopeRepository{
				scopeRepositoryFake:            scope.repository.(*manualLyricsObservationRepository).scopeRepositoryFake,
				validatedLyricsOwnerRepository: owner,
			}
			_, err = admitPreparedManualLyricsFLACExposure(context.Background(), scope, reservation, proof, input)
			if (err != nil) != test.failExposure {
				t.Fatalf("admitted exposure error=%v, want failure=%v", err, test.failExposure)
			}
			if admission.stageCalls != 2 || admission.phase != "validated" || admission.work == nil || admission.beginCalls != 1 {
				t.Fatalf("stage calls=%d phase=%q work=%p begin calls=%d", admission.stageCalls, admission.phase, admission.work, admission.beginCalls)
			}
			if test.failExposure {
				if scope.privateManualLyricsWork != admission.work || !reservation.borrowed {
					t.Fatalf("failed exposure released private=%p work=%p borrowed=%v", scope.privateManualLyricsWork, admission.work, reservation.borrowed)
				}
			} else if scope.privateManualLyricsWork != nil || reservation.borrowed {
				t.Fatalf("successful exposure retained private=%p borrowed=%v", scope.privateManualLyricsWork, reservation.borrowed)
			}
		})
	}
}
