package aggregate

import (
	"context"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

// nilUUID is the zero UUID the gateway marshals for an unset uuid field.
const nilUUID = "00000000-0000-0000-0000-000000000000"

// teamsListResponse is the gateway teams.list response envelope.
type teamsListResponse struct {
	Teams []teamSummary `json:"teams"`
	Count int           `json:"count"`
}

// teamSummary mirrors the gateway store.TeamData payload.
type teamSummary struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	LeadAgentID     string       `json:"lead_agent_id"`
	LeadAgentKey    string       `json:"lead_agent_key,omitempty"`
	LeadDisplayName string       `json:"lead_display_name,omitempty"`
	MemberCount     int          `json:"member_count"`
	Members         []teamMember `json:"members,omitempty"`
}

// teamMember mirrors the gateway store.TeamMemberData payload.
type teamMember struct {
	TeamID      string `json:"team_id"`
	AgentID     string `json:"agent_id"`
	Role        string `json:"role"`
	AgentKey    string `json:"agent_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// teamGetResponse is the gateway teams.get response envelope: the team plus
// its members.
type teamGetResponse struct {
	Team    teamSummary  `json:"team"`
	Members []teamMember `json:"members"`
}

// agentLinksListResponse is the gateway agents.links.list response envelope.
type agentLinksListResponse struct {
	Links []agentLink `json:"links"`
	Count int         `json:"count"`
}

// agentLink mirrors the gateway store.AgentLinkData payload.
type agentLink struct {
	ID             string `json:"id"`
	SourceAgentID  string `json:"source_agent_id"`
	TargetAgentID  string `json:"target_agent_id"`
	Direction      string `json:"direction"`
	Status         string `json:"status"`
	SourceAgentKey string `json:"source_agent_key,omitempty"`
	TargetAgentKey string `json:"target_agent_key,omitempty"`
}

// agentsListResponse is the /v1/agents response envelope.
type agentsListResponse struct {
	Agents []agentSummary `json:"agents"`
}

// agentSummary mirrors the gateway store.AgentData payload (the dashboard
// only needs the identity fields).
type agentSummary struct {
	ID          string `json:"id"`
	AgentKey    string `json:"agent_key"`
	DisplayName string `json:"display_name,omitempty"`
	Model       string `json:"model"`
	Status      string `json:"status"`
}

// OrgChart builds the team org-chart DAG: team -> lead -> members, plus the
// delegation edges between agents.
//
// Data sources (real gateway contract):
//   - GET /v1/agents: agent identity (uuid, key, display name, status).
//   - teams.list RPC: the team roster.
//   - teams.get RPC (teamId): the members of a team.
//   - agents.links.list RPC (agentId, direction): the delegation links of an
//     agent. The method requires an agentId, so it is called per agent of the
//     chart and the links are deduplicated.
func OrgChart(ctx context.Context, ws *goclaw.Client, httpClient *goclaw.HTTPClient) (*OrgChartDAG, error) {
	// Agent identity (HTTP).
	var agentsResp agentsListResponse
	if err := httpClient.Get(ctx, "/v1/agents", &agentsResp); err != nil {
		return nil, fmt.Errorf("agents: %w", err)
	}
	agentMeta := make(map[string]agentSummary, len(agentsResp.Agents))
	for _, a := range agentsResp.Agents {
		agentMeta[a.ID] = a
	}

	// Team roster (RPC).
	var teamsResp teamsListResponse
	if err := wsRPC(ctx, ws, "teams.list", map[string]any{}, &teamsResp); err != nil {
		return nil, fmt.Errorf("teams.list: %w", err)
	}

	dag := &OrgChartDAG{
		Nodes: make([]OrgChartNode, 0, len(teamsResp.Teams)*3),
		Edges: make([]OrgChartEdge, 0),
	}
	seenNodes := make(map[string]bool)
	seenEdges := make(map[string]bool)
	linkAgents := make(map[string]bool)

	addNode := func(n OrgChartNode) {
		if seenNodes[n.ID] {
			return
		}
		seenNodes[n.ID] = true
		dag.Nodes = append(dag.Nodes, n)
	}
	addEdge := func(e OrgChartEdge) {
		key := e.Kind + "|" + e.From + "|" + e.To
		if seenEdges[key] {
			return
		}
		seenEdges[key] = true
		dag.Edges = append(dag.Edges, e)
	}

	for _, t := range teamsResp.Teams {
		teamID := t.ID
		if teamID == "" {
			continue
		}
		addNode(OrgChartNode{ID: "team:" + teamID, Type: "team", Label: t.Name, TeamID: teamID})

		// Members (RPC). The gateway param is camelCase "teamId".
		var teamResp teamGetResponse
		if err := wsRPC(ctx, ws, "teams.get", map[string]any{"teamId": teamID}, &teamResp); err != nil {
			return nil, fmt.Errorf("teams.get team=%s: %w", teamID, err)
		}
		members := teamResp.Members
		if len(members) == 0 {
			members = t.Members
		}

		leadID := t.LeadAgentID
		if leadID == "" || leadID == nilUUID {
			leadID = teamResp.Team.LeadAgentID
		}
		if leadID == nilUUID {
			leadID = ""
		}

		labels := memberLabels(members)

		if leadID != "" {
			addNode(OrgChartNode{
				ID:     "agent:" + leadID,
				Type:   "lead",
				Label:  agentLabel(agentMeta, labels, leadID),
				TeamID: teamID,
			})
			addEdge(OrgChartEdge{From: "team:" + teamID, To: "agent:" + leadID, Kind: EdgeHierarchy})
			linkAgents[leadID] = true
		}

		for _, m := range members {
			if m.AgentID == "" || m.AgentID == leadID {
				continue
			}
			addNode(OrgChartNode{
				ID:     "agent:" + m.AgentID,
				Type:   "member",
				Label:  agentLabel(agentMeta, labels, m.AgentID),
				TeamID: teamID,
			})
			from := "team:" + teamID
			if leadID != "" {
				from = "agent:" + leadID
			}
			addEdge(OrgChartEdge{From: from, To: "agent:" + m.AgentID, Kind: EdgeHierarchy})
			linkAgents[m.AgentID] = true
		}
	}

	// Delegation links (RPC). agents.links.list requires an agentId, so it is
	// queried per agent of the chart and the links are deduplicated.
	for agentID := range linkAgents {
		var linksResp agentLinksListResponse
		if err := wsRPC(ctx, ws, "agents.links.list", map[string]any{"agentId": agentID, "direction": "all"}, &linksResp); err != nil {
			return nil, fmt.Errorf("agents.links.list agent=%s: %w", agentID, err)
		}
		for _, l := range linksResp.Links {
			if l.SourceAgentID == "" || l.TargetAgentID == "" {
				continue
			}
			addNode(OrgChartNode{
				ID:    "agent:" + l.SourceAgentID,
				Type:  "agent",
				Label: agentLabel(agentMeta, nil, l.SourceAgentID),
			})
			addNode(OrgChartNode{
				ID:    "agent:" + l.TargetAgentID,
				Type:  "agent",
				Label: agentLabel(agentMeta, nil, l.TargetAgentID),
			})
			addEdge(OrgChartEdge{
				From: "agent:" + l.SourceAgentID,
				To:   "agent:" + l.TargetAgentID,
				Kind: EdgeDelegation,
			})
		}
	}

	return dag, nil
}

// memberLabels indexes the display labels of a team member list.
func memberLabels(members []teamMember) map[string]string {
	labels := make(map[string]string, len(members))
	for _, m := range members {
		label := m.DisplayName
		if label == "" {
			label = m.AgentKey
		}
		if label == "" {
			label = m.AgentID
		}
		labels[m.AgentID] = label
	}
	return labels
}

// agentLabel resolves the display label of an agent, preferring the team
// member list and falling back to the agent metadata.
func agentLabel(meta map[string]agentSummary, labels map[string]string, id string) string {
	if label, ok := labels[id]; ok && label != "" {
		return label
	}
	if a, ok := meta[id]; ok {
		if a.DisplayName != "" {
			return a.DisplayName
		}
		if a.AgentKey != "" {
			return a.AgentKey
		}
	}
	return id
}

// ListTeams returns the teams the dashboard can see.
func ListTeams(ctx context.Context, ws *goclaw.Client) ([]TeamRef, error) {
	var resp teamsListResponse
	if err := wsRPC(ctx, ws, "teams.list", map[string]any{}, &resp); err != nil {
		return nil, fmt.Errorf("teams.list: %w", err)
	}
	out := make([]TeamRef, 0, len(resp.Teams))
	for _, t := range resp.Teams {
		out = append(out, TeamRef{ID: t.ID, Name: t.Name})
	}
	return out, nil
}

// TeamResolverFor builds a TeamResolver over the given teams.
func TeamResolverFor(teams []TeamRef) TeamResolver {
	names := make(map[string]string, len(teams))
	for _, t := range teams {
		names[t.ID] = t.Name
	}
	return func(teamID string) (string, bool) {
		name, ok := names[teamID]
		return name, ok
	}
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
