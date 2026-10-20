import { useState, useEffect } from "react";
import { apiGet } from "./api/client";
import { LoginPage } from "./components/LoginPage";
import { AppShell } from "./components/app-shell";
import { ThemeProvider } from "./theme/theme-context";

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
    return (
      <div className="flex min-h-dvh items-center justify-center bg-background text-muted-foreground">
        Loading…
      </div>
    );
  }

  if (auth === "anonymous") {
    return <LoginPage onLogin={() => setAuth("authenticated")} />;
  }

  return <AppShell onLogout={() => setAuth("anonymous")} />;
}
