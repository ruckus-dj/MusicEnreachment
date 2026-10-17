package publication

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// LockMode describes the access a publication operation needs to a resource.
type LockMode uint8

const (
	Shared LockMode = iota
	Exclusive
)

// ResourceLock identifies a process-local publication resource and its access mode.
type ResourceLock struct {
	Key  string
	Mode LockMode
}

// Coordinator serializes publication mutations within one process. It does not
// provide cross-process or durable fencing; callers must keep the returned
// release function until all work protected by the lock has completed.
type Coordinator struct {
	mu    sync.Mutex
	locks map[string]*resourceState
}

type resourceState struct {
	readers int
	writer  bool
	waiters []*waiter
}

type waiter struct {
	mode    LockMode
	ready   chan struct{}
	granted bool
}

// NewCoordinator creates an empty process-local publication coordinator.
func NewCoordinator() *Coordinator {
	return &Coordinator{locks: make(map[string]*resourceState)}
}

// Acquire acquires all requested resources in stable key order. Duplicate keys
// are merged, with Exclusive taking precedence over Shared. Cancellation while
// waiting releases any earlier acquisitions; cancellation after success does
// not release the resources, which remain held until the returned function is
// called.
func (c *Coordinator) Acquire(ctx context.Context, resources []ResourceLock) (func(), error) {
	merged := make(map[string]LockMode, len(resources))
	for _, resource := range resources {
		if resource.Key == "" {
			return nil, fmt.Errorf("publication resource key must not be empty")
		}
		if resource.Mode != Shared && resource.Mode != Exclusive {
			return nil, fmt.Errorf("invalid publication resource lock mode %d", resource.Mode)
		}
		if mode, exists := merged[resource.Key]; !exists || resource.Mode > mode {
			merged[resource.Key] = resource.Mode
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	held := make([]ResourceLock, 0, len(keys))
	for _, key := range keys {
		if err := c.acquireOne(ctx, ResourceLock{Key: key, Mode: merged[key]}); err != nil {
			c.release(held)
			return nil, err
		}
		held = append(held, ResourceLock{Key: key, Mode: merged[key]})
	}
	var once sync.Once
	return func() { once.Do(func() { c.release(held) }) }, nil
}

func (c *Coordinator) acquireOne(ctx context.Context, resource ResourceLock) error {
	c.mu.Lock()
	state := c.locks[resource.Key]
	if state == nil {
		state = &resourceState{}
		c.locks[resource.Key] = state
	}
	w := &waiter{mode: resource.Mode, ready: make(chan struct{})}
	state.waiters = append(state.waiters, w)
	c.dispatch(state)
	c.mu.Unlock()

	select {
	case <-w.ready:
		if err := ctx.Err(); err != nil {
			c.mu.Lock()
			if w.granted {
				c.ungrant(state, w.mode)
			}
			c.dispatch(state)
			c.cleanup(resource.Key, state)
			c.mu.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		if w.granted {
			c.ungrant(state, w.mode)
		} else {
			for i, queued := range state.waiters {
				if queued == w {
					state.waiters = append(state.waiters[:i], state.waiters[i+1:]...)
					break
				}
			}
		}
		c.dispatch(state)
		c.cleanup(resource.Key, state)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// dispatch grants a queued writer alone, or the contiguous reader batch at the
// head. A waiting writer therefore prevents later readers from starving it.
func (c *Coordinator) dispatch(state *resourceState) {
	if state.writer || len(state.waiters) == 0 {
		return
	}
	if state.readers > 0 && state.waiters[0].mode == Exclusive {
		return
	}
	if state.waiters[0].mode == Exclusive {
		w := state.waiters[0]
		state.waiters = state.waiters[1:]
		state.writer, w.granted = true, true
		close(w.ready)
		return
	}
	for len(state.waiters) > 0 && state.waiters[0].mode == Shared {
		w := state.waiters[0]
		state.waiters = state.waiters[1:]
		state.readers++
		w.granted = true
		close(w.ready)
	}
}

func (c *Coordinator) ungrant(state *resourceState, mode LockMode) {
	if mode == Exclusive {
		state.writer = false
	} else {
		state.readers--
	}
}

func (c *Coordinator) release(resources []ResourceLock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, resource := range resources {
		state := c.locks[resource.Key]
		c.ungrant(state, resource.Mode)
		c.dispatch(state)
		c.cleanup(resource.Key, state)
	}
}

// cleanup is called with c.mu held, so a state cannot be replaced while any
// acquisition or waiter still refers to it.
func (c *Coordinator) cleanup(key string, state *resourceState) {
	if state.readers == 0 && !state.writer && len(state.waiters) == 0 {
		delete(c.locks, key)
	}
}
