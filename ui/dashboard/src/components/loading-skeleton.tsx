import { Skeleton } from "./ui/skeleton";

/**
 * Port of ui/web/src/components/shared/loading-skeleton.tsx (the card and
 * table variants only).
 */
export function CardSkeleton() {
  return (
    <div className="rounded-lg border p-4">
      <Skeleton className="mb-2 h-5 w-3/4" />
      <Skeleton className="mb-4 h-4 w-1/2" />
      <div className="flex gap-2">
        <Skeleton className="h-5 w-16" />
        <Skeleton className="h-5 w-16" />
      </div>
    </div>
  );
}

export function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-2">
      <Skeleton className="h-10 w-full" />
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  );
}

export function StatTileSkeleton() {
  return (
    <div className="rounded-lg border bg-card p-5">
      <Skeleton className="mb-3 h-8 w-8 rounded-md" />
      <Skeleton className="mb-2 h-3 w-24" />
      <Skeleton className="h-7 w-16" />
    </div>
  );
}
