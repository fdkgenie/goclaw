// Package activity implements the dashboard's activity feed: a capped, in-memory
// ring buffer of activity items with persistent-ID deduplication and gap markers
// for reconnection events.
package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const (
	ringCapacity = 500

	// teamEventLimit is the number of task events loaded per team when the
	// history is backfilled.
	teamEventLimit = 50

	// auditLogLimit is the number of audit log rows loaded by the backfill.
	auditLogLimit = 200
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

// gatewayActivityLog mirrors the gateway store.ActivityLog payload.
type gatewayActivityLog struct {
	ID         string          `json:"id"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Details    json.RawMessage `json:"details"`
	IPAddress  string          `json:"ip_address"`
	CreatedAt  time.Time       `json:"created_at"`
}

// toItem converts an audit log row into a feed item.
func (l gatewayActivityLog) toItem() ActivityItem {
	summary := l.Action
	if l.EntityType != "" {
		summary = l.Action + " " + l.EntityType
		if l.EntityID != "" {
			summary += " " + l.EntityID
		}
	}
	return ActivityItem{
		ID:        l.ID,
		Type:      TypeAuditLog,
		Timestamp: l.CreatedAt,
		AgentID:   l.ActorID,
		Summary:   summary,
		Details:   l,
	}
}

// activityResponse is the /v1/activity response envelope. The gateway
// returns the audit rows under "logs"; the dashboard used to look for
// "items", so the whole audit history was silently dropped.
type activityResponse struct {
	Logs []gatewayActivityLog `json:"logs"`
}

// Backfill fetches the audit log history from the gateway and inserts it into
// the ring, deduplicating against the live stream.
func (r *RingBuffer) Backfill(ctx context.Context, httpClient goclawHTTPClienter) error {
	var resp activityResponse
	if err := httpClient.Get(ctx, "/v1/activity?limit="+strconv.Itoa(auditLogLimit), &resp); err != nil {
		return err
	}
	for _, l := range resp.Logs {
		r.Add(l.toItem())
	}
	return nil
}

// teamEventsResponse is the gateway teams.events.list response envelope.
type teamEventsResponse struct {
	Events []teamTaskEvent `json:"events"`
}

// teamTaskEvent mirrors the gateway store.TeamTaskEventData payload.
type teamTaskEvent struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	EventType string          `json:"event_type"`
	ActorType string          `json:"actor_type"`
	ActorID   string          `json:"actor_id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// toItem converts a team task event into a feed item.
func (e teamTaskEvent) toItem(teamID string) ActivityItem {
	summary := e.EventType
	if e.TaskID != "" {
		summary = e.EventType + " " + e.TaskID
	}
	return ActivityItem{
		ID:        "task-event:" + e.ID,
		Type:      TypeTaskEvent,
		Timestamp: e.CreatedAt,
		AgentID:   e.ActorID,
		TeamID:    teamID,
		Summary:   summary,
		Details:   e,
	}
}

// BackfillTeams loads the task event history of every team and inserts it
// into the ring.
//
// The gateway exposes the task events per team over the teams.events.list
// RPC; the live pump adds the same events while they happen. The two
// sources identify an event differently (the stored row has its own uuid, the
// WS event has the task id and the timestamp), so a task event can show up
// twice: once live and once from the backfill.
func (r *RingBuffer) BackfillTeams(ctx context.Context, ws RPCCaller, teamIDs []string) error {
	if ws == nil {
		return nil
	}
	var firstErr error
	for _, teamID := range teamIDs {
		if teamID == "" {
			continue
		}
		resp, err := ws.Call(ctx, "teams.events.list", map[string]any{
			"team_id": teamID,
			"limit":   teamEventLimit,
			"offset":  0,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if resp == nil {
			continue
		}
		if !resp.OK {
			if firstErr == nil {
				firstErr = fmt.Errorf("teams.events.list team=%s failed", teamID)
			}
			continue
		}
		var parsed teamEventsResponse
		if err := unmarshalPayload(resp.Payload, &parsed); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, ev := range parsed.Events {
			r.Add(ev.toItem(teamID))
		}
	}
	return firstErr
}

// teamTaskEventPayload mirrors the gateway protocol.TeamTaskEventPayload
// (the payload of the team.task.* WS events).
type teamTaskEventPayload struct {
	TeamID    string `json:"team_id"`
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Subject   string `json:"subject"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Timestamp string `json:"timestamp"`
}

// FromTeamTaskEvent converts a team.task.* WS event into a feed item.
// Returns false for a payload that carries no task.
func FromTeamTaskEvent(event string, payload any) (ActivityItem, bool) {
	var wire teamTaskEventPayload
	if err := unmarshalPayload(payload, &wire); err != nil {
		return ActivityItem{}, false
	}
	if wire.TaskID == "" {
		return ActivityItem{}, false
	}
	var ts time.Time
	if wire.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, wire.Timestamp); err == nil {
			ts = parsed
		}
	}
	summary := event
	if wire.Status != "" {
		summary = event + " " + wire.Status
	}
	return ActivityItem{
		ID:        "task-event:" + event + ":" + wire.TaskID + ":" + wire.Timestamp,
		Type:      TypeTaskEvent,
		Timestamp: ts,
		AgentID:   wire.ActorID,
		TeamID:    wire.TeamID,
		Summary:   summary,
		Details:   wire,
	}, true
}

// goclawHTTPClienter is the subset of goclaw.HTTPClient used by Backfill.
type goclawHTTPClienter interface {
	Get(ctx context.Context, path string, out any) error
}

// RPCCaller is the subset of goclaw.Client used by BackfillTeams.
type RPCCaller interface {
	Call(ctx context.Context, method string, params any) (*goclaw.ResponseFrame, error)
}

func unmarshalPayload(payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
