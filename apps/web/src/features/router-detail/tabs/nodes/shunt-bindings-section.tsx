"use client";

import { Split } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { EmptyState } from "~/components/vectra/empty-state";
import type { PasswallDesiredConfig } from "@vectra/contracts";

import {
  getExtraBoolean,
  getExtraString,
} from "~/features/config-editor/config-editor-state";
import {
  BooleanField,
  SelectField,
  type SelectOption,
} from "~/features/config-editor/fields";
import { findShuntNode, setShuntNodeExtra } from "./nodes-state";

const NONE_VALUE = "__none__";

const DOMAIN_STRATEGY_OPTIONS: SelectOption[] = [
  { value: "AsIs", label: "AsIs (только домен)" },
  { value: "IPIfNonMatch", label: "IPIfNonMatch" },
  { value: "IPOnDemand", label: "IPOnDemand" },
];

const DOMAIN_MATCHER_OPTIONS: SelectOption[] = [
  { value: "hybrid", label: "hybrid" },
  { value: "linear", label: "linear" },
];

export interface ShuntBindingsSectionProps {
  config: PasswallDesiredConfig;
  onChange: (next: PasswallDesiredConfig) => void;
  disabled?: boolean;
}

export function ShuntBindingsSection({
  config,
  onChange,
  disabled = false,
}: ShuntBindingsSectionProps) {
  const shunt = findShuntNode(config);

  if (!shunt) {
    return (
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Split className="h-4 w-4" strokeWidth={1.75} />
            Shunt-маршрутизация
          </CardTitle>
          <CardDescription>
            Привязка правил к узлам, FakeDNS и pre-proxy на shunt-узле.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <EmptyState
            icon={Split}
            title="Shunt-узла нет"
            description="Добавьте узел с протоколом «Shunt» и выберите его активным, чтобы разводить трафик по правилам."
          />
        </CardContent>
      </Card>
    );
  }

  const extras = shunt.extras;
  const setExtra = (key: string, value: string | undefined) =>
    onChange(setShuntNodeExtra(config, key, value));

  // Real proxy nodes (exclude the shunt node itself).
  const proxyNodes = config.nodes.filter(
    (node) => node.id !== shunt.id && node.protocol !== "shunt",
  );
  const nodeOptions: SelectOption[] = proxyNodes.map((node) => ({
    value: node.id,
    label: node.label || node.id,
  }));
  const defaultRouteOptions: SelectOption[] = [
    { value: "_direct", label: "Прямое соединение" },
    { value: "_blackhole", label: "Блокировать (blackhole)" },
    ...nodeOptions,
  ];
  const preproxyOptions: SelectOption[] = [
    { value: NONE_VALUE, label: "— нет —" },
    ...nodeOptions,
  ];
  const nodeLabelById = new Map(
    config.nodes.map((node) => [node.id, node.label || node.id]),
  );

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Split className="h-4 w-4" strokeWidth={1.75} />
          Shunt-маршрутизация
        </CardTitle>
        <CardDescription>
          Узел «{shunt.label || shunt.id}» — общие настройки и привязки правил.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <SelectField
            label="Маршрут по умолчанию"
            description="Куда идёт трафик, не попавший ни в одно правило."
            value={getExtraString(extras, "default_node", "_direct")}
            options={defaultRouteOptions}
            onValueChange={(value) => setExtra("default_node", value)}
            disabled={disabled}
          />
          <SelectField
            label="Pre-proxy по умолчанию"
            value={getExtraString(extras, "default_proxy_tag") ?? NONE_VALUE}
            options={preproxyOptions}
            onValueChange={(value) =>
              setExtra(
                "default_proxy_tag",
                value === NONE_VALUE ? undefined : value,
              )
            }
            disabled={disabled}
          />
        </div>

        <div className="grid gap-x-8 rounded-md border border-border/40 px-3 sm:grid-cols-2">
          <BooleanField
            label="FakeDNS (общий)"
            checked={getExtraBoolean(extras, "fakedns")}
            onCheckedChange={(c) => setExtra("fakedns", c ? "1" : undefined)}
            disabled={disabled}
          />
          <BooleanField
            label="FakeDNS для маршрута по умолчанию"
            checked={getExtraBoolean(extras, "default_fakedns")}
            onCheckedChange={(c) =>
              setExtra("default_fakedns", c ? "1" : undefined)
            }
            disabled={disabled}
          />
          <BooleanField
            label="Прямые IP → IPSet"
            description="write_ipset_direct: прямые домены идут мимо ядра."
            checked={getExtraBoolean(extras, "write_ipset_direct", true)}
            onCheckedChange={(c) => setExtra("write_ipset_direct", c ? "1" : "0")}
            disabled={disabled}
          />
          <BooleanField
            label="Парсинг GeoIP (geoview)"
            checked={getExtraBoolean(extras, "enable_geoview_ip", true)}
            onCheckedChange={(c) => setExtra("enable_geoview_ip", c ? "1" : "0")}
            disabled={disabled}
          />
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <SelectField
            label="Domain Strategy (Xray)"
            value={getExtraString(extras, "domainStrategy", "IPOnDemand")}
            options={DOMAIN_STRATEGY_OPTIONS}
            onValueChange={(value) => setExtra("domainStrategy", value)}
            disabled={disabled}
          />
          <SelectField
            label="Domain Matcher (Xray)"
            value={getExtraString(extras, "domainMatcher", "hybrid")}
            options={DOMAIN_MATCHER_OPTIONS}
            onValueChange={(value) => setExtra("domainMatcher", value)}
            disabled={disabled}
          />
        </div>

        <Separator />

        <div className="space-y-2">
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Привязки правил
          </p>
          {config.basicSettings.shuntRules.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Правил шунтирования пока нет — добавьте их во вкладке
              «Конфигурация».
            </p>
          ) : (
            config.basicSettings.shuntRules.map((rule) => (
              <div
                key={rule.id}
                className="rounded-md border border-border/40 bg-card/40 p-3"
              >
                <div className="flex flex-wrap items-center justify-between gap-2 pb-1">
                  <span className="text-sm font-medium text-foreground">
                    {rule.label || rule.id}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    узел:{" "}
                    {rule.outboundNodeId
                      ? (nodeLabelById.get(rule.outboundNodeId) ??
                        rule.outboundNodeId)
                      : "по умолчанию"}
                  </span>
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <BooleanField
                    label="FakeDNS для правила"
                    checked={getExtraBoolean(extras, `${rule.id}_fakedns`)}
                    onCheckedChange={(c) =>
                      setExtra(`${rule.id}_fakedns`, c ? "1" : undefined)
                    }
                    disabled={disabled}
                  />
                  <SelectField
                    label="Pre-proxy правила"
                    value={
                      getExtraString(extras, `${rule.id}_proxy_tag`) ??
                      NONE_VALUE
                    }
                    options={preproxyOptions}
                    onValueChange={(value) =>
                      setExtra(
                        `${rule.id}_proxy_tag`,
                        value === NONE_VALUE ? undefined : value,
                      )
                    }
                    disabled={disabled}
                  />
                </div>
              </div>
            ))
          )}
        </div>
      </CardContent>
    </Card>
  );
}
