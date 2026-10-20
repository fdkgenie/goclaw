import { useEffect, useState } from "react";
import { apiGet } from "../api/client";
import { t } from "../i18n";

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

export const ActivityPage = (): React.ReactNode => {
  const [items, setItems] = useState<ActivityItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    apiGet<ActivityResponse>("/api/activity")
      .then((resp) => setItems(resp.items ?? []))
      .catch((e) => setError((e as unknown as Error).message))
      .finally(() => setLoaded(true));
  }, []);

  if (error) return <div className="card p-4 text-destructive">{error}</div>;
  if (!loaded) return <div className="card p-4 text-muted-foreground">{t("activity.loading")}</div>;

  return (
    <div className="card">
      <h2 className="text-lg font-semibold mb-4">{t("activity.title")}</h2>
      {items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("activity.empty")}</p>
      ) : (
        <ul className="divide-y divide-border">
          {items.map((item) =>
            item.type === "gap" ? (
              <li key={item.id} className="py-3" data-testid="activity-gap">
                <span className="stale-badge">{t("activity.gap")}</span>
                <span className="ml-2 text-xs text-muted-foreground">
                  {new Date(item.timestamp).toLocaleString()}
                </span>
              </li>
            ) : (
              <li key={item.id} className="py-3" data-testid="activity-item">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="inline-flex rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-foreground">
                    {item.type}
                  </span>
                  {item.agent_id && (
                    <span className="text-xs text-muted-foreground">{item.agent_id}</span>
                  )}
                  <span className="text-sm font-medium">{item.summary}</span>
                  <span className="ml-auto text-xs text-muted-foreground">
                    {new Date(item.timestamp).toLocaleString()}
                  </span>
                </div>
                {item.details !== undefined && (
                  <details className="mt-2 text-xs text-muted-foreground">
                    <summary className="cursor-pointer">{t("activity.details")}</summary>
                    <pre className="mt-2 overflow-x-auto rounded bg-muted p-2">
                      {JSON.stringify(item.details, null, 2)}
                    </pre>
                  </details>
                )}
              </li>
            ),
          )}
        </ul>
      )}
    </div>
  );
};

export default ActivityPage;
