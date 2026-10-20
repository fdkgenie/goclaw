package goclaw

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func mustUUIDTest(t *testing.T, s string) uuid.UUID {
	t.Helper()
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return uid
}

func TestCanaryAllAgentsMatch(t *testing.T) {
	tenantID := mustUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	Reset()
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
			{ID: "a2", AgentKey: "a2", DisplayName: "A2", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := VerifyTenantCanary(ctx, c, tenantID); err != nil {
		t.Fatalf("canary should pass: %v", err)
	}
	if IsDegraded() {
		t.Fatal("expected not degraded after successful canary")
	}
}

func TestCanaryAnyAgentMismatch(t *testing.T) {
	tenantID := mustUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	other := mustUUIDTest(t, "6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	Reset()
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
			{ID: "a2", AgentKey: "a2", DisplayName: "A2", TenantID: other.String(), Model: "gpt-4", Status: "active"}, // mismatch
		},
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := VerifyTenantCanary(ctx, c, tenantID)
	if err == nil {
		t.Fatal("expected canary to fail on tenant mismatch")
	}
	if !IsDegraded() {
		t.Fatal("expected degraded after canary mismatch")
	}
	if Reason() == "" {
		t.Fatal("expected degradation reason")
	}
}

func TestCanaryEmptyAgentList(t *testing.T) {
	tenantID := mustUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	Reset()
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents:       []testutil.AgentData{},
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := VerifyTenantCanary(ctx, c, tenantID); err != nil {
		t.Fatalf("canary should pass with empty agent list: %v", err)
	}
	if IsDegraded() {
		t.Fatal("expected not degraded with empty agent list")
	}
}

func TestCanaryCircuitBreakerReset(t *testing.T) {
	b := &CircuitBreaker{}
	if b.IsDegraded() {
		t.Fatal("expected not degraded initially")
	}
	b.SetDegraded("tenant mismatch")
	if !b.IsDegraded() {
		t.Fatal("expected degraded after SetDegraded")
	}
	if b.Reason() != "tenant mismatch" {
		t.Errorf("reason = %q", b.Reason())
	}
	b.Reset()
	if b.IsDegraded() {
		t.Fatal("expected not degraded after Reset")
	}
	if b.Reason() != "" {
		t.Errorf("reason = %q after reset", b.Reason())
	}
}

func TestReconnectCanaryFailClosedThenRecovers(t *testing.T) {
	tenantID := mustUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	other := mustUUIDTest(t, "6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	Reset()
	t.Cleanup(Reset)

	agents := []testutil.AgentData{
		{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
	}
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents:       agents,
	})
	httpClient := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var reconnects atomic.Int64
	client := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		ReconnectMin: 20 * time.Millisecond,
		ReconnectMax: 100 * time.Millisecond,
		OnDisconnect: func(err error) {
			SetDegraded("disconnected: " + err.Error())
		},
		OnReconnect: func() error {
			reconnects.Add(1)
			return VerifyTenantCanary(ctx, httpClient, tenantID)
		},
	})
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	if err := VerifyTenantCanary(ctx, httpClient, tenantID); err != nil {
		t.Fatalf("initial canary: %v", err)
	}

	// Reconnect canary must fail while the gateway reports another tenant.
	f.SetAgents([]testutil.AgentData{
		{ID: "a2", AgentKey: "a2", DisplayName: "A2", TenantID: other.String(), Model: "gpt-4", Status: "active"},
	})
	forceDisconnect(client)
	waitFor(t, 5*time.Second, "degraded after disconnect", func() bool { return IsDegraded() })

	// The reconnect hook must run and keep failing: the client stays
	// disconnected and the breaker stays open.
	waitFor(t, 5*time.Second, "reconnect hook to run", func() bool { return reconnects.Load() > 0 })
	time.Sleep(300 * time.Millisecond)
	if !IsDegraded() {
		t.Fatal("expected service to stay degraded while canary fails")
	}
	if client.Connected() {
		t.Fatal("expected client to stay disconnected while the canary fails")
	}

	// Healing the tenant allows the next reconnect canary to reset the breaker.
	f.SetAgents(agents)
	waitFor(t, 10*time.Second, "recovered after canary", func() bool { return !IsDegraded() && client.Connected() })
}

// TestCloseStopsReconnectLoop proves Close() terminates the supervisor: no
// reconnect handshake may be attempted after Close returns.
func TestCloseStopsReconnectLoop(t *testing.T) {
	tenantID := mustUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	Reset()
	t.Cleanup(Reset)

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := NewClient(Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		ReconnectMin: 20 * time.Millisecond,
		ReconnectMax: 50 * time.Millisecond,
		OnDisconnect: func(err error) {},
	})
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	before := len(f.RecordedConnectParams())
	time.Sleep(300 * time.Millisecond)
	if after := len(f.RecordedConnectParams()); after != before {
		t.Fatalf("reconnect attempts after Close: %d (want 0)", after-before)
	}
	if client.Connected() {
		t.Fatal("expected client to be disconnected after Close")
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func forceDisconnect(c *Client) {
	c.connMu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.connMu.Unlock()
}

func TestCanaryLogsTenantMismatch(t *testing.T) {
	tenantID := mustUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	other := mustUUIDTest(t, "6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	Reset()
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: other.String(), Model: "gpt-4", Status: "active"},
		},
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := VerifyTenantCanary(ctx, c, tenantID); err == nil {
		t.Fatal("expected canary to fail")
	}
	if !IsDegraded() {
		t.Fatal("expected degraded; security.dashboard.tenant_mismatch should be logged")
	}
}
