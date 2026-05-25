"use client";

import {
  ArrowUpToLine,
  Plus,
  RadioTower,
  RefreshCw,
  ScanLine,
  Trash2,
} from "lucide-react";

import {
  addSubscription,
  deleteSubscription,
  moveSubscriptionToTop,
} from "~/components/router-editor-state";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Switch } from "~/components/ui/switch";
import { EmptyState } from "~/components/vectra/empty-state";
import type { PasswallDesiredConfig } from "@vectra/contracts";

import {
  SelectField,
  TextField,
  type SelectOption,
} from "~/features/config-editor/fields";
import { applySubscriptionFieldPatch } from "./nodes-state";

type Subscription = PasswallDesiredConfig["subscriptions"]["items"][number];

const ADD_MODE_OPTIONS: SelectOption[] = [
  { value: "2", label: "Подписка (узлы обновляются)" },
  { value: "1", label: "Разовый импорт" },
];

export interface SubscriptionSectionProps {
  config: PasswallDesiredConfig;
  onChange: (next: PasswallDesiredConfig) => void;
  disabled?: boolean;
  onRefresh: () => void;
  onInspect: () => void;
  refreshing?: boolean;
  inspecting?: boolean;
}

export function SubscriptionSection({
  config,
  onChange,
  disabled = false,
  onRefresh,
  onInspect,
  refreshing = false,
  inspecting = false,
}: SubscriptionSectionProps) {
  const items = config.subscriptions.items;

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 pb-3">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <RadioTower className="h-4 w-4" strokeWidth={1.75} />
            Подписки
          </CardTitle>
          <CardDescription>
            Источники узлов. Обновление подтягивает свежие узлы на роутер.
          </CardDescription>
        </div>
        <Button
          size="sm"
          variant="outline"
          className="shrink-0"
          onClick={() => onChange(addSubscription(config))}
          disabled={disabled}
        >
          <Plus className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
          Подписка
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {items.length === 0 ? (
          <EmptyState
            icon={RadioTower}
            title="Подписок пока нет"
            description="Добавьте подписку, чтобы автоматически подтягивать узлы."
          />
        ) : (
          <>
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={onRefresh}
                disabled={disabled || refreshing}
              >
                <RefreshCw className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
                Обновить узлы
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={onInspect}
                disabled={disabled || inspecting}
              >
                <ScanLine className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
                Проверить
              </Button>
            </div>

            {items.map((item, index) => (
              <SubscriptionRow
                key={item.id}
                item={item}
                index={index}
                disabled={disabled}
                onMoveTop={() => onChange(moveSubscriptionToTop(config, index))}
                onDelete={() => onChange(deleteSubscription(config, index))}
                onPatch={(patch) =>
                  onChange(applySubscriptionFieldPatch(config, item.id, patch))
                }
              />
            ))}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function SubscriptionRow({
  item,
  index,
  disabled,
  onMoveTop,
  onDelete,
  onPatch,
}: {
  item: Subscription;
  index: number;
  disabled: boolean;
  onMoveTop: () => void;
  onDelete: () => void;
  onPatch: (patch: Parameters<typeof applySubscriptionFieldPatch>[2]) => void;
}) {
  return (
    <div className="rounded-md border border-border/50 bg-card/40 p-3">
      <div className="flex items-center justify-between gap-2 pb-1">
        <div className="flex items-center gap-2">
          <Switch
            checked={item.enabled}
            onCheckedChange={(enabled) => onPatch({ enabled })}
            disabled={disabled}
            aria-label="Включить подписку"
          />
          <span className="text-sm font-medium text-foreground">
            {item.remark || `Подписка ${index + 1}`}
          </span>
        </div>
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
            aria-label="Удалить подписку"
          >
            <Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />
          </Button>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <TextField
          label="Название"
          value={item.remark}
          onChange={(remark) => onPatch({ remark })}
          disabled={disabled}
        />
        <SelectField
          label="Режим"
          value={item.addMode}
          options={ADD_MODE_OPTIONS}
          onValueChange={(value) =>
            onPatch({ addMode: value as Subscription["addMode"] })
          }
          disabled={disabled}
        />
      </div>
      <TextField
        label="URL подписки"
        type="url"
        value={item.url}
        placeholder="https://…"
        onChange={(url) => onPatch({ url })}
        disabled={disabled}
      />
    </div>
  );
}
