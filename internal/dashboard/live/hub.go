package live

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const (
	subscriberChannelCap = 256
	keepaliveInterval    = 15 * time.Second
)

// Delta is an incremental change pushed to SSE subscribers, carrying a
// monotonic version so clients can discard stale or out-of-order messages.
type Delta struct {
	Version uint64      `json:"version"`
	Kind    string      `json:"kind"` // "event", "merge", "keepalive", "down"
	Payload interface{} `json:"payload,omitempty"`
	KPI     *LiveKPI    `json:"kpi,omitempty"`
}

// Snapshot is the initial state sent to a new SSE subscriber.
type Snapshot struct {
	Version uint64       `json:"version"`
	Entries []*LiveEntry `json:"entries"`
	KPI     LiveKPI      `json:"kpi"`
}

// Hub fans out live state changes to SSE subscribers. Subscribe snapshot
// generation/registration and Delta publish happen under the same mutex, so a
// subscriber never observes a gap between a state mutation and its publication.
type Hub struct {
	mu          sync.Mutex
	version     uint64
	subscribers map[chan Delta]struct{}
	state       *State
}

// NewHub creates a Hub over the given State.
func NewHub(state *State) *Hub {
	return &Hub{
		subscribers: make(map[chan Delta]struct{}),
		state:       state,
	}
}

// Subscribe returns the current snapshot, a channel of deltas, and an
// unsubscribe function. Snapshot + channel registration are atomic under one
// lock.
func (h *Hub) Subscribe() (Snapshot, chan Delta, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.version++
	snap := Snapshot{
		Version: h.version,
		Entries: h.state.Snapshot(),
		KPI:     h.state.ComputeKPI(),
	}
	ch := make(chan Delta, subscriberChannelCap)
	h.subscribers[ch] = struct{}{}

	return snap, ch, func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		close(ch)
		h.mu.Unlock()
	}
}

// PublishDelta fans out a delta to all subscribers under the hub lock. Slow
// subscribers (full channel) are dropped without blocking others.
func (h *Hub) PublishDelta(delta Delta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(delta)
}

// ApplyAndPublish applies a state mutation and publishes the resulting delta
// atomically under one lock, so no delta can be lost between mutation and
// subscription.
func (h *Hub) ApplyAndPublish(evt goclaw.AgentEventPayload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state.ApplyEvent(evt)
	h.publishLocked(Delta{Kind: "event", Payload: evt})
}

// MergeAndPublish merges a trace snapshot and publishes the resulting delta
// atomically under one lock.
func (h *Hub) MergeAndPublish(traces []TraceData, complete bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state.MergeSnapshot(traces, complete)
	h.publishLocked(Delta{Kind: "merge", Payload: map[string]any{"complete": complete, "count": len(traces)}})
}

// MarkDegradedAndPublish flags the live state as degraded (the gateway
// connection dropped, so the live data is no longer trustworthy) and
// publishes a "down" delta under one lock.
func (h *Hub) MarkDegradedAndPublish(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state.SetDegraded(true)
	h.publishLocked(Delta{Kind: "down", Payload: map[string]any{"reason": reason}})
}

// MarkRecoveredAndPublish clears the degraded flag, marks event-only runs
// unverified (the trace snapshot has not re-confirmed them yet), and
// publishes the resulting KPI under one lock.
func (h *Hub) MarkRecoveredAndPublish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state.SetDegraded(false)
	h.state.MarkUnverified()
	h.publishLocked(Delta{Kind: "reconnect"})
}

// ReplayAndMerge atomically replays buffered events, merges a trace snapshot,
// and publishes a merge delta. Both mutations and publication happen under the
// hub lock, preserving snapshot+subscribe atomicity.
func (h *Hub) ReplayAndMerge(events []goclaw.AgentEventPayload, traces []TraceData, complete bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, evt := range events {
		h.state.ApplyEvent(evt)
	}
	h.state.MergeSnapshot(traces, complete)
	h.publishLocked(Delta{Kind: "merge", Payload: map[string]any{"complete": complete, "count": len(traces)}})
}

// publishLocked assumes h.mu is held.
func (h *Hub) publishLocked(delta Delta) {
	h.version++
	delta.Version = h.version
	kpi := h.state.ComputeKPI()
	delta.KPI = &kpi
	for ch := range h.subscribers {
		select {
		case ch <- delta:
		default:
			delete(h.subscribers, ch)
			close(ch)
		}
	}
}

// Version returns the current hub version.
func (h *Hub) Version() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

// ServeHTTP is the SSE handler. It writes the initial snapshot, then streams
// deltas with a 15s keepalive until the client disconnects.
func (h *Hub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	snap, ch, unsubscribe := h.Subscribe()
	defer unsubscribe()

	b, _ := json.Marshal(map[string]any{"event": "snapshot", "data": snap})
	w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-keepalive.C:
			b, _ = json.Marshal(map[string]any{"event": "keepalive", "data": nil})
			w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
			flusher.Flush()
		case delta, open := <-ch:
			if !open {
				return
			}
			b, _ = json.Marshal(map[string]any{"event": "delta", "data": delta})
			w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
			flusher.Flush()
		}
	}
}
