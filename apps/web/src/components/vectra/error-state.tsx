"use client";

import * as React from "react";
import { AlertTriangle, ClipboardCopy, RefreshCw } from "lucide-react";

import { cn } from "~/lib/utils";
import { Button } from "~/components/ui/button";

export interface ErrorStateProps extends React.HTMLAttributes<HTMLDivElement> {
  title?: string;
  message: string;
  detail?: string;
  onRetry?: () => void;
}

const ErrorState = React.forwardRef<HTMLDivElement, ErrorStateProps>(
  (
    {
      title = "Что-то пошло не так",
      message,
      detail,
      onRetry,
      className,
      ...props
    },
    ref,
  ) => {
    const handleCopy = React.useCallback(() => {
      if (!detail) return;
      void navigator.clipboard?.writeText(detail);
    }, [detail]);

    return (
      <div
        ref={ref}
        role="alert"
        className={cn(
          "flex flex-col gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm text-foreground",
          className,
        )}
        {...props}
      >
        <div className="flex items-start gap-3">
          <AlertTriangle
            className="mt-0.5 h-5 w-5 shrink-0 text-destructive"
            strokeWidth={1.75}
          />
          <div className="flex flex-col gap-1">
            <p className="font-medium text-destructive">{title}</p>
            <p className="text-muted-foreground">{message}</p>
          </div>
        </div>
        {(onRetry ?? detail) && (
          <div className="flex flex-wrap items-center gap-2">
            {onRetry ? (
              <Button size="sm" variant="outline" onClick={onRetry}>
                <RefreshCw className="mr-1.5 h-3.5 w-3.5" />
                Повторить
              </Button>
            ) : null}
            {detail ? (
              <Button size="sm" variant="ghost" onClick={handleCopy}>
                <ClipboardCopy className="mr-1.5 h-3.5 w-3.5" />
                Скопировать детали
              </Button>
            ) : null}
          </div>
        )}
      </div>
    );
  },
);
ErrorState.displayName = "ErrorState";

export { ErrorState };
