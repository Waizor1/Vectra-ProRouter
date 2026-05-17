"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  AlertOctagon,
  CommandIcon,
  LayoutDashboard,
  LogOut,
  Rocket,
  Settings,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { Button } from "~/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import { cn } from "~/lib/utils";
import { CommandPalette } from "~/features/shell/command-palette";
import { SettingsDrawer } from "~/features/shell/settings-drawer";

const PRIMARY_TABS = [
  { id: "fleet", label: "Fleet", href: "/fleet", icon: LayoutDashboard },
  { id: "updates", label: "Updates", href: "/updates", icon: Rocket },
  { id: "rescue", label: "Rescue", href: "/rescue", icon: AlertOctagon },
] as const;

export interface OperatorShellV2Props {
  children: React.ReactNode;
}

function isActive(pathname: string, href: string): boolean {
  if (href === "/fleet") {
    return pathname === "/fleet" || pathname.startsWith("/routers/");
  }
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function OperatorShellV2({ children }: OperatorShellV2Props) {
  const pathname = usePathname();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const openPalette = useCallback(() => setPaletteOpen(true), []);
  const openSettings = useCallback(() => setSettingsOpen(true), []);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((open) => !open);
      }
      if (e.key === "/" && document.activeElement?.tagName !== "INPUT") {
        e.preventDefault();
        setPaletteOpen(true);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <TooltipProvider delayDuration={200}>
      <div className="vectra-shell">
        <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(60,112,156,0.12),transparent_22%),radial-gradient(circle_at_bottom_right,rgba(174,95,42,0.12),transparent_20%)]" />
        <div className="vectra-shell-frame">
          <header className="rounded-lg border border-border/40 bg-card/80 px-3 py-2 shadow-sm backdrop-blur sm:px-4">
            <div className="flex items-center gap-2">
              <Link
                href="/fleet"
                className="flex items-center gap-2 rounded-md px-2 py-1 text-sm font-semibold text-foreground hover:bg-secondary/40"
                aria-label="ProRouter — на главную"
              >
                <span className="grid h-7 w-7 place-items-center rounded-md border border-primary/40 bg-primary/15 font-mono text-[13px] font-bold text-primary">
                  V
                </span>
                <span className="hidden tracking-tight sm:inline">
                  ProRouter
                </span>
              </Link>

              <nav className="ml-2 flex flex-1 items-center gap-1">
                {PRIMARY_TABS.map((tab) => {
                  const active = isActive(pathname, tab.href);
                  const Icon = tab.icon;
                  return (
                    <Link
                      key={tab.id}
                      href={tab.href}
                      className={cn(
                        "inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-sm font-medium transition-colors",
                        active
                          ? "bg-secondary text-foreground"
                          : "text-muted-foreground hover:bg-secondary/40 hover:text-foreground",
                      )}
                      aria-current={active ? "page" : undefined}
                    >
                      <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
                      <span>{tab.label}</span>
                    </Link>
                  );
                })}
              </nav>

              <div className="flex items-center gap-1">
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-8 gap-2 text-muted-foreground hover:text-foreground"
                      onClick={openPalette}
                    >
                      <CommandIcon className="h-3.5 w-3.5" strokeWidth={1.75} />
                      <span className="hidden sm:inline">Поиск</span>
                      <kbd className="hidden rounded border border-border/60 bg-background px-1.5 font-mono text-[10px] sm:inline">
                        ⌘K
                      </kbd>
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="bottom">
                    Глобальный поиск (⌘K / /)
                  </TooltipContent>
                </Tooltip>

                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-muted-foreground hover:text-foreground"
                      onClick={openSettings}
                      aria-label="Открыть настройки"
                    >
                      <Settings className="h-4 w-4" strokeWidth={1.75} />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="bottom">Настройки</TooltipContent>
                </Tooltip>

                <form
                  action="/api/operator/logout"
                  method="post"
                  className="hidden sm:block"
                >
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        type="submit"
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-muted-foreground hover:text-destructive"
                        aria-label="Выйти"
                      >
                        <LogOut className="h-4 w-4" strokeWidth={1.75} />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent side="bottom">Выйти</TooltipContent>
                  </Tooltip>
                </form>
              </div>
            </div>
          </header>

          <main className="vectra-page-stack min-w-0 flex-1">{children}</main>
        </div>

        <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
        <SettingsDrawer
          open={settingsOpen}
          onOpenChange={setSettingsOpen}
        />
      </div>
    </TooltipProvider>
  );
}
