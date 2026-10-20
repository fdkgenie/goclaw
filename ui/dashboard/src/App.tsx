import { useState, useEffect } from "react";
import { apiGet, apiPost } from "./api/client";
import { useSSE } from "./api/sse";
import type { LiveKPI } from "./api/types";
import { LoginPage } from "./components/LoginPage";
import { OverviewPage } from "./pages/OverviewPage";
import { OrgChartPage } from "./pages/OrgChartPage";
import { CostPage } from "./pages/CostPage";
import { ActivityPage } from "./pages/ActivityPage";
import { ThemeProvider } from "./theme/theme-context";
import { t } from "./i18n";

type AuthState = "checking" | "authenticated" | "anonymous";

export default function App() {
  return (
    <ThemeProvider>
      <AppRoot />
    </ThemeProvider>
  );
}

function AppRoot() {
  const [auth, setAuth] = useState<AuthState>("checking");

  useEffect(() => {
    let cancelled = false;
    apiGet<{ authenticated: boolean }>("/api/session")
      .then(() => {
        if (!cancelled) setAuth("authenticated");
      })
      .catch(() => {
        if (!cancelled) setAuth("anonymous");
      });
    const onUnauthorized = () => setAuth("anonymous");
    window.addEventListener("dashboard:unauthorized", onUnauthorized);
    return () => {
      cancelled = true;
      window.removeEventListener("dashboard:unauthorized", onUnauthorized);
    };
  }, []);

  if (auth === "checking") {
    return <div className="flex min-h-dvh items-center justify-center text-muted-foreground">Loading…</div>;
  }

  if (auth === "anonymous") {
    return <LoginPage onLogin={() => setAuth("authenticated")} />;
  }

  return <AppShell onLogout={() => setAuth("anonymous")} />;
}

function AppShell({ onLogout }: { onLogout: () => void }) {
  const [page, setPage] = useState("overview");
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

  return (
    <div className="min-h-dvh bg-background">
      <nav className="border-b border-border bg-card">
        <div className="flex items-center justify-between px-4 py-3 safe-area">
          <div className="font-semibold">{t("dashboard.title")}</div>
          <div className="flex gap-2">
            <button
              className={`btn ${page === "overview" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("overview")}
            >
              {t("nav.overview")}
            </button>
            <button
              className={`btn ${page === "orgchart" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("orgchart")}
            >
              {t("nav.orgchart")}
            </button>
            <button
              className={`btn ${page === "cost" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("cost")}
            >
              {t("nav.cost")}
            </button>
            <button
              className={`btn ${page === "activity" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("activity")}
            >
              {t("nav.activity")}
            </button>
            <button
              className="btn-outline"
              onClick={logout}
            >
              {t("btn.logout")}
            </button>
          </div>
        </div>
      </nav>
      <div className="safe-area p-4">
        {stale && <div className="stale-badge mb-4">{t("stale.title")} — {t("stale.subtitle")}</div>}
        <div className="text-sm text-muted-foreground mb-2">
          Live version: {version ?? "—"}
        </div>
        {page === "overview" && <OverviewPage snapshot={snapshot} />}
        {page === "orgchart" && <OrgChartPage />}
        {page === "cost" && <CostPage />}
        {page === "activity" && <ActivityPage />}
      </div>
    </div>
  );
}
