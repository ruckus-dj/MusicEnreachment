package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type sourceCleanupRetryAPIRepository struct {
	state  persistence.PublicationSourceCleanupExplicitRetryState
	result persistence.PublicationSourceCleanupExplicitRetryResult
	err    error
	calls  int
	admits int
}

func (repository *sourceCleanupRetryAPIRepository) ReadPublicationSourceCleanupExplicitRetryState(context.Context, uuid.UUID) (persistence.PublicationSourceCleanupExplicitRetryState, error) {
	repository.calls++
	return repository.state, repository.err
}

func (repository *sourceCleanupRetryAPIRepository) AdmitPublicationSourceCleanupExplicitRetry(context.Context, uuid.UUID, persistence.RiverInserter) (persistence.PublicationSourceCleanupExplicitRetryResult, error) {
	repository.admits++
	return repository.result, nil
}

func (repository *sourceCleanupRetryAPIRepository) ReplacePublicationSourceCleanupExplicitRetry(context.Context, uuid.UUID, uuid.UUID, persistence.RiverInserter) (persistence.PublicationSourceCleanupExplicitRetryResult, error) {
	return repository.result, nil
}

type sourceCleanupRetryAPIRiver struct{}

func (sourceCleanupRetryAPIRiver) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return nil, errors.New("unexpected River insert")
}

type sourceCleanupRetryAPIOperationsRepository struct{ operation *persistence.Operation }

func (*sourceCleanupRetryAPIOperationsRepository) CreateOperation(context.Context, *persistence.Operation) error {
	return errors.New("unexpected operation create")
}
func (repository *sourceCleanupRetryAPIOperationsRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}
func (*sourceCleanupRetryAPIOperationsRepository) ListOperations(context.Context, ...string) ([]persistence.Operation, error) {
	return nil, nil
}
func (*sourceCleanupRetryAPIOperationsRepository) UpdateOperation(context.Context, *persistence.Operation) error {
	return errors.New("unexpected operation update")
}
func (*sourceCleanupRetryAPIOperationsRepository) TransitionOperation(context.Context, uuid.UUID, func(*persistence.Operation) error) error {
	return errors.New("unexpected operation transition")
}
func (*sourceCleanupRetryAPIOperationsRepository) DismissOperation(context.Context, uuid.UUID) error {
	return errors.New("unexpected operation dismissal")
}
func (*sourceCleanupRetryAPIOperationsRepository) DeleteSucceededBefore(context.Context, time.Time) error {
	return nil
}

func TestRetryPublicationSourceCleanupAcceptsDedicatedServiceResult(t *testing.T) {
	originalID, retryID := uuid.New(), uuid.New()
	repository := &sourceCleanupRetryAPIRepository{
		state: persistence.PublicationSourceCleanupExplicitRetryState{
			OperationID: originalID, Kind: "cleanup_publication_sources_v1",
			State: "failed", Stage: "cleanup_recovery_required", SnapshotValid: true,
		},
		result: persistence.PublicationSourceCleanupExplicitRetryResult{OperationID: retryID},
	}
	operations := service.NewOperations(&sourceCleanupRetryAPIOperationsRepository{operation: &persistence.Operation{
		ID: retryID, Kind: "retry_publication_sources_v1", State: "queued", Stage: "queued",
	}})
	handler := HandlerWithDependencies(Dependencies{
		Operations:           operations,
		SourceCleanupRetries: service.NewSourceCleanupRetries(repository, sourceCleanupRetryAPIRiver{}),
	})
	request := httptest.NewRequest(http.MethodPost, "/operations/"+originalID.String()+"/source-cleanup/retry", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want %d: %s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if repository.calls != 1 {
		t.Fatalf("retry selector calls=%d, want 1", repository.calls)
	}
	if repository.admits != 1 {
		t.Fatalf("retry admission calls=%d, want 1", repository.admits)
	}
	if !strings.Contains(response.Body.String(), retryID.String()) {
		t.Fatalf("accepted response does not contain the new retry snapshot: %s", response.Body.String())
	}
}

func TestRetryPublicationSourceCleanupRejectsMalformedIDAndNotFound(t *testing.T) {
	for _, test := range []struct {
		name       string
		pathID     string
		readErr    error
		wantStatus int
	}{
		{name: "malformed ID", pathID: "not-a-uuid", wantStatus: http.StatusUnprocessableEntity},
		{name: "missing original", pathID: uuid.NewString(), readErr: sql.ErrNoRows, wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &sourceCleanupRetryAPIRepository{err: test.readErr}
			handler := HandlerWithDependencies(Dependencies{
				Operations:           service.NewOperations(&sourceCleanupRetryAPIOperationsRepository{}),
				SourceCleanupRetries: service.NewSourceCleanupRetries(repository, sourceCleanupRetryAPIRiver{}),
			})
			request := httptest.NewRequest(http.MethodPost, "/operations/"+test.pathID+"/source-cleanup/retry", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.readErr == nil && repository.calls != 0 {
				t.Fatalf("retry selector calls=%d for malformed ID, want 0", repository.calls)
			}
		})
	}
}

func TestRetryPublicationSourceCleanupReturnsBlockersWithoutAdmission(t *testing.T) {
	originalID := uuid.New()
	repository := &sourceCleanupRetryAPIRepository{state: persistence.PublicationSourceCleanupExplicitRetryState{
		OperationID: originalID, Kind: "cleanup_publication_sources_v1",
		State: "running", Stage: "cleanup_recovery_required", SnapshotValid: true,
	}}
	handler := HandlerWithDependencies(Dependencies{
		Operations:           service.NewOperations(&sourceCleanupRetryAPIOperationsRepository{}),
		SourceCleanupRetries: service.NewSourceCleanupRetries(repository, sourceCleanupRetryAPIRiver{}),
	})
	request := httptest.NewRequest(http.MethodPost, "/operations/"+originalID.String()+"/source-cleanup/retry", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "original cleanup is not in a valid recovery-required state") {
		t.Fatalf("response omitted safe blocker: %s", response.Body.String())
	}
	if repository.admits != 0 {
		t.Fatalf("retry admission calls=%d for blocked operation, want 0", repository.admits)
	}
}

func TestRetryPublicationSourceCleanupRequiresOperationService(t *testing.T) {
	originalID := uuid.New()
	repository := &sourceCleanupRetryAPIRepository{state: persistence.PublicationSourceCleanupExplicitRetryState{
		OperationID: originalID, Kind: "cleanup_publication_sources_v1",
		State: "failed", Stage: "cleanup_recovery_required", SnapshotValid: true,
	}, result: persistence.PublicationSourceCleanupExplicitRetryResult{OperationID: uuid.New()}}
	handler := HandlerWithDependencies(Dependencies{
		SourceCleanupRetries: service.NewSourceCleanupRetries(repository, sourceCleanupRetryAPIRiver{}),
	})
	request := httptest.NewRequest(http.MethodPost, "/operations/"+originalID.String()+"/source-cleanup/retry", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if repository.calls != 0 || repository.admits != 0 {
		t.Fatalf("cleanup retry service calls=%d admits=%d without an operation service, want 0", repository.calls, repository.admits)
	}
}
