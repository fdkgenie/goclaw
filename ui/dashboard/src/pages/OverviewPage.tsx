import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  Coins,
  ListTree,
  RadioTower,
  RefreshCw,
  Users,
} from "lucide-react";

import { apiGet } from "../api/client";
import { t } from "../i18n";
import type { LiveKPI, OverviewKPI, TeamTaskKPI } from "../api/types";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatTile } from "../components/stat-tile";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";

export interface OverviewPageProps {
  snapshot: LiveKPI | null;
  version?: number | null;
  stale?: boolean;
}

const fmt = (n: number, exact: boolean): string => (exact ? String(n) : `≥${n}`);

const qualityBadge = (quality: "exact" | "approx", testId?: string): React.ReactNode => (
  <Badge
    data-testid={testId}
    variant={quality === "exact" ? "success" : "warning"}
  >
    {quality === "exact" ? t("quality.exact") : t("quality.approx")}
  </Badge>
);

export const OverviewPage = ({
  snapshot,
  version,
  stale,
}: OverviewPageProps): React.ReactNode => {
  const [overview, setOverview] = useState<OverviewKPI | null>(null);
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const [loading, setLoading] = useState(false);

  // The task and cost tiles come from the composite /api/overview endpoint:
  // the live snapshot only carries the run counts.
  const load = useCallback(() => {
    setLoading(true);
    return apiGet<OverviewKPI>("/api/overview")
      .then((data) => {
        setOverview(data);
        setUpdatedAt(new Date());
      })
      .catch(() => {
        // Keep the live tiles: the overview aggregates are best effort.
      })
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    let cancelled = false;
    apiGet<OverviewKPI>("/api/overview")
      .then((data) => {
        if (cancelled) return;
        setOverview(data);
        setUpdatedAt(new Date());
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
  const tracedQuality = useMemo(() => snapshot?.traced_quality ?? "approx", [snapshot]);

  const taskKPI = overview?.task_kpi;
  const activeTasks = taskKPI?.total_active;
  const inReview = taskKPI?.total_in_review;
  const statusGroups = taskKPI?.active_by_status ?? [];
  const teams = taskKPI?.teams ?? [];
  const costUSD = overview?.cost?.summary_usd;
  const costCalls = overview?.cost?.calls;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("overview.title")}
        description={t("overview.description")}
        actions={
          <>
            {version !== undefined && version !== null && (
              <span className="text-xs text-muted-foreground">
                {t("overview.live_version", { version })}
              </span>
            )}
            {updatedAt && (
              <span className="text-xs text-muted-foreground">
                {t("overview.updated", {
                  time: updatedAt.toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                    second: "2-digit",
                  }),
                })}
              </span>
            )}
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void load();
              }}
              disabled={loading}
            >
              <RefreshCw className={loading ? "animate-spin" : undefined} />
              {t("btn.refresh")}
            </Button>
          </>
        }
      />

      {stale && (
        <p className="text-xs text-muted-foreground">{t("stale.subtitle")}</p>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile
          icon={RadioTower}
          label={t("overview.live_runs")}
          value={total}
          sub={
            <div className="flex flex-wrap items-center gap-2">
              <span>
                {t("overview.traced")}: {traced}
              </span>
              {qualityBadge(tracedQuality, "traced-quality")}
              <span>
                {t("overview.untraced")}: {untraced}
              </span>
            </div>
          }
        />

        <StatTile
          icon={Activity}
          label={t("overview.active_runs")}
          value={fmt(total, quality === "exact")}
          sub={<div className="flex items-center gap-2">{qualityBadge(quality, "total-quality")}</div>}
        />

        <StatTile
          icon={ListTree}
          label={t("overview.tasks")}
          value={<span data-testid="tasks-active">{activeTasks ? fmt(activeTasks.value, activeTasks.exact) : "—"}</span>}
          sub={
            <>
              <div data-testid="tasks-in-review">
                {t("overview.in_review")}: {inReview ? fmt(inReview.value, inReview.exact) : "—"}
              </div>
              {statusGroups.length > 0 && (
                <ul className="mt-1.5 space-y-0.5" data-testid="tasks-by-status">
                  {statusGroups.map((g) => (
                    <li key={g.status} className="flex items-center gap-1">
                      <Badge variant="outline">{g.status}</Badge>
                      <span>: {fmt(g.count.value, g.count.exact)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </>
          }
        />

        <StatTile
          icon={Coins}
          label={t("overview.cost")}
          value={
            <span data-testid="cost-total">
              {costUSD === undefined ? "—" : `$${costUSD.toFixed(2)}`}
            </span>
          }
          sub={
            costCalls === undefined ? undefined : t("overview.calls", { count: costCalls })
          }
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("overview.tasks_by_team")}</CardTitle>
          <CardDescription>{t("overview.tasks_by_team_desc")}</CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          {teams.length === 0 ? (
            <EmptyState
              icon={Users}
              title={t("overview.no_team_tasks")}
              description={t("overview.no_team_tasks_desc")}
            />
          ) : (
            <div className="overflow-x-auto">
              <Table className="min-w-[600px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("overview.team")}</TableHead>
                    <TableHead className="text-right">{t("overview.active")}</TableHead>
                    <TableHead className="text-right">{t("overview.in_review")}</TableHead>
                    <TableHead>{t("overview.status_breakdown")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {teams.map((team) => (
                    <TeamRow key={team.team_id} team={team} />
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
};

function TeamRow({ team }: { team: TeamTaskKPI }) {
  return (
    <TableRow data-team-id={team.team_id}>
      <TableCell className="font-medium">
        {team.team_name || team.team_id}
      </TableCell>
      <TableCell className="text-right">
        {fmt(team.active.value, team.active.exact)}
      </TableCell>
      <TableCell className="text-right text-muted-foreground">
        {fmt(team.in_review.value, team.in_review.exact)}
      </TableCell>
      <TableCell>
        {team.active_by_status.length === 0 ? (
          <span className="text-xs text-muted-foreground">—</span>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {team.active_by_status.map((g) => (
              <Badge key={g.status} variant="secondary">
                {g.status}: {fmt(g.count.value, g.count.exact)}
              </Badge>
            ))}
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}
