"use client";

import { useEffect, useId, useState } from "react";
import {
  ArrowUpToLine,
  ChevronDown,
  ChevronRight,
  Plus,
  Split,
  Trash2,
} from "lucide-react";

import {
  addShuntRule,
  deleteShuntRule,
  moveShuntRuleToTop,
  renameShuntRule,
  updateShuntRuleExtra,
} from "~/components/router-editor-state";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { EmptyState } from "~/components/vectra/empty-state";

import {
  encodeExtraTokens,
  getExtraBoolean,
  getExtraString,
  getExtraTokens,
  setShuntRuleField,
  type ConfigSectionProps,
} from "../config-editor-state";
import {
  BooleanField,
  CheckboxGroupField,
  SelectField,
  StringListField,
  TextField,
  type SelectOption,
} from "../fields";

const UNBOUND_VALUE = "__unbound__";

const NETWORK_OPTIONS: SelectOption[] = [
  { value: "tcp,udp", label: "TCP + UDP" },
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
];

const PROTOCOL_OPTIONS: SelectOption[] = [
  { value: "http", label: "HTTP" },
  { value: "tls", label: "TLS" },
  { value: "bittorrent", label: "BitTorrent" },
];

const INBOUND_OPTIONS: SelectOption[] = [
  { value: "tproxy", label: "Прозрачный (tproxy)" },
  { value: "socks", label: "Socks" },
];

export function ShuntRulesSection({
  config,
  onChange,
  disabled,
}: ConfigSectionProps) {
  const rules = config.basicSettings.shuntRules;
  const nodeOptions = [
    { value: UNBOUND_VALUE, label: "— не задан —" },
    ...config.nodes.map((node) => ({
      value: node.id,
      label: node.label || node.id,
    })),
  ];

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 pb-3">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <Split className="h-4 w-4" strokeWidth={1.75} />
            Правила шунтирования
          </CardTitle>
          <CardDescription>
            Какие домены и IP идут через какой узел.
          </CardDescription>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => onChange(addShuntRule(config))}
          disabled={disabled}
        >
          <Plus className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
          Правило
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {rules.length === 0 ? (
          <EmptyState
            icon={Split}
            title="Правил пока нет"
            description="Добавьте правило, чтобы развести трафик по узлам."
          />
        ) : (
          rules.map((rule, index) => (
            <ShuntRuleCard
              key={rule.id}
              rule={rule}
              index={index}
              nodeOptions={nodeOptions}
              disabled={disabled}
              onMoveTop={() => onChange(moveShuntRuleToTop(config, index))}
              onDelete={() => onChange(deleteShuntRule(config, index))}
              onRenameId={(nextId) =>
                onChange(renameShuntRule(config, rule.id, nextId))
              }
              onLabelChange={(label) =>
                onChange(setShuntRuleField(config, rule.id, { label }))
              }
              onOutboundChange={(value) =>
                onChange(
                  setShuntRuleField(config, rule.id, {
                    outboundNodeId:
                      value === UNBOUND_VALUE ? undefined : value,
                  }),
                )
              }
              onDomainRulesChange={(domainRules) =>
                onChange(setShuntRuleField(config, rule.id, { domainRules }))
              }
              onIpRulesChange={(ipRules) =>
                onChange(setShuntRuleField(config, rule.id, { ipRules }))
              }
              onSetExtra={(key, value) =>
                onChange(updateShuntRuleExtra(config, rule.id, key, value))
              }
            />
          ))
        )}
      </CardContent>
    </Card>
  );
}

type ShuntRule =
  ConfigSectionProps["config"]["basicSettings"]["shuntRules"][number];

function ShuntRuleCard({
  rule,
  index,
  nodeOptions,
  disabled = false,
  onMoveTop,
  onDelete,
  onRenameId,
  onLabelChange,
  onOutboundChange,
  onDomainRulesChange,
  onIpRulesChange,
  onSetExtra,
}: {
  rule: ShuntRule;
  index: number;
  nodeOptions: { value: string; label: string }[];
  disabled?: boolean;
  onMoveTop: () => void;
  onDelete: () => void;
  onRenameId: (nextId: string) => void;
  onLabelChange: (label: string) => void;
  onOutboundChange: (value: string) => void;
  onDomainRulesChange: (values: string[]) => void;
  onIpRulesChange: (values: string[]) => void;
  onSetExtra: (key: string, value: string | undefined) => void;
}) {
  const idFieldId = useId();
  const nameFieldId = useId();
  const [idDraft, setIdDraft] = useState(rule.id);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  useEffect(() => {
    setIdDraft(rule.id);
  }, [rule.id]);

  return (
    <div className="rounded-md border border-border/50 bg-card/40 p-3">
      <div className="flex items-center justify-between gap-2 pb-2">
        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Правило {index + 1}
        </p>
        <div className="flex gap-1">
          <Button
            size="icon"
            variant="ghost"
            className="h-7 w-7"
            onClick={onMoveTop}
            disabled={disabled || index === 0}
            aria-label="Поднять наверх"
          >
            <ArrowUpToLine className="h-3.5 w-3.5" strokeWidth={1.75} />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="h-7 w-7 text-destructive hover:text-destructive"
            onClick={onDelete}
            disabled={disabled}
            aria-label="Удалить правило"
          >
            <Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />
          </Button>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor={idFieldId} className="text-sm font-medium">
            Ключ правила
          </Label>
          <Input
            id={idFieldId}
            value={idDraft}
            disabled={disabled}
            onChange={(event) => setIdDraft(event.target.value)}
            onBlur={() => {
              const next = idDraft.trim();
              if (next && next !== rule.id) {
                onRenameId(next);
              } else {
                setIdDraft(rule.id);
              }
            }}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={nameFieldId} className="text-sm font-medium">
            Название
          </Label>
          <Input
            id={nameFieldId}
            value={rule.label}
            disabled={disabled}
            onChange={(event) => onLabelChange(event.target.value)}
          />
        </div>
      </div>

      <SelectField
        label="Исходящий узел"
        value={rule.outboundNodeId ?? UNBOUND_VALUE}
        options={nodeOptions}
        onValueChange={onOutboundChange}
        disabled={disabled}
      />

      <div className="grid gap-3 sm:grid-cols-2">
        <StringListField
          label="Домены"
          description="geosite:youtube, domain:example.com…"
          values={rule.domainRules}
          onCommit={onDomainRulesChange}
          disabled={disabled}
        />
        <StringListField
          label="IP / CIDR"
          description="geoip:ru, 8.8.8.8, 10.0.0.0/8…"
          values={rule.ipRules}
          onCommit={onIpRulesChange}
          disabled={disabled}
        />
      </div>

      <div className="mt-1 border-t border-border/40 pt-1">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 px-1 text-xs text-muted-foreground"
          onClick={() => setAdvancedOpen((open) => !open)}
        >
          {advancedOpen ? (
            <ChevronDown className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
          ) : (
            <ChevronRight className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
          )}
          Дополнительно
        </Button>

        {advancedOpen ? (
          <div className="mt-1">
            <div className="grid gap-3 sm:grid-cols-2">
              <SelectField
                label="Сеть"
                value={getExtraString(rule.extras, "network", "tcp,udp")}
                options={NETWORK_OPTIONS}
                onValueChange={(value) => onSetExtra("network", value)}
                disabled={disabled}
              />
              <TextField
                label="Порт назначения"
                value={getExtraString(rule.extras, "port") ?? ""}
                placeholder="напр. 443 или 19294-19344"
                onChange={(value) =>
                  onSetExtra("port", value.trim() ? value : undefined)
                }
                disabled={disabled}
              />
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <CheckboxGroupField
                label="Протокол (sniffing)"
                options={PROTOCOL_OPTIONS}
                selected={getExtraTokens(rule.extras, "protocol")}
                onChange={(values) =>
                  onSetExtra("protocol", encodeExtraTokens(values))
                }
                disabled={disabled}
              />
              <CheckboxGroupField
                label="Входящий тег"
                options={INBOUND_OPTIONS}
                selected={getExtraTokens(rule.extras, "inbound")}
                onChange={(values) =>
                  onSetExtra("inbound", encodeExtraTokens(values))
                }
                disabled={disabled}
              />
            </div>
            <StringListField
              label="Источник (Source)"
              description="IP / CIDR / geoip:private — по одному на строку."
              values={getExtraTokens(rule.extras, "source")}
              onCommit={(values) =>
                onSetExtra("source", encodeExtraTokens(values))
              }
              disabled={disabled}
            />
            <BooleanField
              label="Инвертировать совпадение"
              description="Только Sing-Box. Правило срабатывает на НЕ совпавших."
              checked={getExtraBoolean(rule.extras, "invert")}
              onCheckedChange={(checked) =>
                onSetExtra("invert", checked ? "1" : undefined)
              }
              disabled={disabled}
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}
