"use client";

import { Wrench } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

import {
  patchMaintenance,
  type ConfigSectionProps,
} from "../config-editor-state";
import { StringListField } from "../fields";

export function MaintenanceSection({
  config,
  onChange,
  disabled,
}: ConfigSectionProps) {
  const maintenance = config.basicSettings.maintenance;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Wrench className="h-4 w-4" strokeWidth={1.75} />
          Обслуживание
        </CardTitle>
        <CardDescription>
          Пути, которые попадают в резервную копию конфигурации.
        </CardDescription>
      </CardHeader>
      <CardContent className="py-0">
        <StringListField
          label="Пути резервной копии"
          description="По одному пути на строку."
          values={maintenance.backupPaths}
          onCommit={(values) =>
            onChange(patchMaintenance(config, { backupPaths: values }))
          }
          placeholder={"/etc/config/passwall2"}
          rows={5}
          disabled={disabled}
        />
      </CardContent>
    </Card>
  );
}
