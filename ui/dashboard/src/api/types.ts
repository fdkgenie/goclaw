export interface TaskKPI {
  value: number;
  exact: boolean;
}

export interface StatusTaskKPI {
  status: string;
  count: TaskKPI;
}

export interface TeamTaskKPI {
  team_id: string;
  team_name?: string;
  active: TaskKPI;
  in_review: TaskKPI;
  active_by_status: StatusTaskKPI[];
}

export interface TaskKPIResult {
  teams: TeamTaskKPI[];
  total_active: TaskKPI;
  total_in_review: TaskKPI;
  active_by_status: StatusTaskKPI[];
}

export interface CostBreakdownRow {
  key: string;
  label?: string;
  cost_usd: number;
  calls: number;
  total_tokens: number;
}

export interface CostSummary {
  summary_usd: number;
  calls: number;
  rows: CostBreakdownRow[];
  other?: CostBreakdownRow;
}

export interface LiveEntry {
  key: string;
  run_id: string;
  trace_id: string;
  agent_id: string;
  team_id: string;
  run_kind: string;
  class: "traced" | "event_only";
  missing_count: number;
  unverified: boolean;
  current_tool: string;
  last_event_at: string;
}

export interface LiveKPI {
  active_runs_traced: number;
  active_runs_untraced: number;
  active_runs_total: number;
  quality: "exact" | "approx";
  traced_quality: "exact" | "approx";
}

export interface OrgChartNode {
  id: string;
  type: "team" | "lead" | "member" | "agent";
  label: string;
  team_id?: string;
}

export interface OrgChartEdge {
  from: string;
  to: string;
  kind?: "hierarchy" | "delegation";
}

export interface OrgChartDAG {
  nodes: OrgChartNode[];
  edges: OrgChartEdge[];
}

export interface OverviewKPI {
  live: LiveKPI;
  task_kpi: TaskKPIResult;
  cost: CostSummary;
}

export interface ApiError extends Error {
  status?: number;
}
