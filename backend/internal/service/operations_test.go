package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"testing"
	"time"
)

type memoryOperations struct {
	values map[uuid.UUID]*persistence.Operation
}

func (m *memoryOperations) CreateOperation(_ context.Context, o *persistence.Operation) error {
	if m.values == nil {
		m.values = map[uuid.UUID]*persistence.Operation{}
	}
	m.values[o.ID] = o
	return nil
}
func (m *memoryOperations) GetOperation(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
	if o := m.values[id]; o != nil {
		return o, nil
	}
	return nil, fmt.Errorf("missing")
}
func (m *memoryOperations) UpdateOperation(_ context.Context, o *persistence.Operation) error {
	m.values[o.ID] = o
	return nil
}
func (m *memoryOperations) TransitionOperation(_ context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	o, err := m.GetOperation(context.Background(), id)
	if err != nil {
		return err
	}
	return transition(o)
}
func (*memoryOperations) DismissOperation(context.Context, uuid.UUID) error      { return nil }
func (*memoryOperations) DeleteSucceededBefore(context.Context, time.Time) error { return nil }
func TestOperationTransitionsAndRetry(t *testing.T) {
	ctx := context.Background()
	operations := NewOperations(&memoryOperations{})
	operation, err := operations.Start(ctx, "install", "download", map[string]string{"target_identity": "fpcalc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := operations.Running(ctx, operation.ID, "download"); err != nil {
		t.Fatal(err)
	}
	if err := operations.Fail(ctx, operation.ID, "verify", "safe failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Retry(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	if operation.State != "queued" {
		t.Fatalf("state = %s", operation.State)
	}
}
