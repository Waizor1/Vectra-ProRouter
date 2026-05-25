"use client";

import { Sliders } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

import { patchMain, type ConfigSectionProps } from "../config-editor-state";
import { BooleanField, NumberField, SelectField } from "../fields";

export function MainSection({
  config,
  onChange,
  disabled = false,
}: ConfigSectionProps) {
  const main = config.basicSettings.main;
  const nodeOptions = config.nodes.map((node) => ({
    value: node.id,
    label: node.label || node.id,
  }));

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Sliders className="h-4 w-4" strokeWidth={1.75} />
          Основное
        </CardTitle>
        <CardDescription>
          Главный переключатель PassWall, активный узел и локальный прокси.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <BooleanField
          label="PassWall включён"
          description="Главный переключатель проксирования на роутере."
          checked={main.mainSwitch}
          onCheckedChange={(value) =>
            onChange(patchMain(config, { mainSwitch: value }))
          }
          disabled={disabled}
        />
        <SelectField
          label="Активный узел"
          description="Узел, через который идёт основной трафик."
          value={main.selectedNodeId ?? ""}
          options={nodeOptions}
          placeholder={
            nodeOptions.length === 0 ? "Нет узлов" : "Узел не выбран"
          }
          onValueChange={(value) =>
            onChange(patchMain(config, { selectedNodeId: value }))
          }
          disabled={disabled || nodeOptions.length === 0}
        />
        <div className="grid gap-x-8 gap-y-0 sm:grid-cols-2">
          <BooleanField
            label="Проксировать localhost роутера"
            checked={main.localhostProxy}
            onCheckedChange={(value) =>
              onChange(patchMain(config, { localhostProxy: value }))
            }
            disabled={disabled}
          />
          <BooleanField
            label="Проксировать клиентов LAN"
            checked={main.clientProxy}
            onCheckedChange={(value) =>
              onChange(patchMain(config, { clientProxy: value }))
            }
            disabled={disabled}
          />
          <BooleanField
            label="SOCKS-прокси узла"
            description="Локальный SOCKS-порт для выбранного узла."
            checked={main.socksMainSwitch}
            onCheckedChange={(value) =>
              onChange(patchMain(config, { socksMainSwitch: value }))
            }
            disabled={disabled}
          />
          <BooleanField
            label="SOCKS только на localhost"
            description="Привязать к 127.0.0.1, без доступа из LAN."
            checked={main.nodeSocksBindLocal}
            onCheckedChange={(value) =>
              onChange(patchMain(config, { nodeSocksBindLocal: value }))
            }
            disabled={disabled || !main.socksMainSwitch}
          />
        </div>
        <NumberField
          label="Порт SOCKS узла"
          value={main.nodeSocksPort}
          min={1}
          max={65535}
          onChange={(value) =>
            onChange(patchMain(config, { nodeSocksPort: value }))
          }
          disabled={disabled || !main.socksMainSwitch}
        />
      </CardContent>
    </Card>
  );
}
