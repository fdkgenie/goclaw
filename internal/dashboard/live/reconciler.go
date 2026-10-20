package live

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const (
	reconcileInterval = 60 * time.Second
	maxPageSize       = 200
	maxPages          = 25 // cap: 25 pages / 5,000 items
	eventBufferCap    = 10_000
)

// tracesResponse is the gateway /v1/traces response envelope.
type tracesResponse struct {
	Traces []TraceData `json:"traces"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
}

// Reconciler runs the live reconcile epoch: buffer WS events, fetch a trace
// snapshot (paged), replay buffered events, then merge into State.
type Reconciler struct {
	state *State
	http  *goclaw.HTTPClient
	hub   *Hub

	// Interval overrides the reconcile period. Zero means the default
	// reconcileInterval; tests shorten it to exercise the loop.
	Interval time.Duration

	// eventBuf holds WS events received while a snapshot is being fetched.
	eventBuf []goclaw.AgentEventPayload
	bufMu    sync.Mutex

	// epochMu serializes event application with the snapshot merge, so an
	// event received during an epoch is either buffered before the merge or
	// applied after it.
	epochMu     sync.Mutex
	epochActive bool
	overflow    bool
}

// NewReconciler creates a Reconciler wired to state, the gateway HTTP client,
// and the SSE hub used to publish live mutations.
func NewReconciler(state *State, httpClient *goclaw.HTTPClient, hub *Hub) *Reconciler {
	if hub == nil {
		hub = NewHub(state)
	}
	return &Reconciler{
		state:    state,
		http:     httpClient,
		hub:      hub,
		eventBuf: make([]goclaw.AgentEventPayload, 0, eventBufferCap),
	}
}

// HandleEvent applies a live WS event. During a reconcile epoch the event is
// buffered for replay before the snapshot merge; otherwise it is applied and
// published atomically through the hub.
func (r *Reconciler) HandleEvent(evt goclaw.AgentEventPayload) {
	r.epochMu.Lock()
	defer r.epochMu.Unlock()

	r.bufMu.Lock()
	if r.epochActive && !r.overflow {
		if len(r.eventBuf) >= eventBufferCap {
			r.overflow = true
		} else {
			r.eventBuf = append(r.eventBuf, evt)
			r.bufMu.Unlock()
			return
		}
	}
	r.bufMu.Unlock()

	r.hub.ApplyAndPublish(evt)
}

// BufferEvent enqueues a WS event received while a snapshot is in flight.
// Returns false if the buffer is full (overflow -> caller should cancel epoch).
func (r *Reconciler) BufferEvent(evt goclaw.AgentEventPayload) bool {
	r.bufMu.Lock()
	defer r.bufMu.Unlock()
	if len(r.eventBuf) >= eventBufferCap {
		r.overflow = true
		return false
	}
	r.eventBuf = append(r.eventBuf, evt)
	return true
}

// Drain returns and clears the buffered events.
func (r *Reconciler) Drain() []goclaw.AgentEventPayload {
	r.bufMu.Lock()
	defer r.bufMu.Unlock()
	buf := r.eventBuf
	r.eventBuf = make([]goclaw.AgentEventPayload, 0, eventBufferCap)
	return buf
}

// FetchSnapshot pages /v1/traces?status=running&limit=200&offset=k until
// offset >= total. Returns complete=false on total drift, page error, or cap
// exceeded.
func (r *Reconciler) FetchSnapshot(ctx context.Context) ([]TraceData, bool) {
	var all []TraceData
	var expectedTotal *int
	offset := 0

	for page := 0; page < maxPages; page++ {
		var resp tracesResponse
		path := fmt.Sprintf("/v1/traces?status=running&limit=%d&offset=%d", maxPageSize, offset)
		if err := r.http.Get(ctx, path, &resp); err != nil {
			return all, false
		}

		if expectedTotal == nil {
			expectedTotal = &resp.Total
		} else if resp.Total != *expectedTotal {
			// Total drifted between pages: snapshot is inconsistent.
			return all, false
		}

		all = append(all, resp.Traces...)
		offset = resp.Offset + len(resp.Traces)
		if offset >= resp.Total {
			return all, true
		}
		if len(resp.Traces) == 0 {
			break
		}
	}

	// Cap exceeded: treat as incomplete.
	return all, false
}

// RunEpoch executes one reconcile cycle: mark the epoch active so live events
// buffer, fetch the snapshot, then replay buffered events and merge the
// snapshot atomically through the hub.
func (r *Reconciler) RunEpoch(ctx context.Context) bool {
	r.epochMu.Lock()
	r.bufMu.Lock()
	r.epochActive = true
	r.bufMu.Unlock()
	r.epochMu.Unlock()

	traces, complete := r.FetchSnapshot(ctx)

	r.epochMu.Lock()
	defer r.epochMu.Unlock()

	r.bufMu.Lock()
	r.epochActive = false
	buf := r.eventBuf
	r.eventBuf = make([]goclaw.AgentEventPayload, 0, eventBufferCap)
	overflow := r.overflow
	r.overflow = false
	r.bufMu.Unlock()

	if overflow {
		// Buffer overflow means events were dropped: force a resync and never
		// run negative pruning on this epoch.
		complete = false
	}

	r.hub.ReplayAndMerge(buf, traces, complete)
	return complete
}

// Run starts the periodic reconcile loop (every reconcileInterval) until ctx
// is cancelled.
//
// Every tick also expires the live state: event-only runs that stopped
// receiving events are marked unverified and then dropped, and expired
// tombstones are removed. Without it a long-lived dashboard accumulated
// event-only entries and tombstones forever.
func (r *Reconciler) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = reconcileInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.state.Expire()
			r.RunEpoch(ctx)
		}
	}
}
