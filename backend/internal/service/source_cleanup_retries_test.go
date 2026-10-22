package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type cleanupRetryServiceFake struct {
	state         persistence.PublicationSourceCleanupExplicitRetryState
	readErr       error
	admitResult   persistence.PublicationSourceCleanupExplicitRetryResult
	admitErr      error
	replaceResult persistence.PublicationSourceCleanupExplicitRetryResult
	replaceErr    error
	reads         int
	admits        int
	replaces      int
	gotOriginal   uuid.UUID
	gotRecovery   uuid.UUID
	gotRiver      persistence.RiverInserter
}

func (fake *cleanupRetryServiceFake) ReadPublicationSourceCleanupExplicitRetryState(_ context.Context, id uuid.UUID) (persistence.PublicationSourceCleanupExplicitRetryState, error) {
	fake.reads++
	fake.gotOriginal = id
	return fake.state, fake.readErr
}
func (fake *cleanupRetryServiceFake) AdmitPublicationSourceCleanupExplicitRetry(_ context.Context, id uuid.UUID, river persistence.RiverInserter) (persistence.PublicationSourceCleanupExplicitRetryResult, error) {
	fake.admits++
	fake.gotOriginal, fake.gotRiver = id, river
	return fake.admitResult, fake.admitErr
}
func (fake *cleanupRetryServiceFake) ReplacePublicationSourceCleanupExplicitRetry(_ context.Context, id, recovery uuid.UUID, river persistence.RiverInserter) (persistence.PublicationSourceCleanupExplicitRetryResult, error) {
	fake.replaces++
	fake.gotOriginal, fake.gotRecovery, fake.gotRiver = id, recovery, river
	return fake.replaceResult, fake.replaceErr
}

type cleanupRetryRiverFake struct{}

func (cleanupRetryRiverFake) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("unexpected insert")
}

func validCleanupRetryState(id uuid.UUID) persistence.PublicationSourceCleanupExplicitRetryState {
	return persistence.PublicationSourceCleanupExplicitRetryState{OperationID: id, Kind: "cleanup_publication_sources_v1", State: "failed", Stage: "cleanup_recovery_required", SnapshotValid: true}
}

func validReturnedCleanupRetryState(id, recovery uuid.UUID) persistence.PublicationSourceCleanupExplicitRetryState {
	jobID := int64(17)
	state := validCleanupRetryState(id)
	state.CurrentGrant, state.RecoveryOperationID = true, recovery
	state.RecoveryKind, state.RecoveryState, state.RecoveryStage = "retry_publication_sources_v1", "failed", "cleanup_retry_recovery_required"
	state.RecoverySnapshotValid, state.RecoveryGrantValid = true, true
	state.RecoveryClaimState, state.RecoveryClaimValid = "recovery_required", true
	state.RecoveryFrontier, state.RecoveryFrontierValid = true, true
	revision := uuid.New()
	state.RecoveryGrantFrontierRevision = &revision
	state.RecoveryAttempt, state.RecoveryRiverJobID = 1, &jobID
	return state
}

func TestSourceCleanupRetriesChoosesInitialAdmissionWithoutCurrentGrant(t *testing.T) {
	id := uuid.New()
	fake, inserter := &cleanupRetryServiceFake{state: validCleanupRetryState(id), admitResult: persistence.PublicationSourceCleanupExplicitRetryResult{OperationID: uuid.New()}}, cleanupRetryRiverFake{}
	result, err := NewSourceCleanupRetries(fake, inserter).Retry(context.Background(), id)
	if err != nil || result.OperationID != fake.admitResult.OperationID || fake.reads != 1 || fake.admits != 1 || fake.replaces != 0 || fake.gotOriginal != id || fake.gotRiver != inserter {
		t.Fatalf("initial retry result=%+v err=%v fake=%+v", result, err, fake)
	}
}

func TestSourceCleanupRetriesReplacesOnlyObservedFailedRecovery(t *testing.T) {
	id, recovery := uuid.New(), uuid.New()
	state := validReturnedCleanupRetryState(id, recovery)
	fake, inserter := &cleanupRetryServiceFake{state: state, replaceResult: persistence.PublicationSourceCleanupExplicitRetryResult{OperationID: uuid.New()}}, cleanupRetryRiverFake{}
	result, err := NewSourceCleanupRetries(fake, inserter).Retry(context.Background(), id)
	if err != nil || result.OperationID != fake.replaceResult.OperationID || fake.admits != 0 || fake.replaces != 1 || fake.gotOriginal != id || fake.gotRecovery != recovery || fake.gotRiver != inserter {
		t.Fatalf("replacement result=%+v err=%v fake=%+v", result, err, fake)
	}
}

func TestSourceCleanupRetriesPropagatesReplacementErrorWithoutInitialFallback(t *testing.T) {
	id, recovery, failure := uuid.New(), uuid.New(), errors.New("replacement unavailable")
	state := validReturnedCleanupRetryState(id, recovery)
	fake := &cleanupRetryServiceFake{state: state, replaceErr: failure, admitResult: persistence.PublicationSourceCleanupExplicitRetryResult{OperationID: uuid.New()}}
	_, err := NewSourceCleanupRetries(fake, cleanupRetryRiverFake{}).Retry(context.Background(), id)
	if !errors.Is(err, failure) || fake.replaces != 1 || fake.admits != 0 || fake.gotOriginal != id || fake.gotRecovery != recovery {
		t.Fatalf("replacement error=%v fake=%+v", err, fake)
	}
}

func TestSourceCleanupRetriesPreservesReplacementBlockersWithoutInitialFallback(t *testing.T) {
	id, recovery := uuid.New(), uuid.New()
	state := validReturnedCleanupRetryState(id, recovery)
	blockers := []string{"current cleanup evidence changed"}
	fake := &cleanupRetryServiceFake{state: state, replaceResult: persistence.PublicationSourceCleanupExplicitRetryResult{Blockers: blockers}}
	result, err := NewSourceCleanupRetries(fake, cleanupRetryRiverFake{}).Retry(context.Background(), id)
	if err != nil || fake.replaces != 1 || fake.admits != 0 || len(result.Blockers) != 1 || result.Blockers[0] != blockers[0] {
		t.Fatalf("replacement blocker result=%+v err=%v fake=%+v", result, err, fake)
	}
}

func TestSourceCleanupRetriesBlocksInvalidOrActiveState(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name string
		edit func(*persistence.PublicationSourceCleanupExplicitRetryState)
	}{
		{"wrong operation kind", func(s *persistence.PublicationSourceCleanupExplicitRetryState) { s.Kind = "other" }},
		{"malformed original", func(s *persistence.PublicationSourceCleanupExplicitRetryState) { s.SnapshotValid = false }},
		{"active recovery", func(s *persistence.PublicationSourceCleanupExplicitRetryState) {
			s.CurrentGrant = true
			s.RecoveryOperationID = uuid.New()
			s.RecoveryKind = "retry_publication_sources_v1"
			s.RecoveryState = "running"
			s.RecoverySnapshotValid = true
			s.RecoveryGrantValid = true
			s.RecoveryFrontier = true
		}},
		{"frontier without grant", func(s *persistence.PublicationSourceCleanupExplicitRetryState) { s.RecoveryFrontier = true }},
		{"invalid returned claim", func(s *persistence.PublicationSourceCleanupExplicitRetryState) {
			s.CurrentGrant, s.RecoveryOperationID = true, uuid.New()
			s.RecoveryKind, s.RecoveryState, s.RecoveryStage = "retry_publication_sources_v1", "failed", "cleanup_retry_recovery_required"
			s.RecoveryGrantValid, s.RecoverySnapshotValid, s.RecoveryFrontier = true, true, true
			s.RecoveryClaimState, s.RecoveryClaimValid, s.RecoveryFrontierValid = "recovery_required", false, true
		}},
		{"invalid returned frontier proof", func(s *persistence.PublicationSourceCleanupExplicitRetryState) {
			s.CurrentGrant, s.RecoveryOperationID = true, uuid.New()
			s.RecoveryKind, s.RecoveryState, s.RecoveryStage = "retry_publication_sources_v1", "failed", "cleanup_retry_recovery_required"
			s.RecoveryGrantValid, s.RecoverySnapshotValid, s.RecoveryFrontier = true, true, true
			s.RecoveryClaimState, s.RecoveryClaimValid, s.RecoveryFrontierValid = "recovery_required", true, false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := validCleanupRetryState(id)
			tc.edit(&state)
			fake := &cleanupRetryServiceFake{state: state}
			result, err := NewSourceCleanupRetries(fake, cleanupRetryRiverFake{}).Retry(context.Background(), id)
			if err != nil || len(result.Blockers) == 0 || fake.admits != 0 || fake.replaces != 0 {
				t.Fatalf("blocked retry result=%+v err=%v fake=%+v", result, err, fake)
			}
		})
	}
}

func TestSourceCleanupRetriesPropagatesSelectorAndAdmissionErrors(t *testing.T) {
	id := uuid.New()
	failure := errors.New("database unavailable")
	fake := &cleanupRetryServiceFake{state: validCleanupRetryState(id), admitErr: failure}
	_, err := NewSourceCleanupRetries(fake, cleanupRetryRiverFake{}).Retry(context.Background(), id)
	if !errors.Is(err, failure) {
		t.Fatalf("admission error = %v", err)
	}
	fake = &cleanupRetryServiceFake{readErr: failure}
	_, err = NewSourceCleanupRetries(fake, cleanupRetryRiverFake{}).Retry(context.Background(), id)
	if !errors.Is(err, failure) || fake.admits != 0 {
		t.Fatalf("selector error = %v fake=%+v", err, fake)
	}
}
