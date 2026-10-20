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
			{ID: "a1", Name: "Agent 1", TenantID: tenantID.String(), Role: "member", Model: "gpt-4", Status: "active"},
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

	var out struct {
		CostUSD float64 `json:"cost_usd"`
	}
	if err := c.Get(context.Background(), "/v1/usage/events/summary", &out); err != nil {
		t.Fatalf("GET: %v", err)
	}
	if out.CostUSD != 123.45 {
		t.Errorf("cost_usd = %f, want 123.45", out.CostUSD)
	}
}

func mustParseUUIDTest(t *testing.T, s string) uuid.UUID {
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return uid
}
