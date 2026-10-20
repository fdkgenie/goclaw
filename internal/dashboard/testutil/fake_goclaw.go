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

// TraceData is a minimal trace record the fake returns for /v1/traces. The
// field names mirror the gateway store.TraceData payload: the trace uuid is
// serialised as "id", not "trace_id".
type TraceData struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Status    string    `json:"status"`
	TenantID  string    `json:"tenant_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AgentData is a minimal agent record the fake returns for /v1/agents. The
// field names mirror the gateway store.AgentData payload.
type AgentData struct {
	ID          string `json:"id"`
	AgentKey    string `json:"agent_key"`
	DisplayName string `json:"display_name,omitempty"`
	TenantID    string `json:"tenant_id"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	AgentType   string `json:"agent_type,omitempty"`
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
	// RPCHandler builds the payload for an RPC method from the raw request
	// params. It takes precedence over RPCPayloads when it returns ok.
	RPCHandler func(method string, params json.RawMessage) (any, bool)
	// RPCErrors maps an RPC method to the error message the fake answers it
	// with. Those methods get an error response (ok=false) instead of a
	// payload.
	RPCErrors map[string]string
	// Agents returned by GET /v1/agents.
	Agents []AgentData
	// TracesPages pages returned by GET /v1/traces (paginated by offset/limit).
	TracesPages [][]TraceData
	// UsageSummary returned by GET /v1/usage/events/summary (the "summary"
	// object of the response envelope).
	UsageSummary map[string]any
	// UsageBreakdown returned by GET /v1/usage/events/breakdown (the "rows"
	// of the response envelope).
	UsageBreakdown []map[string]any
	// ActivityLogs returned by GET /v1/activity (the "logs" array of the
	// response envelope).
	ActivityLogs []map[string]any
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
	recordedRequests []string
	recordedConnect  []connectParams
	recordedRPC      []rpcCall
	connectResponses int
}

type connectParams struct {
	Token      string `json:"token"`
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id"`
	RemoteAddr string
}

// rpcCall is one RPC request frame the fake received from a client.
type rpcCall struct {
	Method string
	Params json.RawMessage
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

// RecordedRPCCalls returns the RPC request frames the fake received (the
// "connect" handshake excluded).
func (f *FakeGoClaw) RecordedRPCCalls() []rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]rpcCall, len(f.recordedRPC))
	copy(out, f.recordedRPC)
	return out
}

// RecordedRequests returns the request targets (path + query) of every HTTP
// request the fake served.
func (f *FakeGoClaw) RecordedRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.recordedRequests))
	copy(out, f.recordedRequests)
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
	f.recordedRequests = append(f.recordedRequests, r.URL.RequestURI())
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

// requireUsageWindow mirrors the gateway usage-events filter parsing: both
// from and to are required, must be RFC3339 and to must be after from.
func requireUsageWindow(r *http.Request) error {
	values := r.URL.Query()
	fromRaw := values.Get("from")
	toRaw := values.Get("to")
	if fromRaw == "" || toRaw == "" {
		return fmt.Errorf("from and to are required")
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		return fmt.Errorf("invalid from")
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		return fmt.Errorf("invalid to")
	}
	if !to.After(from) {
		return fmt.Errorf("to must be after from")
	}
	return nil
}

func (f *FakeGoClaw) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	if err := requireUsageWindow(r); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	f.mu.Lock()
	summary := f.cfg.UsageSummary
	f.mu.Unlock()
	if summary == nil {
		summary = map[string]any{"cost_usd": 0}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"summary": summary})
}

func (f *FakeGoClaw) handleUsageBreakdown(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	if err := requireUsageWindow(r); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	f.mu.Lock()
	rows := f.cfg.UsageBreakdown
	f.mu.Unlock()
	if rows == nil {
		rows = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows})
}

func (f *FakeGoClaw) handleUsageTimeseries(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"points": []any{}})
}

func (f *FakeGoClaw) handleActivity(w http.ResponseWriter, r *http.Request) {
	f.recordHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	logs := f.cfg.ActivityLogs
	f.mu.Unlock()
	if logs == nil {
		logs = []map[string]any{}
	}
	// The gateway returns the audit rows under "logs".
	_ = json.NewEncoder(w).Encode(map[string]any{
		"logs":   logs,
		"total":  len(logs),
		"limit":  50,
		"offset": 0,
	})
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
		f.mu.Lock()
		f.recordedRPC = append(f.recordedRPC, rpcCall{Method: req.Method, Params: req.Params})
		errMsg, failed := f.cfg.RPCErrors[req.Method]
		f.mu.Unlock()
		if failed {
			f.writeMu.Lock()
			_ = conn.WriteJSON(protocol.NewErrorResponse(req.ID, protocol.ErrInternal, errMsg))
			f.writeMu.Unlock()
			return
		}
		payload := map[string]any{"ok": true}
		if f.cfg.RPCHandler != nil {
			if mock, ok := f.cfg.RPCHandler(req.Method, req.Params); ok {
				payload = mock.(map[string]any)
			}
		} else if f.cfg.RPCPayloads != nil {
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
