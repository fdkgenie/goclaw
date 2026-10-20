import type { LucideIcon } from "lucide-react";

import { cn } from "../lib/utils";

export interface StatTileProps {
  icon?: LucideIcon;
  label: string;
  value: React.ReactNode;
  sub?: React.ReactNode;
  className?: string;
  valueClassName?: string;
}

/**
 * Stat tile following the ui/web stat card recipe
 * (ui/web/src/pages/overview/stat-card.tsx:23-49):
 * rounded-lg border bg-card p-5, an icon chip in bg-muted, a muted label, a
 * 2xl semibold value and an xs muted sub line.
 */
export function StatTile({
  icon: Icon,
  label,
  value,
  sub,
  className,
  valueClassName,
}: StatTileProps) {
  return (
    <div
      data-slot="stat-tile"
      className={cn("rounded-lg border bg-card p-5", className)}
    >
      <div className="flex items-center gap-3">
        {Icon && (
          <div className="rounded-md bg-muted p-2">
            <Icon className="h-4 w-4 text-muted-foreground" />
          </div>
        )}
        <div className="min-w-0 flex-1">
          <p className="text-sm text-muted-foreground">{label}</p>
          <div className="flex items-center gap-2">
            <p
              className={cn("text-2xl font-semibold", valueClassName)}
              title={typeof value === "string" ? value : undefined}
            >
              {value}
            </p>
          </div>
          {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
        </div>
      </div>
    </div>
  );
}
