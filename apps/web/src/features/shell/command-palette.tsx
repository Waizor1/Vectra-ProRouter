"use client";

import { useRouter } from "next/navigation";
import {
  AlertOctagon,
  Download,
  LayoutDashboard,
  PackagePlus,
  PlusCircle,
  Rocket,
  Wrench,
} from "lucide-react";

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "~/components/ui/command";

export interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const router = useRouter();

  function go(href: string) {
    onOpenChange(false);
    router.push(href);
  }

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder="Команда, роутер, инцидент…" />
      <CommandList>
        <CommandEmpty>Ничего не найдено.</CommandEmpty>

        <CommandGroup heading="Навигация">
          <CommandItem onSelect={() => go("/fleet")}>
            <LayoutDashboard />
            <span>Открыть Fleet</span>
            <CommandShortcut>g f</CommandShortcut>
          </CommandItem>
          <CommandItem onSelect={() => go("/updates")}>
            <Rocket />
            <span>Открыть Updates</span>
            <CommandShortcut>g u</CommandShortcut>
          </CommandItem>
          <CommandItem onSelect={() => go("/rescue")}>
            <AlertOctagon />
            <span>Открыть Rescue</span>
            <CommandShortcut>g r</CommandShortcut>
          </CommandItem>
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="Быстрые действия">
          <CommandItem onSelect={() => go("/updates?wizard=new")}>
            <PlusCircle />
            <span>Новая раскатка</span>
            <CommandShortcut>n</CommandShortcut>
          </CommandItem>
          <CommandItem onSelect={() => go("/enrollment")}>
            <PackagePlus />
            <span>Онбординг нового роутера</span>
          </CommandItem>
          <CommandItem onSelect={() => go("/drafts")}>
            <Wrench />
            <span>JSON эксперт-режим</span>
          </CommandItem>
          <CommandItem onSelect={() => go("/downloads")}>
            <Download />
            <span>Артефакты и загрузки</span>
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
