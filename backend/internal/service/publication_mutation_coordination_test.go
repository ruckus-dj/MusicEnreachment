package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// cancelOnDoneContext gives a deterministic signal that a coordinator has
// registered a waiter: Acquire installs this context's Done channel as part of
// enqueueing its waiter. Cancellation then tests the actual service entrypoint.
type cancelOnDoneContext struct {
	context.Context
	done     chan struct{}
	once     sync.Once
	waiting  chan struct{}
	observed sync.Once
}

func newCancelOnDoneContext() *cancelOnDoneContext {
	return &cancelOnDoneContext{Context: context.Background(), done: make(chan struct{}), waiting: make(chan struct{})}
}

func (ctx *cancelOnDoneContext) Done() <-chan struct{} {
	ctx.observed.Do(func() { close(ctx.waiting) })
	return ctx.done
}
func (ctx *cancelOnDoneContext) Err() error {
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}
func (ctx *cancelOnDoneContext) cancel() { ctx.once.Do(func() { close(ctx.done) }) }

type coordinatedResetRepository struct {
	called chan struct{}
	once   sync.Once
}

func (r *coordinatedResetRepository) RunOutputResetRequest(context.Context, persistence.OutputResetRequest) (uuid.UUID, error) {
	r.once.Do(func() { close(r.called) })
	return uuid.New(), nil
}
func (*coordinatedResetRepository) RecoverOutputReset(context.Context, func(context.Context, persistence.OutputResetJournal) error) error {
	return nil
}

type coordinatedMetadataRepository struct {
	called chan struct{}
	once   sync.Once
}

func (r *coordinatedMetadataRepository) ReadMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadata, error) {
	return persistence.LibraryMetadata{Revision: 1, Envelope: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{}}}, nil
}
func (r *coordinatedMetadataRepository) UpdateMetadata(_ context.Context, scope librarymetadata.Scope, _, _ uuid.UUID, expected int64, envelope librarymetadata.Envelope) (persistence.LibraryMetadata, error) {
	r.once.Do(func() { close(r.called) })
	return persistence.LibraryMetadata{Revision: expected + 1, Envelope: envelope}, nil
}

func awaitMutationCall(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("mutation repository callback was not entered")
	}
}

func TestPublicationMetadataAndOutputResetShareMutationCoordinator(t *testing.T) {
	coordinator := publication.NewCoordinator()
	resource := []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}}

	metadataRepo := &coordinatedMetadataRepository{called: make(chan struct{})}
	metadata := NewLibraryMetadataService(metadataRepo).WithPublicationCoordinator(coordinator)
	resetRepo := &coordinatedResetRepository{called: make(chan struct{})}
	reset := NewOutputReset(resetRepo, settings.NewResetFilesystem(), nil).WithPublicationCoordinator(coordinator)
	reset.registry = settings.New(newMemoryStore(), nil)

	for _, operation := range []struct {
		name   string
		call   func(context.Context) error
		called <-chan struct{}
	}{
		{name: "metadata save", called: metadataRepo.called, call: func(ctx context.Context) error {
			_, err := metadata.Update(ctx, librarymetadata.ScopeRelease, uuid.New(), uuid.New(), 1, map[string]json.RawMessage{"comment": json.RawMessage(`{"state":"value","value":"saved"}`)})
			return err
		}},
		{name: "runtime output reset", called: resetRepo.called, call: func(ctx context.Context) error {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				return err
			}
			_, err = reset.ResetRuntime(ctx, "", "", root, settings.RuntimeUpdate{})
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			// Hold the exact process-wide resource the publication worker holds.
			release, err := coordinator.Acquire(context.Background(), resource)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
			ctx := newCancelOnDoneContext()
			result := make(chan error, 1)
			go func() { result <- operation.call(ctx) }()
			// Coordinator calls Done only after the waiter is enqueued. Therefore
			// repository entry cannot be racing this assertion.
			select {
			case <-ctx.waiting:
			case <-time.After(5 * time.Second):
				t.Fatal("service did not wait on publication mutation resource")
			case <-operation.called:
				t.Fatal("repository callback entered while publication resource held")
			}
			ctx.cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
			select {
			case <-operation.called:
				t.Fatal("repository callback entered after canceled acquisition")
			default:
			}
			release()

			if err := operation.call(context.Background()); err != nil {
				t.Fatalf("operation after release: %v", err)
			}
			awaitMutationCall(t, operation.called)
		})
	}
}
