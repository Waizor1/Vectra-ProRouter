"use client";

import { useState } from "react";
import {
  ArrowUpToLine,
  Boxes,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Copy,
  Plus,
  Trash2,
} from "lucide-react";

import {
  addNode,
  deleteNode,
  duplicateNode,
  moveNodeToTop,
  selectNode,
} from "~/components/router-editor-state";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { Switch } from "~/components/ui/switch";
import { EmptyState } from "~/components/vectra/empty-state";
import { ToneBadge } from "~/components/vectra/tone-badge";
import type { PasswallDesiredConfig } from "@vectra/contracts";

import {
  BooleanField,
  NumberField,
  SelectField,
  TextField,
  type SelectOption,
} from "~/features/config-editor/fields";
import { applyNodeFieldPatch, toggleNodeEnabled } from "./nodes-state";

type Node = PasswallDesiredConfig["nodes"][number];

const PROTOCOL_OPTIONS: SelectOption[] = [
  { value: "xray", label: "Xray" },
  { value: "sing-box", label: "Sing-Box" },
  { value: "shadowsocks-libev", label: "Shadowsocks (libev)" },
  { value: "shadowsocks-rust", label: "Shadowsocks (rust)" },
  { value: "hysteria2", label: "Hysteria2" },
  { value: "trojan", label: "Trojan" },
  { value: "vmess", label: "VMess" },
  { value: "vless", label: "VLESS" },
  { value: "socks", label: "Socks" },
  { value: "balancing", label: "Balancing" },
  { value: "urltest", label: "URLTest" },
  { value: "shunt", label: "Shunt (分流)" },
  { value: "iface", label: "Interface" },
  { value: "custom", label: "Custom" },
];

const TRANSPORT_OPTIONS: SelectOption[] = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "grpc", label: "gRPC" },
  { value: "ws", label: "WebSocket" },
  { value: "quic", label: "QUIC" },
  { value: "xhttp", label: "XHTTP" },
  { value: "httpupgrade", label: "HTTPUpgrade" },
  { value: "custom", label: "Custom" },
];

// Virtual nodes route/aggregate other nodes and have no server endpoint.
const VIRTUAL_PROTOCOLS = new Set(["shunt", "balancing", "urltest", "iface"]);

const VIRTUAL_ENDPOINT_LABEL: Record<string, string> = {
  shunt: "маршрутизация по правилам",
  balancing: "балансировка узлов",
  urltest: "авто-выбор по задержке",
  iface: "сетевой интерфейс",
};

function isVirtual(node: Node): boolean {
  return VIRTUAL_PROTOCOLS.has(node.protocol);
}

function endpoint(node: Node): string {
  if (isVirtual(node)) {
    return VIRTUAL_ENDPOINT_LABEL[node.protocol] ?? "виртуальный узел";
  }
  if (!node.address) {
    return "адрес не задан";
  }
  return node.port ? `${node.address}:${node.port}` : node.address;
}

export interface NodeListSectionProps {
  config: PasswallDesiredConfig;
  onChange: (next: PasswallDesiredConfig) => void;
  editableNodeIds: string[];
  disabled?: boolean;
}

export function NodeListSection({
  config,
  onChange,
  editableNodeIds,
  disabled = false,
}: NodeListSectionProps) {
  const editable = new Set(editableNodeIds);
  const selectedNodeId = config.basicSettings.main.selectedNodeId;

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 pb-3">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <Boxes className="h-4 w-4" strokeWidth={1.75} />
            Узлы
          </CardTitle>
          <CardDescription>
            Прокси-узлы PassWall. Узлы из подписок обновляются автоматически.
          </CardDescription>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => onChange(addNode(config))}
          disabled={disabled}
        >
          <Plus className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
          Узел
        </Button>
      </CardHeader>
      <CardContent className="space-y-2">
        {config.nodes.length === 0 ? (
          <EmptyState
            icon={Boxes}
            title="Узлов пока нет"
            description="Добавьте узел вручную или импортируйте подписку."
          />
        ) : (
          config.nodes.map((node, index) => (
            <NodeRow
              key={node.id}
              node={node}
              index={index}
              isActive={selectedNodeId === node.id}
              isEditable={editable.has(node.id)}
              disabled={disabled}
              onSelect={() => onChange(selectNode(config, node.id))}
              onToggleEnabled={(enabled) =>
                onChange(toggleNodeEnabled(config, node.id, enabled))
              }
              onDuplicate={() => onChange(duplicateNode(config, index))}
              onMoveTop={() => onChange(moveNodeToTop(config, index))}
              onDelete={() => onChange(deleteNode(config, index))}
              onPatch={(patch) =>
                onChange(applyNodeFieldPatch(config, node.id, patch))
              }
            />
          ))
        )}
      </CardContent>
    </Card>
  );
}

function NodeRow({
  node,
  index,
  isActive,
  isEditable,
  disabled,
  onSelect,
  onToggleEnabled,
  onDuplicate,
  onMoveTop,
  onDelete,
  onPatch,
}: {
  node: Node;
  index: number;
  isActive: boolean;
  isEditable: boolean;
  disabled: boolean;
  onSelect: () => void;
  onToggleEnabled: (enabled: boolean) => void;
  onDuplicate: () => void;
  onMoveTop: () => void;
  onDelete: () => void;
  onPatch: (patch: Parameters<typeof applyNodeFieldPatch>[2]) => void;
}) {
  const [open, setOpen] = useState(false);

  return (
    <div className="rounded-md border border-border/50 bg-card/40">
      <div className="flex flex-wrap items-center gap-2 px-3 py-2.5">
        <button
          type="button"
          onClick={() => isEditable && setOpen((v) => !v)}
          className="flex min-w-0 flex-1 items-center gap-2 text-left disabled:cursor-default"
          disabled={!isEditable}
        >
          {isEditable ? (
            open ? (
              <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" strokeWidth={1.75} />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" strokeWidth={1.75} />
            )
          ) : (
            <Boxes className="h-3.5 w-3.5 shrink-0 text-muted-foreground" strokeWidth={1.75} />
          )}
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="truncate text-sm font-medium text-foreground">
                {node.label || node.id}
              </span>
              {isActive ? (
                <ToneBadge tone="good" dot>
                  активный
                </ToneBadge>
              ) : null}
              {!isEditable ? (
                <ToneBadge tone="info">из подписки</ToneBadge>
              ) : null}
            </div>
            <p className="truncate text-xs text-muted-foreground">
              {node.protocol} · {endpoint(node)}
            </p>
          </div>
        </button>

        <div className="flex items-center gap-1">
          {!isActive ? (
            <Button
              size="sm"
              variant="ghost"
              className="h-7 px-2 text-xs"
              onClick={onSelect}
              disabled={disabled || !node.enabled}
            >
              <CheckCircle2 className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
              Активный
            </Button>
          ) : null}
          <Switch
            checked={node.enabled}
            onCheckedChange={onToggleEnabled}
            disabled={disabled}
            aria-label={node.enabled ? "Выключить узел" : "Включить узел"}
          />
          {isEditable ? (
            <>
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
                className="h-7 w-7"
                onClick={onDuplicate}
                disabled={disabled}
                aria-label="Дублировать"
              >
                <Copy className="h-3.5 w-3.5" strokeWidth={1.75} />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="h-7 w-7 text-destructive hover:text-destructive"
                onClick={onDelete}
                disabled={disabled}
                aria-label="Удалить узел"
              >
                <Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />
              </Button>
            </>
          ) : null}
        </div>
      </div>

      {isEditable && open ? (
        <>
          <Separator />
          <div className="px-3 pb-2">
            <div className="grid gap-3 sm:grid-cols-2">
              <TextField
                label="Название"
                value={node.label}
                onChange={(label) => onPatch({ label })}
                disabled={disabled}
              />
              <SelectField
                label="Протокол"
                value={node.protocol}
                options={PROTOCOL_OPTIONS}
                onValueChange={(value) =>
                  onPatch({ protocol: value as Node["protocol"] })
                }
                disabled={disabled}
              />
              {isVirtual(node) ? null : (
                <>
                  <TextField
                    label="Адрес"
                    value={node.address ?? ""}
                    placeholder="example.com или 1.2.3.4"
                    onChange={(value) =>
                      onPatch({ address: value.trim() ? value : undefined })
                    }
                    disabled={disabled}
                  />
                  <NumberField
                    label="Порт"
                    value={node.port ?? 0}
                    min={0}
                    max={65535}
                    onChange={(port) => onPatch({ port })}
                    disabled={disabled}
                  />
                  <SelectField
                    label="Транспорт"
                    value={node.transport ?? "tcp"}
                    options={TRANSPORT_OPTIONS}
                    onValueChange={(value) =>
                      onPatch({ transport: value as Node["transport"] })
                    }
                    disabled={disabled}
                  />
                </>
              )}
              <TextField
                label="Группа"
                value={node.group}
                onChange={(group) => onPatch({ group })}
                disabled={disabled}
              />
            </div>
            {isVirtual(node) ? (
              <p className="pb-1 text-xs text-muted-foreground">
                {node.protocol === "shunt"
                  ? "Маршрутизация по правилам настраивается ниже, в разделе «Shunt-маршрутизация»."
                  : "Виртуальный узел — адрес и транспорт не требуются."}
              </p>
            ) : (
              <BooleanField
                label="TLS"
                checked={node.tls === true}
                onCheckedChange={(tls) => onPatch({ tls })}
                disabled={disabled}
              />
            )}
          </div>
        </>
      ) : null}
    </div>
  );
}
