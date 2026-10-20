package aggregate

import (
	"context"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func TestOverviewComposite(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	state := live.NewState()
	state.ApplyEvent(goclaw.AgentEventPayload{RunID: "r1", TraceID: "t1", Event: "run.started"})

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   []interface{}{1, 2, 3},
				"hasMore": false,
			},
		},
		UsageSummary: map[string]any{"cost_usd": 100.0},
		UsageBreakdown: []map[string]any{
			{"key": "team-a", "cost_usd": 100.0},
		},
		Agents: []testutil.AgentData{},
	})
	ws := newAggWS(t, f, tenantID)
	client := newAggHTTP(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ov, err := Overview(ctx, state, ws, client, []TeamRef{{ID: "team-a", Name: "Team A"}}, func(string) (string, bool) { return "Team A", true })
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.TaskKPI == nil || ov.TaskKPI.TotalActive.Value != 3 {
		t.Fatalf("task KPI = %+v, want total active 3", ov.TaskKPI)
	}
	if ov.Cost == nil || ov.Cost.SummaryUSD != 100.0 {
		t.Fatalf("cost = %+v, want summary 100", ov.Cost)
	}
	if ov.Live == nil {
		t.Fatal("expected live KPI")
	}
}
