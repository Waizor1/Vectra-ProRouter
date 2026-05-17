import * as React from "react";

import { cn } from "~/lib/utils";
import { toneClasses, type Tone } from "~/lib/tone";

export interface ToneBadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  tone: Tone;
  dot?: boolean;
}

const ToneBadge = React.forwardRef<HTMLSpanElement, ToneBadgeProps>(
  ({ tone, dot = false, className, children, ...props }, ref) => {
    const t = toneClasses[tone];
    return (
      <span
        ref={ref}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium",
          t.background,
          t.border,
          t.text,
          className,
        )}
        {...props}
      >
        {dot ? (
          <span className={cn("h-1.5 w-1.5 rounded-full", t.dot)} />
        ) : null}
        {children}
      </span>
    );
  },
);
ToneBadge.displayName = "ToneBadge";

export { ToneBadge };
