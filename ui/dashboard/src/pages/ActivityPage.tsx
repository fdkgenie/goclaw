import { useCallback, useEffect, useState } from "react";
import { ClipboardList, RefreshCw } from "lucide-react";

import { apiGet } from "../api/client";
import { t } from "../i18n";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import {
  Card,
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

export interface ActivityItem {
  id: string;
  type: "agent_event" | "task_event" | "audit_log" | "gap";
  timestamp: string;
  agent_id?: string;
  team_id?: string;
  summary: string;
  details?: unknown;
}

interface ActivityResponse {
  items: ActivityItem[];
}

const DEFAULT_LIMIT = 100;

const EVENT_BADGE_VARIANT: Record<ActivityItem["type"], "info" | "success" | "secondary" | "warning"> = {
  agent_event: "info",
  task_event: "success",
  audit_log: "secondary",
  gap: "warning",
};

const formatTime = (ts: string): string => {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString();
};

export const ActivityPage = (): React.ReactNode => {
  const [items, setItems] = useState<ActivityItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [limitDraft, setLimitDraft] = useState(String(DEFAULT_LIMIT));
  const [limit, setLimit] = useState(DEFAULT_LIMIT);

  const load = useCallback(
    (nextLimit: number) => {
      setLoaded(false);
      return apiGet<ActivityResponse>(`/api/activity?limit=${nextLimit}`)
        .then((resp) => {
          setItems(resp.items ?? []);
          setError(null);
        })
        .catch((e) => setError((e as unknown as Error).message))
        .finally(() => setLoaded(true));
    },
    [],
  );

  useEffect(() => {
    let cancelled = false;
    apiGet<ActivityResponse>(`/api/activity?limit=${limit}`)
      .then((resp) => {
        if (cancelled) return;
        setItems(resp.items ?? []);
        setError(null);
      })
      .catch((e) => {
        if (!cancelled) setError((e as unknown as Error).message);
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [limit]);

  const applyLimit = () => {
    const parsed = Number.parseInt(limitDraft, 10);
    if (!Number.isFinite(parsed) || parsed <= 0) return;
    setLimit(Math.min(500, parsed));
  };

  if (error) {
    return (
      <div className="rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
        {error}
      </div>
    );
  }

  if (!loaded) {
    return (
      <div className="rounded-lg border bg-card p-4 text-sm text-muted-foreground">
        {t("activity.loading")}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("activity.title")}
        description={t("activity.description")}
        actions={
          <>
            <div className="flex items-end gap-2">
              <div className="space-y-1">
                <Label
                  htmlFor="activity-limit"
                  className="text-xs text-muted-foreground"
                >
                  {t("activity.limit")}
                </Label>
                <Input
                  id="activity-limit"
                  type="number"
                  min={1}
                  max={500}
                  value={limitDraft}
                  onChange={(e) => setLimitDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") applyLimit();
                  }}
                  className="h-8 w-20 text-base md:text-sm"
                />
              </div>
              <Button variant="outline" size="sm" onClick={applyLimit}>
                {t("btn.apply")}
              </Button>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void load(limit);
              }}
            >
              <RefreshCw className="h-3.5 w-3.5" />
              {t("btn.refresh")}
            </Button>
          </>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>{t("activity.feed_title")}</CardTitle>
          <CardDescription>
            {t("activity.count", { count: items.length })}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          {items.length === 0 ? (
            <EmptyState
              icon={ClipboardList}
              title={t("activity.empty")}
              description={t("activity.empty_desc")}
            />
          ) : (
            <div className="overflow-x-auto">
              <Table className="min-w-[600px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("activity.col.type")}</TableHead>
                    <TableHead>{t("activity.col.actor")}</TableHead>
                    <TableHead>{t("activity.col.summary")}</TableHead>
                    <TableHead className="text-right">{t("activity.col.time")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((item) =>
                    item.type === "gap" ? (
                      <TableRow key={item.id} data-testid="activity-gap">
                        <TableCell colSpan={4}>
                          <span className="inline-flex items-center gap-2">
                            <Badge variant="warning">{t("activity.gap")}</Badge>
                            <span className="text-xs text-muted-foreground">
                              {formatTime(item.timestamp)}
                            </span>
                          </span>
                        </TableCell>
                      </TableRow>
                    ) : (
                      <TableRow key={item.id} data-testid="activity-item">
                        <TableCell>
                          <Badge variant={EVENT_BADGE_VARIANT[item.type] ?? "secondary"}>
                            {item.type}
                          </Badge>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">
                          {item.agent_id || item.team_id || "—"}
                        </TableCell>
                        <TableCell>
                          <span className="text-sm font-medium">{item.summary}</span>
                          {item.details !== undefined && (
                            <details className="mt-1 text-xs text-muted-foreground">
                              <summary className="cursor-pointer">
                                {t("activity.details")}
                              </summary>
                              <pre className="mt-2 overflow-x-auto rounded bg-muted p-2">
                                {JSON.stringify(item.details, null, 2)}
                              </pre>
                            </details>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-right text-xs text-muted-foreground">
                          {formatTime(item.timestamp)}
                        </TableCell>
                      </TableRow>
                    ),
                  )}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
};

export default ActivityPage;
