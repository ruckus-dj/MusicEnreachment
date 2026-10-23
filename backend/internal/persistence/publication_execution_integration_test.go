//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type publicationExecutionFixture struct {
	db    *bun.DB
	guard PublicationDeliveryGuard
}

func newValidPublicationStageObservation() PublicationStageObservation {
	return PublicationStageObservation{
		Version: 1, RelativePath: "album/track.flac", PayloadSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ActualAudio:          json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac","supported_source_container":true}}`),
		ActualMetadata:       json.RawMessage(`{"version":1,"tags":{"title":["Track"],"comment":[]}}`),
		ActualAssets:         json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`),
		QualityPolicyVersion: publication.QualityPolicyVersion,
	}
}

// recordValidInitialLyricsOutcome establishes the durable preparation and
// outcome evidence required for first-publication stage transitions.
func recordValidInitialLyricsOutcome(t *testing.T, f publicationExecutionFixture, repo *SetupManagerRepository, observation PublicationStageObservation) {
	t.Helper()
	ctx := context.Background()
	input := publicationInputsFixture(t, "audio")
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "private-op"}
	preparation := PublicationPreparationRecord{
		Version: PublicationPreparationVersion, Kind: "publication_preparation", OperationID: f.guard.OperationID,
		ClaimUUID: f.guard.ClaimUUID, OwnerToken: uuid.New(), PrivateDirRel: "album",
		InputRel: "album/input.flac", OutputRel: observation.RelativePath,
		ParentIdentity: PublicationDirectoryIdentity{Platform: "darwin", Volume: "volume", FileID: "root"},
	}
	for _, step := range []string{"intent", "directory_created"} {
		record := preparation
		record.Step = step
		if step == "directory_created" {
			record.DirectoryIdentity = &directory
		}
		if outcome, err := repo.RecordPublicationPreparation(ctx, f.guard, record); err != nil || outcome != PublicationPreparationRecorded {
			t.Fatalf("preparation %s = %q, %v", step, outcome, err)
		}
	}
	size, mtime, copiedSize, copiedMTime := int64(10), int64(20), int64(10), int64(40)
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	preparation.Step, preparation.DirectoryIdentity, preparation.InputSize, preparation.InputMTime = "input_ready", &directory, &size, &mtime
	preparation.CopiedInput = &PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime, SHA256: digest}
	if outcome, err := repo.RecordPublicationPreparation(ctx, f.guard, preparation); err != nil || outcome != PublicationPreparationRecorded {
		t.Fatalf("preparation input_ready = %q, %v", outcome, err)
	}
	acquisition := PublicationInitialLyricsAcquisitionRecord{
		Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsAcquisitionKind,
		Guard: f.guard, Authority: *input.InitialLyrics, Source: "none",
	}
	if outcome, err := repo.GuardedRecordPublicationInitialLyricsAcquisition(ctx, f.guard, acquisition); err != nil || outcome != PublicationOptionalLyricsRecorded {
		t.Fatalf("initial lyrics acquisition = %q, %v", outcome, err)
	}
	inputIdentity := PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime, SHA256: digest}
	outputIdentity := PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: observation.PayloadSHA256}
	lyricsOutcome := PublicationInitialLyricsOutcomeRecord{
		Version: PublicationInitialLyricsVersion, Kind: PublicationInitialLyricsOutcomeKind, Guard: f.guard,
		Outcome: PublicationInitialLyricsNotRequested, Preparation: preparation, InputIdentity: inputIdentity,
		OutputIdentity: outputIdentity, FinalObservation: observation,
	}
	if outcome, err := repo.GuardedRecordPublicationInitialLyricsOutcome(ctx, f.guard, lyricsOutcome); err != nil || outcome != PublicationOptionalLyricsRecorded {
		t.Fatalf("initial lyrics outcome = %q, %v", outcome, err)
	}
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
	if ok, err := repo.ProgressPublicationDelivery(ctx, f.guard, 1, nil); err != nil || ok {
		t.Fatalf("stale guard progress = %t, %v", ok, err)
	}
	if ok, err := repo.ProgressPublicationDelivery(ctx, fresh, 1, nil); err != nil || !ok {
		t.Fatalf("replacement guard progress = %t, %v", ok, err)
	}
}

func TestPublicationDeliveryQuiescenceRetainsClaimUntilRedeliveryPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("initial claim = %q, %v", outcome, err)
	}
	if ok, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !ok {
		t.Fatalf("mark quiescent = %t, %v", ok, err)
	}
	var state string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &state); err != nil || state != "quiescent" {
		t.Fatalf("claim state = %q, %v", state, err)
	}
	// Quiescence alone does not grant the stopped delivery live ownership.
	if ok, err := repo.ProgressPublicationDelivery(ctx, f.guard, 1, nil); err != nil || ok {
		t.Fatalf("quiescent progress = %t, %v", ok, err)
	}
	// River returns the interrupted job at the same attempt, so a fresh
	// delivery identity replaces the retained claim and revives the operation.
	newDelivery := f.guard
	newDelivery.ClaimUUID = uuid.New()
	newDelivery.DeliveryToken = uuid.NewString()
	newDelivery.BootUUID = uuid.New()
	if outcome, err := repo.ClaimPublicationDelivery(ctx, newDelivery); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("same-attempt redelivery = %q, %v", outcome, err)
	}
	if ok, err := repo.ProgressPublicationDelivery(ctx, f.guard, 2, nil); err != nil || ok {
		t.Fatalf("superseded delivery progress = %t, %v", ok, err)
	}
	if ok, err := repo.ProgressPublicationDelivery(ctx, newDelivery, 2, nil); err != nil || !ok {
		t.Fatalf("redelivered progress = %t, %v", ok, err)
	}
	if ok, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || ok {
		t.Fatalf("superseded guard quiescence = %t, %v", ok, err)
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
	if ok, err := repo.ProgressPublicationDelivery(ctx, f.guard, 3, nil); err != nil || ok {
		t.Fatalf("superseded delivery progress = %t, %v", ok, err)
	}
	if ok, err := repo.ProgressPublicationDelivery(ctx, redelivery, 3, nil); err != nil || !ok {
		t.Fatalf("reclaimed delivery progress = %t, %v", ok, err)
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
		mutable, err := repo.ProgressPublicationDelivery(ctx, guards[i], int64(i+1), nil)
		if err != nil {
			t.Fatalf("progress for claim %d: %v", i, err)
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

func TestPublicationStageAdvancesWithTypedAppendOnlyJournalPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	before, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	frozenBefore := append(json.RawMessage(nil), before.FrozenInputs...)
	observation := newValidPublicationStageObservation()
	size, mtime := int64(10), int64(20)
	observation.Ownership = ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}
	recordValidInitialLyricsOutcome(t, f, repo, observation)
	if ok, err := repo.AdvancePublicationStage(context.Background(), f.guard, "staged", observation); err != nil || ok {
		t.Fatalf("wrong-order stage = %t, %v", ok, err)
	}
	if ok, err := repo.AdvancePublicationStage(context.Background(), f.guard, "preparing", observation); err != nil || !ok {
		t.Fatalf("stage = %t, %v", ok, err)
	}
	if ok, err := repo.AdvancePublicationStage(context.Background(), f.guard, "staged", observation); err != nil || !ok {
		t.Fatalf("validate = %t, %v", ok, err)
	}
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || attempt.Phase != "validated" {
		t.Fatalf("attempt = %+v, %v", attempt, err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 7 {
		t.Fatalf("journal = %s, %v", attempt.MutationJournal, err)
	}
	if !sameJSONValue(attempt.FrozenInputs, frozenBefore) {
		t.Fatalf("frozen inputs changed: before %s, after %s", frozenBefore, attempt.FrozenInputs)
	}
}

func TestPublicationStageRequiresLiveFullGuardAndExecutingClaimPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	variants := []struct {
		name   string
		change func(*PublicationDeliveryGuard)
	}{
		{"operation", func(g *PublicationDeliveryGuard) { g.OperationID = uuid.New() }},
		{"application attempt", func(g *PublicationDeliveryGuard) { g.ApplicationAttempt++ }},
		{"job id", func(g *PublicationDeliveryGuard) { g.RiverJobID++ }},
		{"river attempt", func(g *PublicationDeliveryGuard) { g.RiverAttempt++ }},
		{"claim uuid", func(g *PublicationDeliveryGuard) { g.ClaimUUID = uuid.New() }},
		{"boot uuid", func(g *PublicationDeliveryGuard) { g.BootUUID = uuid.New() }},
		{"delivery token", func(g *PublicationDeliveryGuard) { g.DeliveryToken += "x" }},
		{"epoch", func(g *PublicationDeliveryGuard) { g.Epoch = uuid.New() }},
		{"position", func(g *PublicationDeliveryGuard) { g.ExpectedPositionID = uuid.New() }},
		{"candidate", func(g *PublicationDeliveryGuard) { g.CandidateGeneration = uuid.New() }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			guard := f.guard
			variant.change(&guard)
			if ok, err := repo.AdvancePublicationStage(context.Background(), guard, "preparing", newValidPublicationStageObservation()); err != nil || ok {
				t.Fatalf("advance = %t, %v", ok, err)
			}
		})
	}
	if ok, err := repo.MarkPublicationQuiescent(context.Background(), f.guard); err != nil || !ok {
		t.Fatalf("quiesce = %t, %v", ok, err)
	}
	if ok, err := repo.AdvancePublicationStage(context.Background(), f.guard, "preparing", newValidPublicationStageObservation()); err != nil || ok {
		t.Fatalf("quiescent advance = %t, %v", ok, err)
	}
	var phase string
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &phase); err != nil || phase != "preparing" {
		t.Fatalf("phase = %s, %v", phase, err)
	}
}

func TestPublicationStageRequiresRunningRiverAttemptAndValidArtifactPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	invalid := newValidPublicationStageObservation()
	invalid.RelativePath = "../escape"
	if ok, err := repo.AdvancePublicationStage(context.Background(), f.guard, "preparing", invalid); err == nil || ok {
		t.Fatalf("invalid observation = %t, %v", ok, err)
	}
	var journal string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &journal); err != nil || journal != "[]" {
		t.Fatalf("invalid artifact mutated journal: %s, %v", journal, err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET attempt=attempt+1 WHERE id=?`, f.guard.RiverJobID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.AdvancePublicationStage(context.Background(), f.guard, "preparing", newValidPublicationStageObservation()); err != nil || ok {
		t.Fatalf("mutated River attempt advance = %t, %v", ok, err)
	}
}

func publicationPreparationFixtureRecord(guard PublicationDeliveryGuard) PublicationPreparationRecord {
	return PublicationPreparationRecord{
		Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "intent",
		OperationID: guard.OperationID, ClaimUUID: guard.ClaimUUID, OwnerToken: uuid.New(),
		PrivateDirRel: "private/op", InputRel: "private/op/input.flac", OutputRel: "private/op/output.flac",
		ParentIdentity: PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "parent-42"},
	}
}

func TestPublicationPreparationJournalStagesAndIdempotencyPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	intent := publicationPreparationFixtureRecord(f.guard)
	record := func(value PublicationPreparationRecord, want PublicationPreparationOutcome) {
		t.Helper()
		got, err := repo.RecordPublicationPreparation(context.Background(), f.guard, value)
		if err != nil || got != want {
			t.Fatalf("record %s = %q, %v; want %q", value.Step, got, err, want)
		}
	}
	record(intent, PublicationPreparationRecorded)
	record(intent, PublicationPreparationAlreadyRecorded)
	directory := intent
	directory.Step = "directory_created"
	directory.DirectoryIdentity = &PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "private-99"}
	record(directory, PublicationPreparationRecorded)
	ready := directory
	ready.Step = "input_ready"
	ready.InputSize = new(int64)
	ready.InputMTime = new(int64)
	*ready.InputSize = 0
	*ready.InputMTime = 123
	record(ready, PublicationPreparationRecorded)
	record(directory, PublicationPreparationAlreadyRecorded)
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 3 {
		t.Fatalf("journal has %d entries (%s): %v", len(entries), attempt.MutationJournal, err)
	}
}

func TestPublicationPreparationRejectsInvalidOrderPathsConflictsAndStaleGuardPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	intent := publicationPreparationFixtureRecord(f.guard)
	invalid := intent
	invalid.Step = "directory_created"
	invalid.DirectoryIdentity = &PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk", FileID: "private"}
	if _, err := repo.RecordPublicationPreparation(context.Background(), f.guard, invalid); err == nil {
		t.Fatal("accepted directory_created before intent")
	}
	for _, mutate := range []func(*PublicationPreparationRecord){
		func(r *PublicationPreparationRecord) { r.InputRel = "../../outside" },
		func(r *PublicationPreparationRecord) { r.OutputRel = r.InputRel },
		func(r *PublicationPreparationRecord) { r.OutputRel = r.InputRel + "/nested.flac" },
		func(r *PublicationPreparationRecord) { r.InputRel = r.OutputRel + "/nested.flac" },
		func(r *PublicationPreparationRecord) { r.ParentIdentity.Platform = "plan9" },
		func(r *PublicationPreparationRecord) { r.OwnerToken = uuid.Nil },
	} {
		bad := intent
		mutate(&bad)
		if _, err := repo.RecordPublicationPreparation(context.Background(), f.guard, bad); err == nil {
			t.Fatalf("accepted invalid plan %+v", bad)
		}
	}
	if got, err := repo.RecordPublicationPreparation(context.Background(), f.guard, intent); err != nil || got != PublicationPreparationRecorded {
		t.Fatalf("intent = %q, %v", got, err)
	}
	conflict := intent
	conflict.OwnerToken = uuid.New()
	if _, err := repo.RecordPublicationPreparation(context.Background(), f.guard, conflict); err == nil {
		t.Fatal("accepted conflicting immutable plan")
	}
	staleGuards := []func(*PublicationDeliveryGuard){
		func(g *PublicationDeliveryGuard) { g.OperationID = uuid.New() },
		func(g *PublicationDeliveryGuard) { g.ApplicationAttempt++ },
		func(g *PublicationDeliveryGuard) { g.RiverJobID++ },
		func(g *PublicationDeliveryGuard) { g.RiverAttempt++ },
		func(g *PublicationDeliveryGuard) { g.ClaimUUID = uuid.New() },
		func(g *PublicationDeliveryGuard) { g.BootUUID = uuid.New() },
		func(g *PublicationDeliveryGuard) { g.DeliveryToken += "x" },
		func(g *PublicationDeliveryGuard) { g.Epoch = uuid.New() },
		func(g *PublicationDeliveryGuard) { g.ExpectedPositionID = uuid.New() },
		func(g *PublicationDeliveryGuard) { g.CandidateGeneration = uuid.New() },
	}
	for _, mutate := range staleGuards {
		stale := f.guard
		mutate(&stale)
		if got, err := repo.RecordPublicationPreparation(context.Background(), stale, intent); err != nil || got != PublicationPreparationStale {
			t.Fatalf("stale guard outcome = %q, %v", got, err)
		}
	}
	var entries []json.RawMessage
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || json.Unmarshal(attempt.MutationJournal, &entries) != nil || len(entries) != 1 {
		t.Fatalf("invalid/stale writes changed journal: %s, %v", attempt.MutationJournal, err)
	}
}

func TestPublicationPreparationConcurrentIdenticalAppendPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	intent := publicationPreparationFixtureRecord(f.guard)
	const workers = 8
	var wait sync.WaitGroup
	outcomes := make(chan PublicationPreparationOutcome, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			outcome, err := repo.RecordPublicationPreparation(context.Background(), f.guard, intent)
			outcomes <- outcome
			errs <- err
		}()
	}
	wait.Wait()
	close(outcomes)
	close(errs)
	recorded, already := 0, 0
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for outcome := range outcomes {
		switch outcome {
		case PublicationPreparationRecorded:
			recorded++
		case PublicationPreparationAlreadyRecorded:
			already++
		default:
			t.Fatalf("unexpected concurrent outcome %q", outcome)
		}
	}
	var entries []json.RawMessage
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || json.Unmarshal(attempt.MutationJournal, &entries) != nil || recorded != 1 || already != workers-1 || len(entries) != 1 {
		t.Fatalf("concurrent append outcome recorded=%d already=%d entries=%d, journal=%s err=%v", recorded, already, len(entries), attempt.MutationJournal, err)
	}
}

func TestPublicationPreparationRequiresPreparingPhasePostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	observation := newValidPublicationStageObservation()
	size, mtime := int64(10), int64(20)
	observation.Ownership = ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}
	recordValidInitialLyricsOutcome(t, f, repo, observation)
	if changed, err := repo.AdvancePublicationStage(context.Background(), f.guard, "preparing", observation); err != nil || !changed {
		t.Fatalf("advance stage = %t, %v", changed, err)
	}
	if changed, err := repo.AdvancePublicationStage(context.Background(), f.guard, "staged", observation); err != nil || !changed {
		t.Fatalf("advance staged phase = %t, %v", changed, err)
	}
	if outcome, err := repo.RecordPublicationPreparation(context.Background(), f.guard, publicationPreparationFixtureRecord(f.guard)); err != nil || outcome != PublicationPreparationStale {
		t.Fatalf("post-preparing preparation = %q, %v", outcome, err)
	}
	attempt, err := repo.ReadPublicationAttempt(context.Background(), f.guard.OperationID)
	if err != nil || string(attempt.MutationJournal) == "[]" {
		t.Fatalf("stage journal unexpectedly empty or unreadable: %+v, %v", attempt, err)
	}
}

func TestPublicationPreparationRejectsMalformedPriorJournalPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	malformed := `[{"version":1,"kind":"publication_preparation","step":"directory_created"}]`
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, malformed, f.guard.OperationID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordPublicationPreparation(context.Background(), f.guard, publicationPreparationFixtureRecord(f.guard)); err == nil {
		t.Fatal("accepted malformed prior preparation journal")
	}
	var journal string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &journal); err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if json.Unmarshal([]byte(journal), &entries) != nil || len(entries) != 1 {
		t.Fatalf("malformed journal changed: %s", journal)
	}
}

func TestPublicationPreparationRejectsMismatchedPriorDirectoryIdentityWithoutMutationPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	intent := publicationPreparationFixtureRecord(f.guard)
	directory := intent
	directory.Step = "directory_created"
	directory.DirectoryIdentity = &PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "private-99"}
	ready := directory
	ready.Step = "input_ready"
	ready.DirectoryIdentity = &PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "different-private-dir"}
	ready.InputSize = new(int64)
	ready.InputMTime = new(int64)
	entries := []PublicationPreparationRecord{intent, directory, ready}
	journal, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(journal), f.guard.OperationID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordPublicationPreparation(context.Background(), f.guard, intent); err == nil {
		t.Fatal("accepted a replay against mismatched prior directory identities")
	}
	var stored string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored != original {
		t.Fatalf("mismatched journal changed: %s", stored)
	}
}

func TestPublicationPreparationRejectsOverlappingPriorPlanPathsPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	valid := publicationPreparationFixtureRecord(f.guard)
	for _, paths := range [][2]string{
		{"private/op/input.flac", "private/op/input.flac/nested.flac"},
		{"private/op/output.flac/nested.flac", "private/op/output.flac"},
	} {
		corrupt := valid
		corrupt.InputRel, corrupt.OutputRel = paths[0], paths[1]
		journal, err := json.Marshal([]PublicationPreparationRecord{corrupt})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(journal), f.guard.OperationID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		var original string
		if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &original); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.RecordPublicationPreparation(context.Background(), f.guard, valid); err == nil {
			t.Fatalf("accepted prior plan with overlapping paths %q and %q", paths[0], paths[1])
		}
		var stored string
		if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(context.Background(), &stored); err != nil {
			t.Fatal(err)
		}
		if stored != original {
			t.Fatalf("overlapping prior journal changed: %s", stored)
		}
	}
}

func TestPublicationProgressIsMonotonicAndGuardedPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(context.Background(), f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	total := int64(20)
	if ok, err := repo.ProgressPublicationDelivery(context.Background(), f.guard, 10, &total); err != nil || !ok {
		t.Fatalf("progress = %t, %v", ok, err)
	}
	tooSmall := int64(15)
	if ok, err := repo.ProgressPublicationDelivery(context.Background(), f.guard, 11, &tooSmall); err != nil || ok {
		t.Fatalf("decreasing total = %t, %v", ok, err)
	}
	if ok, err := repo.ProgressPublicationDelivery(context.Background(), f.guard, 9, &total); err != nil || ok {
		t.Fatalf("decreasing completed = %t, %v", ok, err)
	}
	if ok, err := repo.ProgressPublicationDelivery(context.Background(), f.guard, 21, &total); err == nil || ok {
		t.Fatalf("completed beyond total = %t, %v", ok, err)
	}
	var completed, storedTotal int64
	if err := f.db.NewRaw(`SELECT bytes_completed,bytes_total FROM operation WHERE id=?`, f.guard.OperationID).Scan(context.Background(), &completed, &storedTotal); err != nil || completed != 10 || storedTotal != 20 {
		t.Fatalf("stored progress = %d/%d, %v", completed, storedTotal, err)
	}
}
