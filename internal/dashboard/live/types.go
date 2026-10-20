// Package live implements the dashboard's live run consistency algorithm: a
// two-tier consistency model tracking runs as "traced" (confirmed by gateway
// trace snapshot) or "event_only" (known only from WS agent events), with
// grace-period pruning, tombstones, and exact/approx quality tags.
package live

import (
	"encoding/json"
	"time"
)

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
//
// The traced and the untraced counts carry their own quality: the traced
// count is exact when the last snapshot was complete, the untraced count is
// always best-effort. Quality is the quality of the merged total, which can
// never be exact while an event-only run exists.
type LiveKPI struct {
	ActiveRunsTraced   int    `json:"active_runs_traced"`
	ActiveRunsUntraced int    `json:"active_runs_untraced"`
	ActiveRunsTotal    int    `json:"active_runs_total"`
	Quality            string `json:"quality"`        // QualityExact or QualityApprox
	TracedQuality      string `json:"traced_quality"` // QualityExact or QualityApprox
}

// TraceData is a trace record from a gateway trace snapshot.
//
// The gateway identifies a trace by "id" (store.TraceData.ID serialises the
// uuid under that name); the dashboard used to decode "trace_id", which no
// gateway payload carries. With an empty trace id every run-less trace
// collapsed onto the key "trace:", so two distinct running traces were
// counted as one.
type TraceData struct {
	RunID     string    `json:"run_id"`
	TraceID   string    `json:"trace_id"`
	Status    string    `json:"status"`
	AgentID   string    `json:"agent_id,omitempty"`
	TeamID    string    `json:"team_id,omitempty"`
	RunKind   string    `json:"run_kind,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// UnmarshalJSON decodes a gateway trace row.
//
// "id" is the gateway's trace identifier (store.TraceData.ID). "trace_id" is
// accepted as a fallback for payloads produced by other emitters, so a trace
// is never decoded with an empty id.
func (t *TraceData) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID        string    `json:"id"`
		TraceID   string    `json:"trace_id"`
		RunID     string    `json:"run_id"`
		Status    string    `json:"status"`
		AgentID   string    `json:"agent_id"`
		TeamID    string    `json:"team_id"`
		RunKind   string    `json:"run_kind"`
		UpdatedAt time.Time `json:"updated_at"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	t.TraceID = wire.ID
	if t.TraceID == "" {
		t.TraceID = wire.TraceID
	}
	t.RunID = wire.RunID
	t.Status = wire.Status
	t.AgentID = wire.AgentID
	t.TeamID = wire.TeamID
	t.RunKind = wire.RunKind
	t.UpdatedAt = wire.UpdatedAt
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = wire.CreatedAt
	}
	return nil
}
