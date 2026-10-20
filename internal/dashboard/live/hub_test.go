package live

import (
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

func TestHubSubscribeSnapshotAndVersion(t *testing.T) {
	state := NewState()
	h := NewHub(state)

	snap, ch, unsub := h.Subscribe()
	if snap.Version == 0 {
		t.Fatal("expected snapshot version > 0")
	}
	if len(ch) != 0 {
		t.Fatal("expected no initial delta on channel")
	}
	unsub()

	// After unsubscribe, publishing should not block/panic.
	h.PublishDelta(Delta{Kind: "event"})
}

func TestHubVersionMonotonic(t *testing.T) {
	state := NewState()
	h := NewHub(state)

	_, ch1, unsub1 := h.Subscribe()
	v1 := h.Version()
	h.PublishDelta(Delta{Kind: "event"})
	v2 := h.Version()
	if v2 != v1+1 {
		t.Fatalf("version = %d, want %d (v1+1)", v2, v1+1)
	}
	delta := <-ch1
	if delta.Version != v2 {
		t.Fatalf("delta version = %d, want %d", delta.Version, v2)
	}
	unsub1()
}

func TestHubSlowConsumerDropped(t *testing.T) {
	state := NewState()
	h := NewHub(state)

	_, ch, _ := h.Subscribe()
	// Fill the channel (no reader) to simulate a slow consumer.
	for i := 0; i < subscriberChannelCap; i++ {
		ch <- Delta{Kind: "x"}
	}
	// Publishing should drop this slow consumer and close its channel.
	h.PublishDelta(Delta{Kind: "event"})
	// Drain buffered items, then the channel must be closed.
	drained := 0
	for {
		_, open := <-ch
		if !open {
			break
		}
		drained++
		if drained > subscriberChannelCap+1 {
			t.Fatal("channel never closed")
		}
	}
	if drained != subscriberChannelCap {
		t.Fatalf("drained %d items, want %d", drained, subscriberChannelCap)
	}
	// Channel already closed by the drop; do not unsubscribe again.
	snap1, ch1, unsub1 := h.Subscribe()

	v1 := snap1.Version

	h.ApplyAndPublish(goclaw.AgentEventPayload{RunID: "r1", Event: "run.started"})
	v2 := h.Version()
	if v2 != v1+1 {
		t.Fatalf("version after publish = %d, want %d", v2, v1+1)
	}

	delta := <-ch1
	if delta.Version != v2 {
		t.Fatalf("delta version = %d, want %d", delta.Version, v2)
	}

	// Second subscriber after the mutation must see version >= v2 (no gap).
	snap2, _, unsub2 := h.Subscribe()
	if snap2.Version < v2 {
		t.Fatalf("second snapshot version = %d, want >= %d (lost delta)", snap2.Version, v2)
	}
	unsub1()
	unsub2()
}

func TestHubConcurrentSubscribeMonotonic(t *testing.T) {
	state := NewState()
	h := NewHub(state)

	versions := make([]uint64, 5)
	chans := make([]<-chan Delta, 5)
	unsubs := make([]func(), 5)
	for i := 0; i < 5; i++ {
		snap, ch, unsub := h.Subscribe()
		versions[i] = snap.Version
		chans[i] = ch
		unsubs[i] = unsub
	}
	// Each subscribe bumps the version under the lock -> strictly increasing.
	for i := 1; i < 5; i++ {
		if versions[i] <= versions[i-1] {
			t.Fatalf("versions not monotonic: %v", versions)
		}
	}
	for i := range unsubs {
		unsubs[i]()
	}
	// Drain channels to avoid goroutine leaks from the select below.
	for _, ch := range chans {
		go func(c <-chan Delta) {
			for range c {
			}
		}(ch)
	}
	time.Sleep(50 * time.Millisecond)
	h.PublishDelta(Delta{Kind: "event"})
}

func TestHubMergeAndPublishDelivers(t *testing.T) {
	state := NewState()
	h := NewHub(state)

	h.MergeAndPublish([]TraceData{{RunID: "r1", TraceID: "t1"}}, true)
	if h.Version() == 0 {
		t.Fatal("expected merge to bump version")
	}

	snap, ch, unsub := h.Subscribe()
	defer unsub()
	if snap.Version == 0 {
		t.Fatal("expected snapshot version > 0")
	}

	h.PublishDelta(Delta{Kind: "event"})
	select {
	case delta := <-ch:
		if delta.Version <= snap.Version {
			t.Fatalf("delta version = %d, not > snapshot %d", delta.Version, snap.Version)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a delta on the fresh channel")
	}
}

// TestHubDegradedAndRecoveredTransitions covers the gateway outage path: the
// hub flags the live state degraded and publishes a "down" delta, then clears
// the flag and marks event-only runs unverified when the tenant canary
// re-verifies.
func TestHubDegradedAndRecoveredTransitions(t *testing.T) {
	state := NewState()
	hub := NewHub(state)
	state.ApplyEvent(goclaw.AgentEventPayload{RunID: "r1", AgentID: "a1", Event: "run.started"})

	_, ch, unsub := hub.Subscribe()
	defer unsub()

	hub.MarkDegradedAndPublish("gateway disconnected")
	select {
	case delta := <-ch:
		if delta.Kind != "down" {
			t.Fatalf("delta kind = %q, want down", delta.Kind)
		}
		if delta.KPI == nil || delta.KPI.Quality != QualityApprox {
			t.Fatalf("delta KPI = %+v, want approx quality while degraded", delta.KPI)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a down delta")
	}

	if !state.IsDegraded() {
		t.Fatal("expected live state to be degraded")
	}

	hub.MarkRecoveredAndPublish()
	select {
	case delta := <-ch:
		if delta.Kind != "reconnect" {
			t.Fatalf("delta kind = %q, want reconnect", delta.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a reconnect delta")
	}

	if state.IsDegraded() {
		t.Fatal("expected live state to no longer be degraded")
	}
	// The event-only run was not re-confirmed by a trace snapshot, so it must
	// be reported as unverified (quality approx) until the next epoch.
	kpi := state.ComputeKPI()
	if kpi.Quality != QualityApprox {
		t.Fatalf("kpi quality = %q, want approx while unverified", kpi.Quality)
	}
}
