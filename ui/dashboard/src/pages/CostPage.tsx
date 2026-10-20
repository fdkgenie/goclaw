import { useEffect, useState } from "react";
import { apiGet } from "../api/client";
import { t } from "../i18n";
import type { CostSummary } from "../api/types";

const OTHER_EPSILON = 1e-6;

export const CostPage = (): React.ReactNode => {
  const [cost, setCost] = useState<CostSummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    apiGet<CostSummary>("/api/cost")
      .then(setCost)
      .catch((e) => setError((e as unknown as Error).message));
  }, []);

  if (error) return <div className="card p-4 text-red-600">{error}</div>;
  if (!cost) return <div className="card p-4 text-slate-500">{t("cost.loading")}</div>;

  const showOther = cost.other !== undefined && cost.other.cost_usd > OTHER_EPSILON;

  return (
    <div className="card">
      <div className="flex justify-between items-end mb-4">
        <div>
          <div className="text-slate-500 text-xs uppercase tracking-wider">{t("overview.cost")}</div>
          <div className="text-2xl font-bold">${cost.summary_usd.toFixed(2)}</div>
        </div>
        <div className="text-sm text-slate-500">{t("cost.top_n")}</div>
      </div>
      <div className="overflow-x-auto">
        <table className="min-w-[600px] w-full text-left text-sm">
          <thead>
            <tr>
              <th className="pb-2 text-slate-500 font-medium">{t("cost.group")}</th>
              <th className="pb-2 text-slate-500 font-medium text-right">{t("cost.cost_usd")}</th>
            </tr>
          </thead>
          <tbody>
            {cost.rows.map((r) => (
              <tr key={r.key || "personal"} className="border-t border-slate-100">
                <td className="py-2">{r.label || r.key}</td>
                <td className="py-2 text-right">${r.cost_usd.toFixed(2)}</td>
              </tr>
            ))}
            {showOther && cost.other && (
              <tr className="border-t border-slate-200 font-medium" data-testid="other-row">
                <td className="py-2">{cost.other.label || t("cost.other")}</td>
                <td className="py-2 text-right">${cost.other.cost_usd.toFixed(2)}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
};

export default CostPage;
