package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type genreCatalogAPIStore struct {
	state persistence.GenreCatalogState
	err   error
}

func (store *genreCatalogAPIStore) State(context.Context) (persistence.GenreCatalogState, error) {
	return store.state, store.err
}
func (*genreCatalogAPIStore) DispatchDueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer, time.Time, time.Time) (int64, bool, error) {
	return 0, false, nil
}
func (*genreCatalogAPIStore) ClaimRefresh(context.Context, int64) (string, bool, error) {
	return "", false, nil
}
func (*genreCatalogAPIStore) BeginClaimedRefresh(context.Context, int64, string, string, time.Time) (bool, error) {
	return false, nil
}
func (*genreCatalogAPIStore) ReplaceFenced(context.Context, int64, string, string, json.RawMessage, time.Time) (bool, error) {
	return false, nil
}
func (*genreCatalogAPIStore) RecordFailureFenced(context.Context, int64, string, string, time.Time, string) (bool, error) {
	return false, nil
}

type genreCatalogAPIQueue struct{ err error }

func (queue *genreCatalogAPIQueue) EnqueueRefresh(context.Context, persistence.GenreCatalogJobEnqueuer) error {
	return queue.err
}

type genreCatalogOperationRepository struct{ operation *persistence.Operation }

func (*genreCatalogOperationRepository) CreateOperation(context.Context, *persistence.Operation) error {
	return nil
}
func (repository *genreCatalogOperationRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}
func (*genreCatalogOperationRepository) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	return nil, nil
}
func (*genreCatalogOperationRepository) UpdateOperation(context.Context, *persistence.Operation) error {
	return nil
}
func (*genreCatalogOperationRepository) TransitionOperation(context.Context, uuid.UUID, func(*persistence.Operation) error) error {
	return nil
}
func (*genreCatalogOperationRepository) DismissOperation(context.Context, uuid.UUID) error {
	return nil
}
func (*genreCatalogOperationRepository) DeleteSucceededBefore(context.Context, time.Time) error {
	return nil
}

type genreCatalogAPIInserter struct{}

func (genreCatalogAPIInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("unused")
}

func genreCatalogAPIHandler(catalog *service.GenreCatalogService) http.Handler {
	router := chi.NewRouter()
	api := New(router)
	registerGenreCatalog(api, catalog)
	return router
}

func genreCatalogAPIService(state persistence.GenreCatalogState, storeErr error, queueErr error) *service.GenreCatalogService {
	return service.NewGenreCatalogService(&genreCatalogAPIStore{state: state, err: storeErr}, nil, nil).
		WithRefreshQueue(&genreCatalogAPIQueue{err: queueErr}, genreCatalogAPIInserter{})
}

func TestGenreCatalogAPIStatusAndSearch(t *testing.T) {
	store := genreCatalogAPIService(persistence.GenreCatalogState{
		Items:          json.RawMessage(`[{"id":"id-1","name":"Ambient","source_name":"ambient"},{"id":"id-2","name":"Dark Ambient","source_name":"dark ambient"}]`),
		RefreshRequest: persistence.GenreCatalogRefreshRequest{Status: "finished"},
	}, nil, nil)
	handler := genreCatalogAPIHandler(store)

	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/genres/catalog", http.StatusOK},
		{http.MethodGet, "/genres/catalog/search?query=%20%20AMBIENT%20%20", http.StatusOK},
		{http.MethodGet, "/genres/catalog/search?query=ambient&limit=100", http.StatusOK},
		{http.MethodGet, "/genres/catalog/search", http.StatusUnprocessableEntity},
		{http.MethodGet, "/genres/catalog/search?query=%20%20%20", http.StatusUnprocessableEntity},
		{http.MethodGet, "/genres/catalog/search?query=x&limit=0", http.StatusUnprocessableEntity},
		{http.MethodGet, "/genres/catalog/search?query=x&limit=101", http.StatusUnprocessableEntity},
		{http.MethodGet, "/genres/catalog/search?query=x&limit=nope", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.status, recorder.Body.String())
			}
			if tc.path == "/genres/catalog/search?query=%20%20AMBIENT%20%20" {
				var body struct {
					Items []service.GenreCatalogItem `json:"items"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Items) != 2 || body.Items[0].Name != "Ambient" {
					t.Fatalf("search items = %+v", body.Items)
				}
			}
		})
	}
}

func TestGenreCatalogAPIErrorHandlingAndRefresh(t *testing.T) {
	for _, tc := range []struct {
		name string
		cat  *service.GenreCatalogService
		path string
		want int
	}{
		{"status read failure", genreCatalogAPIService(persistence.GenreCatalogState{}, errors.New("db failure"), nil), "/genres/catalog", http.StatusInternalServerError},
		{"search read failure", genreCatalogAPIService(persistence.GenreCatalogState{}, errors.New("db failure"), nil), "/genres/catalog/search?query=x", http.StatusInternalServerError},
		{"refresh queue failure", genreCatalogAPIService(persistence.GenreCatalogState{}, nil, errors.New("queue failure")), "/genres/catalog/refresh", http.StatusServiceUnavailable},
		{"unavailable", nil, "/genres/catalog", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			method := http.MethodGet
			if tc.path == "/genres/catalog/refresh" {
				method = http.MethodPost
			}
			genreCatalogAPIHandler(tc.cat).ServeHTTP(recorder, httptest.NewRequest(method, tc.path, nil))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}

	operationID := uuid.New()
	state := persistence.GenreCatalogState{RefreshRequest: persistence.GenreCatalogRefreshRequest{OperationID: operationID, Status: "queued"}}
	service := genreCatalogAPIService(state, nil, nil).WithOperations(service.NewOperations(&genreCatalogOperationRepository{operation: &persistence.Operation{
		ID: operationID, Kind: "refresh_genre_catalog_v1", State: "queued", Stage: "queued", InputSnapshot: json.RawMessage(`{}`),
	}}))
	handler := genreCatalogAPIHandler(service)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/genres/catalog/refresh", nil))
	var response struct {
		ID    uuid.UUID `json:"id"`
		Kind  string    `json:"kind"`
		State string    `json:"state"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode refresh response: %v; status=%d body=%s", err, recorder.Code, recorder.Body.String())
	}
	if recorder.Code != http.StatusOK || response.ID != operationID || response.Kind != "refresh_genre_catalog_v1" || response.State != "queued" {
		t.Fatalf("successful refresh response = %d %q", recorder.Code, recorder.Body.String())
	}
}
