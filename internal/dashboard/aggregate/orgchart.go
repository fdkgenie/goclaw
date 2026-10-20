package aggregate

import (
	"context"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

type teamsListResponse struct {
	Teams []teamSummary `json:"teams"`
}
type teamSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	LeadAgentID string `json:"lead_agent_id,omitempty"`
}

type teamGetResponse struct {
	Team teamSummary `json:"team"`
}

type agentsLinksListResponse struct {
	Links []agentLink `json:"links"`
}
type agentLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type agentsListResponse struct {
	Agents []agentSummary `json:"agents"`
}
type agentSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	TeamID string `json:"team_id,omitempty"`
	Role   string `json:"role,omitempty"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status,omitempty"`
}

// OrgChart builds the team org-chart DAG: team -> lead -> members, plus
// directed agent_links edges.
func OrgChart(ctx context.Context, ws *goclaw.Client, httpClient *goclaw.HTTPClient) (*OrgChartDAG, error) {
	// Fetch all agents (HTTP) for member status and names.
	var agentsResp agentsListResponse
	if err := httpClient.Get(ctx, "/v1/agents", &agentsResp); err != nil {
		return nil, fmt.Errorf("agents: %w", err)
	}
	agentByID := make(map[string]agentSummary, len(agentsResp.Agents))
	for _, a := range agentsResp.Agents {
		agentByID[a.ID] = a
	}

	// Fetch teams via WS RPC.
	var teamsResp teamsListResponse
	if err := wsRPC(ctx, ws, "teams.list", map[string]any{}, &teamsResp); err != nil {
		return nil, fmt.Errorf("teams.list: %w", err)
	}

	nodes := make([]OrgChartNode, 0, len(teamsResp.Teams)*3)
	edges := make([]OrgChartEdge, 0)
	teamMemberCount := make(map[string]int)

	for _, t := range teamsResp.Teams {
		tid := t.ID
		teamNode := OrgChartNode{ID: "team:" + tid, Type: "team", Label: t.Name, TeamID: tid}
		nodes = append(nodes, teamNode)

		// Fetch team detail (lead) via WS RPC.
		var teamResp teamGetResponse
		if err := wsRPC(ctx, ws, "teams.get", map[string]any{"team_id": tid}, &teamResp); err != nil {
			return nil, fmt.Errorf("teams.get team=%s: %w", tid, err)
		}
		leadID := teamResp.Team.LeadAgentID

		if leadID != "" {
			leadNode := OrgChartNode{ID: "agent:" + leadID, Type: "lead", Label: agentName(agentByID, leadID), TeamID: tid}
			nodes = append(nodes, leadNode)
			edges = append(edges, OrgChartEdge{From: "team:" + tid, To: "agent:" + leadID})
		}

		// Members: agents whose team_id == tid and not the lead.
		for _, a := range agentsResp.Agents {
			if a.TeamID == tid && a.ID != leadID {
				memberNode := OrgChartNode{ID: "agent:" + a.ID, Type: "member", Label: a.Name, TeamID: tid}
				nodes = append(nodes, memberNode)
				edges = append(edges, OrgChartEdge{From: "agent:" + leadID, To: "agent:" + a.ID})
				teamMemberCount[tid]++
			}
		}
	}

	// Agent links (delegation) edges.
	var linksResp agentsLinksListResponse
	if err := wsRPC(ctx, ws, "agents.links.list", map[string]any{}, &linksResp); err != nil {
		return nil, fmt.Errorf("agents.links.list: %w", err)
	}
	for _, l := range linksResp.Links {
		edges = append(edges, OrgChartEdge{From: "agent:" + l.Source, To: "agent:" + l.Target})
	}

	return &OrgChartDAG{Nodes: nodes, Edges: edges}, nil
}

func wsRPC(ctx context.Context, ws *goclaw.Client, method string, params any, out any) error {
	resp, err := ws.Call(ctx, method, params)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("rpc %s failed: %+v", method, resp.Error)
	}
	return unmarshalPayload(resp.Payload, out)
}

func agentName(byID map[string]agentSummary, id string) string {
	if a, ok := byID[id]; ok && a.Name != "" {
		return a.Name
	}
	return id
}
