package cache

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheMiss(t *testing.T) {
	c := New[int](time.Minute)
	var calls atomic.Int32
	val, err := c.GetOrFetch("k1", func() (int, error) {
		calls.Add(1)
		return 42, nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if val != 42 {
		t.Errorf("val = %d, want 42", val)
	}
	if calls.Load() != 1 {
		t.Errorf("fetcher calls = %d, want 1", calls.Load())
	}
}

func TestCacheHitWithinTTL(t *testing.T) {
	c := New[int](time.Minute)
	var calls atomic.Int32
	val, err := c.GetOrFetch("k1", func() (int, error) {
		calls.Add(1)
		return 7, nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if val != 7 {
		t.Errorf("val = %d, want 7", val)
	}

	val2, ok := c.Get("k1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if val2 != 7 {
		t.Errorf("val2 = %d, want 7", val2)
	}
	if calls.Load() != 1 {
		t.Errorf("fetcher calls = %d, want 1 (cached)", calls.Load())
	}
}

func TestCacheRefetchAfterTTL(t *testing.T) {
	c := New[int](50 * time.Millisecond)
	var calls atomic.Int32
	_, err := c.GetOrFetch("k1", func() (int, error) {
		calls.Add(1)
		return 1, nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	val, err := c.GetOrFetch("k1", func() (int, error) {
		calls.Add(1)
		return 2, nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if val != 2 {
		t.Errorf("val = %d, want 2 (expired, refetched)", val)
	}
	if calls.Load() != 2 {
		t.Errorf("fetcher calls = %d, want 2", calls.Load())
	}
}

func TestCacheEventInvalidationTeam(t *testing.T) {
	c := New[string](time.Minute)
	c.RegisterEventTag("team.updated", "teams:list", "team:")

	c.Set("teams:list", "[a,b]")
	c.Set("team:abc", "{id:abc}")
	c.Set("unrelated", "keep")

	c.InvalidateOnEvent("team.updated")

	if c.Len() != 1 {
		t.Fatalf("cache len = %d, want 1 (only unrelated)", c.Len())
	}
	if _, ok := c.Get("unrelated"); !ok {
		t.Fatal("unrelated key should survive team invalidation")
	}
}

func TestCacheEventInvalidationAgentLink(t *testing.T) {
	c := New[string](time.Minute)
	c.RegisterEventTag("agent_link.created", "orgchart:team:1")

	c.Set("orgchart:team:1", "{nodes:...}")
	c.Set("orgchart:team:2", "{nodes:...}")

	c.InvalidateOnEvent("agent_link.created")

	if _, ok := c.Get("orgchart:team:1"); ok {
		t.Fatal("orgchart:team:1 should be invalidated")
	}
	if _, ok := c.Get("orgchart:team:2"); !ok {
		t.Fatal("orgchart:team:2 should survive (different tag)")
	}
}

func TestCacheInvalidateAll(t *testing.T) {
	c := New[string](time.Minute)
	c.Set("a", "1")
	c.Set("b", "2")
	c.InvalidateAll()
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, want 0 after InvalidateAll", c.Len())
	}
}
