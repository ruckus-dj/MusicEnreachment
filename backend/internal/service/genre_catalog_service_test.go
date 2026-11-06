package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type staleGenreCatalogDeliveryStore struct{}

func (store staleGenreCatalogDeliveryStore) State(context.Context) (persistence.GenreCatalogState, error) {
	return persistence.GenreCatalogState{}, nil
}
func (staleGenreCatalogDeliveryStore) DispatchDueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, error) {
	return 0, false, nil
}
func (staleGenreCatalogDeliveryStore) ClaimRefresh(context.Context, int64) (string, bool, error) {
	return "", false, nil
}
func (staleGenreCatalogDeliveryStore) BeginClaimedRefresh(context.Context, int64, string, string, time.Time) (bool, error) {
	return false, nil
}
func (staleGenreCatalogDeliveryStore) ReplaceFenced(context.Context, int64, string, string, json.RawMessage, time.Time) (bool, error) {
	return false, nil
}
func (staleGenreCatalogDeliveryStore) RecordFailureFenced(context.Context, int64, string, string, time.Time, string) (bool, error) {
	return false, nil
}

type countingGenreCatalogConfigReader struct{ reads *int }

func (reader countingGenreCatalogConfigReader) GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error) {
	(*reader.reads)++
	return settings.MusicBrainzConfig{}, nil
}

func TestRefreshDeliverySkipsCompletedOrStaleDelivery(t *testing.T) {
	configReads := 0
	catalog := NewGenreCatalogService(staleGenreCatalogDeliveryStore{}, countingGenreCatalogConfigReader{reads: &configReads}, nil)

	if err := catalog.RefreshDelivery(context.Background(), uuid.New(), 42); err != nil {
		t.Fatalf("RefreshDelivery() error = %v", err)
	}
	if configReads != 0 {
		t.Fatalf("provider configuration read %d times for an unclaimed delivery", configReads)
	}
}

type failedConfigGenreCatalogStore struct {
	calls           []string
	failureCall     bool
	failureCtxValid bool
	identity        string
	status          string
}

func (store *failedConfigGenreCatalogStore) State(context.Context) (persistence.GenreCatalogState, error) {
	store.calls = append(store.calls, "state")
	return persistence.GenreCatalogState{ConfigurationIdentity: "saved-identity"}, nil
}
func (store *failedConfigGenreCatalogStore) DispatchDueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, error) {
	store.calls = append(store.calls, "dispatch")
	return 0, false, nil
}
func (store *failedConfigGenreCatalogStore) DispatchDueRefreshWithRetired(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, uuid.UUID, error) {
	store.calls = append(store.calls, "dispatch")
	return 0, false, uuid.Nil, nil
}
func (store *failedConfigGenreCatalogStore) ClaimRefresh(context.Context, int64) (string, bool, error) {
	store.calls = append(store.calls, "claim")
	return "delivery-token", true, nil
}
func (store *failedConfigGenreCatalogStore) BeginClaimedRefresh(context.Context, int64, string, string, time.Time) (bool, error) {
	store.calls = append(store.calls, "begin")
	return true, nil
}
func (store *failedConfigGenreCatalogStore) ReplaceFenced(context.Context, int64, string, string, json.RawMessage, time.Time) (bool, error) {
	store.calls = append(store.calls, "replace")
	return true, nil
}
func (store *failedConfigGenreCatalogStore) RecordFailureFenced(ctx context.Context, _ int64, token, identity string, _ time.Time, message string) (bool, error) {
	store.calls = append(store.calls, "failure")
	store.failureCall = true
	_, hasDeadline := ctx.Deadline()
	store.failureCtxValid = ctx.Err() == nil && hasDeadline
	store.identity = identity
	store.status = "finished"
	if token != "delivery-token" || message != "MusicBrainz genre catalog refresh failed" {
		return false, errors.New("failure write was not fenced or safe")
	}
	return true, nil
}

type configRaceGenreCatalogStore struct{ *failedConfigGenreCatalogStore }

func (store configRaceGenreCatalogStore) ReplaceFenced(context.Context, int64, string, string, json.RawMessage, time.Time) (bool, error) {
	store.calls = append(store.calls, "replace-config-race")
	return false, nil
}

func TestPublishDeliveryFinalConfigRaceFinishesCurrentRequest(t *testing.T) {
	base := &failedConfigGenreCatalogStore{}
	operations := NewOperations(nil)
	operationID := uuid.New()
	wake, stop := operations.Subscribe(operationID)
	defer stop()
	catalog := NewGenreCatalogService(configRaceGenreCatalogStore{failedConfigGenreCatalogStore: base}, nil, nil).WithOperations(operations)
	err := catalog.publishDelivery(context.Background(), operationID, 73, "delivery-token", "current-identity", json.RawMessage(`[]`))
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("publishDelivery() error = %v, want superseded error", err)
	}
	if !base.failureCall || base.status != "finished" {
		t.Fatalf("final config race did not record a terminal failure: recorded=%v status=%q", base.failureCall, base.status)
	}
	select {
	case <-wake:
	default:
		t.Fatal("recorded failure did not notify its operation")
	}
}

func TestPublishDeliveryNotifiesOnlyChangedOperation(t *testing.T) {
	store := &failedConfigGenreCatalogStore{}
	operations := NewOperations(nil)
	operationID, unrelatedID := uuid.New(), uuid.New()
	wake, stop := operations.Subscribe(operationID)
	defer stop()
	unrelated, stopUnrelated := operations.Subscribe(unrelatedID)
	defer stopUnrelated()
	catalog := NewGenreCatalogService(store, nil, nil).WithOperations(operations)
	if err := catalog.publishDelivery(context.Background(), operationID, 73, "delivery-token", "current-identity", json.RawMessage(`[]`)); err != nil {
		t.Fatalf("publishDelivery() error = %v", err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("successful publication did not notify its operation")
	}
	select {
	case <-unrelated:
		t.Fatal("successful publication notified an unrelated operation")
	default:
	}
}

type failingGenreCatalogConfigReader struct {
	calls *[]string
	err   error
}

func (reader failingGenreCatalogConfigReader) GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error) {
	*reader.calls = append(*reader.calls, "config")
	return settings.MusicBrainzConfig{}, reader.err
}

func TestRefreshDeliveryClaimsBeforeConfigAndPersistsDetachedFailure(t *testing.T) {
	store := &failedConfigGenreCatalogStore{}
	catalog := NewGenreCatalogService(store, failingGenreCatalogConfigReader{calls: &store.calls, err: errors.New("registry unavailable")}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := catalog.RefreshDelivery(ctx, uuid.New(), 73)
	if err == nil || !strings.Contains(err.Error(), "registry unavailable") {
		t.Fatalf("RefreshDelivery() error = %v, want configuration failure", err)
	}
	if strings.Join(store.calls, ",") != "claim,state,config,failure" {
		t.Fatalf("call order = %v, want claim,state,config,failure", store.calls)
	}
	if !store.failureCall || store.identity != "saved-identity" {
		t.Fatalf("failure recorded = %v, identity = %q", store.failureCall, store.identity)
	}
	if !store.failureCtxValid {
		t.Fatal("failure recording context was canceled or had no deadline during persistence")
	}
}

type genreCatalogSetupState bool

func (state genreCatalogSetupState) SetupCompleted(context.Context) (bool, error) {
	return bool(state), nil
}

type genreCatalogAdmissionQueue struct{}

func (genreCatalogAdmissionQueue) EnqueueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer) error {
	return nil
}

type genreCatalogInsertClient struct{}

func (genreCatalogInsertClient) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, nil
}

func TestDispatchDueRefreshRequiresCompletedSetup(t *testing.T) {
	for _, test := range []struct {
		name      string
		completed bool
		wantCalls string
	}{
		{name: "incomplete setup", wantCalls: ""},
		{name: "completed setup", completed: true, wantCalls: "dispatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &failedConfigGenreCatalogStore{}
			catalog := NewGenreCatalogService(store, nil, nil).
				WithSetupCompletion(genreCatalogSetupState(test.completed)).
				WithRefreshQueue(genreCatalogAdmissionQueue{}, genreCatalogInsertClient{})

			if err := catalog.DispatchDueRefresh(context.Background()); err != nil {
				t.Fatalf("DispatchDueRefresh() error = %v", err)
			}
			if got := strings.Join(store.calls, ","); got != test.wantCalls {
				t.Fatalf("repository calls = %q, want %q", got, test.wantCalls)
			}
		})
	}
}

type retiringGenreCatalogStore struct {
	*failedConfigGenreCatalogStore
	retiredID uuid.UUID
}

func (store retiringGenreCatalogStore) DispatchDueRefreshWithRetired(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, uuid.UUID, error) {
	store.calls = append(store.calls, "dispatch")
	return 0, false, store.retiredID, nil
}

func TestDispatchDueRefreshNotifiesRetiredOperationAfterSuccess(t *testing.T) {
	id := uuid.New()
	store := retiringGenreCatalogStore{failedConfigGenreCatalogStore: &failedConfigGenreCatalogStore{}, retiredID: id}
	operations := NewOperations(nil)
	wake, stop := operations.Subscribe(id)
	defer stop()
	catalog := NewGenreCatalogService(store, nil, nil).
		WithOperations(operations).
		WithSetupCompletion(genreCatalogSetupState(true)).
		WithRefreshQueue(genreCatalogAdmissionQueue{}, genreCatalogInsertClient{})
	if err := catalog.DispatchDueRefresh(context.Background()); err != nil {
		t.Fatalf("DispatchDueRefresh() error = %v", err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("retired operation was not notified after dispatch commit")
	}
}

type blockingGenreCatalogDispatcher struct{ started chan struct{} }

func (dispatcher blockingGenreCatalogDispatcher) DispatchDueRefresh(ctx context.Context) error {
	close(dispatcher.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestSetupGenreCatalogDispatchDoesNotBlockCaller(t *testing.T) {
	started := make(chan struct{})
	setup := &SetupService{genreCatalogRefresh: blockingGenreCatalogDispatcher{started: started}}
	done := make(chan struct{})
	go func() {
		setup.dispatchGenreCatalogRefresh()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("genre catalog dispatch blocked setup completion")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("genre catalog dispatch did not start")
	}
}
