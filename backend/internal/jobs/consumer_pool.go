package jobs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const maxRiverQueueWorkers = 10000

type AnalysisConsumer interface {
	Stop(context.Context) error
}

type analysisConsumer = AnalysisConsumer

type analysisConsumerFactory func(context.Context, int) (analysisConsumer, error)

// AnalysisConsumerPool adds independent River clients as queued analysis work
// demands capacity. The worker's shared file limiter remains authoritative.
type AnalysisConsumerPool struct {
	mu         sync.Mutex
	base       int
	clients    []*analysisConsumerGroup
	retiring   map[*analysisConsumerGroup]struct{}
	factory    analysisConsumerFactory
	generation atomic.Uint64
	wake       chan struct{}
}

type analysisConsumerGroup struct {
	capacity        int
	client          analysisConsumer
	retiringAttempt bool
	attemptDone     chan struct{}
}

func NewAnalysisConsumerPool(base int, factory analysisConsumerFactory) (*AnalysisConsumerPool, error) {
	if base < 1 || base > maxRiverQueueWorkers {
		return nil, fmt.Errorf("primary source analysis capacity must be between 1 and %d", maxRiverQueueWorkers)
	}
	if factory == nil {
		return nil, fmt.Errorf("source analysis consumer factory is required")
	}
	return &AnalysisConsumerPool{base: base, factory: factory, retiring: make(map[*analysisConsumerGroup]struct{}), wake: make(chan struct{}, 1)}, nil
}

// Wake schedules a fresh settings/demand read. It deliberately carries no
// value: commit notifications can be coalesced without replaying stale limits.
func (pool *AnalysisConsumerPool) Wake() {
	pool.generation.Add(1)
	select {
	case pool.wake <- struct{}{}:
	default:
	}
}

// Wakeups returns a coalescing wake channel for the app-owned controller.
func (pool *AnalysisConsumerPool) Wakeups() <-chan struct{} { return pool.wake }

// SetLimit reconciles capacity synchronously for compatibility with callers
// that already hold a freshly-read limit. Shrink drains happen asynchronously.
func (pool *AnalysisConsumerPool) SetLimit(ctx context.Context, limit int) error {
	return pool.SetDemand(ctx, limit, maxRiverQueueWorkers*maxRiverQueueWorkers)
}

// SetDemand caps extra worker capacity by currently available queue demand.
func (pool *AnalysisConsumerPool) SetDemand(ctx context.Context, limit, demand int) error {
	if limit < 1 || demand < 0 {
		return fmt.Errorf("source file concurrency and demand must be non-negative")
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.reconcileLocked(ctx, limit, demand, 0, false)
}

// SetDemandForGeneration refuses a read made obsolete by a newer commit wake.
func (pool *AnalysisConsumerPool) SetDemandForGeneration(ctx context.Context, generation uint64, limit, demand int) error {
	if limit < 1 || demand < 0 {
		return fmt.Errorf("source file concurrency and demand must be non-negative")
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if generation != pool.generation.Load() {
		return nil
	}
	return pool.reconcileLocked(ctx, limit, demand, generation, true)
}

func (pool *AnalysisConsumerPool) Generation() uint64 {
	return pool.generation.Load()
}

func (pool *AnalysisConsumerPool) reconcileLocked(ctx context.Context, limit, demand int, generation uint64, guarded bool) error {
	for group := range pool.retiring {
		pool.startRetirementLocked(group)
	}
	remaining := limit - pool.base
	if remaining < 0 {
		remaining = 0
	}
	extraDemand := demand - pool.base
	if extraDemand < 0 {
		extraDemand = 0
	}
	if extraDemand < remaining {
		remaining = extraDemand
	}
	wanted := remaining / maxRiverQueueWorkers
	if remaining%maxRiverQueueWorkers != 0 {
		wanted++
	}
	capacities := make([]int, wanted)
	for i := range capacities {
		capacities[i] = remaining - i*maxRiverQueueWorkers
		if capacities[i] > maxRiverQueueWorkers {
			capacities[i] = maxRiverQueueWorkers
		}
	}
	// Remove excess clients from the active set immediately; Stop can take as
	// long as an in-flight River job and must never block a settings request.
	for len(pool.clients) > wanted {
		last := len(pool.clients) - 1
		group := pool.clients[last]
		pool.clients = pool.clients[:last]
		pool.retiring[group] = struct{}{}
		pool.startRetirementLocked(group)
	}
	for i := 0; i < wanted; i++ {
		if i < len(pool.clients) && pool.clients[i].capacity == capacities[i] {
			continue
		}
		if i < len(pool.clients) {
			// A replacement invalidates this consumer and every later
			// capacity: keeping the suffix would silently lose its handles
			// when the active slice is shortened.
			groups := append([]*analysisConsumerGroup(nil), pool.clients[i:]...)
			pool.clients = pool.clients[:i]
			for _, group := range groups {
				pool.retiring[group] = struct{}{}
				pool.startRetirementLocked(group)
			}
		}
		if guarded && generation != pool.generation.Load() {
			return nil
		}
		client, err := pool.factory(ctx, capacities[i])
		if err != nil {
			return fmt.Errorf("add source analysis consumer: %w", err)
		}
		group := &analysisConsumerGroup{capacity: capacities[i], client: client}
		if guarded && generation != pool.generation.Load() {
			pool.retiring[group] = struct{}{}
			pool.startRetirementLocked(group)
			return nil
		}
		pool.clients = append(pool.clients, group)
	}
	return nil
}

// startRetirementLocked is called with pool.mu held. At most one asynchronous
// Stop attempt may be queued or in flight for a group.
func (pool *AnalysisConsumerPool) startRetirementLocked(group *analysisConsumerGroup) {
	if _, retiring := pool.retiring[group]; !retiring || group.retiringAttempt {
		return
	}
	group.retiringAttempt = true
	group.attemptDone = make(chan struct{})
	go pool.stopRetiring(group, group.attemptDone)
}

func (pool *AnalysisConsumerPool) stopRetiring(group *analysisConsumerGroup, done chan struct{}) {
	// An error does not transfer ownership away from the pool. Retain the handle
	// so a later reconciliation or shutdown can retry.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := group.client.Stop(ctx)
	pool.mu.Lock()
	group.retiringAttempt = false
	group.attemptDone = nil
	if err == nil {
		delete(pool.retiring, group)
	}
	close(done)
	pool.mu.Unlock()
}

func (pool *AnalysisConsumerPool) Stop(ctx context.Context) error {
	pool.mu.Lock()
	for _, group := range pool.clients {
		pool.retiring[group] = struct{}{}
	}
	groups := make([]*analysisConsumerGroup, 0, len(pool.retiring))
	for group := range pool.retiring {
		groups = append(groups, group)
	}
	pool.clients = nil
	pool.mu.Unlock()
	var firstErr error
	for _, group := range groups {
		for {
			pool.mu.Lock()
			if _, exists := pool.retiring[group]; !exists {
				pool.mu.Unlock()
				break
			}
			if group.retiringAttempt {
				done := group.attemptDone
				pool.mu.Unlock()
				select {
				case <-done:
					continue
				case <-ctx.Done():
					if firstErr == nil {
						firstErr = ctx.Err()
					}
				}
				break
			}
			group.retiringAttempt = true
			group.attemptDone = make(chan struct{})
			done := group.attemptDone
			pool.mu.Unlock()
			err := group.client.Stop(ctx)
			pool.mu.Lock()
			group.retiringAttempt = false
			group.attemptDone = nil
			if err == nil {
				delete(pool.retiring, group)
			}
			close(done)
			pool.mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			break
		}
	}
	return firstErr
}
