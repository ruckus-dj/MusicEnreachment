// Package service coordinates application use cases without HTTP concerns.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type OperationRepository interface {
	CreateOperation(context.Context, *persistence.Operation) error
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	UpdateOperation(context.Context, *persistence.Operation) error
	DismissOperation(context.Context, uuid.UUID) error
	DeleteSucceededBefore(context.Context, time.Time) error
}

// Operations provides the REST source of truth; Subscribe is only a wake-up
// signal, so reconnecting clients must re-read the operation snapshot.
type Operations struct {
	repository OperationRepository
	mu         sync.Mutex
	watchers   map[uuid.UUID]map[chan struct{}]struct{}
	now        func() time.Time
}

func NewOperations(repository OperationRepository) *Operations {
	return &Operations{repository: repository, watchers: map[uuid.UUID]map[chan struct{}]struct{}{}, now: time.Now}
}
func (s *Operations) Start(ctx context.Context, kind, stage string, snapshot any) (*persistence.Operation, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	operation := &persistence.Operation{ID: uuid.New(), Kind: kind, State: "queued", Stage: stage, InputSnapshot: raw}
	if err = s.repository.CreateOperation(ctx, operation); err != nil {
		return nil, err
	}
	s.notify(operation.ID)
	return operation, nil
}
func (s *Operations) Get(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	return s.repository.GetOperation(ctx, id)
}
func (s *Operations) Running(ctx context.Context, id uuid.UUID, stage string) error {
	return s.transition(ctx, id, "running", stage, "", nil)
}
func (s *Operations) Progress(ctx context.Context, id uuid.UUID, stage string, completed int64, total *int64) error {
	return s.transition(ctx, id, "running", stage, "", func(o *persistence.Operation) { o.BytesCompleted = completed; o.BytesTotal = total })
}
func (s *Operations) Succeed(ctx context.Context, id uuid.UUID, stage string) error {
	return s.transition(ctx, id, "succeeded", stage, "", nil)
}
func (s *Operations) Fail(ctx context.Context, id uuid.UUID, stage, safe string) error {
	if safe == "" {
		return fmt.Errorf("operation failures require a safe error")
	}
	return s.transition(ctx, id, "failed", stage, safe, nil)
}
func (s *Operations) Retry(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	existing, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.State != "failed" {
		return nil, fmt.Errorf("only failed operations can be retried")
	}
	existing.State = "queued"
	existing.Stage = "retry"
	existing.SafeError = nil
	existing.StartedAt = nil
	existing.FinishedAt = nil
	existing.BytesCompleted = 0
	if err := s.repository.UpdateOperation(ctx, existing); err != nil {
		return nil, err
	}
	s.notify(id)
	return existing, nil
}
func (s *Operations) Dismiss(ctx context.Context, id uuid.UUID) error {
	return s.repository.DismissOperation(ctx, id)
}
func (s *Operations) Cleanup(ctx context.Context) error {
	return s.repository.DeleteSucceededBefore(ctx, s.now().Add(-24*time.Hour))
}
func (s *Operations) transition(ctx context.Context, id uuid.UUID, state, stage, safe string, modify func(*persistence.Operation)) error {
	o, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if o.State == "succeeded" || o.State == "failed" {
		return fmt.Errorf("operation is already final")
	}
	now := s.now()
	o.State = state
	o.Stage = stage
	if state == "running" && o.StartedAt == nil {
		o.StartedAt = &now
	}
	if state == "failed" {
		o.SafeError = &safe
		o.FinishedAt = &now
	}
	if state == "succeeded" {
		o.FinishedAt = &now
	}
	if modify != nil {
		modify(o)
	}
	if err := s.repository.UpdateOperation(ctx, o); err != nil {
		return err
	}
	s.notify(id)
	return nil
}
func (s *Operations) Subscribe(id uuid.UUID) (<-chan struct{}, func()) {
	channel := make(chan struct{}, 1)
	s.mu.Lock()
	if s.watchers[id] == nil {
		s.watchers[id] = map[chan struct{}]struct{}{}
	}
	s.watchers[id][channel] = struct{}{}
	s.mu.Unlock()
	return channel, func() {
		s.mu.Lock()
		delete(s.watchers[id], channel)
		if len(s.watchers[id]) == 0 {
			delete(s.watchers, id)
		}
		close(channel)
		s.mu.Unlock()
	}
}
func (s *Operations) notify(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for channel := range s.watchers[id] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}
