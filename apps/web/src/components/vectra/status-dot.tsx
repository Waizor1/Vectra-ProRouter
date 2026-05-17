import * as React from "react";

import { cn } from "~/lib/utils";
import { toneClasses, type Tone } from "~/lib/tone";

export interface StatusDotProps extends React.HTMLAttributes<HTMLSpanElement> {
  tone: Tone;
  pulse?: boolean;
}

const StatusDot = React.forwardRef<HTMLSpanElement, StatusDotProps>(
  ({ tone, pulse = false, className, ...props }, ref) => {
    const t = toneClasses[tone];
    return (
      <span
        ref={ref}
        aria-hidden
        className={cn(
          "relative inline-flex h-2.5 w-2.5 items-center justify-center rounded-full",
          t.dot,
          className,
        )}
        {...props}
      >
        {pulse ? (
          <span
            className={cn(
              "absolute inset-0 animate-ping rounded-full opacity-60",
              t.dot,
            )}
          />
        ) : null}
      </span>
    );
  },
);
StatusDot.displayName = "StatusDot";

export { StatusDot };
