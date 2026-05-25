"use client";

import { ScrollText } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

import { patchLog, type ConfigSectionProps } from "../config-editor-state";
import { BooleanField, SelectField, type SelectOption } from "../fields";

const LEVEL_OPTIONS: SelectOption[] = [
  { value: "debug", label: "debug" },
  { value: "info", label: "info" },
  { value: "warning", label: "warning" },
  { value: "error", label: "error" },
];

export function LogSection({
  config,
  onChange,
  disabled = false,
}: ConfigSectionProps) {
  const log = config.basicSettings.log;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <ScrollText className="h-4 w-4" strokeWidth={1.75} />
          Логирование
        </CardTitle>
        <CardDescription>Логи узлов PassWall на роутере.</CardDescription>
      </CardHeader>
      <CardContent className="divide-y divide-border/40 py-0">
        <BooleanField
          label="Логи узлов"
          checked={log.enableNodeLog}
          onCheckedChange={(value) =>
            onChange(patchLog(config, { enableNodeLog: value }))
          }
          disabled={disabled}
        />
        <SelectField
          label="Уровень логирования"
          value={log.level}
          options={LEVEL_OPTIONS}
          onValueChange={(value) =>
            onChange(patchLog(config, { level: value as typeof log.level }))
          }
          disabled={disabled || !log.enableNodeLog}
        />
      </CardContent>
    </Card>
  );
}
