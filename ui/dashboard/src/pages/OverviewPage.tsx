import { useEffect, useMemo, useState } from "react";
import { apiGet } from "../api/client";
import { t } from "../i18n";
import type { LiveKPI, OverviewKPI } from "../api/types";

export interface OverviewPageProps {
  snapshot: LiveKPI | null;
}

const fmt = (n: number, exact: boolean): string =>
  exact ? String(n) : `≥${n}`;

const qualityBadge = (quality: "exact" | "approx"): React.ReactNode => (
  <span
    className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-semibold ${
      quality === "exact" ? "bg-green-100 text-green-700" : "bg-amber-100 text-amber-800"
    }`}
  >
    {quality === "exact" ? t("quality.exact") : t("quality.approx")}
  </span>
);

export const OverviewPage = ({ snapshot }: OverviewPageProps): React.ReactNode => {
  const [overview, setOverview] = useState<OverviewKPI | null>(null);

  // The task and cost tiles come from the composite /api/overview endpoint:
  // the live snapshot only carries the run counts.
  useEffect(() => {
    let cancelled = false;
    apiGet<OverviewKPI>("/api/overview")
      .then((data) => {
        if (!cancelled) setOverview(data);
      })
      .catch(() => {
        // Keep the live tiles: the overview aggregates are best effort.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const traced = useMemo(() => snapshot?.active_runs_traced ?? 0, [snapshot]);
  const untraced = useMemo(() => snapshot?.active_runs_untraced ?? 0, [snapshot]);
  const total = useMemo(() => snapshot?.active_runs_total ?? 0, [snapshot]);
  const quality = useMemo(() => snapshot?.quality ?? "approx", [snapshot]);

  const taskKPI = overview?.task_kpi;
  const activeTasks = taskKPI?.total_active;
  const inReview = taskKPI?.total_in_review;
  const costUSD = overview?.cost?.summary_usd;

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
      <div className="card">
        <div className="text-slate-500 text-xs uppercase tracking-wider mb-1">{t("overview.live_runs")}</div>
        <div className="text-2xl font-bold">{total}</div>
        <div className="flex gap-2 mt-2">
          <span className="text-sm">{t("overview.traced")}: {traced}</span>
          <span className="text-sm">{t("overview.untraced")}: {untraced}</span>
        </div>
        <div className="mt-3">{qualityBadge(quality)}</div>
      </div>

      <div className="card">
        <div className="text-slate-500 text-xs uppercase tracking-wider mb-1">{t("overview.active_runs")}</div>
        <div className="text-2xl font-bold">{fmt(total, quality === "exact")}</div>
      </div>

      <div className="card">
        <div className="text-slate-500 text-xs uppercase tracking-wider mb-1">{t("overview.tasks")}</div>
        <div className="text-2xl font-bold" data-testid="tasks-active">
          {activeTasks ? fmt(activeTasks.value, activeTasks.exact) : "—"}
        </div>
        <div className="text-sm text-slate-500 mt-1" data-testid="tasks-in-review">
          {t("overview.in_review")}: {inReview ? fmt(inReview.value, inReview.exact) : "—"}
        </div>
      </div>

      <div className="card">
        <div className="text-slate-500 text-xs uppercase tracking-wider mb-1">{t("overview.cost")}</div>
        <div className="text-2xl font-bold" data-testid="cost-total">
          {costUSD === undefined ? "—" : `$${costUSD.toFixed(2)}`}
        </div>
      </div>
    </div>
  );
};
