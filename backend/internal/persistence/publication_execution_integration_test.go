//go:build integration

package persistence

import (
	"context"
	"encoding/json"
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
	frozenInputs, err := json.Marshal(publicationInputsFixture(t, "audio"))
	if err != nil {
		t.Fatal(err)
	}
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
		VALUES (?,?,?,NULL,?,'audio','queued',?::jsonb,'{}','[]')`, operationID, positionID, epoch, candidateID, string(frozenInputs)); err != nil {
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
	if err != nil || attempt == nil || len(attempt.FrozenInputs) == 0 || string(attempt.FrozenInputs) == "{}" {
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

func TestPublicationDeliveryClaimBlocksStaleFencesButReplacesSameAttemptIdentityPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	if got, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("initial claim = %q, %v", got, err)
	}
	stale := []struct {
		name   string
		change func(*PublicationDeliveryGuard)
	}{
		{"job attempt", func(g *PublicationDeliveryGuard) { g.RiverAttempt++ }},
		{"application attempt", func(g *PublicationDeliveryGuard) { g.ApplicationAttempt++ }},
		{"epoch", func(g *PublicationDeliveryGuard) { g.Epoch = uuid.New() }},
		{"candidate", func(g *PublicationDeliveryGuard) { g.CandidateGeneration = uuid.New() }},
		{"position", func(g *PublicationDeliveryGuard) { g.ExpectedPositionID = uuid.New() }},
	}
	for _, variant := range stale {
		t.Run(variant.name, func(t *testing.T) {
			guard := f.guard
			variant.change(&guard)
			outcome, err := repo.ClaimPublicationDelivery(ctx, guard)
			if err != nil || outcome != PublicationDeliveryBlocked {
				t.Fatalf("stale claim = %q, %v", outcome, err)
			}
		})
	}
	// A fresh delivery identity at the same River attempt is a legitimate
	// redelivery and replaces the executing claim. In-process coordination, not
	// the database claim, decides which delivery may actually mutate.
	fresh := f.guard
	fresh.ClaimUUID, fresh.BootUUID, fresh.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	if outcome, err := repo.ClaimPublicationDelivery(ctx, fresh); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("same-attempt replacement = %q, %v", outcome, err)
	}
	var claim PublicationExecutionClaim
	if err := f.db.NewSelect().Model(&claim).Where("operation_id=?", f.guard.OperationID).Scan(ctx, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.ClaimUUID != fresh.ClaimUUID || claim.BootUUID != fresh.BootUUID || claim.DeliveryToken != fresh.DeliveryToken || claim.State != "executing" {
		t.Fatalf("replacement claim = %+v", claim)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, f.guard); err != nil || ok {
		t.Fatalf("stale guard delivery check = %t, %v", ok, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, fresh); err != nil || !ok {
		t.Fatalf("replacement guard delivery check = %t, %v", ok, err)
	}
}

func TestPublicationDeliveryReclaimsEqualRiverAttemptWithRunningJobPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("initial claim = %q, %v", outcome, err)
	}
	redelivery := f.guard
	redelivery.ClaimUUID, redelivery.BootUUID, redelivery.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	// The equal attempt is only valid while the River row itself is running.
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='available' WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, redelivery); err != nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("claim with returned River job = %q, %v", outcome, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='running' WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, redelivery); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("same-attempt reclaim = %q, %v", outcome, err)
	}
	var claim PublicationExecutionClaim
	if err := f.db.NewSelect().Model(&claim).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if claim.ClaimUUID != redelivery.ClaimUUID || claim.BootUUID != redelivery.BootUUID || claim.DeliveryToken != redelivery.DeliveryToken ||
		claim.RiverAttempt != redelivery.RiverAttempt || claim.State != "executing" {
		t.Fatalf("reclaimed claim = %+v", claim)
	}
	var operationState, stage, phase string
	if err := f.db.NewRaw(`SELECT state, stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &operationState, &stage); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &phase); err != nil {
		t.Fatal(err)
	}
	if operationState != "running" || stage != "preparing" || phase != "preparing" {
		t.Fatalf("reclaimed state = operation:%s/%s attempt:%s", operationState, stage, phase)
	}
	// The prior delivery identity no longer authorizes live mutations.
	if ok, err := repo.CheckPublicationDelivery(ctx, f.guard); err != nil || ok {
		t.Fatalf("superseded delivery check = %t, %v", ok, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, redelivery); err != nil || !ok {
		t.Fatalf("reclaimed delivery check = %t, %v", ok, err)
	}
}

func TestPublicationDeliveryRetryReplacesExecutingClaimAndRestartsPreparationPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("initial claim = %q, %v", outcome, err)
	}
	var previousClaimState string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &previousClaimState); err != nil {
		t.Fatal(err)
	}
	if previousClaimState != "executing" {
		t.Fatalf("prior claim state = %q, want executing", previousClaimState)
	}
	var frozenInputs, manifest, journal string
	if err := f.db.NewRaw(`SELECT frozen_inputs, namespace_manifest, mutation_journal FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).
		Scan(ctx, &frozenInputs, &manifest, &journal); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET stage='validated' WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET phase='validated' WHERE operation_id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET attempt=2, state='running' WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	retry := f.guard
	retry.RiverAttempt = 2
	retry.ClaimUUID, retry.BootUUID, retry.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	fabricated := retry
	fabricated.RiverAttempt = 3
	fabricated.ClaimUUID, fabricated.BootUUID, fabricated.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	if outcome, err := repo.ClaimPublicationDelivery(ctx, fabricated); err != nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("fabricated future River attempt = %q, %v", outcome, err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, retry); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("retry claim = %q, %v", outcome, err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryBlocked {
		t.Fatalf("old River delivery after replacement = %q, %v", outcome, err)
	}
	var operationState, stage, phase, claimState string
	var riverAttempt int
	if err := f.db.NewRaw(`SELECT state, stage FROM operation WHERE id=?`, retry.OperationID).Scan(ctx, &operationState, &stage); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, retry.OperationID).Scan(ctx, &phase); err != nil {
		t.Fatal(err)
	}
	var claim PublicationExecutionClaim
	if err := f.db.NewSelect().Model(&claim).Where("operation_id=?", retry.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT attempt FROM river_job WHERE id=?`, retry.RiverJobID).Scan(ctx, &riverAttempt); err != nil {
		t.Fatal(err)
	}
	claimState = claim.State
	if operationState != "running" || stage != "preparing" || phase != "preparing" || claimState != "executing" ||
		claim.RiverAttempt != retry.RiverAttempt || claim.ClaimUUID != retry.ClaimUUID || riverAttempt != retry.RiverAttempt {
		t.Fatalf("retry state = operation:%s/%s attempt:%s claim:%+v river-attempt:%d", operationState, stage, phase, claim, riverAttempt)
	}
	var afterInputs, afterManifest, afterJournal string
	if err := f.db.NewRaw(`SELECT frozen_inputs, namespace_manifest, mutation_journal FROM track_publication_attempt WHERE operation_id=?`, retry.OperationID).
		Scan(ctx, &afterInputs, &afterManifest, &afterJournal); err != nil {
		t.Fatal(err)
	}
	if string(afterInputs) != string(frozenInputs) || string(afterManifest) != string(manifest) || string(afterJournal) != string(journal) {
		t.Fatal("retry changed frozen inputs, namespace manifest, or mutation journal")
	}
}

func TestPublicationDeliveryConcurrentEqualAttemptClaimsSerializeToOneLiveGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
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
			results[i], errs[i] = repo.ClaimPublicationDelivery(ctx, guards[i])
		}(i)
	}
	close(start)
	wg.Wait()
	// The two fresh identities share an equal River attempt. The locked attempt
	// and operation rows serialize them, and equal-attempt replacement lets both
	// acquire; exactly one durable claim row keeps the identity that committed
	// last. In-process coordination, not this claim, prevents two live mutators.
	var claims []PublicationExecutionClaim
	if err := f.db.NewSelect().Model(&claims).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want exactly one row", len(claims))
	}
	live := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent claim %d: %v", i, errs[i])
		}
		if results[i] != PublicationDeliveryAcquired {
			t.Fatalf("concurrent claim %d outcome = %q, want acquired", i, results[i])
		}
		mutable, err := repo.CheckPublicationDelivery(ctx, guards[i])
		if err != nil {
			t.Fatalf("delivery check for claim %d: %v", i, err)
		}
		if mutable {
			live++
			if claims[0].ClaimUUID != guards[i].ClaimUUID || claims[0].BootUUID != guards[i].BootUUID || claims[0].DeliveryToken != guards[i].DeliveryToken {
				t.Fatalf("mutable guard %d does not match the durable claim %+v", i, claims[0])
			}
		}
	}
	if live != 1 {
		t.Fatalf("live guards = %d, want exactly one of %+v", live, results)
	}
}
