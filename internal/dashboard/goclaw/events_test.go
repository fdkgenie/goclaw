package goclaw

import "testing"

func TestAgentEventFromFrame(t *testing.T) {
	frame := &EventFrame{
		Event: "agent",
		Payload: map[string]any{
			"type":    "tool.call",
			"agentId": "a1",
			"runId":   "r1",
			"runKind": "delegation",
			"teamId":  "team1",
			"payload": map[string]any{"name": "read_file"},
		},
	}

	evt, ok := AgentEventFromFrame(frame)
	if !ok {
		t.Fatal("expected agent event to parse")
	}
	if evt.Event != "tool.call" || evt.RunID != "r1" || evt.AgentID != "a1" || evt.TeamID != "team1" {
		t.Fatalf("unexpected event: %+v", evt)
	}
	if evt.RunKind != "delegation" {
		t.Errorf("run kind = %q", evt.RunKind)
	}
	if evt.Tool != "read_file" {
		t.Errorf("tool = %q, want read_file", evt.Tool)
	}
}

func TestAgentEventFromFrameRejectsOtherEvents(t *testing.T) {
	if _, ok := AgentEventFromFrame(&EventFrame{Event: "chat", Payload: map[string]any{"type": "run.started"}}); ok {
		t.Fatal("chat event must not parse as agent event")
	}
	if _, ok := AgentEventFromFrame(&EventFrame{Event: "agent", Payload: map[string]any{}}); ok {
		t.Fatal("agent event without type must not parse")
	}
}

func TestTraceStatusFromFrame(t *testing.T) {
	frame := &EventFrame{
		Event:   "trace.status",
		Payload: map[string]any{"traceId": "t1", "status": "completed"},
	}
	evt, ok := TraceStatusFromFrame(frame)
	if !ok {
		t.Fatal("expected trace status to parse")
	}
	if evt.Event != "trace.status" || evt.TraceID != "t1" || evt.Status != "completed" {
		t.Fatalf("unexpected event: %+v", evt)
	}
}
