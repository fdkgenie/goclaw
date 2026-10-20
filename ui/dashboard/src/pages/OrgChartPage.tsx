import { useEffect, useState } from "react";
import { apiGet } from "../api/client";
import type { OrgChartDAG } from "../api/types";

export const OrgChartPage = (): React.ReactNode => {
  const [dag, setDag] = useState<OrgChartDAG | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    apiGet<OrgChartDAG>("/api/orgchart")
      .then(setDag)
      .catch((e) => setError((e as unknown as Error).message));
  }, []);

  if (error) return <div className="card p-4 text-red-600">{error}</div>;
  if (!dag) return <div className="card p-4 text-slate-500">Loading org chart…</div>;

  return (
    <div className="card">
      <div className="flex gap-4 mb-2">
        <span className="font-medium">Teams:</span>
        <span className="text-sm text-slate-500">{dag.nodes.filter((n) => n.type === "team").length}</span>
        <span className="font-medium ml-4">Leads:</span>
        <span className="text-sm text-slate-500">{dag.nodes.filter((n) => n.type === "lead").length}</span>
        <span className="font-medium ml-4">Members:</span>
        <span className="text-sm text-slate-500">{dag.nodes.filter((n) => n.type === "member").length}</span>
        <span className="font-medium ml-4">Links:</span>
        <span className="text-sm text-slate-500">{dag.edges.length}</span>
      </div>
      <div className="h-64 border border-slate-200 rounded-lg bg-slate-50">
        <p className="text-slate-400 text-sm text-center pt-16">DAG canvas placeholder ({dag.nodes.length} nodes, {dag.edges.length} edges)</p>
      </div>
    </div>
  );
};

export default OrgChartPage;
