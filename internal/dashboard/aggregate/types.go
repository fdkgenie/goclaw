// Package aggregate computes dashboard KPIs, cost breakdowns, org charts, and
// composite overview data from GoClaw Gateway data fetched via the WS and HTTP
// clients (read-only, allow-listed).
package aggregate

// TaskKPI is a count with a quality flag: exact when the count is complete,
// false when truncated by pagination (hasMore).
type TaskKPI struct {
	Value int  `json:"value"`
	Exact bool `json:"exact"`
}

// TeamTaskKPI is the task KPI for a single team.
type TeamTaskKPI struct {
	TeamID   string  `json:"team_id"`
	TeamName string  `json:"team_name,omitempty"`
	Active   TaskKPI `json:"active"`
	InReview TaskKPI `json:"in_review"`
}

// TaskKPIResult aggregates task KPIs across teams.
type TaskKPIResult struct {
	Teams         []TeamTaskKPI `json:"teams"`
	TotalActive   TaskKPI       `json:"total_active"`
	TotalInReview TaskKPI       `json:"total_in_review"`
}

// CostBreakdownRow is a single row in a cost breakdown.
type CostBreakdownRow struct {
	Key     string  `json:"key"`
	Label   string  `json:"label,omitempty"`
	CostUSD float64 `json:"cost_usd"`
}

// CostSummary is a cost breakdown with an optional "Other" row.
type CostSummary struct {
	SummaryUSD float64            `json:"summary_usd"`
	Rows       []CostBreakdownRow `json:"rows"`
	Other      *CostBreakdownRow  `json:"other,omitempty"`
}

// OrgChartNode is a node in the team org-chart DAG.
type OrgChartNode struct {
	ID     string `json:"id"`
	Type   string `json:"type"` // team, lead, member
	Label  string `json:"label"`
	TeamID string `json:"team_id,omitempty"`
}

// OrgChartEdge is a directed edge in the org-chart DAG.
type OrgChartEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// OrgChartDAG is the team -> lead -> members org chart with agent_links edges.
type OrgChartDAG struct {
	Nodes []OrgChartNode `json:"nodes"`
	Edges []OrgChartEdge `json:"edges"`
}

// OverviewKPI is the composite dashboard overview.
type OverviewKPI struct {
	Live    interface{}    `json:"live"`
	TaskKPI *TaskKPIResult `json:"task_kpi"`
	Cost    *CostSummary   `json:"cost"`
}
