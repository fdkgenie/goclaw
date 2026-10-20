package live

import (
	"encoding/json"
	"testing"
)

// gatewayTracesFixture is a verbatim /v1/traces page as the gateway
// serialises store.TraceData: the trace uuid is the "id" field, agent_id and
// team_id are nullable uuids, and created_at carries the row timestamp.
const gatewayTracesFixture = `{
  "traces": [
    {
      "id": "11111111-1111-1111-1111-111111111111",
      "agent_id": "22222222-2222-2222-2222-222222222222",
      "user_id": "dashboard",
      "session_key": "session-1",
      "status": "running",
      "name": "agent.run",
      "start_time": "2026-10-19T10:00:00Z",
      "total_input_tokens": 120,
      "total_output_tokens": 80,
      "total_cost": 0.25,
      "span_count": 3,
      "llm_call_count": 1,
      "tool_call_count": 2,
      "created_at": "2026-10-19T10:00:01Z"
    },
    {
      "id": "33333333-3333-3333-3333-333333333333",
      "agent_id": null,
      "user_id": "dashboard",
      "status": "running",
      "start_time": "2026-10-19T10:00:00Z",
      "created_at": "2026-10-19T10:00:01Z"
    },
    {
      "id": "44444444-4444-4444-4444-444444444444",
      "agent_id": "22222222-2222-2222-2222-222222222222",
      "run_id": "55555555-5555-5555-5555-555555555555",
      "status": "running",
      "start_time": "2026-10-19T10:00:00Z",
      "created_at": "2026-10-19T10:00:01Z"
    }
  ],
  "total": 3,
  "limit": 200,
  "offset": 0
}`

// TestTraceDataDecodesGatewayTraceID pins the gateway trace contract: the
// trace uuid arrives as "id". Decoding "trace_id" left every trace id empty,
// so all run-less traces collapsed onto the key "trace:".
func TestTraceDataDecodesGatewayTraceID(t *testing.T) {
	var resp tracesResponse
	if err := json.Unmarshal([]byte(gatewayTracesFixture), &resp); err != nil {
		t.Fatalf("decode traces page: %v", err)
	}
	if len(resp.Traces) != 3 {
		t.Fatalf("traces = %d, want 3", len(resp.Traces))
	}

	wantIDs := []string{
		"11111111-1111-1111-1111-111111111111",
		"33333333-3333-3333-3333-333333333333",
		"44444444-4444-4444-4444-444444444444",
	}
	for i, want := range wantIDs {
		if resp.Traces[i].TraceID != want {
			t.Errorf("traces[%d].trace_id = %q, want %q", i, resp.Traces[i].TraceID, want)
		}
	}
	if resp.Traces[0].AgentID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("traces[0].agent_id = %q, want the gateway uuid", resp.Traces[0].AgentID)
	}
	if resp.Traces[1].AgentID != "" {
		t.Errorf("traces[1].agent_id = %q, want empty for a null uuid", resp.Traces[1].AgentID)
	}
	if resp.Traces[2].RunID != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("traces[2].run_id = %q, want the gateway run id", resp.Traces[2].RunID)
	}
	if resp.Traces[0].UpdatedAt.IsZero() {
		t.Error("traces[0].updated_at = zero, want the gateway created_at")
	}
}

// TestMergeSnapshotKeepsRunlessTracesDistinct proves the collapse is gone:
// two running traces without a run_id must produce two live entries, not one
// shared "trace:" key.
func TestMergeSnapshotKeepsRunlessTracesDistinct(t *testing.T) {
	var resp tracesResponse
	if err := json.Unmarshal([]byte(gatewayTracesFixture), &resp); err != nil {
		t.Fatalf("decode traces page: %v", err)
	}

	s := NewState()
	s.MergeSnapshot(resp.Traces, true)

	entries := s.Snapshot()
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (two run-less traces must not collapse)", len(entries))
	}

	keys := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Key == "trace:" {
			t.Fatalf("entry %+v uses the empty trace key", e)
		}
		if keys[e.Key] {
			t.Fatalf("duplicate entry key %q", e.Key)
		}
		keys[e.Key] = true
	}
	for _, want := range []string{
		"trace:11111111-1111-1111-1111-111111111111",
		"trace:33333333-3333-3333-3333-333333333333",
		"run:55555555-5555-5555-5555-555555555555",
	} {
		if !keys[want] {
			t.Errorf("missing entry key %q (got %v)", want, keys)
		}
	}
}
