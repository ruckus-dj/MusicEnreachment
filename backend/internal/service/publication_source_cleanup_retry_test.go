package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type countingRetryInserter struct {
	inserts int
}

func (inserter *countingRetryInserter) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	inserter.inserts++
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: 1}}, nil
}

// TestPublicationSourceCleanupRetryIsRejectedBeforeMutation pins the owner
// decision that publication source cleanup has no generic retry: both entry
// paths refuse it before changing state, attempt, or a River delivery, whatever
// the current state is.
func TestPublicationSourceCleanupRetryIsRejectedBeforeMutation(t *testing.T) {
	for _, kind := range []string{publicationSourceCleanupOperationKind, publicationSourceCleanupRecoveryOperationKind} {
		for _, state := range []string{"failed", "running", "queued"} {
			for _, enqueued := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/enqueued=%t", kind, state, enqueued), func(t *testing.T) {
					ctx := context.Background()
					jobID := int64(77)
					safe := "cleanup delivery failed"
					started := time.Now().UTC()
					existing := &persistence.Operation{
						ID: uuid.New(), Kind: kind,
						State: state, Stage: "unlink", Attempt: 3, RiverJobID: &jobID,
						SafeError: &safe, StartedAt: &started, InputSnapshot: []byte(`{"version":1}`),
					}
					repository := &riverMemoryOperations{memoryOperations: &memoryOperations{
						values: map[uuid.UUID]*persistence.Operation{existing.ID: existing},
					}}
					inserter := &countingRetryInserter{}
					var operations *Operations
					if enqueued {
						operations = NewOperationsWithRiver(repository, inserter)
					} else {
						operations = NewOperations(repository)
					}

					got, err := operations.Retry(ctx, existing.ID)
					if err == nil || got != nil {
						t.Fatalf("cleanup retry = %v, %v; want rejection", got, err)
					}
					if strings.TrimSpace(err.Error()) == "" || !strings.Contains(err.Error(), "explicit recovery") {
						t.Fatalf("cleanup retry error = %q; want a nonempty explicit recovery message", err)
					}
					if repository.updates != 0 || repository.transitions != 0 {
						t.Fatalf("cleanup retry mutated the operation: updates=%d transitions=%d", repository.updates, repository.transitions)
					}
					if repository.genericRetries != 0 {
						t.Fatalf("cleanup retry reached the generic enqueue %d times", repository.genericRetries)
					}
					if inserter.inserts != 0 {
						t.Fatalf("cleanup retry inserted %d River jobs", inserter.inserts)
					}
					if existing.State != state || existing.Stage != "unlink" || existing.Attempt != 3 ||
						existing.RiverJobID == nil || *existing.RiverJobID != jobID ||
						existing.SafeError == nil || *existing.SafeError != safe {
						t.Fatalf("cleanup retry changed the operation: %+v", existing)
					}
				})
			}
		}
	}
}

// TestGenericPublicationRetryStillDelegates keeps the ordinary publication
// retry on the generic enqueue path.
func TestGenericPublicationRetryStillDelegates(t *testing.T) {
	existing := &persistence.Operation{ID: uuid.New(), Kind: "publish_track_v1", State: "failed", Stage: "failed", Attempt: 2}
	repository := &riverMemoryOperations{memoryOperations: &memoryOperations{
		values: map[uuid.UUID]*persistence.Operation{existing.ID: existing},
	}}
	operations := NewOperationsWithRiver(repository, &countingRetryInserter{})

	if _, err := operations.Retry(context.Background(), existing.ID); err != nil {
		t.Fatalf("publication retry: %v", err)
	}
	if repository.genericRetries != 1 {
		t.Fatalf("generic retry count = %d, want 1", repository.genericRetries)
	}
}
