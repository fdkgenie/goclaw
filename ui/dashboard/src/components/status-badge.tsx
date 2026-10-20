import { cn } from "../lib/utils";

type Status = "success" | "warning" | "error" | "info" | "default";

const statusClasses: Record<Status, string> = {
  success: "bg-green-500/15 text-green-600 dark:text-green-400",
  warning: "bg-yellow-500/15 text-yellow-600 dark:text-yellow-400",
  error: "bg-red-500/15 text-red-600 dark:text-red-400",
  info: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  default: "bg-muted text-muted-foreground",
};

const dotClasses: Record<Status, string> = {
  success: "bg-green-500",
  warning: "bg-yellow-500",
  error: "bg-red-500",
  info: "bg-blue-500",
  default: "bg-muted-foreground",
};

export interface StatusBadgeProps {
  status: Status;
  label: string;
  className?: string;
}

/**
 * Port of ui/web/src/components/shared/status-badge.tsx: a tinted pill with a
 * small solid dot, used for connection state and other semantic states.
 */
export function StatusBadge({ status, label, className }: StatusBadgeProps) {
  return (
    <span
      data-slot="status-badge"
      data-status={status}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium",
        statusClasses[status],
        className,
      )}
    >
      <span
        aria-hidden="true"
        className={cn("h-1.5 w-1.5 rounded-full", dotClasses[status])}
      />
      {label}
    </span>
  );
}
