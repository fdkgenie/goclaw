import { useState, type FormEvent } from "react";
import { AlertCircle, Moon, Sun } from "lucide-react";

import { apiPost } from "../api/client";
import { t } from "../i18n";
import { useTheme } from "../theme/theme-context";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

export interface LoginPageProps {
  onLogin: () => void;
}

/**
 * Dashboard login screen.
 *
 * Layout and interaction are ported 1:1 from the GoClaw web UI login:
 *   ui/web/src/pages/login/login-layout.tsx  (card, header, theme toggle)
 *   ui/web/src/pages/login/token-form.tsx     (field, error banner, submit)
 *
 * The dashboard has its own credential flow (POST /api/login with a shared
 * password + HttpOnly session cookie), so the token/pairing tabs and the
 * user-id field have no counterpart here.
 */
export function LoginPage({ onLogin }: LoginPageProps) {
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setErr("");
    setSubmitting(true);
    try {
      const res = await apiPost("/api/login", { password: pw });
      if (res.ok) {
        onLogin();
        return;
      }
      if (res.status === 429) {
        setErr(t("error.too_many_attempts"));
        return;
      }
      if (res.status === 403) {
        setErr(t("error.forbidden_origin"));
        return;
      }
    } catch {
      // fall through to the invalid-password message
    } finally {
      setSubmitting(false);
    }
    setErr(t("error.invalid_password"));
  };

  return (
    <div className="relative flex min-h-dvh items-center justify-center bg-background px-4">
      <ThemeToggle />
      <div className="w-full max-w-sm space-y-6 rounded-lg border bg-card p-6 shadow-sm sm:p-8">
        <div className="text-center">
          <img
            src="/goclaw-icon.svg"
            alt={t("login.title")}
            className="mx-auto mb-3 h-20 w-20"
          />
          <h1 className="text-3xl font-bold tracking-tight">{t("login.title")}</h1>
          <p className="mt-2 text-sm text-muted-foreground">{t("login.subtitle")}</p>
        </div>

        <form className="space-y-4" onSubmit={submit}>
          <div className="space-y-2">
            <Label htmlFor="dashboard-password" className="text-sm font-medium">
              {t("label.password")}
            </Label>
            <Input
              id="dashboard-password"
              type="password"
              name="password"
              autoComplete="current-password"
              placeholder={t("login.passwordPlaceholder")}
              className="text-base md:text-sm"
              value={pw}
              onChange={(e) => setPw(e.target.value)}
              disabled={submitting}
              autoFocus
            />
            <p className="text-xs text-muted-foreground">{t("login.passwordHint")}</p>
          </div>

          {err && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{err}</span>
            </div>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="inline-flex h-9 w-full items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow transition-colors hover:bg-primary/90 disabled:pointer-events-none disabled:opacity-50"
          >
            {submitting ? t("btn.signing_in") : t("btn.signin")}
          </button>
        </form>
      </div>
    </div>
  );
}

function ThemeToggle() {
  const { resolvedTheme, toggleTheme } = useTheme();
  const isDark = resolvedTheme === "dark";

  return (
    <button
      type="button"
      onClick={toggleTheme}
      data-slot="button"
      data-size="icon"
      className="absolute top-[max(1rem,var(--safe-top))] right-[max(1rem,var(--safe-right))] cursor-pointer rounded-md border bg-card p-2 text-muted-foreground shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground"
      title={t("theme.toggle")}
      aria-label={t("theme.toggle")}
    >
      {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </button>
  );
}

export default LoginPage;
