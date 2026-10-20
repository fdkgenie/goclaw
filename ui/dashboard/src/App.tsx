import { useState, useEffect } from "react";
import { apiGet, apiPost } from "./api/client";
import { useSSE } from "./api/sse";
import { OverviewPage } from "./pages/OverviewPage";
import { t } from "./i18n";

type AuthState = "checking" | "authenticated" | "anonymous";

export default function App() {
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
    return <div className="flex min-h-dvh items-center justify-center text-slate-500">Loading…</div>;
  }

  if (auth === "anonymous") {
    return <Login onLogin={() => setAuth("authenticated")} />;
  }

  return <AppShell onLogout={() => setAuth("anonymous")} />;
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      const res = await apiPost("/api/login", { password: pw });
      if (res.ok) {
        onLogin();
        return;
      }
    } catch {
      // fall through to the invalid-password message
    }
    setErr(t("error.invalid_password"));
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-slate-50">
      <form className="card w-full max-w-md" onSubmit={submit}>
        <h2 className="text-center text-lg font-semibold mb-6">{t("dashboard.title")}</h2>
        <label className="block text-sm font-medium mb-2" htmlFor="dashboard-password">
          {t("label.password")}
        </label>
        <input
          id="dashboard-password"
          type="password"
          className="w-full border border-slate-300 rounded-md px-3 py-2 text-base md:text-sm"
          value={pw}
          onChange={(e) => setPw(e.target.value)}
        />
        <button type="submit" className="btn-primary w-full mt-4">
          {t("btn.signin")}
        </button>
        {err && <p className="text-sm text-red-600 mt-2">{err}</p>}
      </form>
    </div>
  );
}

function AppShell({ onLogout }: { onLogout: () => void }) {
  const [page, setPage] = useState("overview");
  const { snapshot, version, stale } = useSSE<{ active_runs_total: number }>("/api/overview", {
    active_runs_traced: 0,
    active_runs_untraced: 0,
    active_runs_total: 0,
    quality: "exact",
  });

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
    <div className="min-h-dvh bg-slate-50">
      <nav className="border-b border-slate-200 bg-white">
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
        <div className="text-sm text-slate-500 mb-2">
          Live version: {version ?? "—"}
        </div>
        {page === "overview" && <OverviewPage snapshot={snapshot} />}
        {page === "orgchart" && <div className="card">Org Chart (placeholder)</div>}
        {page === "cost" && <div className="card">Cost (placeholder)</div>}
      </div>
    </div>
  );
}
