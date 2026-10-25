//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type libraryProviderRefreshArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (libraryProviderRefreshArgs) Kind() string { return "provider_fetch_v1" }

type libraryProviderRefreshInserter struct {
	failAfterInsert bool
	jobID           int64
}

func (inserter *libraryProviderRefreshInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), encoded).Scan(&id); err != nil {
		return nil, err
	}
	inserter.jobID = id
	if inserter.failAfterInsert {
		return nil, errors.New("injected failure after River row insertion")
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

func TestLibraryProviderRefreshAdmissionBindsAndEnqueuesAtomicallyPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	identity := "cfg-library-refresh"
	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES('musicbrainz_config_identity',?)`, identity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	provider := Provider{ID: uuid.New(), Code: "musicbrainz"}
	cache := NewProviderCacheRepository(db)
	if err := cache.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}
	source := ProviderSource{ID: uuid.New(), ProviderID: provider.ID, Namespace: "library-refresh-test", Endpoint: "https://musicbrainz.org/ws/2", ConfigurationIdentity: identity}
	if err := cache.CreateProviderSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	releaseID, providerReleaseID := uuid.New(), uuid.New()
	boundMBID, reboundMBID := uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,'Refresh admission')`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Refresh admission')`, providerReleaseID, provider.ID, boundMBID.String()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, provider.ID, providerReleaseID, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewSetupManagerRepository(db)
	newOperation := func(mbid uuid.UUID) *Operation {
		sourceID, configIdentity := source.ID, identity
		cacheKey := "release-lookup:" + mbid.String()
		return &Operation{
			ID: uuid.New(), Kind: "provider_release_lookup", State: "queued", Stage: "queued",
			InputSnapshot:    json.RawMessage(`{"schema_version":1,"mbid":"` + mbid.String() + `"}`),
			ProviderSourceID: &sourceID, ProviderConfigurationIdentity: &configIdentity,
			ProviderID: &provider.ID, ProviderEndpoint: source.Endpoint, ProviderCacheKey: &cacheKey,
			ProviderExplicitRefresh:   true,
			MusicBrainzReleaseBinding: &MusicBrainzReleaseBinding{LocalReleaseID: releaseID, MBID: mbid.String()},
		}
	}
	createArgs := func(operation *Operation) libraryProviderRefreshArgs {
		return libraryProviderRefreshArgs{OperationID: operation.ID}
	}

	// A currently bound refresh admits the real operation and matching River payload.
	operation := newOperation(boundMBID)
	inserter := new(libraryProviderRefreshInserter)
	if err := repo.CreateOperationAndEnqueue(ctx, operation, inserter, createArgs(operation), nil); err != nil {
		t.Fatalf("admit bound refresh: %v", err)
	}
	var storedOperation, storedJob int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=? AND provider_explicit_refresh=true AND provider_source_id=? AND provider_configuration_identity=? AND provider_cache_key=? AND input_snapshot=?::jsonb AND river_job_id=?`, operation.ID, source.ID, identity, "release-lookup:"+boundMBID.String(), string(operation.InputSnapshot), inserter.jobID).Scan(ctx, &storedOperation); err != nil {
		t.Fatal(err)
	}
	var jobOperationID uuid.UUID
	if err := db.NewRaw(`SELECT (args->>'operation_id')::uuid FROM river_job WHERE id=?`, inserter.jobID).Scan(ctx, &jobOperationID); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id=? AND kind='provider_fetch_v1'`, inserter.jobID).Scan(ctx, &storedJob); err != nil {
		t.Fatal(err)
	}
	if storedOperation != 1 || storedJob != 1 || jobOperationID != operation.ID {
		t.Fatalf("admitted operation/job mismatch: operation=%d job=%d job operation=%s", storedOperation, storedJob, jobOperationID)
	}

	// Rebinding the local release invalidates the previously captured MBID.
	if _, err := db.NewRaw(`UPDATE provider_release SET provider_key=? WHERE id=?`, reboundMBID.String(), providerReleaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	stale := newOperation(boundMBID)
	staleInserter := new(libraryProviderRefreshInserter)
	if err := repo.CreateOperationAndEnqueue(ctx, stale, staleInserter, createArgs(stale), nil); !errors.Is(err, ErrMusicBrainzReleaseBindingChanged) {
		t.Fatalf("stale binding admission error = %v, want binding change", err)
	}
	var staleOperations, staleJobs int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, stale.ID).Scan(ctx, &staleOperations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='provider_fetch_v1' AND args->>'operation_id'=?`, stale.ID.String()).Scan(ctx, &staleJobs); err != nil {
		t.Fatal(err)
	}
	if staleOperations != 0 || staleJobs != 0 {
		t.Fatalf("stale binding left partial admission: operations=%d jobs=%d", staleOperations, staleJobs)
	}

	// An error after inserting a real River row rolls back both records.
	rollback := newOperation(reboundMBID)
	failingInserter := &libraryProviderRefreshInserter{failAfterInsert: true}
	if err := repo.CreateOperationAndEnqueue(ctx, rollback, failingInserter, createArgs(rollback), nil); err == nil {
		t.Fatal("injected post-insert failure unexpectedly committed")
	}
	var rollbackOperations, rollbackJobs int
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, rollback.ID).Scan(ctx, &rollbackOperations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE id=?`, failingInserter.jobID).Scan(ctx, &rollbackJobs); err != nil {
		t.Fatal(err)
	}
	if rollbackOperations != 0 || rollbackJobs != 0 {
		t.Fatalf("failed enqueue partially committed: operations=%d jobs=%d", rollbackOperations, rollbackJobs)
	}
}
