package live

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func newTestReconciler(t *testing.T, f *testutil.FakeGoClaw, tenantID uuid.UUID) (*Reconciler, *State) {
	t.Helper()
	state := NewState()
	httpClient := goclaw.NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)
	rec := NewReconciler(state, httpClient, NewHub(state))
	return rec, state
}

func mustParseUUIDLive(t *testing.T, s string) uuid.UUID {
	t.Helper()
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return uid
}

func TestFetchSnapshotSinglePageComplete(t *testing.T) {
	tenantID := mustParseUUIDLive(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID: tenantID,
		TracesPages: [][]testutil.TraceData{
			{{ID: "t1", RunID: "r1"}, {ID: "t2", RunID: "r2"}},
		},
	})
	rec, _ := newTestReconciler(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, complete := rec.FetchSnapshot(ctx)
	if !complete {
		t.Fatal("expected complete snapshot")
	}
	if len(got) != 2 {
		t.Fatalf("got %d traces, want 2", len(got))
	}
}

func TestFetchSnapshotMultiPageComplete(t *testing.T) {
	tenantID := mustParseUUIDLive(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	p1 := make([]testutil.TraceData, 200)
	for i := range p1 {
		p1[i] = testutil.TraceData{ID: "t", RunID: "r", Status: "running"}
	}
	p2 := make([]testutil.TraceData, 50)
	for i := range p2 {
		p2[i] = testutil.TraceData{ID: "t", RunID: "r", Status: "running"}
	}
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:    tenantID,
		TracesPages: [][]testutil.TraceData{p1, p2},
	})
	rec, _ := newTestReconciler(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, complete := rec.FetchSnapshot(ctx)
	if !complete {
		t.Fatal("expected complete snapshot across pages")
	}
	if len(got) != 250 {
		t.Fatalf("got %d traces, want 250", len(got))
	}
}

func TestFetchSnapshotCapExceedsIncomplete(t *testing.T) {
	tenantID := mustParseUUIDLive(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	pages := make([][]testutil.TraceData, 0, 26)
	for i := 0; i < 26; i++ {
		page := make([]testutil.TraceData, 200)
		for j := range page {
			page[j] = testutil.TraceData{ID: "t", RunID: "r", Status: "running"}
		}
		pages = append(pages, page)
	}
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:    tenantID,
		TracesPages: pages,
	})
	rec, _ := newTestReconciler(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, complete := rec.FetchSnapshot(ctx)
	if complete {
		t.Fatal("expected incomplete when page cap exceeded")
	}
	if len(got) != 5000 {
		t.Fatalf("got %d traces, want 5000 (cap)", len(got))
	}
}

func TestBufferReplayBeforeMerge(t *testing.T) {
	tenantID := mustParseUUIDLive(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID: tenantID,
		// Empty snapshot: nothing traced.
		TracesPages: [][]testutil.TraceData{{}},
	})
	rec, state := newTestReconciler(t, f, tenantID)

	// Buffer a run.started event while snapshot is in flight.
	if !rec.BufferEvent(goclaw.AgentEventPayload{RunID: "r1", TraceID: "t1", Event: "run.started", AgentID: "a1", TeamID: "team1"}) {
		t.Fatal("buffer should accept event")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rec.RunEpoch(ctx)

	// Buffered event applied before merge; empty snapshot won't prune event_only.
	if e := state.entries[runKey("r1")]; e == nil {
		t.Fatal("buffered run.started not applied before merge")
	} else if e.Class != ClassEventOnly {
		t.Errorf("class = %s, want event_only", e.Class)
	}
}

func TestHandleEventPublishesDeltaToSubscriber(t *testing.T) {
	state := NewState()
	hub := NewHub(state)
	rec := NewReconciler(state, nil, hub)

	snap, ch, unsub := hub.Subscribe()
	defer unsub()

	rec.HandleEvent(goclaw.AgentEventPayload{
		RunID:   "r1",
		AgentID: "a1",
		Event:   "run.started",
	})

	select {
	case delta := <-ch:
		if delta.Kind != "event" {
			t.Fatalf("delta kind = %q, want event", delta.Kind)
		}
		if delta.Version <= snap.Version {
			t.Fatalf("delta version %d not > snapshot %d", delta.Version, snap.Version)
		}
		if delta.KPI == nil || delta.KPI.ActiveRunsUntraced != 1 {
			t.Fatalf("delta KPI = %+v, want one untraced run", delta.KPI)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not receive delta for live event")
	}

	if state.Len() != 1 {
		t.Fatalf("state entries = %d, want 1", state.Len())
	}
}

func TestBufferOverflowCancelsEpoch(t *testing.T) {
	tenantID := mustParseUUIDLive(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{TenantID: tenantID})
	rec, _ := newTestReconciler(t, f, tenantID)

	rejected := false
	for i := 0; i < eventBufferCap+1; i++ {
		if !rec.BufferEvent(goclaw.AgentEventPayload{RunID: "r", Event: "run.started"}) {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Fatal("expected buffer overflow to reject event")
	}
}
