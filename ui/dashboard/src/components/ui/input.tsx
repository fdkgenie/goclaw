import * as React from "react";

import { cn } from "../../lib/utils";

/**
 * Port of ui/web/src/components/ui/input.tsx. The upstream variant is built
 * with class-variance-authority; the dashboard has no cva dependency, so the
 * default variant is inlined verbatim.
 */
const inputBase =
  "file:text-foreground placeholder:text-muted-foreground selection:bg-primary selection:text-primary-foreground dark:bg-input/30 border-input w-full min-w-0 rounded-md border bg-transparent px-3 py-1 text-base shadow-xs transition-[color,box-shadow] outline-none file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-1 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive";

function Input({
  className,
  type,
  size = "default",
  ...props
}: Omit<React.ComponentProps<"input">, "size"> & {
  size?: "default" | "sm" | "lg";
}) {
  const sizeClass =
    size === "sm" ? "h-8 text-sm" : size === "lg" ? "h-10" : "h-9";
  return (
    <input
      type={type}
      data-slot="input"
      data-size={size}
      className={cn(inputBase, sizeClass, className)}
      {...props}
    />
  );
}

export { Input };
