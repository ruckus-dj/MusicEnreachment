package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type artifactCleanupDrainFixture struct {
	candidates []persistence.SourceAnalysisArtifactCleanupCandidate
	operations []persistence.Operation
	admissions [][]uuid.UUID
	listLimit  int
	listErr    error
	admitErr   error
}

func (fixture *artifactCleanupDrainFixture) ListSourceAnalysisArtifactCleanupCandidates(_ context.Context, limit int) ([]persistence.SourceAnalysisArtifactCleanupCandidate, error) {
	fixture.listLimit = limit
	return append([]persistence.SourceAnalysisArtifactCleanupCandidate(nil), fixture.candidates...), nil
}

func (fixture *artifactCleanupDrainFixture) AdmitSourceAnalysisArtifactCleanupWithArgsFactory(_ context.Context, artifactIDs []uuid.UUID, _ persistence.RiverInserter, _ func(uuid.UUID) river.JobArgs, _ *river.InsertOpts) (*persistence.Operation, error) {
	fixture.admissions = append(fixture.admissions, append([]uuid.UUID(nil), artifactIDs...))
	if fixture.admitErr != nil {
		return nil, fixture.admitErr
	}
	snapshot, err := json.Marshal(persistence.SourceAnalysisArtifactCleanupSnapshot{ArtifactIDs: artifactIDs})
	if err != nil {
		return nil, err
	}
	operation := persistence.Operation{
		ID: uuid.New(), Kind: persistence.SourceAnalysisArtifactCleanupOperationKind,
		State: "queued", InputSnapshot: snapshot,
	}
	fixture.operations = append(fixture.operations, operation)
	return &operation, nil
}

func (fixture *artifactCleanupDrainFixture) ListSourceAnalysisArtifactCleanupItems(context.Context, uuid.UUID) ([]persistence.SourceAnalysisArtifactCleanupItem, error) {
	return nil, nil
}

func (fixture *artifactCleanupDrainFixture) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	if fixture.listErr != nil {
		return nil, fixture.listErr
	}
	return append([]persistence.Operation(nil), fixture.operations...), nil
}

type artifactCleanupDrainRiver struct{}

func (artifactCleanupDrainRiver) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("unexpected cleanup River insert")
}

func newArtifactCleanupDrainFixture(candidates []persistence.SourceAnalysisArtifactCleanupCandidate, operations []persistence.Operation) (*artifactCleanupDrainFixture, *SourceAnalysisArtifactCleanupDrain) {
	fixture := &artifactCleanupDrainFixture{candidates: candidates, operations: operations}
	cleanup := NewSourceAnalysisArtifactCleanup(fixture, nil, artifactCleanupDrainRiver{})
	return fixture, NewSourceAnalysisArtifactCleanupDrain(cleanup, fixture)
}

func eligibleCleanupCandidate(id uuid.UUID) persistence.SourceAnalysisArtifactCleanupCandidate {
	return persistence.SourceAnalysisArtifactCleanupCandidate{ArtifactID: id, SourceRelativePath: "retired-" + id.String(), State: persistence.SourceAnalysisArtifactCleanupEligible}
}

func queuedCleanupOperation(t *testing.T, artifactIDs ...uuid.UUID) persistence.Operation {
	t.Helper()
	snapshot, err := json.Marshal(persistence.SourceAnalysisArtifactCleanupSnapshot{ArtifactIDs: artifactIDs})
	if err != nil {
		t.Fatal(err)
	}
	return persistence.Operation{ID: uuid.New(), Kind: persistence.SourceAnalysisArtifactCleanupOperationKind, State: "queued", InputSnapshot: snapshot}
}

func TestSourceAnalysisArtifactCleanupDrainSkipsReservedAndFailedArtifacts(t *testing.T) {
	first, second, fourth := uuid.New(), uuid.New(), uuid.New()
	failed := uuid.New()
	reservedQueued, reservedRunning := uuid.New(), uuid.New()
	fixture, drain := newArtifactCleanupDrainFixture(
		[]persistence.SourceAnalysisArtifactCleanupCandidate{
			eligibleCleanupCandidate(first),
			{ArtifactID: failed, State: persistence.SourceAnalysisArtifactCleanupFailed},
			eligibleCleanupCandidate(reservedQueued),
			eligibleCleanupCandidate(reservedRunning),
			eligibleCleanupCandidate(second),
		},
		[]persistence.Operation{
			queuedCleanupOperation(t, reservedQueued),
			{ID: uuid.New(), Kind: persistence.SourceAnalysisArtifactCleanupOperationKind, State: "running", InputSnapshot: mustSnapshot(t, reservedRunning)},
			// A foreign operation kind can share the snapshot key; only cleanup
			// operations reserve cleanup work.
			{ID: uuid.New(), Kind: "analyze_source", State: "running", InputSnapshot: mustSnapshot(t, second)},
		},
	)
	admitted, err := drain.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if admitted != 2 || len(fixture.admissions) != 1 || len(fixture.admissions[0]) != 2 || fixture.admissions[0][0] != first || fixture.admissions[0][1] != second {
		t.Fatalf("first drain admitted %v (%d artifacts), want %s and %s", fixture.admissions, admitted, first, second)
	}
	if fixture.listLimit != sourceAnalysisArtifactCleanupDrainScan {
		t.Fatalf("candidate limit=%d, want %d", fixture.listLimit, sourceAnalysisArtifactCleanupDrainScan)
	}

	// A second pass must not select artifacts already reserved by the queued
	// operation admitted above, while still selecting newly eligible work.
	fixture.candidates = append(fixture.candidates, eligibleCleanupCandidate(fourth))
	admitted, err = drain.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if admitted != 1 || len(fixture.admissions) != 2 || len(fixture.admissions[1]) != 1 || fixture.admissions[1][0] != fourth {
		t.Fatalf("second drain admitted %v (%d artifacts), want only %s", fixture.admissions, admitted, fourth)
	}
}

func mustSnapshot(t *testing.T, artifactID uuid.UUID) json.RawMessage {
	t.Helper()
	snapshot, err := json.Marshal(persistence.SourceAnalysisArtifactCleanupSnapshot{ArtifactIDs: []uuid.UUID{artifactID}})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSourceAnalysisArtifactCleanupDrainIsBoundedAndFailsClosed(t *testing.T) {
	t.Run("bounded batch", func(t *testing.T) {
		candidates := make([]persistence.SourceAnalysisArtifactCleanupCandidate, 0, SourceAnalysisArtifactCleanupDrainBatch+1)
		for index := 0; index < SourceAnalysisArtifactCleanupDrainBatch+1; index++ {
			candidates = append(candidates, eligibleCleanupCandidate(uuid.New()))
		}
		fixture, drain := newArtifactCleanupDrainFixture(candidates, nil)
		admitted, err := drain.Drain(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if admitted != SourceAnalysisArtifactCleanupDrainBatch || len(fixture.admissions) != 1 || len(fixture.admissions[0]) != SourceAnalysisArtifactCleanupDrainBatch {
			t.Fatalf("drain admitted %d artifacts over %d admissions, want one batch of %d", admitted, len(fixture.admissions), SourceAnalysisArtifactCleanupDrainBatch)
		}
	})

	t.Run("unreadable reservation fails closed", func(t *testing.T) {
		id := uuid.New()
		fixture, drain := newArtifactCleanupDrainFixture(
			[]persistence.SourceAnalysisArtifactCleanupCandidate{eligibleCleanupCandidate(id)},
			[]persistence.Operation{{
				ID: uuid.New(), Kind: persistence.SourceAnalysisArtifactCleanupOperationKind,
				State: "queued", InputSnapshot: json.RawMessage(`{`),
			}},
		)
		if _, err := drain.Drain(context.Background()); err == nil {
			t.Fatal("drain accepted an unreadable cleanup snapshot")
		}
		if len(fixture.admissions) != 0 {
			t.Fatalf("drain admitted %v despite an unreadable reservation", fixture.admissions)
		}
	})

	t.Run("no eligible artifacts", func(t *testing.T) {
		fixture, drain := newArtifactCleanupDrainFixture(
			[]persistence.SourceAnalysisArtifactCleanupCandidate{{ArtifactID: uuid.New(), State: persistence.SourceAnalysisArtifactCleanupFailed}},
			nil,
		)
		admitted, err := drain.Drain(context.Background())
		if err != nil || admitted != 0 || len(fixture.admissions) != 0 {
			t.Fatalf("drain of failed-only candidates = %d, %v (%d admissions); want no admission", admitted, err, len(fixture.admissions))
		}
	})
}
