//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestIncomingMatchEvaluationCASDistinguishesNeverAndEmptyAndSerializesWriters(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation(nil)

	if err := repository.ReplaceIfCurrent(ctx, evaluation, map[uuid.UUID]*string{fixture.group.ID: nil}); err != nil {
		t.Fatalf("publish initial evaluated-empty result: %v", err)
	}
	header, candidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if header.EvaluationRevision == "" || len(candidates) != 0 {
		t.Fatalf("evaluated-empty header = %#v candidates=%#v", header, candidates)
	}
	if err := repository.ReplaceIfCurrent(ctx, evaluation, map[uuid.UUID]*string{fixture.group.ID: nil}); !errors.Is(err, persistence.ErrIncomingMatchConflict) {
		t.Fatalf("nil prior revision against evaluated-empty result = %v, want conflict", err)
	}

	prior := header.EvaluationRevision
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- repository.ReplaceIfCurrent(ctx, evaluation, map[uuid.UUID]*string{fixture.group.ID: &prior})
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, persistence.ErrIncomingMatchConflict):
			conflicts++
		default:
			t.Fatalf("concurrent CAS returned unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS outcomes = %d successes, %d conflicts", successes, conflicts)
	}
	updated, _, err := repository.Get(ctx, fixture.group.ID)
	if err != nil || updated == nil || updated.EvaluationRevision == prior {
		t.Fatalf("evaluation revision did not advance: %#v, err=%v", updated, err)
	}
}
