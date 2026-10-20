package aggregate

import (
	"context"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func TestOrgChartDAG(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	leadID := "lead-1"
	memberID := "member-1"
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.list": map[string]any{
				"teams": []interface{}{
					map[string]any{"id": "team-1", "name": "Team 1", "lead_agent_id": leadID},
				},
			},
			"teams.get": map[string]any{
				"team": map[string]any{"id": "team-1", "name": "Team 1", "lead_agent_id": leadID},
			},
			"agents.links.list": map[string]any{
				"links": []interface{}{
					map[string]any{"source": leadID, "target": memberID},
				},
			},
		},
		Agents: []testutil.AgentData{
			{ID: leadID, Name: "Lead One", TeamID: "team-1", Role: "lead", Model: "gpt-4", Status: "active"},
			{ID: memberID, Name: "Member One", TeamID: "team-1", Role: "member", Model: "gpt-4", Status: "active"},
		},
	})
	ws := newAggWS(t, f, tenantID)
	client := newAggHTTP(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dag, err := OrgChart(ctx, ws, client)
	if err != nil {
		t.Fatalf("OrgChart: %v", err)
	}
	if len(dag.Nodes) < 3 {
		t.Fatalf("nodes = %d, want >= 3 (team, lead, member)", len(dag.Nodes))
	}
	if len(dag.Edges) < 2 {
		t.Fatalf("edges = %d, want >= 2 (team->lead, lead->member)", len(dag.Edges))
	}
}
