package cache

import (
	"context"
	"testing"
	"time"
)

// Expired entries are otherwise dropped only when read, so a key written once
// and never read again would stay in memory forever. The sweep is what bounds
// that, and it is invisible through the Cache interface — an expired entry
// reads as absent either way — so this test looks inside.
func TestMemorySweepDropsEntriesNobodyReads(t *testing.T) {
	const interval = 20 * time.Millisecond
	m := NewMemory(interval)
	defer func() { _ = m.Close() }()

	for _, k := range []string{"a", "b", "c"} {
		if err := m.Set(context.Background(), k, []byte("v"), time.Millisecond); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	// Several sweep intervals, so one late tick cannot fail the test.
	deadline := time.Now().Add(20 * interval)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.entries)
		m.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(interval)
	}
	t.Errorf("expired, unread entries were never swept")
}
