package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// Exercise the registered Huma routes and Operations state machine together.
// The fixture supplies persistence only; retry validation and state changes
// are performed by service.Operations.
func TestOperationTransitionHTTPResponses(t *testing.T) {
	failedID, dismissID, queuedID := uuid.New(), uuid.New(), uuid.New()
	repository := &transitionOperationsRepository{rows: map[uuid.UUID]*persistence.Operation{
		failedID:  {ID: failedID, Kind: "install", State: "failed", Stage: "verify", SafeError: transitionStringPointer("installation failed safely")},
		dismissID: {ID: dismissID, Kind: "install", State: "failed", Stage: "download", SafeError: transitionStringPointer("download failed safely")},
		queuedID:  {ID: queuedID, Kind: "install", State: "queued", Stage: "queued"},
	}}
	operations := service.NewOperations(repository)
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	router := chi.NewRouter()
	huma := api.New(router)
	api.RegisterAll(huma, api.Dependencies{Operations: operations, Setup: setup})

	response := transitionRequest(t, router, http.MethodPost, "/operations/"+failedID.String()+"/retry", "")
	if response.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	var snapshot api.OperationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != failedID || snapshot.State != "queued" || snapshot.Stage != "retry" || snapshot.SafeError != nil {
		t.Fatalf("retry response did not expose transitioned snapshot: %#v", snapshot)
	}

	response = transitionRequest(t, router, http.MethodPost, "/operations/"+queuedID.String()+"/retry", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("retry of queued operation status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, http.StatusConflict)
	current, err := operations.Snapshot(context.Background(), queuedID)
	if err != nil || current.State != "queued" {
		t.Fatalf("rejected retry changed operation: snapshot=%#v err=%v", current, err)
	}

	response = transitionRequest(t, router, http.MethodDelete, "/operations/"+queuedID.String(), "")
	if response.Code != http.StatusConflict {
		t.Fatalf("dismiss of queued operation status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	assertTransitionErrorBody(t, response, http.StatusConflict)
	response = transitionRequest(t, router, http.MethodDelete, "/operations/"+dismissID.String(), "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("dismiss of failed operation status=%d want=%d body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if _, err := operations.Snapshot(context.Background(), dismissID); err == nil {
		t.Fatal("dismissed failed operation remains in repository")
	}
}

func assertTransitionErrorBody(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	var body struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode HTTP error body %q: %v", response.Body.String(), err)
	}
	if body.Status != want {
		t.Fatalf("error body status=%d want=%d: %s", body.Status, want, response.Body.String())
	}
}

func transitionRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func transitionStringPointer(value string) *string { return &value }

type transitionOperationsRepository struct {
	rows map[uuid.UUID]*persistence.Operation
}

func (r *transitionOperationsRepository) CreateOperation(_ context.Context, operation *persistence.Operation) error {
	r.rows[operation.ID] = operation
	return nil
}
func (r *transitionOperationsRepository) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if operation := r.rows[id]; operation != nil {
		return operation, nil
	}
	return nil, errors.New("not found")
}
func (r *transitionOperationsRepository) ListOperations(_ context.Context, states ...string) ([]persistence.Operation, error) {
	result := make([]persistence.Operation, 0, len(r.rows))
	for _, operation := range r.rows {
		included := len(states) == 0
		for _, state := range states {
			included = included || operation.State == state
		}
		if included {
			result = append(result, *operation)
		}
	}
	return result, nil
}
func (r *transitionOperationsRepository) UpdateOperation(_ context.Context, operation *persistence.Operation) error {
	r.rows[operation.ID] = operation
	return nil
}
func (r *transitionOperationsRepository) TransitionOperation(_ context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	operation := r.rows[id]
	if operation == nil {
		return errors.New("not found")
	}
	return transition(operation)
}
func (r *transitionOperationsRepository) DismissOperation(_ context.Context, id uuid.UUID) error {
	operation := r.rows[id]
	if operation == nil || operation.State != "failed" {
		return errors.New("only failed operations can be dismissed")
	}
	delete(r.rows, id)
	return nil
}
func (*transitionOperationsRepository) DeleteSucceededBefore(context.Context, time.Time) error {
	return nil
}
