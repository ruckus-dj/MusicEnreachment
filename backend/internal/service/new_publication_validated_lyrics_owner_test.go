package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// validatedLyricsOwnerRepository observes the exact private work capability at
// each durable acknowledgement, while forwarding all unrelated repository
// behavior to the existing fixture repository.
type validatedLyricsOwnerRepository struct {
	*optionalLyricsV3Repository
	stage              *preparedPublicationStage
	scope              *PublicationExecutionScope
	reservation        *publicationSyncReservation
	work               *privateManualLyricsWork
	phase              string
	stageCalls         int
	failAt             int
	failMode           string
	mutate             bool
	observations       []persistence.PublicationStageObservation
	preparationInputs  int
	outcomeExpectation persistence.PublicationStageObservation
	outcomePreparation persistence.PublicationPreparationRecord
}

type validatedLyricsScopeRepository struct {
	*scopeRepositoryFake
	*validatedLyricsOwnerRepository
}

func (repository *validatedLyricsOwnerRepository) ownsWork() bool {
	if repository.work == nil {
		repository.work = repository.scope.privateManualLyricsWork
	}
	return repository.reservation.borrowed && repository.scope.privateManualLyricsWork == repository.work &&
		repository.scope.validPrivateManualLyricsWork(repository.work, repository.stage)
}

func (repository *validatedLyricsOwnerRepository) assertLaunchesFenced() error {
	if _, err := repository.scope.Run(context.Background(), "ffmpeg"); err == nil {
		return errors.New("generic Run launched during private manual lyrics work")
	}
	if _, err := repository.scope.RunFile(context.Background(), "ffmpeg", nil, nil); err == nil {
		return errors.New("RunFile launched during private manual lyrics work")
	}
	if err := repository.scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		return errors.New("Finalize succeeded during private manual lyrics work")
	}
	return nil
}

func (repository *validatedLyricsOwnerRepository) PublicationManualLyricsPreparationInputs(ctx context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	repository.preparationInputs++
	if repository.phase == "staged" {
		return persistence.PublicationInputs{}, false, errors.New("preparation inputs read after staged acknowledgement")
	}
	return repository.optionalLyricsV3Repository.PublicationManualLyricsPreparationInputs(ctx, guard)
}

func (repository *validatedLyricsOwnerRepository) PublicationManualLyricsCurrentInputs(_ context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	if repository.phase != "validated" && repository.phase != "exposing" {
		return persistence.PublicationInputs{}, false, errors.New("current inputs read before validated acknowledgement")
	}
	if guard != repository.stage.guard {
		return persistence.PublicationInputs{}, false, nil
	}
	return repository.stage.privateInputs.Clone(), true, nil
}

func (repository *validatedLyricsOwnerRepository) GuardedRecordPublicationOptionalLyricsOutcome(ctx context.Context, guard persistence.PublicationDeliveryGuard, evidence persistence.PublicationOptionalLyricsOutcomeEvidence) (persistence.PublicationOptionalLyricsOutcome, error) {
	if !repository.ownsWork() {
		return persistence.PublicationOptionalLyricsStale, errors.New("outcome acknowledgement did not retain private work")
	}
	if err := repository.assertLaunchesFenced(); err != nil {
		return persistence.PublicationOptionalLyricsStale, err
	}
	repository.outcomeExpectation = evidence.FinalObservation.Clone()
	repository.outcomePreparation = cloneManualLyricsPreparationRecord(evidence.Preparation)
	if repository.mutate {
		if evidence.FinalObservation.Ownership.ExpectedSize != nil {
			*evidence.FinalObservation.Ownership.ExpectedSize++
		}
		evidence.FinalObservation.ActualMetadata[0] = ' '
		if evidence.Preparation.CopiedInput != nil {
			evidence.Preparation.CopiedInput.SizeBytes = nil
		}
		if evidence.Preparation.InputSize != nil {
			*evidence.Preparation.InputSize++
		}
	}
	return repository.optionalLyricsV3Repository.GuardedRecordPublicationOptionalLyricsOutcome(ctx, guard, evidence)
}

func (repository *validatedLyricsOwnerRepository) GuardedAdvanceManualLyricsStage(_ context.Context, _ persistence.PublicationDeliveryGuard, from string, observation persistence.PublicationStageObservation) (persistence.ManualLyricsStageOutcome, error) {
	repository.stageCalls++
	if !repository.ownsWork() {
		return persistence.ManualLyricsStageStale, errors.New("stage acknowledgement did not retain private work")
	}
	if err := repository.assertLaunchesFenced(); err != nil {
		return persistence.ManualLyricsStageStale, err
	}
	if from != repository.phase {
		return persistence.ManualLyricsStageStale, errors.New("unexpected stage acknowledgement phase")
	}
	repository.observations = append(repository.observations, observation.Clone())
	if repository.failAt == repository.stageCalls {
		switch repository.failMode {
		case "error":
			return persistence.ManualLyricsStageStale, errors.New("injected stage acknowledgement error")
		case "stale":
			return persistence.ManualLyricsStageStale, nil
		default:
			return persistence.ManualLyricsStageOutcome("unknown"), nil
		}
	}
	if from == "preparing" {
		repository.phase = "staged"
	} else {
		repository.phase = "validated"
	}
	return persistence.ManualLyricsStageAdvanced, nil
}

func TestValidatedManualLyricsStageAcknowledgementsRetainOneOwner(t *testing.T) {
	t.Run("same owner through outcome staged validated and isolated evidence", func(t *testing.T) {
		stage, input, _, scope, reservation, proof := manualLyricsObservationFixture(t)
		// The private stage contract is frozen by preparation. Keep the in-memory
		// fixture aligned with this test's repository input mutations.
		stage.privateInputs = input.Clone()
		base := stage.repository.(*optionalLyricsV3Repository)
		base.outcome = persistence.PublicationOptionalLyricsRecorded
		owner := &validatedLyricsOwnerRepository{optionalLyricsV3Repository: base, stage: stage, scope: scope, reservation: reservation, phase: "preparing", mutate: true}
		stage.repository = owner
		scope.repository = &validatedLyricsScopeRepository{scopeRepositoryFake: scope.repository.(*manualLyricsObservationRepository).scopeRepositoryFake, validatedLyricsOwnerRepository: owner}
		beforePreparation := cloneManualLyricsPreparationRecord(stage.preparationRecord)
		result, err := validatePreparedManualLyricsFLAC(context.Background(), scope, reservation, proof, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Lyrics.State != persistence.PublicationAssetEmbedded || owner.stageCalls != 2 || len(owner.observations) != 2 {
			t.Fatalf("result=%#v stage calls=%d observations=%d", result, owner.stageCalls, len(owner.observations))
		}
		if !reflect.DeepEqual(stage.preparationRecord, beforePreparation) {
			t.Fatal("repository mutation changed the private preparation record")
		}
		if len(base.records) != 1 || !reflect.DeepEqual(owner.outcomePreparation, beforePreparation) {
			t.Fatal("repository mutation changed the separately captured preparation evidence")
		}
		if !reflect.DeepEqual(owner.outcomeExpectation, owner.observations[0]) {
			t.Fatal("repository mutation changed the separately captured outcome expectation")
		}
		if !reflect.DeepEqual(owner.observations[0], owner.observations[1]) {
			t.Fatal("staged and validated acknowledgements did not receive the same detached observation")
		}
		if owner.preparationInputs == 0 {
			t.Fatal("preparation input reader was not exercised")
		}
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if scope.privateManualLyricsWork != nil || reservation.borrowed || reservation.active == false || scope.syncWork != 1 {
			t.Fatalf("successful work ownership state: private=%p borrowed=%v active=%v sync=%d", scope.privateManualLyricsWork, reservation.borrowed, reservation.active, scope.syncWork)
		}
	})
}

func TestValidatedManualLyricsStageFailureRetainsUnknownOwner(t *testing.T) {
	for _, test := range []struct {
		name      string
		at        int
		mode      string
		wantCalls int
	}{
		{name: "stage one error", at: 1, mode: "error", wantCalls: 1},
		{name: "stage one stale", at: 1, mode: "stale", wantCalls: 1},
		{name: "stage one unknown", at: 1, mode: "unknown", wantCalls: 1},
		{name: "stage two error", at: 2, mode: "error", wantCalls: 2},
		{name: "stage two stale", at: 2, mode: "stale", wantCalls: 2},
		{name: "stage two unknown", at: 2, mode: "unknown", wantCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			stage, input, _, scope, reservation, proof := manualLyricsObservationFixture(t)
			stage.privateInputs = input.Clone()
			base := stage.repository.(*optionalLyricsV3Repository)
			base.outcome = persistence.PublicationOptionalLyricsRecorded
			owner := &validatedLyricsOwnerRepository{optionalLyricsV3Repository: base, stage: stage, scope: scope, reservation: reservation, phase: "preparing", failAt: test.at, failMode: test.mode}
			stage.repository = owner
			scope.repository = &validatedLyricsScopeRepository{scopeRepositoryFake: scope.repository.(*manualLyricsObservationRepository).scopeRepositoryFake, validatedLyricsOwnerRepository: owner}
			if _, err := validatePreparedManualLyricsFLAC(context.Background(), scope, reservation, proof, input); err == nil {
				t.Fatal("expected stage acknowledgement failure")
			}
			if owner.stageCalls != test.wantCalls || !reservation.borrowed || scope.privateManualLyricsWork != owner.work {
				t.Fatalf("calls=%d borrowed=%v private=%p want=%p", owner.stageCalls, reservation.borrowed, scope.privateManualLyricsWork, owner.work)
			}
			scope.mu.Lock()
			unknown := scope.unknown
			scope.mu.Unlock()
			if !unknown {
				t.Fatal("failed stage acknowledgement did not quarantine scope")
			}
			if test.at == 1 && owner.phase != "preparing" {
				t.Fatalf("failed first stage advanced repository phase to %q", owner.phase)
			}
		})
	}
}
