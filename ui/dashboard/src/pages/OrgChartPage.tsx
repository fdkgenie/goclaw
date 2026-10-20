import { useEffect, useMemo, useState } from "react";
import dagre from "dagre";
import { apiGet } from "../api/client";
import { t } from "../i18n";
import type { OrgChartDAG, OrgChartEdge, OrgChartNode } from "../api/types";

const NODE_WIDTH = 180;
const NODE_HEIGHT = 52;

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

export const OrgChartPage = (): React.ReactNode => {
  const [dag, setDag] = useState<OrgChartDAG | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    apiGet<OrgChartDAG>("/api/orgchart")
      .then(setDag)
      .catch((e) => setError((e as unknown as Error).message));
  }, []);

  const layout = useMemo(() => (dag ? layoutDAG(dag) : null), [dag]);

  if (error) return <div className="card p-4 text-red-600">{error}</div>;
  if (!layout) return <div className="card p-4 text-slate-500">{t("orgchart.loading")}</div>;

  const count = (type: OrgChartNode["type"]) =>
    layout.nodes.filter((n) => n.type === type).length;

  const delegationLinks = layout.edges.filter((e) => e.kind === "delegation").length;
  const hierarchyEdges = layout.edges.length - delegationLinks;

  return (
    <div className="card">
      <div className="flex flex-wrap gap-4 mb-4 text-sm">
        <span>
          <span className="font-medium">{t("orgchart.teams")}</span> {count("team")}
        </span>
        <span>
          <span className="font-medium">{t("orgchart.leads")}</span> {count("lead")}
        </span>
        <span>
          <span className="font-medium">{t("orgchart.members")}</span> {count("member")}
        </span>
        <span>
          <span className="font-medium">{t("orgchart.links")}</span> {hierarchyEdges}
        </span>
        <span>
          <span className="font-medium">{t("orgchart.delegation_links")}</span>{" "}
          {delegationLinks}
        </span>
      </div>

      {layout.nodes.length === 0 ? (
        <p className="text-sm text-slate-500">{t("orgchart.empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <svg
            role="img"
            aria-label={t("orgchart.title")}
            viewBox={`0 0 ${layout.width} ${layout.height}`}
            className="w-full min-w-[600px] h-auto"
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
                <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" className="text-slate-400" />
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
                  strokeDasharray={delegation ? "6 4" : undefined}
                  className="text-slate-400"
                  markerEnd="url(#orgchart-arrow)"
                />
              );
            })}
            {layout.nodes.map((node) => {
              const pos = layout.positions.get(node.id);
              if (!pos) return null;
              const fill =
                node.type === "team"
                  ? "fill-blue-50 stroke-blue-400"
                  : node.type === "lead"
                    ? "fill-emerald-50 stroke-emerald-400"
                    : node.type === "agent"
                      ? "fill-amber-50 stroke-amber-400"
                      : "fill-white stroke-slate-300";
              return (
                <g key={node.id} data-node-id={node.id} data-node-type={node.type}>
                  <rect
                    x={pos.x - NODE_WIDTH / 2}
                    y={pos.y - NODE_HEIGHT / 2}
                    width={NODE_WIDTH}
                    height={NODE_HEIGHT}
                    rx={10}
                    className={`${fill} stroke-2`}
                  />
                  <text
                    x={pos.x}
                    y={pos.y - 2}
                    textAnchor="middle"
                    className="fill-slate-900 text-sm font-medium"
                  >
                    {node.label}
                  </text>
                  <text
                    x={pos.x}
                    y={pos.y + 16}
                    textAnchor="middle"
                    className="fill-slate-500 text-xs uppercase"
                  >
                    {node.type}
                  </text>
                </g>
              );
            })}
          </svg>
        </div>
      )}
    </div>
  );
};

export default OrgChartPage;
