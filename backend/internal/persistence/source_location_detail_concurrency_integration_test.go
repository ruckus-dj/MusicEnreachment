//go:build integration

package persistence_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

// TestSourceLocationDetailIsOneSnapshotAcrossAnalysisApplyWithPostgreSQL
// establishes the detail reader's snapshot before the first atomic apply commits.
// It must return the old state (no result, still-active operation), never a mixed
// view containing neither.

// TestSourceLocationDetailSurvivesConcurrentOrphanVariantDeleteWithPostgreSQL
// deletes a genuinely unheld variant after the reader has established its
// snapshot and reached the variant query. The read must neither lock the row nor
// fail because the concurrent unlink/delete committed.

type pausingQueryBarrier struct {
	needle    string
	armed     atomic.Bool
	once      sync.Once
	attempted chan struct{}
	release   chan struct{}
}

func newPausingQueryBarrier(needle string) *pausingQueryBarrier {
	return &pausingQueryBarrier{needle: needle, attempted: make(chan struct{}), release: make(chan struct{})}
}

func (b *pausingQueryBarrier) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if b.armed.Load() && strings.Contains(event.Query, b.needle) {
		b.once.Do(func() { close(b.attempted) })
		<-b.release
	}
	return ctx
}

func (*pausingQueryBarrier) AfterQuery(context.Context, *bun.QueryEvent) {}

func awaitValue[T any](t *testing.T, result <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(analysisRaceTimeout):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}
