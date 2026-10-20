package live

import (
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const (
	tombstoneTTL             = 10 * time.Minute
	eventOnlyUnverifiedAfter = 30 * time.Minute
	eventOnlyPurgeAfter      = 60 * time.Minute
)

// State manages the live run consistency algorithm. It is safe for concurrent
// use.
type State struct {
	mu         sync.RWMutex
	entries    map[string]*LiveEntry
	tombstones map[string]time.Time
	degraded   bool
}

// NewState creates an empty State.
func NewState() *State {
	return &State{
		entries:    make(map[string]*LiveEntry),
		tombstones: make(map[string]time.Time),
	}
}

// runKey returns the entry key for a run.
func runKey(runID string) string { return "run:" + runID }

// traceKey returns the entry key for a trace.
func traceKey(traceID string) string { return "trace:" + traceID }

// entryKey returns the state key for a trace snapshot row: run:<runId> when
// present, otherwise trace:<traceId> for run-less traces.
func entryKey(tr TraceData) string {
	if tr.RunID != "" {
		return runKey(tr.RunID)
	}
	return traceKey(tr.TraceID)
}

func now() time.Time { return time.Now() }

// isTerminalTraceStatus reports whether a trace status ends the trace.
// Mirrors the gateway trace store terminal statuses.
func isTerminalTraceStatus(status string) bool {
	switch status {
	case "completed", "error", "cancelled":
		return true
	default:
		return false
	}
}

// ApplyEvent applies a single agent lifecycle or trace status event to the
// live state.
//
// Fail-closed rules:
//   - Empty runId (and not a trace.status) is ignored.
//   - Terminal run events delete the entry and tombstone run + trace.
//   - Terminal trace.status deletes the trace entry and tombstones it.
//   - run.started creates an event_only entry unless tombstoned.
//   - tool.call/tool.result update CurrentTool.
func (s *State) ApplyEvent(evt goclaw.AgentEventPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := now()

	switch evt.Event {
	case "run.completed", "run.failed", "run.cancelled":
		if evt.RunID == "" {
			return
		}
		rk := runKey(evt.RunID)
		delete(s.entries, rk)
		s.tombstones[rk] = now
		if evt.TraceID != "" {
			tk := traceKey(evt.TraceID)
			delete(s.entries, tk)
			s.tombstones[tk] = now
		}

	case "trace.status":
		// Terminal trace status: delete the trace entry and tombstone it so a
		// later snapshot cannot resurrect a finished trace. Non-terminal
		// status updates must not remove a running trace.
		if evt.TraceID == "" {
			return
		}
		if evt.Status != "" && !isTerminalTraceStatus(evt.Status) {
			return
		}
		tk := traceKey(evt.TraceID)
		delete(s.entries, tk)
		s.tombstones[tk] = now
		// The trace status event carries no run id, so the run is retired
		// through the entry that references the trace. Without this the run
		// kept counting as live and a later snapshot resurrected it.
		for key, e := range s.entries {
			if e.TraceID != evt.TraceID {
				continue
			}
			delete(s.entries, key)
			s.tombstones[key] = now
		}

	case "run.started":
		if evt.RunID == "" {
			return
		}
		rk := runKey(evt.RunID)
		if _, tombstoned := s.tombstones[rk]; tombstoned {
			return // do not resurrect a terminated run
		}
		if e, exists := s.entries[rk]; exists {
			e.LastEventAt = now
			if evt.TraceID != "" {
				e.TraceID = evt.TraceID
			}
			return
		}
		s.entries[rk] = &LiveEntry{
			Key:         rk,
			RunID:       evt.RunID,
			TraceID:     evt.TraceID,
			AgentID:     evt.AgentID,
			TeamID:      evt.TeamID,
			Class:       ClassEventOnly,
			LastEventAt: now,
		}

	case "tool.call":
		if evt.RunID == "" {
			return
		}
		if e := s.entries[runKey(evt.RunID)]; e != nil {
			e.CurrentTool = evt.Tool
			e.LastEventAt = now
		}

	case "tool.result":
		if evt.RunID == "" {
			return
		}
		if e := s.entries[runKey(evt.RunID)]; e != nil {
			e.CurrentTool = ""
			e.LastEventAt = now
		}
	}
}

// MergeSnapshot merges a trace snapshot into the live state (positive merge)
// and optionally prunes stale traced runs (negative prune).
//
// complete == true: snapshot is complete; negative prune runs.
// complete == false: snapshot is incomplete (degraded); positive merge still
// runs, but no pruning and no missing-count changes.
func (s *State) MergeSnapshot(traces []TraceData, complete bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := now()
	seen := make(map[string]bool, len(traces))

	// Positive merge: for each trace, promote/add as traced unless tombstoned.
	for _, tr := range traces {
		key := entryKey(tr)
		tk := traceKey(tr.TraceID)

		if _, tombstoned := s.tombstones[key]; tombstoned {
			continue
		}
		if _, tombstoned := s.tombstones[tk]; tombstoned {
			continue
		}

		if e, exists := s.entries[key]; exists {
			e.Class = ClassTraced
			e.TraceID = tr.TraceID
			e.RunID = tr.RunID
			e.MissingCount = 0
			e.Unverified = false
			e.LastEventAt = now
		} else {
			s.entries[key] = &LiveEntry{
				Key:         key,
				RunID:       tr.RunID,
				TraceID:     tr.TraceID,
				AgentID:     tr.AgentID,
				TeamID:      tr.TeamID,
				RunKind:     tr.RunKind,
				Class:       ClassTraced,
				LastEventAt: now,
			}
		}
		seen[key] = true
	}

	if !complete {
		// Incomplete snapshot: degrade, but never prune or change missing count.
		s.degraded = true
		return
	}

	// Negative prune: only when snapshot is complete.
	for key, e := range s.entries {
		if e.Class != ClassTraced {
			continue
		}
		if seen[key] {
			continue
		}
		e.MissingCount++
		if e.MissingCount >= 2 {
			delete(s.entries, key)
		}
	}

	s.degraded = false
}

// MarkUnverified marks all event_only entries as unverified (called on WS
// reconnect).
func (s *State) MarkUnverified() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.Class == ClassEventOnly {
			e.Unverified = true
		}
	}
}

// Expire purges event_only entries that have had no event for too long, and
// removes expired tombstones.
func (s *State) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := now()
	for key, e := range s.entries {
		if e.Class == ClassEventOnly && now.Sub(e.LastEventAt) > eventOnlyPurgeAfter {
			delete(s.entries, key)
		}
	}
	for key, t := range s.tombstones {
		if now.Sub(t) > tombstoneTTL {
			delete(s.tombstones, key)
		}
	}
}

// SetDegraded forces the degraded flag (e.g. traces endpoint failure).
func (s *State) SetDegraded(v bool) {
	s.mu.Lock()
	s.degraded = v
	s.mu.Unlock()
}

// ComputeKPI computes the aggregated live KPI with quality tags.
//
// The traced count is exact when the last snapshot was complete, the state is
// not degraded, no run is stale and no traced entry is missing (grace period
// not resolved). The merged total is never exact while an event-only run
// exists: those runs are best-effort and only known from agent events.
func (s *State) ComputeKPI() LiveKPI {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var traced, untraced int
	stale := false
	missing := false
	for _, e := range s.entries {
		switch e.Class {
		case ClassTraced:
			traced++
			if e.MissingCount > 0 {
				missing = true
			}
		case ClassEventOnly:
			untraced++
			if e.Unverified {
				stale = true
			}
		}
	}

	tracedQuality := QualityExact
	if s.degraded || stale || missing {
		tracedQuality = QualityApprox
	}
	quality := tracedQuality
	if untraced > 0 {
		// The total merges the traced count with the best-effort event-only
		// count, so it can never claim exactness.
		quality = QualityApprox
	}

	return LiveKPI{
		ActiveRunsTraced:   traced,
		ActiveRunsUntraced: untraced,
		ActiveRunsTotal:    traced + untraced,
		Quality:            quality,
		TracedQuality:      tracedQuality,
	}
}

// Snapshot returns a copy of all entries (for the SSE hub).
func (s *State) Snapshot() []*LiveEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*LiveEntry, 0, len(s.entries))
	for _, e := range s.entries {
		cp := *e
		out = append(out, &cp)
	}
	return out
}

// IsDegraded returns the degraded flag.
func (s *State) IsDegraded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.degraded
}

// Len returns the number of entries (for tests).
func (s *State) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
