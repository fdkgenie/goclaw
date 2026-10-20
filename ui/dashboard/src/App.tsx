import { useState, useEffect } from "react";
import { apiGet } from "./api/client";
import { useSSE } from "./api/sse";
import { OverviewPage } from "./pages/OverviewPage";

export default function App() {
  const [token, setToken] = useState<string | null>(() => {
    if (typeof document === "undefined") return null;
    const m = document.cookie.match(/(?:^|; )goclaw_dashboard_session=([^;]+)/);
    return m ? m[1] : null;
  });

  useEffect(() => {
    if (token) {
      fetch("/api/overview", { headers: { Cookie: `goclaw_dashboard_session=${token}` } }).then(
        (r) => {
          if (r.status === 401) setToken(null);
        },
      );
    }
  }, [token]);

  if (!token) {
    return <Login onLogin={setToken} />;
  }

  return <AppShell token={token} />;
}

function Login({ onLogin }: { onLogin: (t: string) => void }) {
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    setErr("");
    const res = await fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": "" },
      body: JSON.stringify({ password: pw }),
    });
    if (res.ok) {
      const m = res.headers.get("set-cookie")?.match(/goclaw_dashboard_session=([^;]+)/);
      if (m) {
        document.cookie = `${m[0]}; path=/`;
        onLogin(m[1]);
      }
    } else {
      setErr("Invalid password");
    }
  };
  return (
    <div className="flex min-h-dvh items-center justify-center bg-slate-50">
      <div className="card w-full max-w-md">
        <h2 className="text-center text-lg font-semibold mb-6">GoClaw Dashboard</h2>
        <label className="block text-sm font-medium mb-2">Password</label>
        <input
          type="password"
          className="w-full border border-slate-300 rounded-md px-3 py-2"
          value={pw}
          onChange={(e) => setPw(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit(e)}
        />
        <button
          className="btn-primary w-full mt-4"
          onClick={(e) => { e.preventDefault(); submit(e); }}
        >
          Sign in
        </button>
        {err && <p className="text-sm text-red-600 mt-2">{err}</p>}
      </div>
    </div>
  );
}

function AppShell({ token }: { token: string }) {
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

  return (
    <div className="min-h-dvh bg-slate-50">
      <nav className="border-b border-slate-200 bg-white">
        <div className="flex items-center justify-between px-4 py-3 safe-area">
          <div className="font-semibold">GoClaw Dashboard</div>
          <div className="flex gap-2">
            <button
              className={`btn ${page === "overview" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("overview")}
            >
              Overview
            </button>
            <button
              className={`btn ${page === "orgchart" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("orgchart")}
            >
              Org Chart
            </button>
            <button
              className={`btn ${page === "cost" ? "btn-primary" : "btn-outline"}`}
              onClick={() => setPage("cost")}
            >
              Cost
            </button>
            <button
              className="btn-outline"
              onClick={() => {
                document.cookie =
                  "goclaw_dashboard_session=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
                window.location.reload();
              }}
            >
              Logout
            </button>
          </div>
        </div>
      </nav>
      <div className="safe-area p-4">
        {stale && <div className="stale-badge mb-4">Connection stale — data may be outdated</div>}
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
