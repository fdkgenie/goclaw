// Package invariants contains the P0 tenant-isolation suite.
//
// This file is intentionally free of the `integration` build tag: the event
// delivery path (bus → per-client filter → WebSocket frame) needs no
// database, so the P0 cross-tenant event leak regression must run in every
// build, with or without Postgres.
package invariants

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// stubTenantStore resolves the tenants used by the event isolation test.
// Embedding the interface keeps the stub to the two lookups the connect
// handshake performs.
type stubTenantStore struct {
	store.TenantStore
	tenants map[uuid.UUID]*store.TenantData
}

func (s *stubTenantStore) GetTenant(ctx context.Context, id uuid.UUID) (*store.TenantData, error) {
	if t, ok := s.tenants[id]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("tenant not found: %s", id)
}

func (s *stubTenantStore) GetTenantBySlug(ctx context.Context, slug string) (*store.TenantData, error) {
	for _, t := range s.tenants {
		if t.Slug == slug {
			return t, nil
		}
	}
	return nil, fmt.Errorf("tenant not found: %s", slug)
}

// wsEventClient is a real WebSocket client connected to the gateway.
type wsEventClient struct {
	conn *websocket.Conn
}

// connectWS dials the gateway and performs the connect handshake.
func connectWS(t *testing.T, addr, token, userID string, tenantID uuid.UUID) *wsEventClient {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	params, err := json.Marshal(map[string]any{
		"token":     token,
		"user_id":   userID,
		"tenant_id": tenantID.String(),
	})
	if err != nil {
		t.Fatalf("marshal connect params: %v", err)
	}
	req, err := json.Marshal(protocol.RequestFrame{
		Type:   protocol.FrameTypeRequest,
		ID:     "req-connect",
		Method: protocol.MethodConnect,
		Params: params,
	})
	if err != nil {
		t.Fatalf("marshal connect request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, req); err != nil {
		t.Fatalf("write connect request: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read connect response: %v", err)
	}
	var resp protocol.ResponseFrame
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal connect response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("connect rejected: %+v", resp.Error)
	}
	body, ok := resp.Payload.(map[string]any)
	if !ok {
		t.Fatalf("connect payload is %T, want map", resp.Payload)
	}
	if body["tenant_id"] != tenantID.String() {
		t.Fatalf("connect tenant_id = %v, want %s", body["tenant_id"], tenantID)
	}

	return &wsEventClient{conn: conn}
}

// collectEventNames reads event frames for the given window and returns their names.
func collectEventNames(t *testing.T, c *wsEventClient, window time.Duration) []string {
	t.Helper()

	var names []string
	deadline := time.Now().Add(window)
	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return names
		}
		var frame protocol.EventFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("unmarshal event frame: %v", err)
		}
		if frame.Type == protocol.FrameTypeEvent {
			names = append(names, frame.Event)
		}
	}
}

// INVARIANT (P0): A client of tenant B must never receive cron or heartbeat
// events emitted for tenant A.
func TestTenantIsolation_CronAndHeartbeatEvents(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	tenantStore := &stubTenantStore{tenants: map[uuid.UUID]*store.TenantData{
		tenantA: {ID: tenantA, Name: "Tenant A", Slug: "tenant-a"},
		tenantB: {ID: tenantB, Name: "Tenant B", Slug: "tenant-b"},
	}}

	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Token = "invariant-token"
	cfg.Gateway.OwnerIDs = []string{"owner-a", "owner-b"}

	msgBus := bus.New()
	server := gateway.NewServer(cfg, msgBus, nil, nil)
	server.Router().SetTenantStore(tenantStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, start := gateway.StartTestServer(server, ctx)
	go start()

	clientA := connectWS(t, addr, cfg.Gateway.Token, "owner-a", tenantA)
	clientB := connectWS(t, addr, cfg.Gateway.Token, "owner-b", tenantB)

	// Emit tenant A cron + heartbeat events on the bus.
	msgBus.Broadcast(bus.Event{
		Name:     protocol.EventCron,
		TenantID: tenantA,
		Payload: store.CronEvent{
			Action:   "running",
			JobID:    "cron-job-1",
			UserID:   "owner-a",
			TenantID: tenantA,
		},
	})
	msgBus.Broadcast(bus.Event{
		Name:     protocol.EventHeartbeat,
		TenantID: tenantA,
		Payload: store.HeartbeatEvent{
			Action:   "running",
			AgentID:  "agent-1",
			TenantID: tenantA,
		},
	})

	gotA := collectEventNames(t, clientA, 2*time.Second)
	gotB := collectEventNames(t, clientB, 500*time.Millisecond)

	for _, name := range gotB {
		if name == protocol.EventCron || name == protocol.EventHeartbeat {
			t.Errorf("tenant isolation leak: tenant B client received %q event from tenant A", name)
		}
	}

	seenCron, seenHeartbeat := false, false
	for _, name := range gotA {
		switch name {
		case protocol.EventCron:
			seenCron = true
		case protocol.EventHeartbeat:
			seenHeartbeat = true
		}
	}
	if !seenCron {
		t.Errorf("tenant A client did not receive the cron event (got %v)", gotA)
	}
	if !seenHeartbeat {
		t.Errorf("tenant A client did not receive the heartbeat event (got %v)", gotA)
	}
}
