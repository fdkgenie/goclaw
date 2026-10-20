import * as React from "react";

import { cn } from "../../lib/utils";

/**
 * Port of ui/web/src/components/ui/separator.tsx.
 *
 * Upstream wraps Radix's Separator primitive; a plain <div> with the same
 * role is equivalent for a purely visual rule.
 */
function Separator({
  className,
  orientation = "horizontal",
  ...props
}: React.ComponentProps<"div"> & { orientation?: "horizontal" | "vertical" }) {
  return (
    <div
      data-slot="separator"
      role="separator"
      aria-orientation={orientation}
      className={cn(
        "bg-border shrink-0 data-[orientation=horizontal]:h-px data-[orientation=horizontal]:w-full data-[orientation=vertical]:h-full data-[orientation=vertical]:w-px",
        orientation === "horizontal" ? "h-px w-full" : "h-full w-px",
        className,
      )}
      {...props}
    />
  );
}

export { Separator };
