package aggregate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

const (
	orgTeamID   = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	orgLeadID   = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	orgMemberID = "6ba7b812-9dad-11d1-80b4-00c04fd430c8"
	orgLinkID   = "6ba7b813-9dad-11d1-80b4-00c04fd430c8"
)

// TestOrgChartUsesRealGatewayContract asserts the org chart is built from
// the real teams/members/agent-links contract: teams.list for the roster,
// teams.get with the camelCase teamId param for the members, and
// agents.links.list with the required agentId param for the delegation
// links.
func TestOrgChartUsesRealGatewayContract(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.list": map[string]any{
				"teams": []interface{}{
					map[string]any{
						"id":            orgTeamID,
						"name":          "Team 1",
						"lead_agent_id": orgLeadID,
					},
				},
				"count": 1,
			},
			"teams.get": map[string]any{
				"team": map[string]any{
					"id":            orgTeamID,
					"name":          "Team 1",
					"lead_agent_id": orgLeadID,
				},
				"members": []interface{}{
					map[string]any{
						"team_id":      orgTeamID,
						"agent_id":     orgLeadID,
						"role":         "lead",
						"agent_key":    "lead-1",
						"display_name": "Lead One",
					},
					map[string]any{
						"team_id":      orgTeamID,
						"agent_id":     orgMemberID,
						"role":         "member",
						"agent_key":    "member-1",
						"display_name": "Member One",
					},
				},
			},
			"agents.links.list": map[string]any{
				"links": []interface{}{
					map[string]any{
						"id":              orgLinkID,
						"source_agent_id": orgLeadID,
						"target_agent_id": orgMemberID,
						"direction":       "outbound",
						"status":          "active",
					},
				},
				"count": 1,
			},
		},
		Agents: []testutil.AgentData{
			{ID: orgLeadID, AgentKey: "lead-1", DisplayName: "Lead One", Model: "gpt-4", Status: "active"},
			{ID: orgMemberID, AgentKey: "member-1", DisplayName: "Member One", Model: "gpt-4", Status: "active"},
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

	// The RPC calls use the real parameter names.
	calls := f.RecordedRPCCalls()
	var sawTeamsList, sawTeamsGet, sawLinksList bool
	for _, call := range calls {
		switch call.Method {
		case "teams.list":
			sawTeamsList = true
		case "teams.get":
			sawTeamsGet = true
			var params map[string]any
			if err := json.Unmarshal(call.Params, &params); err != nil {
				t.Fatalf("parse teams.get params: %v", err)
			}
			if params["teamId"] != orgTeamID {
				t.Fatalf("teams.get params = %+v, want teamId=%s", params, orgTeamID)
			}
			if _, ok := params["team_id"]; ok {
				t.Fatalf("teams.get must not use team_id: %+v", params)
			}
		case "agents.links.list":
			sawLinksList = true
			var params map[string]any
			if err := json.Unmarshal(call.Params, &params); err != nil {
				t.Fatalf("parse agents.links.list params: %v", err)
			}
			if params["agentId"] == nil || params["agentId"] == "" {
				t.Fatalf("agents.links.list params = %+v, want an agentId", params)
			}
			if params["direction"] != "all" {
				t.Fatalf("agents.links.list direction = %v, want all", params["direction"])
			}
		}
	}
	if !sawTeamsList {
		t.Fatal("expected a teams.list call")
	}
	if !sawTeamsGet {
		t.Fatal("expected a teams.get call")
	}
	if !sawLinksList {
		t.Fatal("expected an agents.links.list call")
	}

	// The DAG carries the team, the lead, the member and the delegation edge.
	byID := map[string]OrgChartNode{}
	for _, n := range dag.Nodes {
		byID[n.ID] = n
	}
	team, ok := byID["team:"+orgTeamID]
	if !ok || team.Type != "team" || team.Label != "Team 1" {
		t.Fatalf("team node = %+v", team)
	}
	lead, ok := byID["agent:"+orgLeadID]
	if !ok || lead.Type != "lead" || lead.Label != "Lead One" {
		t.Fatalf("lead node = %+v", lead)
	}
	member, ok := byID["agent:"+orgMemberID]
	if !ok || member.Type != "member" || member.Label != "Member One" {
		t.Fatalf("member node = %+v", member)
	}

	var hierarchy, delegation int
	for _, e := range dag.Edges {
		switch e.Kind {
		case EdgeHierarchy:
			hierarchy++
		case EdgeDelegation:
			delegation++
		}
	}
	if hierarchy != 2 {
		t.Fatalf("hierarchy edges = %d, want 2 (team->lead, lead->member)", hierarchy)
	}
	if delegation != 1 {
		t.Fatalf("delegation edges = %d, want 1", delegation)
	}
}

// TestOrgChartMembersComeFromTeamsGet proves the member list of the chart
// comes from the teams.get response (the real members endpoint) and not from
// the agent list, which carries no team membership.
func TestOrgChartMembersComeFromTeamsGet(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.list": map[string]any{
				"teams": []interface{}{
					map[string]any{"id": orgTeamID, "name": "Team 1", "lead_agent_id": orgLeadID},
				},
			},
			"teams.get": map[string]any{
				"team": map[string]any{"id": orgTeamID, "name": "Team 1", "lead_agent_id": orgLeadID},
				"members": []interface{}{
					map[string]any{"agent_id": orgMemberID, "role": "member", "display_name": "Member One"},
				},
			},
			"agents.links.list": map[string]any{"links": []interface{}{}},
		},
		// The agent list carries no team membership (the real /v1/agents
		// payload has no team_id field).
		Agents: []testutil.AgentData{
			{ID: orgMemberID, AgentKey: "member-1", DisplayName: "Member One", Status: "active"},
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
	var found bool
	for _, n := range dag.Nodes {
		if n.ID == "agent:"+orgMemberID {
			found = true
			if n.Type != "member" {
				t.Fatalf("member node type = %q, want member", n.Type)
			}
		}
	}
	if !found {
		t.Fatalf("member node missing from the chart: %+v", dag.Nodes)
	}
}
