import * as React from "react";
import type { LucideIcon } from "lucide-react";

import { cn } from "~/lib/utils";
import { Button } from "~/components/ui/button";

export interface EmptyStateAction {
  label: string;
  onClick?: () => void;
  href?: string;
}

export interface EmptyStateProps extends React.HTMLAttributes<HTMLDivElement> {
  icon?: LucideIcon;
  title: string;
  description?: string;
  primaryAction?: EmptyStateAction;
  secondaryAction?: EmptyStateAction;
}

const EmptyState = React.forwardRef<HTMLDivElement, EmptyStateProps>(
  (
    {
      icon: Icon,
      title,
      description,
      primaryAction,
      secondaryAction,
      className,
      ...props
    },
    ref,
  ) => {
    return (
      <div
        ref={ref}
        className={cn(
          "flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-card/30 px-6 py-12 text-center",
          className,
        )}
        {...props}
      >
        {Icon ? (
          <Icon className="h-10 w-10 text-muted-foreground" strokeWidth={1.5} />
        ) : null}
        <div className="space-y-1">
          <p className="text-base font-semibold text-foreground">{title}</p>
          {description ? (
            <p className="max-w-prose text-sm text-muted-foreground">
              {description}
            </p>
          ) : null}
        </div>
        {primaryAction ?? secondaryAction ? (
          <div className="mt-2 flex flex-wrap items-center justify-center gap-2">
            {primaryAction ? (
              primaryAction.href ? (
                <Button asChild>
                  <a href={primaryAction.href}>{primaryAction.label}</a>
                </Button>
              ) : (
                <Button onClick={primaryAction.onClick}>
                  {primaryAction.label}
                </Button>
              )
            ) : null}
            {secondaryAction ? (
              secondaryAction.href ? (
                <Button asChild variant="ghost">
                  <a href={secondaryAction.href}>{secondaryAction.label}</a>
                </Button>
              ) : (
                <Button variant="ghost" onClick={secondaryAction.onClick}>
                  {secondaryAction.label}
                </Button>
              )
            ) : null}
          </div>
        ) : null}
      </div>
    );
  },
);
EmptyState.displayName = "EmptyState";

export { EmptyState };
