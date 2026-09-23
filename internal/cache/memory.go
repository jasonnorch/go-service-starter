package cache

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process Cache. Every instance has its own, so two instances
// behind a load balancer disagree about what is cached; use it for tests and
// for services that run exactly one instance.
//
// Expired entries are dropped on read, and by a sweep every interval so keys
// that are written and never read again do not accumulate. The sweep is the
// one goroutine Memory owns, and Close stops it.
type Memory struct {
	mu      sync.Mutex
	entries map[string]entry

	stop chan struct{}
	done chan struct{}
}

type entry struct {
	value   []byte
	expires time.Time
}

// NewMemory returns a Memory that sweeps expired entries every interval.
func NewMemory(interval time.Duration) *Memory {
	m := &Memory{
		entries: map[string]entry{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go m.sweep(interval)
	return m
}

func (m *Memory) sweep(interval time.Duration) {
	defer close(m.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			for k, e := range m.entries {
				if !now.Before(e.expires) {
					delete(m.entries, k)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Get implements Cache. The context is checked, not ignored, so a canceled
// request behaves the same against Memory as against Redis.
func (m *Memory) Get(ctx context.Context, key string) (value []byte, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !time.Now().Before(e.expires) {
		delete(m.entries, key)
		return nil, false, nil
	}
	// A copy, so a caller mutating the result cannot change the cached value —
	// which Redis, returning fresh bytes every time, could never do either.
	return append([]byte(nil), e.value...), true, nil
}

// Set implements Cache.
func (m *Memory) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 {
		return ErrInvalidTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = entry{value: append([]byte(nil), value...), expires: time.Now().Add(ttl)}
	return nil
}

// Delete implements Cache.
func (m *Memory) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return nil
}

// Ping implements Cache. Memory is always reachable.
func (m *Memory) Ping(ctx context.Context) error { return ctx.Err() }

// Close stops the sweep and waits for it to exit. It is safe to call once.
func (m *Memory) Close() error {
	close(m.stop)
	<-m.done
	return nil
}
