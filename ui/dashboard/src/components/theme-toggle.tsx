import { Moon, Sun } from "lucide-react";

import { t } from "../i18n";
import { useTheme } from "../theme/theme-context";

/**
 * Theme toggle for the app shell. Uses the same icon-button treatment as the
 * ui/web top bar (rounded-md p-2 text-muted-foreground hover:bg-accent).
 */
export function ThemeToggle() {
  const { resolvedTheme, toggleTheme } = useTheme();
  const isDark = resolvedTheme === "dark";

  return (
    <button
      type="button"
      onClick={toggleTheme}
      data-slot="button"
      data-size="icon"
      aria-label={t("theme.toggle")}
      title={t("theme.toggle")}
      className="cursor-pointer rounded-md p-2 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
    >
      {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </button>
  );
}
