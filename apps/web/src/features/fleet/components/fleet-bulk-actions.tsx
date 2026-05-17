"use client";

import { LayoutTemplate, RefreshCw, RotateCcw, X } from "lucide-react";

import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";

export interface FleetBulkActionsProps {
  selectedIds: string[];
  onClear: () => void;
  className?: string;
}

export function FleetBulkActions({
  selectedIds,
  onClear,
  className,
}: FleetBulkActionsProps) {
  if (selectedIds.length === 0) {
    return null;
  }

  const handlePlaceholder = (label: string) => () => {
    // TODO: wire to actual bulk operation endpoints in a follow-up phase.
    // Keeping a console hint instead of an alert so the UI stays unobtrusive
    // while we ship the visual scaffold.
    if (typeof window !== "undefined") {
      console.info(
        `[fleet-v2] ${label} pending wiring for ${selectedIds.length} router(s)`,
        selectedIds,
      );
    }
  };

  return (
    <div
      role="region"
      aria-label="Массовые действия"
      className={cn(
        "flex flex-col gap-2 rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-sm sm:flex-row sm:items-center sm:justify-between",
        className,
      )}
    >
      <div className="flex items-center gap-2">
        <span className="font-medium text-foreground">
          Выбрано: {selectedIds.length}
        </span>
        <Button
          size="sm"
          variant="ghost"
          className="h-7 px-2 text-xs text-muted-foreground"
          onClick={onClear}
        >
          <X className="mr-1 h-3 w-3" />
          Снять выделение
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          className="h-8"
          onClick={handlePlaceholder("Применить шаблон")}
        >
          <LayoutTemplate className="mr-1.5 h-3.5 w-3.5" />
          Применить шаблон…
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="h-8"
          onClick={handlePlaceholder("Force re-import")}
        >
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" />
          Force re-import
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="h-8"
          onClick={handlePlaceholder("Reboot выбранные")}
        >
          <RotateCcw className="mr-1.5 h-3.5 w-3.5" />
          Reboot выбранные
        </Button>
      </div>
    </div>
  );
}
