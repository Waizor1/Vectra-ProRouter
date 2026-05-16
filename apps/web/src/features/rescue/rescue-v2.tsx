import Link from "next/link";
import {
  AlertOctagon,
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Clock,
  ShieldAlert,
} from "lucide-react";

import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Separator } from "~/components/ui/separator";
import { ToneBadge } from "~/components/vectra/tone-badge";

interface DirectModeRouter {
  id: string;
  displayName: string | null;
  hostname: string | null;
  deviceIdentifier: string;
  lastRescueReason: string | null;
}

interface RescueIncident {
  id: string;
  type: string;
  reason: string;
}

interface RescueCase {
  id: string;
  routerId: string;
  trigger: string;
  state: string;
  startedAt: Date;
  resolvedAt?: Date | null;
}

interface RescuePolicy {
  triggerFailureCount: number;
  recoverySuccessCount: number;
  cooldownSeconds: number;
  requireDirectPathSuccess: boolean;
  directModeReason: string;
}

export interface RescueV2Props {
  policy: RescuePolicy;
  incidents: RescueIncident[];
  directRouters: DirectModeRouter[];
  rescueCases: RescueCase[];
}

const RESOLVED_STATES = new Set(["resolved", "closed", "ok"]);

function isResolved(state: string): boolean {
  return RESOLVED_STATES.has(state.toLowerCase());
}

export function RescueV2({
  policy,
  incidents,
  directRouters,
  rescueCases,
}: RescueV2Props) {
  const activeCases = rescueCases.filter((c) => !isResolved(c.state));
  const resolvedCases = rescueCases.filter((c) => isResolved(c.state));
  const allCalm =
    incidents.length === 0 &&
    directRouters.length === 0 &&
    activeCases.length === 0;

  return (
    <section className="mx-auto w-full max-w-5xl space-y-4">
      <header className="space-y-1">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
          Восстановление
        </p>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">
          Rescue
        </h1>
        <p className="text-sm text-muted-foreground">
          Парк, direct mode и инциденты под одним экраном. Тут включается
          aut-rescue и сюда идёшь, когда что-то горит.
        </p>
      </header>

      {allCalm ? (
        <CalmState resolvedCases={resolvedCases} />
      ) : (
        <ActiveIncidents
          directRouters={directRouters}
          incidents={incidents}
          activeCases={activeCases}
        />
      )}

      <PolicyDisclosure policy={policy} />
    </section>
  );
}

function CalmState({ resolvedCases }: { resolvedCases: RescueCase[] }) {
  const recent = resolvedCases.slice(0, 5);

  return (
    <Card className="border-emerald-500/30 bg-emerald-500/[0.05]">
      <CardContent className="flex flex-col items-center gap-4 px-6 py-12 text-center">
        <div className="grid h-14 w-14 place-items-center rounded-full bg-emerald-500/15 text-emerald-300">
          <CheckCircle2 className="h-7 w-7" strokeWidth={1.75} />
        </div>
        <div className="space-y-1">
          <h2 className="text-xl font-semibold text-foreground">
            Парк стабилен
          </h2>
          <p className="max-w-prose text-sm text-muted-foreground">
            Никто в direct mode, инцидентов нет, активных rescue-cases нет.
            Auto-Rescue продолжает следить и поднимет case, если что-то
            свалится.
          </p>
        </div>

        {recent.length > 0 ? (
          <div className="mt-2 w-full max-w-md text-left">
            <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Недавно закрытые ({recent.length})
            </p>
            <ul className="space-y-1">
              {recent.map((c) => (
                <li key={c.id}>
                  <Link
                    href={`/rescue/cases/${c.id}`}
                    className="flex items-center gap-2 rounded-md border border-border/40 bg-card/50 px-3 py-2 text-xs transition-colors hover:bg-secondary/40"
                  >
                    <CheckCircle2 className="h-3 w-3 text-emerald-400" />
                    <span className="flex-1 truncate text-foreground">
                      {c.trigger}
                    </span>
                    <span className="text-muted-foreground">
                      {c.startedAt.toLocaleDateString("ru-RU")}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function ActiveIncidents({
  directRouters,
  incidents,
  activeCases,
}: {
  directRouters: DirectModeRouter[];
  incidents: RescueIncident[];
  activeCases: RescueCase[];
}) {
  return (
    <div className="space-y-4">
      {directRouters.length > 0 ? (
        <Card className="border-amber-500/30">
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlert
                className="h-4 w-4 text-amber-300"
                strokeWidth={1.75}
              />
              Direct mode
              <ToneBadge tone="warning" className="ml-1">
                {directRouters.length}
              </ToneBadge>
            </CardTitle>
            <CardDescription>
              Эти роутеры сейчас работают мимо proxy. Auto-Rescue ждёт
              стабильности перед возвратом.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {directRouters.map((router) => (
              <Link
                key={router.id}
                href={`/routers/${router.id}`}
                className="flex items-center gap-3 rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm transition-colors hover:border-amber-500/40 hover:bg-secondary/40"
              >
                <div className="flex-1 min-w-0">
                  <p className="truncate font-medium text-foreground">
                    {router.displayName ??
                      router.hostname ??
                      router.deviceIdentifier}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {router.lastRescueReason ?? "Причина не записана"}
                  </p>
                </div>
                <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" />
              </Link>
            ))}
          </CardContent>
        </Card>
      ) : null}

      {activeCases.length > 0 ? (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <AlertOctagon
                className="h-4 w-4 text-rose-300"
                strokeWidth={1.75}
              />
              Активные rescue cases
              <ToneBadge tone="critical" className="ml-1">
                {activeCases.length}
              </ToneBadge>
            </CardTitle>
            <CardDescription>
              Caseʼы которые Auto-Rescue открыл, но ещё не закрыл.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {activeCases.map((c) => (
              <Link
                key={c.id}
                href={`/rescue/cases/${c.id}`}
                className="flex items-start gap-3 rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm transition-colors hover:border-rose-500/40 hover:bg-secondary/40"
              >
                <Clock
                  className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground"
                  strokeWidth={1.75}
                />
                <div className="flex-1 min-w-0">
                  <p className="font-medium text-foreground">
                    {c.trigger}
                    <Badge variant="outline" className="ml-2 align-middle">
                      {c.state}
                    </Badge>
                  </p>
                  <p className="text-xs text-muted-foreground">
                    Router {c.routerId} ·{" "}
                    {c.startedAt.toLocaleString("ru-RU")}
                  </p>
                </div>
                <Button size="sm" variant="ghost" asChild>
                  <span>
                    Открыть <ArrowRight className="ml-1 h-3 w-3" />
                  </span>
                </Button>
              </Link>
            ))}
          </CardContent>
        </Card>
      ) : null}

      {incidents.length > 0 ? (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <AlertTriangle
                className="h-4 w-4 text-amber-300"
                strokeWidth={1.75}
              />
              Открытые инциденты
              <ToneBadge tone="warning" className="ml-1">
                {incidents.length}
              </ToneBadge>
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {incidents.map((incident) => (
              <div
                key={incident.id}
                className="rounded-md border border-border/40 bg-card/40 px-3 py-2.5 text-sm"
              >
                <p className="font-medium text-foreground">{incident.type}</p>
                <p className="text-xs text-muted-foreground">
                  {incident.reason}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

function PolicyDisclosure({ policy }: { policy: RescuePolicy }) {
  return (
    <details className="group rounded-lg border border-border/40 bg-card/30">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3">
        <div>
          <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Auto-Rescue политика
          </p>
          <p className="mt-0.5 text-sm font-medium text-foreground">
            Триггер, возврат, сообщение direct mode
          </p>
        </div>
        <span className="text-xs text-muted-foreground group-open:hidden">
          раскрыть
        </span>
        <span className="hidden text-xs text-muted-foreground group-open:inline">
          скрыть
        </span>
      </summary>

      <Separator />

      <div className="grid gap-3 px-4 py-4 sm:grid-cols-2 lg:grid-cols-4">
        <PolicyTile
          label="Триггер"
          value={String(policy.triggerFailureCount)}
          hint="неудачных проверок до direct"
        />
        <PolicyTile
          label="Возврат"
          value={String(policy.recoverySuccessCount)}
          hint="успешных до возврата в proxy"
        />
        <PolicyTile
          label="Пауза"
          value={`${Math.round(policy.cooldownSeconds / 60)} мин`}
          hint="между циклами проверок"
        />
        <PolicyTile
          label="Direct path"
          value={policy.requireDirectPathSuccess ? "нужен" : "не обязателен"}
          hint="для выхода из direct mode"
        />
      </div>

      <div className="mx-4 mb-4 rounded-md border border-amber-500/20 bg-amber-500/5 px-3 py-2.5">
        <p className="text-xs font-semibold uppercase tracking-wider text-amber-300">
          Сообщение direct mode
        </p>
        <p className="mt-1 text-sm font-medium text-foreground">
          {policy.directModeReason}
        </p>
      </div>
    </details>
  );
}

function PolicyTile({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint: string;
}) {
  return (
    <div className="rounded-md border border-border/40 bg-background/40 px-3 py-2">
      <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </p>
      <p className="mt-1 text-base font-semibold text-foreground">{value}</p>
      <p className="text-xs text-muted-foreground">{hint}</p>
    </div>
  );
}
