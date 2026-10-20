// Package live implements the dashboard's live run consistency algorithm: a
// two-tier consistency model tracking runs as "traced" (confirmed by gateway
// trace snapshot) or "event_only" (known only from WS agent events), with
// grace-period pruning, tombstones, and exact/approx quality tags.
package live

import "time"

// RunClass describes how a live run is known.
const (
	ClassTraced    = "traced"     // confirmed by a trace snapshot
	ClassEventOnly = "event_only" // known only from agent events
)

// Quality describes the confidence tag on a KPI value.
const (
	QualityExact  = "exact"
	QualityApprox = "approx"
)

// LiveEntry is the in-memory representation of a single live run.
type LiveEntry struct {
	Key          string    `json:"key"`
	RunID        string    `json:"run_id"`
	TraceID      string    `json:"trace_id"`
	AgentID      string    `json:"agent_id"`
	TeamID       string    `json:"team_id"`
	RunKind      string    `json:"run_kind"`
	Class        string    `json:"class"` // ClassTraced or ClassEventOnly
	MissingCount int       `json:"missing_count"`
	Unverified   bool      `json:"unverified"`
	CurrentTool  string    `json:"current_tool"`
	LastEventAt  time.Time `json:"last_event_at"`
}

// LiveKPI aggregates live run counts with a quality tag.
type LiveKPI struct {
	ActiveRunsTraced   int    `json:"active_runs_traced"`
	ActiveRunsUntraced int    `json:"active_runs_untraced"`
	ActiveRunsTotal    int    `json:"active_runs_total"`
	Quality            string `json:"quality"` // QualityExact or QualityApprox
}

// TraceData is a trace record from a gateway trace snapshot.
type TraceData struct {
	RunID     string    `json:"run_id"`
	TraceID   string    `json:"trace_id"`
	Status    string    `json:"status"`
	AgentID   string    `json:"agent_id,omitempty"`
	TeamID    string    `json:"team_id,omitempty"`
	RunKind   string    `json:"run_kind,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}
