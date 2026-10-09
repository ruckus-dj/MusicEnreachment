package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testAnalysisConsumer struct {
	mu      sync.Mutex
	stopped int
	stopErr error
	done    chan struct{}
}

func (consumer *testAnalysisConsumer) Stop(context.Context) error {
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	consumer.stopped++
	if consumer.stopErr != nil {
		return consumer.stopErr
	}
	if consumer.done != nil {
		select {
		case <-consumer.done:
		default:
			close(consumer.done)
		}
	}
	return nil
}

func TestAnalysisConsumerPoolAddsAndDrainsChunks(t *testing.T) {
	var capacities []int
	var consumers []*testAnalysisConsumer
	pool, err := NewAnalysisConsumerPool(4, func(_ context.Context, capacity int) (analysisConsumer, error) {
		capacities = append(capacities, capacity)
		consumer := &testAnalysisConsumer{}
		consumers = append(consumers, consumer)
		return consumer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.SetLimit(context.Background(), maxRiverQueueWorkers+9); err != nil {
		t.Fatal(err)
	}
	if len(capacities) != 2 || capacities[0] != maxRiverQueueWorkers || capacities[1] != 5 {
		t.Fatalf("unexpected consumer capacities: %v", capacities)
	}
	if err := pool.SetLimit(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		consumers[0].mu.Lock()
		first := consumers[0].stopped
		consumers[0].mu.Unlock()
		consumers[1].mu.Lock()
		second := consumers[1].stopped
		consumers[1].mu.Unlock()
		if first == 1 && second == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retiring consumers were not stopped")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestAnalysisConsumerPoolReplacementRetiresEntireSuffix(t *testing.T) {
	var consumers []*testAnalysisConsumer
	pool, err := NewAnalysisConsumerPool(1, func(context.Context, int) (analysisConsumer, error) {
		consumer := &testAnalysisConsumer{}
		consumers = append(consumers, consumer)
		return consumer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.SetLimit(context.Background(), 2*maxRiverQueueWorkers+2); err != nil {
		t.Fatal(err)
	}
	if len(consumers) != 3 {
		t.Fatalf("expected three consumers, got %d", len(consumers))
	}
	if err := pool.SetLimit(context.Background(), maxRiverQueueWorkers+2); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(time.Second)
	for {
		stopped := make([]int, len(consumers))
		for i, consumer := range consumers {
			consumer.mu.Lock()
			stopped[i] = consumer.stopped
			consumer.mu.Unlock()
		}
		if stopped[1] == 1 && stopped[2] == 1 {
			if stopped[0] != 0 {
				t.Fatalf("unchanged consumer was retired: %v", stopped)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("replacement suffix was not fully retired: %v", stopped)
		case <-time.After(time.Millisecond):
		}
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, consumer := range consumers {
		consumer.mu.Lock()
		stopped := consumer.stopped
		consumer.mu.Unlock()
		if stopped != 1 {
			t.Fatalf("consumer %d stopped %d times, want 1", i, stopped)
		}
	}
}

func TestAnalysisConsumerPoolRetriesFailedDrainAtShutdown(t *testing.T) {
	failure := errors.New("stop timed out")
	consumer := &testAnalysisConsumer{stopErr: failure}
	pool, err := NewAnalysisConsumerPool(1, func(context.Context, int) (analysisConsumer, error) { return consumer, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.SetLimit(context.Background(), maxRiverQueueWorkers+1); err != nil {
		t.Fatal(err)
	}
	if err := pool.SetLimit(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		consumer.mu.Lock()
		attempts := consumer.stopped
		consumer.mu.Unlock()
		if attempts != 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retiring consumer was not asked to stop")
		case <-time.After(time.Millisecond):
		}
	}
	consumer.mu.Lock()
	consumer.stopErr = nil
	consumer.mu.Unlock()
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("shutdown retry failed: %v", err)
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if consumer.stopped < 2 {
		t.Fatalf("failed stop handle was not retained for retry: %d attempts", consumer.stopped)
	}
}

func TestAnalysisConsumerPoolRetainsPartialGrowthOnFailure(t *testing.T) {
	failure := errors.New("start failed")
	var calls int
	var consumers []*testAnalysisConsumer
	pool, err := NewAnalysisConsumerPool(1, func(_ context.Context, _ int) (analysisConsumer, error) {
		calls++
		if calls == 2 {
			return nil, failure
		}
		consumer := &testAnalysisConsumer{}
		consumers = append(consumers, consumer)
		return consumer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.SetLimit(context.Background(), maxRiverQueueWorkers+2); err == nil {
		t.Fatal("expected consumer startup failure")
	}
	consumers[0].mu.Lock()
	stopped := consumers[0].stopped
	consumers[0].mu.Unlock()
	if len(consumers) != 1 || stopped != 0 {
		t.Fatalf("partially created consumer ownership was lost: %+v", consumers)
	}
	if err := pool.SetLimit(context.Background(), 1); err != nil {
		t.Fatalf("failed growth changed the active pool: %v", err)
	}
	deadline := time.After(time.Second)
	for {
		consumers[0].mu.Lock()
		stopped := consumers[0].stopped
		consumers[0].mu.Unlock()
		if stopped == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retained consumer was not drained")
		case <-time.After(time.Millisecond):
		}
	}
}
