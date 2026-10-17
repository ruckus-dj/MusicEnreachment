package service

import (
	"context"
	"errors"
	"sync"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

// PublicationCommandResult reports process output and whether the runner has
// explicitly established that the process can no longer mutate its inputs.
// Output capture alone is not evidence of process termination.
type PublicationCommandResult struct {
	Output                []byte
	QuiescenceEstablished bool
}

// PublicationCommandRunner must report explicit quiescence for each command.
// A false value is unknown, not an implicit success.
type PublicationCommandRunner interface {
	Run(context.Context, string, ...string) (PublicationCommandResult, error)
}

// BoundedOutputCollector retains at most its configured number of bytes while
// continuing to accept and count all writes. It is only an output collector;
// it provides no process-lifecycle guarantee.
type BoundedOutputCollector struct {
	mu       sync.Mutex
	limit    int
	output   []byte
	total    uint64
	overflow bool
}

func NewBoundedOutputCollector(limit int) (*BoundedOutputCollector, error) {
	if limit <= 0 {
		return nil, errors.New("publication command output limit must be positive")
	}
	return &BoundedOutputCollector{limit: limit, output: make([]byte, 0, limit)}, nil
}

func (collector *BoundedOutputCollector) Write(data []byte) (int, error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	remaining := collector.limit - len(collector.output)
	if remaining > len(data) {
		remaining = len(data)
	}
	collector.output = append(collector.output, data[:remaining]...)
	if remaining != len(data) {
		collector.overflow = true
	}
	collector.total += uint64(len(data))
	return len(data), nil
}

// Snapshot returns a defensive copy and the total bytes accepted and whether
// the retained output was truncated.
func (collector *BoundedOutputCollector) Snapshot() (output []byte, total uint64, overflow bool) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return append([]byte(nil), collector.output...), collector.total, collector.overflow
}

// publicationCommandAdapter exposes a publication scope to legacy tool
// consumers. It discards only the quiescence bit after the scope has recorded
// it; callers still receive command output and errors.
type publicationCommandAdapter struct{ scope *PublicationExecutionScope }

func (adapter publicationCommandAdapter) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if adapter.scope == nil {
		return nil, errors.New("publication execution scope is unavailable")
	}
	result, err := adapter.scope.Run(ctx, path, args...)
	return result.Output, err
}

var _ tools.CommandRunner = publicationCommandAdapter{}

func unavailablePublicationCommand() (PublicationCommandResult, error) {
	return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("publication command backend is unavailable")
}
