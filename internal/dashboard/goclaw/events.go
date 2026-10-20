package goclaw

import "time"

// agentEventWire is the gateway wire shape for the "agent" WS event payload.
// Gateway uses camelCase keys (internal/agent/loop_types.go AgentEvent).
type agentEventWire struct {
	Type    string `json:"type"`
	AgentID string `json:"agentId"`
	RunID   string `json:"runId"`
	RunKind string `json:"runKind,omitempty"`
	TeamID  string `json:"teamId,omitempty"`
	Payload struct {
		Name string `json:"name,omitempty"`
	} `json:"payload,omitempty"`
}

// traceStatusWire is the gateway wire shape for the "trace.status" WS event.
type traceStatusWire struct {
	TraceID string     `json:"traceId"`
	Status  string     `json:"status"`
	EndedAt *time.Time `json:"endedAt,omitempty"`
}

// AgentEventFromFrame converts an "agent" WS event frame into the dashboard's
// AgentEventPayload. Returns false for malformed or unrelated payloads.
func AgentEventFromFrame(frame *EventFrame) (AgentEventPayload, bool) {
	if frame == nil || frame.Event != "agent" {
		return AgentEventPayload{}, false
	}
	var wire agentEventWire
	if err := unmarshalPayload(frame.Payload, &wire); err != nil {
		return AgentEventPayload{}, false
	}
	if wire.Type == "" {
		return AgentEventPayload{}, false
	}
	return AgentEventPayload{
		RunID:     wire.RunID,
		AgentID:   wire.AgentID,
		TeamID:    wire.TeamID,
		RunKind:   wire.RunKind,
		Event:     wire.Type,
		Tool:      wire.Payload.Name,
		Timestamp: time.Now(),
	}, true
}

// TraceStatusFromFrame converts a "trace.status" WS event frame into the
// dashboard's AgentEventPayload.
func TraceStatusFromFrame(frame *EventFrame) (AgentEventPayload, bool) {
	if frame == nil || frame.Event != "trace.status" {
		return AgentEventPayload{}, false
	}
	var wire traceStatusWire
	if err := unmarshalPayload(frame.Payload, &wire); err != nil {
		return AgentEventPayload{}, false
	}
	if wire.TraceID == "" || wire.Status == "" {
		return AgentEventPayload{}, false
	}
	return AgentEventPayload{
		TraceID:   wire.TraceID,
		Event:     "trace.status",
		Status:    wire.Status,
		Timestamp: time.Now(),
	}, true
}
