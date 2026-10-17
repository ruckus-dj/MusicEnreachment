//go:build integration

package persistence

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type publicationExecutionFixture struct {
	db    *bun.DB
	guard PublicationDeliveryGuard
}

func newPublicationExecutionFixture(t *testing.T) publicationExecutionFixture {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	positionID, operationID, candidateID := uuid.New(), uuid.New(), uuid.New()
	releaseID, trackID := uuid.New(), uuid.New()
	var jobID int64
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'publication execution')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'publication execution')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ('publish_track_v1','{}','running',1) RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + epoch.String() + `","intent_kind":"audio"}`
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id) VALUES (?,'publish_track_v1','queued','queued',?::jsonb,1,?)`, operationID, snapshot, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO track_publication_attempt
		(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal)
		VALUES (?,?,?,NULL,?,'audio','queued','{}','{}','[]')`, operationID, positionID, epoch, candidateID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return publicationExecutionFixture{db: db, guard: PublicationDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(), Epoch: epoch,
		CandidateGeneration: candidateID, ExpectedPositionID: positionID,
	}}
}

func TestPublicationDeliveryClaimAcquiresAndPersistsTransitionPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard)
	if err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim outcome = %q, %v", outcome, err)
	}
	var operationState, phase, claimState string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &operationState); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &phase); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &claimState); err != nil {
		t.Fatal(err)
	}
	if operationState != "running" || phase != "preparing" || claimState != "executing" {
		t.Fatalf("transition = operation:%s attempt:%s claim:%s", operationState, phase, claimState)
	}
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || attempt == nil || string(attempt.FrozenInputs) != "{}" {
		t.Fatalf("read persisted attempt = %+v, %v", attempt, err)
	}
	if got, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || got != PublicationDeliveryAlreadyOwned {
		t.Fatalf("repeat claim = %q, %v", got, err)
	}
}

func TestPublicationDeliveryInvalidGuardDoesNotTransitionPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	f.guard.DeliveryToken = ""
	outcome, err := NewSetupManagerRepository(f.db).ClaimPublicationDelivery(context.Background(), f.guard)
	if err == nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("invalid guard = %q, %v", outcome, err)
	}
	var operationState, phase string
	if err := f.db.NewRaw(`SELECT state FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &operationState); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &phase); err != nil {
		t.Fatal(err)
	}
	if operationState != "queued" || phase != "queued" {
		t.Fatalf("invalid guard mutated rows: operation=%s attempt=%s", operationState, phase)
	}
}

func TestPublicationDeliveryClaimBlocksDifferentIdentityAndStaleFencesPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if got, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("initial claim = %q, %v", got, err)
	}
	variants := []struct {
		name   string
		change func(*PublicationDeliveryGuard)
	}{
		{"boot", func(g *PublicationDeliveryGuard) { g.BootUUID = uuid.New() }},
		{"token", func(g *PublicationDeliveryGuard) { g.DeliveryToken += "-other" }},
		{"claim", func(g *PublicationDeliveryGuard) { g.ClaimUUID = uuid.New() }},
		{"job attempt", func(g *PublicationDeliveryGuard) { g.RiverAttempt++ }},
		{"application attempt", func(g *PublicationDeliveryGuard) { g.ApplicationAttempt++ }},
		{"epoch", func(g *PublicationDeliveryGuard) { g.Epoch = uuid.New() }},
		{"candidate", func(g *PublicationDeliveryGuard) { g.CandidateGeneration = uuid.New() }},
		{"position", func(g *PublicationDeliveryGuard) { g.ExpectedPositionID = uuid.New() }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			guard := f.guard
			variant.change(&guard)
			outcome, err := repo.ClaimPublicationDelivery(context.Background(), guard)
			if err != nil || outcome != PublicationDeliveryBlocked {
				t.Fatalf("stale claim = %q, %v", outcome, err)
			}
		})
	}
}

func TestPublicationDeliveryQuiescenceDoesNotPermitTakeoverPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("initial claim = %q, %v", outcome, err)
	}
	if ok, err := repo.MarkPublicationQuiescent(context.Background(), f.guard); err != nil || !ok {
		t.Fatalf("mark quiescent = %t, %v", ok, err)
	}
	var state string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &state); err != nil || state != "quiescent" {
		t.Fatalf("claim state = %q, %v", state, err)
	}
	newDelivery := f.guard
	newDelivery.ClaimUUID = uuid.New()
	newDelivery.DeliveryToken = uuid.NewString()
	newDelivery.BootUUID = uuid.New()
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), newDelivery); err != nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("takeover = %q, %v", outcome, err)
	}
	if ok, err := repo.MarkPublicationQuiescent(context.Background(), newDelivery); err != nil || ok {
		t.Fatalf("different guard quiescence = %t, %v", ok, err)
	}
}

func TestPublicationDeliveryConcurrentClaimsHaveOneWinnerPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	guards := []PublicationDeliveryGuard{f.guard, f.guard}
	guards[1].ClaimUUID, guards[1].BootUUID, guards[1].DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	results := make([]PublicationDeliveryClaimOutcome, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range guards {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = repo.ClaimPublicationDelivery(context.Background(), guards[i])
		}(i)
	}
	close(start)
	wg.Wait()
	winners, blocked := 0, 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent claim %d: %v", i, errs[i])
		}
		switch results[i] {
		case PublicationDeliveryAcquired:
			winners++
		case PublicationDeliveryBlocked:
			blocked++
		default:
			t.Fatalf("unexpected concurrent claim %d outcome %q", i, results[i])
		}
	}
	if winners != 1 || blocked != 1 {
		t.Fatalf("concurrent results = %q/%q, want one acquired and one blocked", results[0], results[1])
	}
}
