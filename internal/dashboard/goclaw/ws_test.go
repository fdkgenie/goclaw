package goclaw

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid: %v", err)
	}
	return uid
}

func connectClient(t *testing.T, f *testutil.FakeGoClaw, tenantID uuid.UUID) *Client {
	t.Helper()
	c := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		UserID:       "user-1",
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := c.Connect(ctx); err != nil {
		cancel()
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cancel(); c.Close() })
	return c
}

func TestWSConnectSuccess(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := connectClient(t, f, tenantID)
	if !c.Connected() {
		t.Fatal("expected connected")
	}
	resp := c.ConnectResponse()
	if resp == nil {
		t.Fatal("connect response nil")
	}
	if resp.TenantID != tenantID.String() {
		t.Errorf("tenant = %q", resp.TenantID)
	}
}

func TestWSConnectMissingCapability(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: []string{protocol.CapEventsCronHeartbeatTenantScoped}, // missing B2 caps
	})
	c := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected connect to fail with missing capability")
	}
	if !IsCapabilityMissing(err) {
		t.Errorf("err = %v, want missing_capability", err)
	}
	c.Close()
}

func TestWSConnectWrongRole(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "operator",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected connect to fail with wrong role")
	}
	c.Close()
}

func TestWSConnectIsOwner(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      true,
		Capabilities: RequiredCapabilities,
	})
	c := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected connect to fail when is_owner")
	}
	c.Close()
}

func TestWSConnectTenantMismatch(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	other := mustUUID(t, "6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     other,
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected connect to fail on tenant mismatch")
	}
	c.Close()
}

func TestWSCall(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "Agent 1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
	})
	c := connectClient(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.Call(ctx, "teams.list", map[string]any{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("call not ok: %+v", resp.Error)
	}
	if resp.ID == "" {
		t.Fatal("empty response ID")
	}
}

// TestWSDisallowedRPCMethodRejected proves the client enforces the
// read-only allowlist: a mutating method is rejected before the frame is
// written, so the gateway never receives it.
func TestWSDisallowedRPCMethodRejected(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := connectClient(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	blocked := []string{
		"agents.delete",
		"agents.update",
		"agents.links.create",
		"chat.send",
		"config.apply",
		"teams.create",
		"teams.members.add",
		"exec.run",
	}
	for _, method := range blocked {
		resp, err := c.Call(ctx, method, map[string]any{"id": "x"})
		if err == nil {
			t.Fatalf("Call(%q) returned a response, want an error", method)
		}
		if resp != nil {
			t.Fatalf("Call(%q) returned a response frame, want nil", method)
		}
		if !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("Call(%q) error = %v, want a not-allowed error", method, err)
		}
	}

	if calls := f.RecordedRPCCalls(); len(calls) != 0 {
		t.Fatalf("gateway received %d rpc calls, want 0 (blocked methods must not be sent): %+v", len(calls), calls)
	}
}

// TestWSAllowedRPCMethodReachesGateway proves the allowlist does not break
// the read-only calls the dashboard actually makes.
func TestWSAllowedRPCMethodReachesGateway(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := connectClient(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, "agents.links.list", map[string]any{"agentId": "a1"}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	calls := f.RecordedRPCCalls()
	if len(calls) != 1 {
		t.Fatalf("gateway received %d rpc calls, want 1", len(calls))
	}
	if calls[0].Method != "agents.links.list" {
		t.Fatalf("gateway received method %q, want agents.links.list", calls[0].Method)
	}
}

func TestWSEventSubscription(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := connectClient(t, f, tenantID)

	ch := make(chan *EventFrame, 8)
	unsub := c.Subscribe("agent.started", ch)

	f.BroadcastEvent("agent.started", map[string]any{"agent_id": "a1"})

	select {
	case evt := <-ch:
		if evt.Event != "agent.started" {
			t.Errorf("event = %q, want agent.started", evt.Event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")
	}

	unsub()
	f.BroadcastEvent("agent.started", map[string]any{"agent_id": "a2"})
	select {
	case <-ch:
		t.Fatal("received event after unsubscribe")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWSOutstandingCallsFailOnDisconnect(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := connectClient(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, "teams.list", map[string]any{})
		done <- err
	}()

	c.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error on disconnected Call")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call did not return after disconnect")
	}
}

// TestWSIdleConnectionSurvivesGatewayPings covers the idle-connection
// regression: the client used to drop a healthy but idle gateway connection
// when its read deadline expired, because gorilla does not extend the
// deadline on control frames. The dashboard then flapped into its degraded
// (fail-closed, 503) state every minute.
func TestWSIdleConnectionSurvivesGatewayPings(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		PingInterval: 50 * time.Millisecond,
	})

	disconnected := make(chan error, 1)
	c := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		UserID:       "user-1",
		ReadDeadline: 200 * time.Millisecond,
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {
			select {
			case disconnected <- err:
			default:
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	// Idle for several read deadlines: only the gateway pings keep the
	// connection alive.
	time.Sleep(1 * time.Second)

	select {
	case err := <-disconnected:
		t.Fatalf("connection dropped while the gateway was pinging: %v", err)
	default:
	}
	if !c.Connected() {
		t.Fatal("expected the client to stay connected")
	}
}

func TestWSReconnectReverifiesCapabilities(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := connectClient(t, f, tenantID)
	if !c.Connected() {
		t.Fatal("expected connected")
	}

	// Force disconnect by closing the underlying WS connection.
	c.connMu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.connMu.Unlock()

	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if c.Connected() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !c.Connected() {
		t.Fatal("expected reconnect")
	}
	resp := c.ConnectResponse()
	if resp == nil || resp.TenantID != tenantID.String() {
		t.Errorf("reconnect tenant = %q", resp.TenantID)
	}
}

// TestConcurrentRPCsAreSerialized drives two RPC calls at the same time on
// the same connection. gorilla/websocket accepts a single writer: without a
// write lock the two frames interleave and corrupt the stream (the race
// detector flags it).
func TestConcurrentRPCsAreSerialized(t *testing.T) {
	tenantID := mustUUID(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.list": map[string]any{"teams": []interface{}{}, "count": 0},
		},
	})
	c := connectClient(t, f, tenantID)

	const calls = 8
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			resp, err := c.Call(ctx, "teams.list", map[string]any{})
			if err != nil {
				errs <- err
				return
			}
			if !resp.OK {
				errs <- fmt.Errorf("rpc not ok: %+v", resp.Error)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent rpc: %v", err)
	}

	if got := len(f.RecordedRPCCalls()); got != calls {
		t.Fatalf("gateway rpc calls = %d, want %d", got, calls)
	}
}
