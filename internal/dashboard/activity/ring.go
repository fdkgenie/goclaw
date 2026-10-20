// Package activity implements the dashboard's activity feed: a capped, in-memory
// ring buffer of activity items with persistent-ID deduplication and gap markers
// for reconnection events.
package activity

import (
	"context"
	"sync"
	"time"
)

const (
	ringCapacity = 500
)

// ActivityType categorizes an activity item.
type ActivityType string

const (
	TypeAgentEvent ActivityType = "agent_event"
	TypeTaskEvent  ActivityType = "task_event"
	TypeAuditLog   ActivityType = "audit_log"
	TypeGap        ActivityType = "gap"
)

// ActivityItem is a single activity feed entry.
type ActivityItem struct {
	ID        string       `json:"id"`
	Type      ActivityType `json:"type"`
	Timestamp time.Time    `json:"timestamp"`
	AgentID   string       `json:"agent_id,omitempty"`
	TeamID    string       `json:"team_id,omitempty"`
	Summary   string       `json:"summary"`
	Details   interface{}  `json:"details,omitempty"`
}

// RingBuffer is a fixed-size circular buffer of activity items with
// persistent-ID deduplication.
type RingBuffer struct {
	mu    sync.RWMutex
	items [ringCapacity]*ActivityItem
	write int                 // next write index
	count int                 // number of valid items
	seen  map[string]struct{} // persistent IDs already inserted
}

// NewRingBuffer creates an empty RingBuffer.
func NewRingBuffer() *RingBuffer {
	return &RingBuffer{
		seen: make(map[string]struct{}),
	}
}

// Add inserts item into the ring if its ID has not been seen. Returns false if
// the item was a duplicate (ignored).
func (r *RingBuffer) Add(item ActivityItem) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if item.ID != "" {
		if _, ok := r.seen[item.ID]; ok {
			return false // duplicate
		}
		r.seen[item.ID] = struct{}{}
		// Bound the seen set; drop oldest entries when it grows large.
		if len(r.seen) > ringCapacity*2 {
			r.pruneSeen()
		}
	}

	r.items[r.write] = &item
	r.write = (r.write + 1) % ringCapacity
	if r.count < ringCapacity {
		r.count++
	}
	return true
}

// pruneSeen drops seen IDs for items no longer in the ring to bound memory.
// Called with r.mu held.
func (r *RingBuffer) pruneSeen() {
	present := make(map[string]struct{}, r.count)
	idx := r.write
	for i := 0; i < r.count; i++ {
		idx = (idx - 1 + ringCapacity) % ringCapacity
		if it := r.items[idx]; it != nil && it.ID != "" {
			present[it.ID] = struct{}{}
		}
	}
	for id := range r.seen {
		if _, ok := present[id]; !ok {
			delete(r.seen, id)
		}
	}
}

// List returns up to limit items in reverse chronological order (newest first).
func (r *RingBuffer) List(limit int) []ActivityItem {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 || limit > r.count {
		limit = r.count
	}
	out := make([]ActivityItem, 0, limit)
	idx := r.write
	for i := 0; i < limit; i++ {
		idx = (idx - 1 + ringCapacity) % ringCapacity
		if it := r.items[idx]; it != nil {
			out = append(out, *it)
		}
	}
	return out
}

// InsertGapMarker inserts a gap-type item at the given timestamp, marking a
// reconnection/recovery gap in the feed.
func (r *RingBuffer) InsertGapMarker(since time.Time) {
	r.Add(ActivityItem{
		ID:        "gap:" + since.UTC().Format(time.RFC3339Nano),
		Type:      TypeGap,
		Timestamp: since,
		Summary:   "Reconnection gap",
	})
}

// activityResponse is the /v1/activity response envelope.
type activityResponse struct {
	Items []ActivityItem `json:"items"`
}

// Backfill fetches historical activity from the gateway and inserts it into
// the ring, deduplicating against the live stream.
func (r *RingBuffer) Backfill(ctx context.Context, httpClient goclawHTTPClienter) error {
	var resp activityResponse
	if err := httpClient.Get(ctx, "/v1/activity", &resp); err != nil {
		return err
	}
	for _, it := range resp.Items {
		r.Add(it)
	}
	return nil
}

// goclawHTTPClienter is the subset of goclaw.HTTPClient used by Backfill.
type goclawHTTPClienter interface {
	Get(ctx context.Context, path string, out any) error
}
