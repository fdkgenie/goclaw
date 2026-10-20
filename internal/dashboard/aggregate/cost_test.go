package aggregate

import (
	"context"
	"net/url"
	"strings"
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

// findRequest returns the recorded request whose path matches, or "".
func findRequest(requests []string, path string) string {
	for _, r := range requests {
		if strings.HasPrefix(r, path) {
			return r
		}
	}
	return ""
}

func TestCostOtherRow(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 120.0, "calls": 12},
		UsageBreakdown: []map[string]any{
			{"key": "team-a", "cost_usd": 70.0, "calls": 7, "total_tokens": 700},
			{"key": "team-b", "cost_usd": 30.0, "calls": 3, "total_tokens": 300},
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
	cs, err := Cost(ctx, client, resolve, CostQuery{})
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if cs.SummaryUSD != 120.0 {
		t.Fatalf("summary = %f, want 120", cs.SummaryUSD)
	}
	if cs.Calls != 12 {
		t.Fatalf("calls = %d, want 12", cs.Calls)
	}
	if len(cs.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(cs.Rows))
	}
	if cs.Rows[0].Label != "Team A" || cs.Rows[0].CostUSD != 70.0 {
		t.Fatalf("row0 = %+v", cs.Rows[0])
	}
	if cs.Rows[0].Calls != 7 || cs.Rows[0].TotalTokens != 700 {
		t.Fatalf("row0 metrics = %+v", cs.Rows[0])
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
	cs, err := Cost(ctx, client, func(string) (string, bool) { return "", false }, CostQuery{})
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
	cs, err := Cost(ctx, client, func(string) (string, bool) { return "", false }, CostQuery{})
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

// TestCostUsesUsageEventsContract asserts the cost page talks to the real
// gateway usage-events contract: both endpoints are called with an explicit
// time window (the gateway answers 400 without one) and the breakdown is
// grouped by team so every row key is a team id.
func TestCostUsesUsageEventsContract(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 12.5, "calls": 4},
		UsageBreakdown: []map[string]any{
			{"key": "team-a", "cost_usd": 12.5, "calls": 4},
		},
	})
	client := newAggHTTP(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Cost(ctx, client, nil, CostQuery{}); err != nil {
		t.Fatalf("Cost: %v", err)
	}

	requests := f.RecordedRequests()
	summaryReq := findRequest(requests, "/v1/usage/events/summary")
	if summaryReq == "" {
		t.Fatalf("no summary request recorded: %v", requests)
	}
	breakdownReq := findRequest(requests, "/v1/usage/events/breakdown")
	if breakdownReq == "" {
		t.Fatalf("no breakdown request recorded: %v", requests)
	}

	summaryQuery := requestQuery(t, summaryReq)
	if summaryQuery.Get("from") == "" || summaryQuery.Get("to") == "" {
		t.Fatalf("summary request %q is missing from/to", summaryReq)
	}
	if _, err := time.Parse(time.RFC3339, summaryQuery.Get("from")); err != nil {
		t.Fatalf("summary from is not RFC3339: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, summaryQuery.Get("to")); err != nil {
		t.Fatalf("summary to is not RFC3339: %v", err)
	}
	if summaryQuery.Get("group_by") != "" {
		t.Fatalf("summary request must not group by: %q", summaryReq)
	}

	breakdownQuery := requestQuery(t, breakdownReq)
	if breakdownQuery.Get("from") == "" || breakdownQuery.Get("to") == "" {
		t.Fatalf("breakdown request %q is missing from/to", breakdownReq)
	}
	if breakdownQuery.Get("group_by") != "team" {
		t.Fatalf("breakdown group_by = %q, want team", breakdownQuery.Get("group_by"))
	}
}

// TestCostTeamFilterSendsTeamID asserts the optional team filter is passed
// to the gateway as team_id.
func TestCostTeamFilterSendsTeamID(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	teamID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		UsageSummary: map[string]any{"cost_usd": 1},
		UsageBreakdown: []map[string]any{
			{"key": teamID, "cost_usd": 1},
		},
	})
	client := newAggHTTP(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Cost(ctx, client, nil, CostQuery{TeamID: teamID}); err != nil {
		t.Fatalf("Cost: %v", err)
	}

	for _, path := range []string{"/v1/usage/events/summary", "/v1/usage/events/breakdown"} {
		req := findRequest(f.RecordedRequests(), path)
		if req == "" {
			t.Fatalf("no %s request recorded", path)
		}
		if got := requestQuery(t, req).Get("team_id"); got != teamID {
			t.Fatalf("%s team_id = %q, want %q", path, got, teamID)
		}
	}
}

// requestQuery parses the query of a recorded request target.
func requestQuery(t *testing.T, request string) url.Values {
	t.Helper()
	idx := strings.Index(request, "?")
	if idx < 0 {
		return url.Values{}
	}
	values, err := url.ParseQuery(request[idx+1:])
	if err != nil {
		t.Fatalf("parse query %q: %v", request, err)
	}
	return values
}
