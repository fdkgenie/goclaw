import { useEffect, useMemo, useState } from "react";
import { Activity, Coins, Filter, Wallet } from "lucide-react";

import { apiGet } from "../api/client";
import { t } from "../i18n";
import type { CostSummary } from "../api/types";
import { PageHeader } from "../components/page-header";
import { StatTile } from "../components/stat-tile";
import { Button } from "../components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";

const OTHER_EPSILON = 1e-6;

export const CostPage = (): React.ReactNode => {
  const [cost, setCost] = useState<CostSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [teamDraft, setTeamDraft] = useState("");
  const [teamFilter, setTeamFilter] = useState("");

  useEffect(() => {
    let cancelled = false;
    const path = teamFilter
      ? `/api/cost?team_id=${encodeURIComponent(teamFilter)}`
      : "/api/cost";
    apiGet<CostSummary>(path)
      .then((data) => {
        if (cancelled) return;
        setCost(data);
        setError(null);
      })
      .catch((e) => {
        if (!cancelled) setError((e as unknown as Error).message);
      });
    return () => {
      cancelled = true;
    };
  }, [teamFilter]);

  const showOther = cost?.other !== undefined && cost.other.cost_usd > OTHER_EPSILON;

  const totalTokens = useMemo(() => {
    if (!cost) return 0;
    const rowTokens = cost.rows.reduce((sum, r) => sum + (r.total_tokens ?? 0), 0);
    return rowTokens + (showOther && cost.other ? cost.other.total_tokens ?? 0 : 0);
  }, [cost, showOther]);

  const maxCost = useMemo(() => {
    if (!cost) return 0;
    const rowMax = cost.rows.reduce((max, r) => Math.max(max, r.cost_usd), 0);
    return showOther && cost.other
      ? Math.max(rowMax, cost.other.cost_usd)
      : rowMax;
  }, [cost, showOther]);

  const applyFilter = () => setTeamFilter(teamDraft.trim());
  const clearFilter = () => {
    setTeamDraft("");
    setTeamFilter("");
  };

  if (error) {
    return (
      <div className="rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        {error}
      </div>
    );
  }

  if (!cost) {
    return (
      <div className="rounded-lg border bg-card p-4 text-sm text-muted-foreground">
        {t("cost.loading")}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("cost.title")}
        description={t("cost.description")}
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatTile
          icon={Wallet}
          label={t("cost.total")}
          value={`$${cost.summary_usd.toFixed(2)}`}
          sub={t("cost.window")}
        />
        <StatTile
          icon={Activity}
          label={t("cost.calls")}
          value={cost.calls.toLocaleString()}
          sub={t("cost.window")}
        />
        <StatTile
          icon={Coins}
          label={t("cost.tokens")}
          value={totalTokens.toLocaleString()}
          sub={t("cost.window")}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("cost.top_n")}</CardTitle>
          <CardDescription>{t("cost.top_n_desc")}</CardDescription>
          <CardAction>
            <div className="flex flex-wrap items-end justify-end gap-2">
              <div className="space-y-1">
                <Label
                  htmlFor="cost-team-filter"
                  className="text-xs text-muted-foreground"
                >
                  {t("cost.filter_team")}
                </Label>
                <Input
                  id="cost-team-filter"
                  value={teamDraft}
                  onChange={(e) => setTeamDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") applyFilter();
                  }}
                  placeholder={t("cost.filter_team_placeholder")}
                  className="h-8 w-[200px] text-base md:text-sm"
                />
              </div>
              <Button variant="outline" size="sm" onClick={applyFilter}>
                <Filter className="h-3.5 w-3.5" />
                {t("btn.apply")}
              </Button>
              {teamFilter && (
                <Button variant="ghost" size="sm" onClick={clearFilter}>
                  {t("btn.clear")}
                </Button>
              )}
            </div>
          </CardAction>
        </CardHeader>
        <CardContent className="px-0">
          <div className="overflow-x-auto">
            <Table className="min-w-[600px]">
              <TableHeader>
                <TableRow>
                  <TableHead>{t("cost.group")}</TableHead>
                  <TableHead className="text-right">{t("cost.calls")}</TableHead>
                  <TableHead className="text-right">{t("cost.tokens")}</TableHead>
                  <TableHead className="text-right">{t("cost.cost_usd")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {cost.rows.length === 0 && !showOther && (
                  <TableRow>
                    <TableCell
                      colSpan={4}
                      className="py-10 text-center text-sm text-muted-foreground"
                    >
                      {t("cost.empty")}
                    </TableCell>
                  </TableRow>
                )}
                {cost.rows.map((r) => (
                    <TableRow key={r.key || "personal"}>
                      <TableCell className="font-medium">
                        {r.label || r.key}
                      </TableCell>
                      <TableCell
                        className="text-right text-muted-foreground"
                        data-testid="calls-cell"
                      >
                        {r.calls}
                      </TableCell>
                      <TableCell className="text-right text-muted-foreground">
                        {(r.total_tokens ?? 0).toLocaleString()}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-3">
                          <span>${r.cost_usd.toFixed(2)}</span>
                          <CostBar value={r.cost_usd} max={maxCost} />
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                  {showOther && cost.other && (
                    <TableRow data-testid="other-row" className="font-medium">
                      <TableCell>
                        {cost.other.label || t("cost.other")}
                      </TableCell>
                      <TableCell
                        className="text-right text-muted-foreground"
                        data-testid="calls-cell"
                      >
                        {cost.other.calls}
                      </TableCell>
                      <TableCell className="text-right text-muted-foreground">
                        {(cost.other.total_tokens ?? 0).toLocaleString()}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-3">
                          <span>${cost.other.cost_usd.toFixed(2)}</span>
                          <CostBar value={cost.other.cost_usd} max={maxCost} />
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
        </CardContent>
      </Card>
    </div>
  );
};

/**
 * Lightweight token-based cost bar. No chart library: a single div scaled
 * against the largest row, coloured with the primary token.
 */
function CostBar({ value, max }: { value: number; max: number }) {
  const pct = max > 0 ? Math.max(2, Math.round((value / max) * 100)) : 0;
  return (
    <span
      data-slot="cost-bar"
      aria-hidden="true"
      className="hidden h-1.5 w-24 shrink-0 overflow-hidden rounded-full bg-muted sm:block"
    >
      <span
        className="block h-full rounded-full bg-primary"
        style={{ width: `${pct}%` }}
      />
    </span>
  );
}

export default CostPage;
