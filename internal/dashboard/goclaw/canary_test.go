package goclaw

import (
	"context"
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
			{ID: "a1", Name: "A1", TenantID: tenantID.String(), Role: "member", Model: "gpt-4", Status: "active"},
			{ID: "a2", Name: "A2", TenantID: tenantID.String(), Role: "member", Model: "gpt-4", Status: "active"},
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
			{ID: "a1", Name: "A1", TenantID: tenantID.String(), Role: "member", Model: "gpt-4", Status: "active"},
			{ID: "a2", Name: "A2", TenantID: other.String(), Role: "member", Model: "gpt-4", Status: "active"}, // mismatch
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
			{ID: "a1", Name: "A1", TenantID: other.String(), Role: "member", Model: "gpt-4", Status: "active"},
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
