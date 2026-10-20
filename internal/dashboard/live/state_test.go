package live

import (
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

func trace(runID, traceID, status string, agentID, teamID string) TraceData {
	return TraceData{
		RunID:   runID,
		TraceID: traceID,
		Status:  status,
		AgentID: agentID,
		TeamID:  teamID,
	}
}

func event(runID, traceID, event, agentID, teamID string) goclaw.AgentEventPayload {
	return goclaw.AgentEventPayload{
		RunID:     runID,
		TraceID:   traceID,
		AgentID:   agentID,
		TeamID:    teamID,
		Event:     event,
		Timestamp: time.Now(),
	}
}

func TestRunEndedDuringSeedDoesNotResurrect(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.completed", "a1", "team1"))
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)
	if e := s.entries[runKey("r1")]; e != nil {
		t.Fatal("terminated run was resurrected by snapshot")
	}
	if _, ok := s.tombstones[runKey("r1")]; !ok {
		t.Fatal("expected tombstone for terminated run")
	}
}

func TestAnnounceRunPreserved(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	for i := 0; i < 3; i++ {
		s.MergeSnapshot([]TraceData{}, true)
	}
	if e := s.entries[runKey("r1")]; e == nil {
		t.Fatal("announced event_only run was pruned")
	} else if e.Class != ClassEventOnly {
		t.Errorf("class = %s, want event_only", e.Class)
	}
}

func TestTracingUnavailableDegraded(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MergeSnapshot([]TraceData{}, false)
	if !s.IsDegraded() {
		t.Fatal("expected degraded on incomplete snapshot")
	}
	k := s.ComputeKPI()
	if k.Quality != QualityApprox {
		t.Errorf("quality = %s, want approx", k.Quality)
	}
}

func TestEmptyRunIDUsesTraceKey(t *testing.T) {
	s := NewState()
	s.MergeSnapshot([]TraceData{{RunID: "", TraceID: "t1", Status: "running"}}, true)
	if e := s.entries[traceKey("t1")]; e == nil {
		t.Fatal("expected trace-keyed entry for empty run_id")
	} else if e.Key != traceKey("t1") {
		t.Errorf("key = %s, want %s", e.Key, traceKey("t1"))
	}
	s.MergeSnapshot([]TraceData{{RunID: "", TraceID: "t1", Status: "running"}}, true)
	if s.Len() != 1 {
		t.Fatalf("len = %d, want 1 (no duplicate trace key)", s.Len())
	}
}

func TestEmptyRunIDInEventIgnored(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("", "t1", "run.started", "", ""))
	if s.Len() != 0 {
		t.Fatalf("len = %d, want 0 (empty runId event ignored)", s.Len())
	}
}

func TestIncompleteSnapshotNeverPrunes(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)
	s.MergeSnapshot([]TraceData{}, false)
	e := s.entries[runKey("r1")]
	if e == nil {
		t.Fatal("traced run deleted by incomplete snapshot")
	}
	if e.MissingCount != 0 {
		t.Errorf("missing = %d, want 0", e.MissingCount)
	}
	if !s.IsDegraded() {
		t.Fatal("expected degraded flag set")
	}
}

func TestPositiveMergeEvenWhenIncomplete(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, false)
	e := s.entries[runKey("r1")]
	if e == nil {
		t.Fatal("run missing after positive merge")
	}
	if e.Class != ClassTraced {
		t.Errorf("class = %s, want traced", e.Class)
	}
	if !s.IsDegraded() {
		t.Fatal("expected degraded flag set for incomplete snapshot")
	}
}

func TestGracePeriodPrune(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)

	s.MergeSnapshot([]TraceData{}, true)
	e := s.entries[runKey("r1")]
	if e == nil {
		t.Fatal("run deleted after first missing snapshot")
	}
	if e.MissingCount != 1 {
		t.Errorf("missing = %d, want 1", e.MissingCount)
	}
	k := s.ComputeKPI()
	if k.Quality != QualityApprox {
		t.Errorf("quality = %s, want approx", k.Quality)
	}

	s.MergeSnapshot([]TraceData{}, true)
	if e := s.entries[runKey("r1")]; e != nil {
		t.Fatal("run not deleted after two missing snapshots")
	}
	k = s.ComputeKPI()
	if k.Quality != QualityExact {
		t.Errorf("quality = %s, want exact", k.Quality)
	}
}

func TestKPIQualityMath(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)
	k := s.ComputeKPI()
	if k.ActiveRunsTraced != 1 {
		t.Errorf("traced = %d, want 1", k.ActiveRunsTraced)
	}
	if k.Quality != QualityExact {
		t.Errorf("quality = %s, want exact", k.Quality)
	}

	s2 := NewState()
	s2.MergeSnapshot([]TraceData{trace("r2", "t2", "running", "a1", "team1")}, true)
	k2 := s2.ComputeKPI()
	if k2.Quality != QualityExact {
		t.Errorf("quality = %s, want exact", k2.Quality)
	}
}

func TestEventOnlyExpiry(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	e := s.entries[runKey("r1")]
	e.LastEventAt = time.Now().Add(-70 * time.Minute)
	s.Expire()
	if s.Len() != 0 {
		t.Fatalf("len = %d, want 0 (event_only purged after 60m)", s.Len())
	}
}

func TestReconnectMarksUnverified(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MarkUnverified()
	e := s.entries[runKey("r1")]
	if e == nil || !e.Unverified {
		t.Fatal("expected event_only run marked unverified on reconnect")
	}
}

func TestTraceStatusTerminalDeletesAndTombstones(t *testing.T) {
	s := NewState()
	// Trace-only entry keyed by trace:<traceId> (empty run_id).
	s.MergeSnapshot([]TraceData{{RunID: "", TraceID: "t1", Status: "running"}}, true)
	if e := s.entries[traceKey("t1")]; e == nil {
		t.Fatal("expected trace-keyed entry")
	}
	// Non-terminal status update never removes a running trace.
	running := event("", "t1", "trace.status", "", "")
	running.Status = "running"
	s.ApplyEvent(running)
	if e := s.entries[traceKey("t1")]; e == nil {
		t.Fatal("running trace.status removed the trace entry")
	}

	// Terminal trace.status deletes it and tombstones by trace id.
	terminal := event("", "t1", "trace.status", "", "")
	terminal.Status = "completed"
	s.ApplyEvent(terminal)
	if e := s.entries[traceKey("t1")]; e != nil {
		t.Fatal("trace entry not deleted by terminal trace.status")
	}
	if _, ok := s.tombstones[traceKey("t1")]; !ok {
		t.Fatal("expected tombstone for terminated trace")
	}
	// Later snapshot containing t1 must not resurrect.
	s.MergeSnapshot([]TraceData{{RunID: "", TraceID: "t1", Status: "running"}}, true)
	if e := s.entries[traceKey("t1")]; e != nil {
		t.Fatal("terminated trace resurrected by snapshot")
	}
}

// TestTerminalTraceStatusRetiresTheRun pins the tombstone contract: the
// trace status event carries no run id, so a terminal status must retire the
// run that references the trace. Otherwise the finished run keeps counting
// as live and a later snapshot resurrects it.
func TestTerminalTraceStatusRetiresTheRun(t *testing.T) {
	s := NewState()

	// The run was announced and later confirmed by a trace snapshot.
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)
	if e := s.entries[runKey("r1")]; e == nil {
		t.Fatal("expected the run entry before the terminal trace status")
	}

	// Terminal trace status (the gateway payload carries no run id).
	s.ApplyEvent(goclaw.AgentEventPayload{
		TraceID: "t1",
		Event:   "trace.status",
		Status:  "completed",
	})

	if e := s.entries[runKey("r1")]; e != nil {
		t.Fatal("terminal trace status did not retire the run entry")
	}
	if _, ok := s.tombstones[runKey("r1")]; !ok {
		t.Fatal("expected a tombstone for the retired run")
	}
	if _, ok := s.tombstones[traceKey("t1")]; !ok {
		t.Fatal("expected a tombstone for the terminal trace")
	}

	// A later snapshot must not resurrect the finished run.
	s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)
	if e := s.entries[runKey("r1")]; e != nil {
		t.Fatal("snapshot resurrected a run that reached a terminal trace status")
	}
}

// TestNonTerminalTraceStatusKeepsTheRun proves the retirement is limited to
// terminal statuses.
func TestNonTerminalTraceStatusKeepsTheRun(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.started", "a1", "team1"))

	s.ApplyEvent(goclaw.AgentEventPayload{
		TraceID: "t1",
		Event:   "trace.status",
		Status:  "running",
	})

	if e := s.entries[runKey("r1")]; e == nil {
		t.Fatal("a non-terminal trace status removed the run entry")
	}
}

// TestKPIQualitySeparateTracedAndTotal pins the two-tier quality of the
// live KPI: the traced count is exact on a complete snapshot, but the
// merged total is never exact while an event-only run exists.
func TestKPIQualitySeparateTracedAndTotal(t *testing.T) {
	t.Run("traced only is exact", func(t *testing.T) {
		s := NewState()
		s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)

		kpi := s.ComputeKPI()
		if kpi.ActiveRunsTraced != 1 || kpi.ActiveRunsUntraced != 0 {
			t.Fatalf("kpi = %+v, want 1 traced and 0 untraced", kpi)
		}
		if kpi.TracedQuality != QualityExact {
			t.Errorf("traced quality = %q, want %q", kpi.TracedQuality, QualityExact)
		}
		if kpi.Quality != QualityExact {
			t.Errorf("total quality = %q, want %q", kpi.Quality, QualityExact)
		}
	})

	t.Run("event only run makes the total approximate", func(t *testing.T) {
		s := NewState()
		s.MergeSnapshot([]TraceData{trace("r1", "t1", "running", "a1", "team1")}, true)
		s.ApplyEvent(event("r2", "", "run.started", "a2", "team1"))

		kpi := s.ComputeKPI()
		if kpi.ActiveRunsUntraced != 1 {
			t.Fatalf("kpi = %+v, want 1 untraced run", kpi)
		}
		if kpi.TracedQuality != QualityExact {
			t.Errorf("traced quality = %q, want %q", kpi.TracedQuality, QualityExact)
		}
		if kpi.Quality != QualityApprox {
			t.Errorf("total quality = %q, want %q while an event-only run exists", kpi.Quality, QualityApprox)
		}
	})

	t.Run("event only run alone makes the total approximate", func(t *testing.T) {
		s := NewState()
		s.ApplyEvent(event("r2", "", "run.started", "a2", "team1"))

		kpi := s.ComputeKPI()
		if kpi.Quality != QualityApprox {
			t.Errorf("total quality = %q, want %q", kpi.Quality, QualityApprox)
		}
		if kpi.TracedQuality != QualityExact {
			t.Errorf("traced quality = %q, want %q", kpi.TracedQuality, QualityExact)
		}
	})
}

// TestExpireMarksUnverifiedThenPurges pins the expiry ladder of the
// event-only runs: no event for 30 minutes marks the entry unverified, no
// event for 60 minutes drops it.
func TestExpireMarksUnverifiedThenPurges(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "", "run.started", "a1", "team1"))

	// No event for 45 minutes: unverified but still counted.
	s.mu.Lock()
	s.entries[runKey("r1")].LastEventAt = time.Now().Add(-45 * time.Minute)
	s.mu.Unlock()
	s.Expire()

	e := s.entries[runKey("r1")]
	if e == nil {
		t.Fatal("event-only run was dropped before the purge window")
	}
	if !e.Unverified {
		t.Error("event-only run was not marked unverified")
	}

	// No event for 90 minutes: dropped.
	s.mu.Lock()
	s.entries[runKey("r1")].LastEventAt = time.Now().Add(-90 * time.Minute)
	s.mu.Unlock()
	s.Expire()

	if e := s.entries[runKey("r1")]; e != nil {
		t.Fatal("event-only run was not dropped after the purge window")
	}
}

// TestExpireRemovesExpiredTombstones pins the tombstone TTL cleanup.
func TestExpireRemovesExpiredTombstones(t *testing.T) {
	s := NewState()
	s.ApplyEvent(event("r1", "t1", "run.completed", "a1", "team1"))
	if _, ok := s.tombstones[runKey("r1")]; !ok {
		t.Fatal("expected a tombstone for the terminated run")
	}

	s.mu.Lock()
	s.tombstones[runKey("r1")] = time.Now().Add(-2 * tombstoneTTL)
	s.mu.Unlock()
	s.Expire()

	if _, ok := s.tombstones[runKey("r1")]; ok {
		t.Fatal("expired tombstone was not removed")
	}
}
