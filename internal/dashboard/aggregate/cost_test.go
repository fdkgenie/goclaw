package aggregate

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func newAggHTTP(t *testing.T, f *testutil.FakeGoClaw, tenantID uuid.UUID) *goclaw.HTTPClient {
	t.Helper()
	return goclaw.NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)
}

func TestCostOtherRow(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 120.0},
		UsageBreakdown: []map[string]any{
			{"key": "team-a", "cost_usd": 70.0},
			{"key": "team-b", "cost_usd": 30.0},
		},
	})
	client := newAggHTTP(t, f, tenantID)

	resolve := func(teamID string) (string, bool) {
		if teamID == "team-a" {
			return "Team A", true
		}
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := Cost(ctx, client, resolve)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if cs.SummaryUSD != 120.0 {
		t.Fatalf("summary = %f, want 120", cs.SummaryUSD)
	}
	if len(cs.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(cs.Rows))
	}
	if cs.Rows[0].Label != "Team A" || cs.Rows[0].CostUSD != 70.0 {
		t.Fatalf("row0 = %+v", cs.Rows[0])
	}
	if cs.Other == nil {
		t.Fatal("expected Other row")
	}
	if cs.Other.CostUSD != 20.0 || cs.Other.Label != "Other" {
		t.Fatalf("other = %+v, want {20, Other}", cs.Other)
	}
}

func TestCostNoOtherWhenZeroDiff(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 50.0},
		UsageBreakdown: []map[string]any{
			{"key": "team-a", "cost_usd": 50.0},
		},
	})
	client := newAggHTTP(t, f, tenantID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := Cost(ctx, client, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if cs.Other != nil {
		t.Fatalf("expected no Other row, got %+v", cs.Other)
	}
}

func TestCostKeyEmptyAndDeleted(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	deleted := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 10.0},
		UsageBreakdown: []map[string]any{
			{"key": "", "cost_usd": 4.0},
			{"key": deleted, "cost_usd": 6.0},
		},
	})
	client := newAggHTTP(t, f, tenantID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := Cost(ctx, client, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if len(cs.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(cs.Rows))
	}
	if cs.Rows[0].Label != "Personal / Direct" {
		t.Fatalf("row0 label = %q, want Personal / Direct", cs.Rows[0].Label)
	}
	if cs.Rows[1].Label != "Deleted Team ("+deleted+")" {
		t.Fatalf("row1 label = %q", cs.Rows[1].Label)
	}
}
