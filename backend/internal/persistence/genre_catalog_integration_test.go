//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type genreCatalogIntegrationInserter struct {
	mu              sync.Mutex
	ids             []int64
	fail            bool
	failAfterInsert bool
}

func (i *genreCatalogIntegrationInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	refreshArgs, ok := args.(GenreCatalogRefreshArgs)
	if !ok || refreshArgs.OperationID == uuid.Nil {
		return nil, errors.New("unexpected job args")
	}
	if i.fail {
		return nil, errors.New("injected insert failure")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), string(encoded)).Scan(&id)
	if err != nil {
		return nil, err
	}
	i.mu.Lock()
	i.ids = append(i.ids, id)
	i.mu.Unlock()
	if i.failAfterInsert {
		return nil, errors.New("injected post-insert failure")
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

func TestGenreCatalogAdmissionFencedLifecyclePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	repo := NewGenreCatalogRepository(db, NewSetupManagerRepository(db))
	ctx := context.Background()
	inserter := &genreCatalogIntegrationInserter{}
	registry := settings.New(NewSettingsRepository(db), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://musicbrainz.example/a"); err != nil {
		t.Fatal(err)
	}
	configA, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

	// Concurrent admission serializes on the singleton row and coalesces to one real River row.
	const callers = 8
	ids := make([]int64, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for n := range callers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ids[n], errs[n] = repo.AdmitRefresh(ctx, inserter, time.Now().UTC(), &due)
		}(n)
	}
	wg.Wait()
	for n, err := range errs {
		if err != nil {
			t.Fatalf("admission %d: %v", n, err)
		}
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("non-coalesced job IDs: %v", ids)
		}
	}
	var jobs int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, (GenreCatalogRefreshArgs{}).Kind()).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("River rows = %d, want 1", jobs)
	}
	state, err := repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RefreshDueAt == nil || !state.RefreshDueAt.Equal(due) {
		t.Fatalf("due time = %v, want %v", state.RefreshDueAt, due)
	}
	operation, err := NewSetupManagerRepository(db).GetOperation(ctx, state.RefreshRequest.OperationID)
	if err != nil || operation.State != "queued" || operation.Kind != "refresh_genre_catalog_v1" || string(operation.InputSnapshot) != `{}` {
		t.Fatalf("admitted operation = %+v, err=%v", operation, err)
	}

	oldToken, claimed, err := repo.ClaimRefresh(ctx, ids[0])
	if err != nil || !claimed {
		t.Fatalf("initial claim: %v %v", claimed, err)
	}
	newToken, claimed, err := repo.ClaimRefresh(ctx, ids[0])
	if err != nil || !claimed || oldToken == newToken {
		t.Fatalf("redelivery claim failed to rotate token: %v %v", claimed, err)
	}
	started, err := repo.BeginClaimedRefresh(ctx, ids[0], newToken, configA.Identity, time.Now().UTC())
	if err != nil || !started {
		t.Fatalf("begin: %v %v", started, err)
	}
	if ok, err := repo.ReplaceFenced(ctx, ids[0], oldToken, configA.Identity, json.RawMessage(`["stale"]`), time.Now()); err != nil || ok {
		t.Fatalf("stale token published: %v %v", ok, err)
	}
	if ok, err := repo.ReplaceFenced(ctx, ids[0], newToken, configA.Identity, json.RawMessage(`["good"]`), time.Now().UTC()); err != nil || !ok {
		t.Fatalf("replace: %v %v", ok, err)
	}
	if ok, err := repo.ReplaceFenced(ctx, ids[0], newToken, configA.Identity, json.RawMessage(`["duplicate"]`), time.Now().UTC()); err != nil || ok {
		t.Fatalf("finished delivery was not a no-op: %v %v", ok, err)
	}
	state, err = repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(state.Items) != `["good"]` || state.RefreshRequest.Status != "finished" || state.RefreshDueAt == nil || !state.RefreshDueAt.Equal(due) {
		t.Fatalf("unexpected finished state: %+v", state)
	}
	operation, err = NewSetupManagerRepository(db).GetOperation(ctx, state.RefreshRequest.OperationID)
	if err != nil || operation.State != "succeeded" || operation.FinishedAt == nil {
		t.Fatalf("successful refresh operation = %+v, err=%v", operation, err)
	}
}

func TestGenreCatalogRollbackABAFailureAndManualDuePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	repo := NewGenreCatalogRepository(db, NewSetupManagerRepository(db))
	ctx := context.Background()
	inserter := &genreCatalogIntegrationInserter{}
	registry := settings.New(NewSettingsRepository(db), nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://musicbrainz.example/a"); err != nil {
		t.Fatal(err)
	}
	configA, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Now().UTC()
	if _, err := db.NewRaw(`UPDATE genre_catalog SET items='["retained"]'::jsonb, updated_at=? WHERE singleton=true`, baseline).Exec(ctx); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	due := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	inserter.failAfterInsert = true
	if _, err := repo.AdmitRefresh(ctx, inserter, time.Now().UTC(), &due); err == nil {
		t.Fatal("post-insert failure unexpectedly succeeded")
	}
	var jobs int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=?`, (GenreCatalogRefreshArgs{}).Kind()).Scan(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("transactional rollback left %d River jobs", jobs)
	}
	var operations int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind='refresh_genre_catalog_v1'`).Scan(ctx, &operations); err != nil || operations != 0 {
		t.Fatalf("transactional rollback left %d operations: %v", operations, err)
	}
	state, err := repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RefreshRequest.Status != "idle" || state.RefreshDueAt != nil {
		t.Fatalf("failed admission persisted request: %+v", state)
	}
	inserter.failAfterInsert = false
	job, err := repo.AdmitRefresh(ctx, inserter, time.Now().UTC(), &due)
	if err != nil {
		t.Fatal(err)
	}
	if _, admitted, err := repo.DispatchDueRefresh(ctx, inserter, time.Now().UTC(), due.Add(24*time.Hour)); err != nil || admitted {
		t.Fatalf("future deadline dispatched early: admitted=%v err=%v", admitted, err)
	}
	// Manual coalescing must not postpone the persisted automatic deadline.
	if got, err := repo.AdmitRefresh(ctx, inserter, time.Now().UTC(), nil); err != nil || got != job {
		t.Fatalf("manual coalesce: %d %v", got, err)
	}
	state, err = repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RefreshDueAt == nil || !state.RefreshDueAt.Equal(due) {
		t.Fatalf("manual admission moved due time: %v, want %v", state.RefreshDueAt, due)
	}

	token, ok, err := repo.ClaimRefresh(ctx, job)
	if err != nil || !ok {
		t.Fatalf("claim ABA job: %v %v", ok, err)
	}
	staleToken := token
	token, ok, err = repo.ClaimRefresh(ctx, job)
	if err != nil || !ok || staleToken == token {
		t.Fatalf("redelivery did not rotate ABA token: %v %v", ok, err)
	}
	if started, err := repo.BeginClaimedRefresh(ctx, job, token, configA.Identity, time.Now()); err != nil || !started {
		t.Fatalf("begin ABA job: %v %v", started, err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://musicbrainz.example/b"); err != nil {
		t.Fatal(err)
	}
	configB, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if configA.Identity == configB.Identity {
		t.Fatal("MusicBrainz configuration identity did not rotate for endpoint B")
	}
	if started, err := repo.BeginClaimedRefresh(ctx, job, token, configB.Identity, time.Now()); err != nil || !started {
		t.Fatalf("begin with endpoint B identity: %v %v", started, err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", "https://musicbrainz.example/a"); err != nil {
		t.Fatal(err)
	}
	configAAgain, err := registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if configAAgain.Identity == configA.Identity || configAAgain.BaseURL != configA.BaseURL {
		t.Fatalf("A -> B -> A did not rotate identity: first=%+v final=%+v", configA, configAAgain)
	}
	if ok, err := repo.ReplaceFenced(ctx, job, token, configB.Identity, json.RawMessage(`["wrong"]`), time.Now()); err != nil || ok {
		t.Fatalf("stale configuration published: %v %v", ok, err)
	}
	if ok, err := repo.RecordFailureFenced(ctx, job, staleToken, configA.Identity, time.Now(), "stale delivery failed"); err != nil || ok {
		t.Fatalf("stale delivery recorded failure: %v %v", ok, err)
	}
	if ok, err := repo.RecordFailureFenced(ctx, job, token, configA.Identity, time.Now(), "failed"); err != nil || !ok {
		t.Fatalf("current delivery failure was not recorded: %v %v", ok, err)
	}
	state, err = repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(state.Items) != `["retained"]` {
		t.Fatalf("failure/stale worker lost last good catalog: %s", state.Items)
	}
	if state.LastError == nil || *state.LastError != "failed" {
		t.Fatalf("failure diagnostic not recorded: %v", state.LastError)
	}
	operation, err := NewSetupManagerRepository(db).GetOperation(ctx, state.RefreshRequest.OperationID)
	if err != nil || operation.State != "failed" || operation.FinishedAt == nil {
		t.Fatalf("failed refresh operation = %+v, err=%v", operation, err)
	}
}

func TestGenreCatalogAdmissionRetiresLostOperationPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	operations := NewSetupManagerRepository(db)
	repo := NewGenreCatalogRepository(db, operations)
	ctx := context.Background()
	inserter := &genreCatalogIntegrationInserter{}
	firstJob, err := repo.AdmitRefresh(ctx, inserter, time.Now().UTC(), nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstOperation := state.RefreshRequest.OperationID
	if _, err := db.NewRaw(`DELETE FROM river_job WHERE id=?`, firstJob).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	secondJob, err := repo.AdmitRefresh(ctx, inserter, time.Now().UTC(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondJob == firstJob {
		t.Fatalf("lost delivery was not replaced: job=%d", secondJob)
	}
	operation, err := operations.GetOperation(ctx, firstOperation)
	if err != nil || operation.State != "failed" || operation.FinishedAt == nil {
		t.Fatalf("lost delivery operation = %+v, err=%v", operation, err)
	}
}

func TestGenreCatalogReconcilesTerminalRequestBeforeFutureDeadlinePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigratedWithRiver(t)
	operations := NewSetupManagerRepository(db)
	repo := NewGenreCatalogRepository(db, operations)
	ctx := context.Background()
	inserter := &genreCatalogIntegrationInserter{}
	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(24 * time.Hour)
	firstJob, err := repo.AdmitRefresh(ctx, inserter, now, &due)
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstOperation := state.RefreshRequest.OperationID
	if _, err := db.NewRaw(`UPDATE operation SET state='failed', stage='failed', safe_error='externally failed', finished_at=?, updated_at=? WHERE id=?`, now, now, firstOperation).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='cancelled', finalized_at=? WHERE id=?`, now, firstJob).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	state, err = repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RefreshRequest.Status != "finished" {
		t.Fatalf("terminal operation exposed active request: %+v", state.RefreshRequest)
	}
	if _, admitted, err := repo.DispatchDueRefresh(ctx, inserter, now, due.Add(24*time.Hour)); err != nil || admitted {
		t.Fatalf("future deadline dispatched after retiring dead request: admitted=%v err=%v", admitted, err)
	}
	state, err = repo.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.RefreshRequest.Status != "finished" || state.RefreshDueAt == nil || !state.RefreshDueAt.Equal(due) {
		t.Fatalf("retired request changed deadline or remained active: %+v", state)
	}
	secondJob, err := repo.AdmitRefresh(ctx, inserter, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondJob == firstJob {
		t.Fatalf("manual request coalesced onto dead delivery: job=%d", secondJob)
	}
}
