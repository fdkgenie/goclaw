package testutil

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

func mustDialWS(t *testing.T, f *FakeGoClaw) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(f.WSURL(), nil)
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func sendConnect(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	req := protocol.RequestFrame{
		Type:   protocol.FrameTypeRequest,
		ID:     "req-1",
		Method: "connect",
		Params: json.RawMessage(`{"tenant_id":"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}`),
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read connect response: %v", err)
	}
	var res protocol.ResponseFrame
	if err := json.Unmarshal(msg, &res); err != nil {
		t.Fatalf("parse connect response: %v", err)
	}
	if !res.OK {
		t.Fatalf("connect not ok: %+v", res.Error)
	}
	payload, ok := res.Payload.(map[string]any)
	if !ok {
		t.Fatalf("connect payload not map: %T", res.Payload)
	}
	return payload
}

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return uid
}

func TestFakeGoClawHTTPAndWS(t *testing.T) {
	tenantID := mustParseUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := NewFakeGoClaw(t, Config{
		Role:     "admin",
		TenantID: tenantID,
		IsOwner:  false,
		Capabilities: []string{
			protocol.CapEventsCronHeartbeatTenantScoped,
			"events.team_link_tenant_tagged",
			"usage.events.team",
			"teams.tasks.list.has_more",
		},
		Agents: []AgentData{
			{ID: "a1", Name: "Agent 1", TenantID: tenantID.String(), Role: "member", Model: "gpt-4", Status: "active"},
		},
		Version: "1.0.0",
	})

	// HTTP /v1/agents records tenant header.
	req, _ := http.NewRequest(http.MethodGet, f.URL()+"/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer ak")
	req.Header.Set("X-GoClaw-Tenant-Id", tenantID.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET agents: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("agents status = %d", resp.StatusCode)
	}

	headers := f.RecordedHTTPHeaders()
	if len(headers) == 0 {
		t.Fatal("no HTTP headers recorded")
	}
	if got := headers[0].Get("X-GoClaw-Tenant-Id"); got != tenantID.String() {
		t.Errorf("tenant header = %q, want %q", got, tenantID)
	}
	if got := headers[0].Get("Authorization"); got != "Bearer ak" {
		t.Errorf("auth header = %q, want Bearer ak", got)
	}

	// WS connect handshake.
	conn := mustDialWS(t, f)
	payload := sendConnect(t, conn)
	if got := payload["role"]; got != "admin" {
		t.Errorf("role = %v, want admin", got)
	}
	if got := payload["is_owner"]; got != false {
		t.Errorf("is_owner = %v, want false", got)
	}
	if got := payload["tenant_id"]; got != tenantID.String() {
		t.Errorf("tenant_id = %v, want %s", got, tenantID)
	}
	server, _ := payload["server"].(map[string]any)
	caps, _ := server["capabilities"].([]any)
	if len(caps) != 4 {
		t.Errorf("capabilities = %d, want 4", len(caps))
	}

	// Connect params recorded.
	cp := f.RecordedConnectParams()
	if len(cp) != 1 {
		t.Fatalf("connect params = %d, want 1", len(cp))
	}
	if cp[0].TenantID != tenantID.String() {
		t.Errorf("connect tenant = %q", cp[0].TenantID)
	}

	// Broadcast event reaches client.
	f.BroadcastEvent("team.updated", map[string]any{"team_id": tenantID.String()})
	_, evtMsg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var evt protocol.EventFrame
	if err := json.Unmarshal(evtMsg, &evt); err != nil {
		t.Fatalf("parse event: %v", err)
	}
	if evt.Event != "team.updated" {
		t.Errorf("event = %q, want team.updated", evt.Event)
	}
}

func TestFakeGoClawMissingCapability(t *testing.T) {
	tenantID := mustParseUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := NewFakeGoClaw(t, Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: []string{"events.cron_heartbeat_tenant_scoped"}, // missing others
		Agents:       []AgentData{},
	})
	conn := mustDialWS(t, f)
	payload := sendConnect(t, conn)
	server, _ := payload["server"].(map[string]any)
	caps, _ := server["capabilities"].([]any)
	found := false
	for _, c := range caps {
		if c == "teams.tasks.list.has_more" {
			found = true
		}
	}
	if found {
		t.Fatal("should not have teams.tasks.list.has_more capability")
	}
}

func TestFakeGoClawTracesPagination(t *testing.T) {
	tenantID := mustParseUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := NewFakeGoClaw(t, Config{
		TenantID: tenantID,
		TracesPages: [][]TraceData{
			{{RunID: "r1", TraceID: "t1"}, {RunID: "r2", TraceID: "t2"}},
			{{RunID: "r3", TraceID: "t3"}},
		},
	})
	resp, err := http.Get(f.URL() + "/v1/traces?offset=0&limit=1")
	if err != nil {
		t.Fatalf("GET traces: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Traces []TraceData `json:"traces"`
		Total  int         `json:"total"`
		Offset int         `json:"offset"`
		Limit  int         `json:"limit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode traces: %v", err)
	}
	if len(body.Traces) != 1 {
		t.Fatalf("page1 traces = %d, want 1", len(body.Traces))
	}
	if body.Total != 3 {
		t.Errorf("total = %d, want 3", body.Total)
	}
}
