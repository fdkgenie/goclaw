package goclaw

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func TestHTTPClientInjectsHeaders(t *testing.T) {
	tenantID := mustParseUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "Agent 1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	var out struct {
		Agents []testutil.AgentData `json:"agents"`
	}
	if err := c.Get(context.Background(), "/v1/agents", &out); err != nil {
		t.Fatalf("GET: %v", err)
	}
	if len(out.Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(out.Agents))
	}

	// Verify headers were recorded by the fake.
	headers := f.RecordedHTTPHeaders()
	if len(headers) == 0 {
		t.Fatal("no headers recorded")
	}
	if got := headers[0].Get("Authorization"); got != "Bearer ak" {
		t.Errorf("authorization = %q, want Bearer ak", got)
	}
	if got := headers[0].Get("X-GoClaw-Tenant-Id"); got != tenantID.String() {
		t.Errorf("tenant header = %q, want %s", got, tenantID)
	}
}

func TestHTTPClientBlocksNonAllowlistedPath(t *testing.T) {
	tenantID := mustParseUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	var out any
	err := c.Get(context.Background(), "/v1/chat/completions", &out)
	if err == nil {
		t.Fatal("expected error for blocked path")
	}
}

func TestHTTPClientUnmarshalsResponse(t *testing.T) {
	tenantID := mustParseUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 123.45},
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	// The gateway requires a time window on the usage-events endpoints.
	var out struct {
		Summary struct {
			CostUSD float64 `json:"cost_usd"`
		} `json:"summary"`
	}
	path := "/v1/usage/events/summary?from=2024-01-01T00:00:00Z&to=2024-01-02T00:00:00Z"
	if err := c.Get(context.Background(), path, &out); err != nil {
		t.Fatalf("GET: %v", err)
	}
	if out.Summary.CostUSD != 123.45 {
		t.Errorf("cost_usd = %f, want 123.45", out.Summary.CostUSD)
	}
}

// TestHTTPClientUsageEventsRequireWindow proves the dashboard cannot query
// the usage-events endpoints without an explicit time window: the gateway
// answers 400 and the client surfaces the error.
func TestHTTPClientUsageEventsRequireWindow(t *testing.T) {
	tenantID := mustParseUUIDTest(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: RequiredCapabilities,
	})
	c := NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)

	var out any
	if err := c.Get(context.Background(), "/v1/usage/events/summary", &out); err == nil {
		t.Fatal("expected an error when the time window is missing")
	}
}

func mustParseUUIDTest(t *testing.T, s string) uuid.UUID {
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return uid
}
