// Package testutil provides a fake GoClaw Gateway for deterministic dashboard
// tests. It speaks the gateway WS RPC protocol (pkg/protocol) and exposes a
// subset of HTTP endpoints the dashboard consumes.
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// TraceData is a minimal trace record the fake returns for /v1/traces.
type TraceData struct {
	RunID     string    `json:"run_id"`
	TraceID   string    `json:"trace_id"`
	Status    string    `json:"status"`
	TenantID  string    `json:"tenant_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AgentData is a minimal agent record the fake returns for /v1/agents.
type AgentData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TenantID  string `json:"tenant_id"`
	TeamID    string `json:"team_id,omitempty"`
	Role      string `json:"role"`
	Model     string `json:"model"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

// Config configures the fake GoClaw server.
type Config struct {
	// Role advertised in connect response ("admin" or "operator").
	Role string
	// TenantID advertised in connect response and echoed in HTTP headers.
	TenantID uuid.UUID
	// IsOwner advertised in connect response.
	IsOwner bool
	// Capabilities advertised in connect response.
	Capabilities []string
	// RPCPayloads returns a canned payload for the given RPC method (keyed by
	// method name). Used by aggregator tests.
	RPCPayloads map[string]any
	// Agents returned by GET /v1/agents.
	Agents []AgentData
	// TracesPages pages returned by GET /v1/traces (paginated by offset/limit).
	TracesPages [][]TraceData
	// UsageSummary returned by GET /v1/usage/events/summary.
	UsageSummary map[string]any
	// UsageBreakdown returned by GET /v1/usage/events/breakdown.
	UsageBreakdown []map[string]any
	// Version advertised in connect response.
	Version string
}

// FakeGoClaw is an in-process fake gateway (HTTP + WS).
type FakeGoClaw struct {
	server   *httptest.Server
	upgrader websocket.Upgrader

	cfg     Config
	mu      sync.Mutex
	writeMu sync.Mutex // serializes writes to any WS conn (gorilla not safe for concurrent writes)
	conns   map[*websocket.Conn]bool

	recordedHTTP     []http.Header
	recordedConnect  []connectParams
	connectResponses int
}

type connectParams struct {
	Token      string `json:"token"`
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id"`
	RemoteAddr string
}

// NewFakeGoClaw starts the fake server. t.Cleanup is used to close it.
func NewFakeGoClaw(t interface{ Cleanup(func()) }, cfg Config) *FakeGoClaw {
	f := &FakeGoClaw{
		cfg:   cfg,
		conns: make(map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agents", f.handleAgents)
	mux.HandleFunc("/v1/traces", f.handleTraces)
	mux.HandleFunc("/v1/usage/events/summary", f.handleUsageSummary)
	mux.HandleFunc("/v1/usage/events/breakdown", f.handleUsageBreakdown)
	mux.HandleFunc("/v1/usage/events/timeseries", f.handleUsageTimeseries)
	mux.HandleFunc("/v1/activity", f.handleActivity)
	mux.HandleFunc("/ws", f.handleWS)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// URL returns the HTTP base URL.
func (f *FakeGoClaw) URL() string { return f.server.URL }

// WSURL returns the WebSocket URL (ws scheme).
func (f *FakeGoClaw) WSURL() string {
	u := f.server.URL
	if len(u) >= 5 && u[:5] == "https" {
		return "wss" + u[5:] + "/ws"
	}
	return "ws" + u[4:] + "/ws"
}

// Close shuts down the fake server and all WS connections.
func (f *FakeGoClaw) Close() {
	f.mu.Lock()
	for c := range f.conns {
		c.Close()
	}
	f.conns = nil
	f.mu.Unlock()
	f.server.Close()
}

// RecordedHTTPHeaders returns headers captured from outgoing dashboard HTTP
// requests (Authorization, X-GoClaw-Tenant-Id).
func (f *FakeGoClaw) RecordedHTTPHeaders() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]http.Header, len(f.recordedHTTP))
	for i, h := range f.recordedHTTP {
		out[i] = h.Clone()
	}
	return out
}

// RecordedConnectParams returns connect params captured from WS handshakes.
func (f *FakeGoClaw) RecordedConnectParams() []connectParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]connectParams, len(f.recordedConnect))
	copy(out, f.recordedConnect)
	return out
}

// SetAgents replaces the agents returned by GET /v1/agents (for tests that
// change tenant state between reconnects).
func (f *FakeGoClaw) SetAgents(agents []AgentData) {
	f.mu.Lock()
	f.cfg.Agents = agents
	f.mu.Unlock()
}

// DropConnections force-closes every open WebSocket connection, simulating a
// gateway restart or a network drop.
func (f *FakeGoClaw) DropConnections() {
	f.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(f.conns))
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// BroadcastEvent pushes an event frame to all connected WS clients.
func (f *FakeGoClaw) BroadcastEvent(event string, payload any) {
	frame := protocol.NewEvent(event, payload)
	raw, err := json.Marshal(frame)
	if err != nil {
		return
	}
	f.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(f.conns))
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	for _, c := range conns {
		f.writeMu.Lock()
		_ = c.WriteMessage(websocket.TextMessage, raw)
		f.writeMu.Unlock()
	}
}

// ----- HTTP handlers -----

func (f *FakeGoClaw) recordHeaders(r *http.Request) {
	f.mu.Lock()
	f.recordedHTTP = append(f.recordedHTTP, r.Header.Clone())
	f.mu.Unlock()
}

func (f *FakeGoClaw) handleAgents(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f.mu.Lock()
	agents := append([]AgentData(nil), f.cfg.Agents...)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"agents": agents})
}

func (f *FakeGoClaw) handleTraces(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	offset := 0
	limit := 200
	if v := r.URL.Query().Get("offset"); v != "" {
		fmtScan(v, &offset)
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		fmtScan(v, &limit)
	}
	total := 0
	for _, p := range f.cfg.TracesPages {
		total += len(p)
	}
	var page []TraceData
	remaining := offset
	for _, p := range f.cfg.TracesPages {
		if remaining < len(p) {
			end := remaining + limit
			if end > len(p) {
				end = len(p)
			}
			page = p[remaining:end]
			break
		}
		remaining -= len(p)
	}
	if page == nil {
		page = []TraceData{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"traces": page,
		"total":  total,
		"offset": offset,
		"limit":  limit,
	})
}

func (f *FakeGoClaw) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	if f.cfg.UsageSummary == nil {
		f.cfg.UsageSummary = map[string]any{"cost_usd": 0}
	}
	_ = json.NewEncoder(w).Encode(f.cfg.UsageSummary)
}

func (f *FakeGoClaw) handleUsageBreakdown(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	if f.cfg.UsageBreakdown == nil {
		f.cfg.UsageBreakdown = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"rows": f.cfg.UsageBreakdown})
}

func (f *FakeGoClaw) handleUsageTimeseries(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"points": []any{}})
}

func (f *FakeGoClaw) handleActivity(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
}

// ----- WebSocket handler -----

func (f *FakeGoClaw) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conns[conn] = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.conns, conn)
		f.mu.Unlock()
		conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var raw protocol.RawFrame
		if err := json.Unmarshal(msg, &raw); err != nil {
			continue
		}
		switch raw.Type {
		case protocol.FrameTypeRequest:
			var req protocol.RequestFrame
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			f.handleRPC(conn, &req)
		}
	}
}

func (f *FakeGoClaw) handleRPC(conn *websocket.Conn, req *protocol.RequestFrame) {
	switch req.Method {
	case "connect":
		var cp connectParams
		_ = json.Unmarshal(req.Params, &cp)
		f.mu.Lock()
		f.recordedConnect = append(f.recordedConnect, cp)
		f.connectResponses++
		f.mu.Unlock()

		caps := f.cfg.Capabilities
		if caps == nil {
			caps = []string{protocol.CapEventsCronHeartbeatTenantScoped}
		}
		role := f.cfg.Role
		if role == "" {
			role = "admin"
		}
		tenantID := f.cfg.TenantID
		if tenantID == uuid.Nil {
			tenantID = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
		}
		payload := map[string]any{
			"protocol":  protocol.ProtocolVersion,
			"role":      role,
			"user_id":   cp.UserID,
			"tenant_id": tenantID.String(),
			"is_owner":  f.cfg.IsOwner,
			"edition":   f.cfg.Version,
			"server": map[string]any{
				"name":         "goclaw",
				"version":      f.cfg.Version,
				"capabilities": caps,
			},
		}
		f.writeMu.Lock()
		_ = conn.WriteJSON(protocol.NewOKResponse(req.ID, payload))
		f.writeMu.Unlock()
	default:
		payload := map[string]any{"ok": true}
		if f.cfg.RPCPayloads != nil {
			if mock, ok := f.cfg.RPCPayloads[req.Method]; ok {
				payload = mock.(map[string]any)
			}
		}
		f.writeMu.Lock()
		_ = conn.WriteJSON(protocol.NewOKResponse(req.ID, payload))
		f.writeMu.Unlock()
	}
}

func fmtScan(s string, out *int) {
	_, _ = fmt.Sscanf(s, "%d", out)
}
