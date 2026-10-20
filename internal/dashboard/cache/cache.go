// Package cache implements a TTL cache-aside with event-driven invalidation
// for the dashboard. It is used to cache gateway fetches (agents, traces,
// usage, org chart) and invalidate them when relevant WebSocket events arrive.
package cache

import (
	"sync"
	"time"
)

// item is a cached value with an expiry.
type item[V any] struct {
	value     V
	expiresAt time.Time
}

// Cache is a concurrent-safe TTL cache with event-driven invalidation.
type Cache[V any] struct {
	mu    sync.RWMutex
	items map[string]item[V]
	ttl   time.Duration

	// eventTags maps event name patterns to cache key prefixes/tags to
	// invalidate when that event arrives. E.g. "team.updated" -> ["teams:list", "team:"].
	eventTags map[string][]string
}

// New creates a Cache with the given default TTL.
func New[V any](ttl time.Duration) *Cache[V] {
	return &Cache[V]{
		items:     make(map[string]item[V]),
		ttl:       ttl,
		eventTags: make(map[string][]string),
	}
}

// GetOrFetch returns the cached value for key if present and not expired;
// otherwise it calls fetcher, stores the result, and returns it.
// The fetcher is called while holding the read lock upgraded to write only
// for the store, so concurrent callers for the same key may each fetch once
// (acceptable for cache-aside).
func (c *Cache[V]) GetOrFetch(key string, fetcher func() (V, error)) (V, error) {
	var zero V

	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()
	if ok && it.expiresAt.After(time.Now()) {
		return it.value, nil
	}

	val, err := fetcher()
	if err != nil {
		return zero, err
	}

	c.mu.Lock()
	c.items[key] = item[V]{value: val, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return val, nil
}

// Get returns the cached value for key and a bool indicating a fresh hit.
func (c *Cache[V]) Get(key string) (V, bool) {
	var zero V
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()
	if ok && it.expiresAt.After(time.Now()) {
		return it.value, true
	}
	return zero, false
}

// Set stores value for key with the cache's TTL.
func (c *Cache[V]) Set(key string, value V) {
	c.mu.Lock()
	c.items[key] = item[V]{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// Invalidate removes a single key.
func (c *Cache[V]) Invalidate(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// InvalidateAll clears the entire cache (used on WS reconnect).
func (c *Cache[V]) InvalidateAll() {
	c.mu.Lock()
	c.items = make(map[string]item[V])
	c.mu.Unlock()
}

// RegisterEventTag maps an event name to cache key prefixes that should be
// invalidated when the event arrives. Multiple registrations accumulate.
func (c *Cache[V]) RegisterEventTag(eventName string, tags ...string) {
	c.mu.Lock()
	c.eventTags[eventName] = append(c.eventTags[eventName], tags...)
	c.mu.Unlock()
}

// InvalidateOnEvent invalidates all cache keys whose key starts with any tag
// registered for the given event name.
func (c *Cache[V]) InvalidateOnEvent(eventName string) {
	c.mu.RLock()
	tags := c.eventTags[eventName]
	c.mu.RUnlock()
	if len(tags) == 0 {
		return
	}

	c.mu.Lock()
	for key := range c.items {
		for _, tag := range tags {
			if tag == "" || hasPrefix(key, tag) {
				delete(c.items, key)
				break
			}
		}
	}
	c.mu.Unlock()
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Len returns the number of cached items (for tests).
func (c *Cache[V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
