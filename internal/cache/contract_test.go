package cache_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jasonnorch/go-service-starter/internal/cache"
)

// runContract is the behavior every Cache must have. Memory runs it in the
// ordinary tests and Redis runs it in the integration tests, so a difference
// between the two — the thing that makes a test backed by Memory lie about
// production backed by Redis — fails in one place.
//
// Keys carry a random suffix, so runs against a shared Redis never see each
// other's entries.
func runContract(t *testing.T, open func(t *testing.T) cache.Cache) {
	t.Helper()
	ctx := context.Background()

	key := func(t *testing.T) string {
		t.Helper()
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		return t.Name() + "/" + hex.EncodeToString(b)
	}

	t.Run("missing key is absent, not an error", func(t *testing.T) {
		c := open(t)
		v, ok, err := c.Get(ctx, key(t))
		if err != nil || ok || v != nil {
			t.Errorf("Get(missing) = %q, %v, %v; want nil, false, nil", v, ok, err)
		}
	})

	t.Run("set then get returns the value", func(t *testing.T) {
		c, k := open(t), key(t)
		if err := c.Set(ctx, k, []byte("v1"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		v, ok, err := c.Get(ctx, k)
		if err != nil || !ok || !bytes.Equal(v, []byte("v1")) {
			t.Errorf("Get = %q, %v, %v; want v1, true, nil", v, ok, err)
		}
	})

	t.Run("set overwrites", func(t *testing.T) {
		c, k := open(t), key(t)
		_ = c.Set(ctx, k, []byte("v1"), time.Minute)
		_ = c.Set(ctx, k, []byte("v2"), time.Minute)
		if v, _, _ := c.Get(ctx, k); !bytes.Equal(v, []byte("v2")) {
			t.Errorf("Get after overwrite = %q, want v2", v)
		}
	})

	t.Run("delete removes, and deleting a missing key is not an error", func(t *testing.T) {
		c, k := open(t), key(t)
		_ = c.Set(ctx, k, []byte("v1"), time.Minute)
		if err := c.Delete(ctx, k); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, ok, _ := c.Get(ctx, k); ok {
			t.Error("the key survived Delete")
		}
		if err := c.Delete(ctx, k); err != nil {
			t.Errorf("Delete(missing) = %v, want nil", err)
		}
	})

	// The one behavior a cache exists for. Waiting a multiple of the ttl
	// keeps the test tied to the value under test rather than a guess about
	// how fast the machine is.
	t.Run("an entry is absent after its ttl", func(t *testing.T) {
		c, k := open(t), key(t)
		const ttl = 100 * time.Millisecond
		if err := c.Set(ctx, k, []byte("v1"), ttl); err != nil {
			t.Fatalf("Set: %v", err)
		}
		time.Sleep(3 * ttl)
		if _, ok, _ := c.Get(ctx, k); ok {
			t.Errorf("the entry was still there %s after a %s ttl", 3*ttl, ttl)
		}
	})

	// go-redis reads 0 as "never expire" and -1 as "keep the old ttl". A
	// caller who passed either by mistake would get an entry that lives
	// forever, so both implementations refuse them.
	t.Run("a non-positive ttl is refused", func(t *testing.T) {
		c := open(t)
		for _, ttl := range []time.Duration{0, -1, -time.Second} {
			if err := c.Set(ctx, key(t), []byte("v"), ttl); !errors.Is(err, cache.ErrInvalidTTL) {
				t.Errorf("Set with ttl %s = %v, want ErrInvalidTTL", ttl, err)
			}
		}
	})

	t.Run("a canceled context is honored", func(t *testing.T) {
		c := open(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := c.Get(canceled, key(t)); err == nil {
			t.Error("Get ignored a canceled context")
		}
		if err := c.Set(canceled, key(t), []byte("v"), time.Minute); err == nil {
			t.Error("Set ignored a canceled context")
		}
		if err := c.Delete(canceled, key(t)); err == nil {
			t.Error("Delete ignored a canceled context")
		}
	})
}

func TestMemoryContract(t *testing.T) {
	runContract(t, func(t *testing.T) cache.Cache {
		m := cache.NewMemory(time.Minute)
		t.Cleanup(func() { _ = m.Close() })
		return m
	})
}

// Memory hands out copies. Without them, a caller that modified the bytes it
// got back would silently change the cached value for every later reader — a
// bug Redis, which returns fresh bytes every time, can never have, so a test
// run against Memory would not match production.
func TestMemoryGetReturnsACopy(t *testing.T) {
	ctx := context.Background()
	m := cache.NewMemory(time.Minute)
	defer func() { _ = m.Close() }()

	_ = m.Set(ctx, "k", []byte("original"), time.Minute)
	got, _, _ := m.Get(ctx, "k")
	copy(got, "MUTATED!")

	if again, _, _ := m.Get(ctx, "k"); !bytes.Equal(again, []byte("original")) {
		t.Errorf("mutating a Get result changed the cache: %q", again)
	}
}
