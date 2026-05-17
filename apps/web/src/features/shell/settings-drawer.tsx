"use client";

import Link from "next/link";
import {
  Download,
  ExternalLink,
  KeyRound,
  LogOut,
  PackagePlus,
  Wrench,
} from "lucide-react";

import { Button } from "~/components/ui/button";
import { Separator } from "~/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "~/components/ui/sheet";

export interface SettingsDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

interface DrawerLink {
  href: string;
  icon: typeof Download;
  label: string;
  description: string;
}

const TOOLS: DrawerLink[] = [
  {
    href: "/enrollment",
    icon: PackagePlus,
    label: "Онбординг роутера",
    description: "Bootstrap-команда и шаги для нового устройства",
  },
  {
    href: "/drafts",
    icon: Wrench,
    label: "JSON эксперт-режим",
    description: "Прямое редактирование конфигов PassWall2",
  },
  {
    href: "/downloads",
    icon: Download,
    label: "Артефакты",
    description: "Релиз-каталог controller / PassWall / firmware",
  },
];

export function SettingsDrawer({ open, onOpenChange }: SettingsDrawerProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full max-w-sm border-border/60 bg-background/95 backdrop-blur"
      >
        <SheetHeader>
          <SheetTitle>Настройки и инструменты</SheetTitle>
          <SheetDescription>
            Редкие действия и переходы — те, что не заслуживают места в шапке.
          </SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-1">
          {TOOLS.map((tool) => {
            const Icon = tool.icon;
            return (
              <Link
                key={tool.href}
                href={tool.href}
                onClick={() => onOpenChange(false)}
                className="flex items-start gap-3 rounded-md p-3 text-sm transition-colors hover:bg-secondary/50"
              >
                <Icon
                  className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground"
                  strokeWidth={1.75}
                />
                <div className="flex flex-col gap-0.5">
                  <span className="font-medium text-foreground">
                    {tool.label}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {tool.description}
                  </span>
                </div>
              </Link>
            );
          })}
        </div>

        <Separator className="my-6" />

        <div className="space-y-3">
          <p className="px-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Сессия
          </p>
          <form action="/api/operator/logout" method="post">
            <Button
              type="submit"
              variant="outline"
              size="sm"
              className="w-full justify-start"
            >
              <LogOut className="mr-2 h-3.5 w-3.5" />
              Выйти из панели
            </Button>
          </form>
        </div>

        <Separator className="my-6" />

        <div className="space-y-3">
          <p className="px-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Справка
          </p>
          <a
            href="https://router.vectra-pro.net/install"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 rounded-md p-2 text-sm text-muted-foreground transition-colors hover:bg-secondary/40 hover:text-foreground"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            Публичная страница установки
          </a>
          <p className="px-2 text-xs text-muted-foreground">
            <KeyRound className="mr-1.5 inline h-3 w-3" />
            ⌘K — поиск · ⌘L — диагностика · g f / u / r — навигация
          </p>
        </div>
      </SheetContent>
    </Sheet>
  );
}
