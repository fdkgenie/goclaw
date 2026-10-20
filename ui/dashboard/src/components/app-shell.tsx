import { useEffect, useState, type ComponentType } from "react";
import {
  Activity,
  DollarSign,
  LayoutDashboard,
  LogOut,
  Menu,
  Network,
  X,
} from "lucide-react";

import { apiGet, apiPost } from "../api/client";
import { useSSE } from "../api/sse";
import type { LiveKPI } from "../api/types";
import { cn } from "../lib/utils";
import { t } from "../i18n";
import { ActivityPage } from "../pages/ActivityPage";
import { CostPage } from "../pages/CostPage";
import { OrgChartPage } from "../pages/OrgChartPage";
import { OverviewPage } from "../pages/OverviewPage";
import { StatusBadge } from "./status-badge";
import { ThemeToggle } from "./theme-toggle";
import { Button } from "./ui/button";

export type DashboardPage = "overview" | "orgchart" | "cost" | "activity";

interface NavEntry {
  id: DashboardPage;
  icon: ComponentType<{ className?: string }>;
  labelKey: string;
}

const NAV_ENTRIES: NavEntry[] = [
  { id: "overview", icon: LayoutDashboard, labelKey: "nav.overview" },
  { id: "orgchart", icon: Network, labelKey: "nav.orgchart" },
  { id: "cost", icon: DollarSign, labelKey: "nav.cost" },
  { id: "activity", icon: Activity, labelKey: "nav.activity" },
];

export interface AppShellProps {
  onLogout: () => void;
}

/**
 * Application shell for the dashboard.
 *
 * Mirrors the ui/web app shell: a fixed sidebar on desktop
 * (ui/web/src/components/layout/sidebar.tsx:72-75), a slide-out drawer on
 * mobile (ui/web/src/components/layout/app-layout.tsx:44-61) and a 56px
 * top bar (ui/web/src/components/layout/topbar.tsx:46). The nav items reuse
 * the ui/web sidebar item recipe
 * (ui/web/src/components/layout/sidebar-item.tsx:25-34).
 */
export function AppShell({ onLogout }: AppShellProps) {
  const [page, setPage] = useState<DashboardPage>("overview");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const { snapshot, version, stale } = useSSE<LiveKPI>();

  useEffect(() => {
    const iv = setInterval(() => {
      apiGet("/api/activity").catch(() => {});
    }, 10000);
    return () => clearInterval(iv);
  }, []);

  const logout = async () => {
    try {
      await apiPost("/api/logout", {});
    } finally {
      onLogout();
    }
  };

  const navigate = (next: DashboardPage) => {
    setPage(next);
    setDrawerOpen(false);
  };

  return (
    <div className="flex h-dvh overflow-hidden safe-top">
      {/* Desktop sidebar */}
      <aside className="hidden w-64 shrink-0 flex-col overflow-hidden overscroll-none border-r bg-sidebar text-sidebar-foreground md:flex">
        <div className="flex h-14 items-center gap-2.5 border-b border-sidebar-border px-4">
          <img src="/goclaw-icon.svg" alt="" className="h-8 w-8" />
          <span className="truncate text-lg font-bold tracking-tight text-sidebar-primary">
            {t("dashboard.title")}
          </span>
        </div>
        <nav
          aria-label={t("nav.label")}
          className="flex-1 space-y-0.5 overflow-y-auto p-3"
        >
          {NAV_ENTRIES.map((entry) => (
            <NavButton
              key={entry.id}
              entry={entry}
              active={page === entry.id}
              onSelect={() => navigate(entry.id)}
            />
          ))}
        </nav>
        <div className="border-t border-sidebar-border px-4 py-3 text-xs text-muted-foreground">
          {t("shell.footer")}
        </div>
      </aside>

      {/* Mobile drawer */}
      {drawerOpen && (
        <>
          <div
            data-slot="drawer-backdrop"
            className="fixed inset-0 z-40 bg-black/50 transition-opacity md:hidden"
            onClick={() => setDrawerOpen(false)}
          />
          <div
            data-slot="drawer"
            className="fixed inset-y-0 left-0 z-50 flex w-64 flex-col overflow-hidden border-r bg-sidebar text-sidebar-foreground safe-top safe-bottom safe-left transition-transform duration-200 ease-in-out md:hidden"
          >
            <div className="flex h-14 items-center gap-2.5 border-b border-sidebar-border px-4">
              <img src="/goclaw-icon.svg" alt="" className="h-8 w-8" />
              <span className="truncate text-lg font-bold tracking-tight text-sidebar-primary">
                {t("dashboard.title")}
              </span>
              <button
                type="button"
                onClick={() => setDrawerOpen(false)}
                aria-label={t("btn.close_menu")}
                className="ml-auto cursor-pointer rounded-md p-2 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <nav
              aria-label={t("nav.label")}
              className="flex-1 space-y-0.5 overflow-y-auto p-3"
            >
              {NAV_ENTRIES.map((entry) => (
                <NavButton
                  key={entry.id}
                  entry={entry}
                  active={page === entry.id}
                  onSelect={() => navigate(entry.id)}
                />
              ))}
            </nav>
          </div>
        </>
      )}

      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <header className="flex h-14 shrink-0 items-center justify-between border-b bg-background px-4 landscape-compact">
          <div className="flex items-center gap-2 md:hidden">
            <button
              type="button"
              onClick={() => setDrawerOpen(true)}
              aria-label={t("btn.open_menu")}
              aria-expanded={drawerOpen}
              className="cursor-pointer rounded-md p-2 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            >
              <Menu className="h-4 w-4" />
            </button>
            <span className="truncate text-sm font-semibold">
              {t("dashboard.title")}
            </span>
          </div>
          <div className="hidden md:block" />

          <div className="flex items-center gap-2">
            <StatusBadge
              status={stale ? "warning" : "success"}
              label={stale ? t("stale.title") : t("shell.live")}
            />
            <ThemeToggle />
            <Button
              variant="ghost"
              size="sm"
              onClick={logout}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              <LogOut className="h-4 w-4" />
              <span className="hidden sm:inline">{t("btn.logout")}</span>
            </Button>
          </div>
        </header>

        <main className="min-w-0 flex-1 overflow-y-auto overscroll-contain">
          <div className="space-y-6 p-4 pb-10 sm:p-6">
            {stale && (
              <div
                role="status"
                className="flex items-center gap-2 rounded-lg border border-yellow-500/30 bg-yellow-500/10 px-4 py-2 text-sm text-yellow-700 dark:text-yellow-400"
              >
                <span className="font-medium">{t("stale.title")}</span>
                <span className="text-muted-foreground">{t("stale.subtitle")}</span>
              </div>
            )}
            {page === "overview" && (
              <OverviewPage snapshot={snapshot} version={version} stale={stale} />
            )}
            {page === "orgchart" && <OrgChartPage />}
            {page === "cost" && <CostPage />}
            {page === "activity" && <ActivityPage />}
          </div>
        </main>
      </div>
    </div>
  );
}

function NavButton({
  entry,
  active,
  onSelect,
}: {
  entry: NavEntry;
  active: boolean;
  onSelect: () => void;
}) {
  const Icon = entry.icon;
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? "page" : undefined}
      data-active={active ? "true" : undefined}
      className={cn(
        "flex w-full cursor-pointer items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors",
        "hover:bg-accent hover:text-accent-foreground",
        active && "bg-accent text-accent-foreground font-medium",
      )}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="truncate">{t(entry.labelKey)}</span>
    </button>
  );
}
