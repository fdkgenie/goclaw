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

	// eventBuf holds WS events received while a snapshot is being fetched.
	eventBuf []goclaw.AgentEventPayload
	bufMu    sync.Mutex
}

// NewReconciler creates a Reconciler wired to state and the gateway HTTP client.
func NewReconciler(state *State, httpClient *goclaw.HTTPClient) *Reconciler {
	return &Reconciler{
		state:    state,
		http:     httpClient,
		eventBuf: make([]goclaw.AgentEventPayload, 0, eventBufferCap),
	}
}

// BufferEvent enqueues a WS event received while a snapshot is in flight.
// Returns false if the buffer is full (overflow -> caller should cancel epoch).
func (r *Reconciler) BufferEvent(evt goclaw.AgentEventPayload) bool {
	r.bufMu.Lock()
	defer r.bufMu.Unlock()
	if len(r.eventBuf) >= eventBufferCap {
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

// RunEpoch executes one reconcile cycle: drain buffered events, fetch the
// snapshot, replay buffered events (in order), then merge into State.
func (r *Reconciler) RunEpoch(ctx context.Context) bool {
	buf := r.Drain()
	traces, complete := r.FetchSnapshot(ctx)

	// Replay buffered events in order before merging the snapshot, so events
	// received during the fetch are not lost.
	for _, evt := range buf {
		r.state.ApplyEvent(evt)
	}

	r.state.MergeSnapshot(traces, complete)
	return complete
}

// Run starts the periodic reconcile loop (every reconcileInterval) until ctx
// is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RunEpoch(ctx)
		}
	}
}
