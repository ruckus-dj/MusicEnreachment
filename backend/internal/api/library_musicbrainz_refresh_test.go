package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type musicBrainzRefreshRepositoryFixture struct {
	mbid string
	err  error
}

func (fixture musicBrainzRefreshRepositoryFixture) ReadMusicBrainzReleaseBinding(context.Context, uuid.UUID) (string, error) {
	return fixture.mbid, fixture.err
}

type musicBrainzRefreshStarterFixture struct {
	operation *persistence.Operation
	releaseID uuid.UUID
	mbid      string
	calls     int
}

func (fixture *musicBrainzRefreshStarterFixture) StartMusicBrainzReleaseRefresh(_ context.Context, releaseID uuid.UUID, mbid string) (*persistence.Operation, error) {
	fixture.releaseID, fixture.mbid = releaseID, mbid
	fixture.calls++
	return fixture.operation, nil
}

type musicBrainzRefreshOperationRepository struct{ operation *persistence.Operation }

func (repository musicBrainzRefreshOperationRepository) CreateOperation(context.Context, *persistence.Operation) error {
	return errors.New("unexpected operation create")
}
func (repository musicBrainzRefreshOperationRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}
func (musicBrainzRefreshOperationRepository) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	return nil, nil
}
func (musicBrainzRefreshOperationRepository) UpdateOperation(context.Context, *persistence.Operation) error {
	return errors.New("unexpected operation update")
}
func (musicBrainzRefreshOperationRepository) TransitionOperation(context.Context, uuid.UUID, func(*persistence.Operation) error) error {
	return errors.New("unexpected operation transition")
}
func (musicBrainzRefreshOperationRepository) DismissOperation(context.Context, uuid.UUID) error {
	return errors.New("unexpected operation dismiss")
}
func (musicBrainzRefreshOperationRepository) DeleteSucceededBefore(context.Context, time.Time) error {
	return errors.New("unexpected operation cleanup")
}

func TestLibraryMusicBrainzRefreshHTTP(t *testing.T) {
	releaseID, mbid, operationID := uuid.New(), uuid.New(), uuid.New()
	operation := &persistence.Operation{ID: operationID, Kind: "provider_release_lookup", State: "queued", Stage: "queued", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	starter := &musicBrainzRefreshStarterFixture{operation: operation}
	refresh := service.NewLibraryProviderRefresh(musicBrainzRefreshRepositoryFixture{mbid: mbid.String()}, starter)
	operations := service.NewOperations(musicBrainzRefreshOperationRepository{operation: operation})
	handler := HandlerWithDependencies(Dependencies{LibraryProviderRefresh: refresh, Operations: operations})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/musicbrainz-refresh", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d: %s", response.Code, response.Body)
	}
	var body struct {
		ID    uuid.UUID `json:"id"`
		Kind  string    `json:"kind"`
		State string    `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != operationID || body.Kind != operation.Kind || body.State != operation.State {
		t.Fatalf("operation response = %+v", body)
	}
	if starter.calls != 1 || starter.releaseID != releaseID || starter.mbid != mbid.String() {
		t.Fatalf("refresh starter got calls=%d release=%s MBID=%q", starter.calls, starter.releaseID, starter.mbid)
	}
}

func TestLibraryMusicBrainzRefreshHTTPRejectsMissingOrUnboundRelease(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		mbid       string
		repository error
		wantStatus int
	}{
		{name: "missing release", repository: persistence.ErrLibraryReleaseNotFound, wantStatus: http.StatusNotFound},
		{name: "wrong provider or no binding", wantStatus: http.StatusConflict},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			starter := &musicBrainzRefreshStarterFixture{}
			refresh := service.NewLibraryProviderRefresh(musicBrainzRefreshRepositoryFixture{mbid: scenario.mbid, err: scenario.repository}, starter)
			handler := HandlerWithDependencies(Dependencies{LibraryProviderRefresh: refresh, Operations: service.NewOperations(musicBrainzRefreshOperationRepository{})})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/library/releases/"+uuid.NewString()+"/musicbrainz-refresh", nil))
			if response.Code != scenario.wantStatus {
				t.Fatalf("POST status = %d: %s, want %d", response.Code, response.Body, scenario.wantStatus)
			}
			if starter.calls != 0 {
				t.Fatalf("refresh started without a valid binding: %d calls", starter.calls)
			}
		})
	}
}
