import { useEffect, useMemo, useState } from "react";
import dagre from "dagre";
import { Network } from "lucide-react";

import { apiGet } from "../api/client";
import { t } from "../i18n";
import type { OrgChartDAG, OrgChartEdge, OrgChartNode } from "../api/types";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { cn } from "../lib/utils";

const NODE_WIDTH = 180;
const NODE_HEIGHT = 58;

interface Layout {
  nodes: OrgChartNode[];
  edges: OrgChartEdge[];
  positions: Map<string, { x: number; y: number }>;
  width: number;
  height: number;
}

function layoutDAG(dag: OrgChartDAG): Layout {
  const nodes = Array.from(new Map(dag.nodes.map((n) => [n.id, n])).values());
  const ids = new Set(nodes.map((n) => n.id));
  const edges = dag.edges.filter(
    (e) => e.from !== e.to && ids.has(e.from) && ids.has(e.to),
  );

  const graph = new dagre.graphlib.Graph();
  graph.setGraph({ rankdir: "TB", nodesep: 40, ranksep: 70, marginx: 24, marginy: 24 });
  graph.setDefaultEdgeLabel(() => ({}));
  for (const node of nodes) {
    graph.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const edge of edges) {
    graph.setEdge(edge.from, edge.to);
  }
  dagre.layout(graph);

  const positions = new Map<string, { x: number; y: number }>();
  for (const node of nodes) {
    const pos = graph.node(node.id);
    positions.set(node.id, { x: pos.x, y: pos.y });
  }

  return {
    nodes,
    edges,
    positions,
    width: Math.max(graph.graph().width ?? NODE_WIDTH, NODE_WIDTH),
    height: Math.max(graph.graph().height ?? NODE_HEIGHT, NODE_HEIGHT),
  };
}

const NODE_DOT: Record<OrgChartNode["type"], string> = {
  team: "bg-primary",
  lead: "bg-emerald-500",
  member: "bg-sky-500",
  agent: "bg-amber-500",
};

export const OrgChartPage = (): React.ReactNode => {
  const [dag, setDag] = useState<OrgChartDAG | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    apiGet<OrgChartDAG>("/api/orgchart")
      .then(setDag)
      .catch((e) => setError((e as unknown as Error).message));
  }, []);

  const layout = useMemo(() => (dag ? layoutDAG(dag) : null), [dag]);

  if (error) {
    return (
      <div className="rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        {error}
      </div>
    );
  }
  if (!layout) {
    return (
      <div className="rounded-lg border bg-card p-4 text-sm text-muted-foreground">
        {t("orgchart.loading")}
      </div>
    );
  }

  const count = (type: OrgChartNode["type"]) =>
    layout.nodes.filter((n) => n.type === type).length;

  const delegationLinks = layout.edges.filter((e) => e.kind === "delegation").length;
  const hierarchyEdges = layout.edges.length - delegationLinks;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("orgchart.title")}
        description={t("orgchart.description")}
        actions={<EdgeLegend />}
      />

      <div className="rounded-xl border bg-card py-6 shadow-sm">
        <div className="px-6">
          <h2 className="text-base font-semibold leading-none">{t("orgchart.title")}</h2>
          <div className="mt-3 flex flex-wrap gap-x-6 gap-y-2 text-sm">
            <span>
              <span className="font-medium">{t("orgchart.teams")}</span>{" "}
              {count("team")}
            </span>
            <span>
              <span className="font-medium">{t("orgchart.leads")}</span>{" "}
              {count("lead")}
            </span>
            <span>
              <span className="font-medium">{t("orgchart.members")}</span>{" "}
              {count("member")}
            </span>
            <span>
              <span className="font-medium">{t("orgchart.agents")}</span>{" "}
              {count("agent")}
            </span>
            <span>
              <span className="font-medium">{t("orgchart.links")}</span>{" "}
              {hierarchyEdges}
            </span>
            <span>
              <span className="font-medium">{t("orgchart.delegation_links")}</span>{" "}
              {delegationLinks}
            </span>
          </div>
        </div>

        <div className="mt-6">
          {layout.nodes.length === 0 ? (
            <EmptyState
              icon={Network}
              title={t("orgchart.empty")}
              description={t("orgchart.empty_desc")}
            />
          ) : (
            <div className="overflow-x-auto">
              <div
                data-slot="orgchart-graph"
                className="relative min-w-[600px]"
                style={{ width: layout.width, height: layout.height }}
              >
                <svg
                  role="img"
                  aria-label={t("orgchart.title")}
                  width={layout.width}
                  height={layout.height}
                  viewBox={`0 0 ${layout.width} ${layout.height}`}
                  className="pointer-events-none absolute left-0 top-0"
                >
                  <defs>
                    <marker
                      id="orgchart-arrow"
                      viewBox="0 0 10 10"
                      refX="10"
                      refY="5"
                      markerWidth="6"
                      markerHeight="6"
                      orient="auto-start-reverse"
                    >
                      <path
                        d="M 0 0 L 10 5 L 0 10 z"
                        fill="currentColor"
                        className="text-muted-foreground/70"
                      />
                    </marker>
                    <marker
                      id="orgchart-arrow-delegation"
                      viewBox="0 0 10 10"
                      refX="10"
                      refY="5"
                      markerWidth="6"
                      markerHeight="6"
                      orient="auto-start-reverse"
                    >
                      <path
                        d="M 0 0 L 10 5 L 0 10 z"
                        fill="currentColor"
                        className="text-primary"
                      />
                    </marker>
                  </defs>
                  {layout.edges.map((edge, i) => {
                    const from = layout.positions.get(edge.from);
                    const to = layout.positions.get(edge.to);
                    if (!from || !to) return null;
                    const delegation = edge.kind === "delegation";
                    return (
                      <line
                        key={`${edge.from}-${edge.to}-${i}`}
                        data-edge-from={edge.from}
                        data-edge-to={edge.to}
                        data-edge-kind={edge.kind ?? "hierarchy"}
                        x1={from.x}
                        y1={from.y + NODE_HEIGHT / 2}
                        x2={to.x}
                        y2={to.y - NODE_HEIGHT / 2}
                        stroke="currentColor"
                        strokeWidth={delegation ? 1.5 : 2}
                        strokeDasharray={delegation ? "6 4" : undefined}
                        className={delegation ? "text-primary" : "text-muted-foreground/70"}
                        markerEnd={
                          delegation
                            ? "url(#orgchart-arrow-delegation)"
                            : "url(#orgchart-arrow)"
                        }
                      />
                    );
                  })}
                </svg>

                {layout.nodes.map((node) => {
                  const pos = layout.positions.get(node.id);
                  if (!pos) return null;
                  return (
                    <div
                      key={node.id}
                      data-node-id={node.id}
                      data-node-type={node.type}
                      className={cn(
                        "absolute flex flex-col justify-center rounded-lg border bg-card px-3 py-2 shadow-sm",
                        node.type === "team" &&
                          "border-primary/40 ring-1 ring-primary/20",
                        node.type === "lead" &&
                          "border-emerald-500/40 ring-1 ring-emerald-500/15",
                      )}
                      style={{
                        left: pos.x - NODE_WIDTH / 2,
                        top: pos.y - NODE_HEIGHT / 2,
                        width: NODE_WIDTH,
                        height: NODE_HEIGHT,
                      }}
                    >
                      <span
                        className="truncate text-sm font-medium"
                        title={node.label}
                      >
                        {node.label}
                      </span>
                      <span className="mt-0.5 flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                        <span
                          aria-hidden="true"
                          className={cn(
                            "h-1.5 w-1.5 rounded-full",
                            NODE_DOT[node.type] ?? "bg-muted-foreground",
                          )}
                        />
                        {t(`orgchart.type.${node.type}`)}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

function EdgeLegend() {
  return (
    <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
      <span className="flex items-center gap-1.5">
        <svg width="24" height="8" aria-hidden="true">
          <line
            x1="0"
            y1="4"
            x2="24"
            y2="4"
            stroke="currentColor"
            strokeWidth="2"
            className="text-muted-foreground/70"
          />
        </svg>
        {t("orgchart.legend_hierarchy")}
      </span>
      <span className="flex items-center gap-1.5">
        <svg width="24" height="8" aria-hidden="true">
          <line
            x1="0"
            y1="4"
            x2="24"
            y2="4"
            stroke="currentColor"
            strokeWidth="2"
            strokeDasharray="6 4"
            className="text-primary"
          />
        </svg>
        {t("orgchart.legend_delegation")}
      </span>
    </div>
  );
}

export default OrgChartPage;
