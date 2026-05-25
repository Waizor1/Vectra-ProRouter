"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import {
  Boxes,
  CircuitBoard,
  Layers,
  Pencil,
  Plus,
  Rocket,
  Server,
  ShieldCheck,
  Trash2,
  Users,
} from "lucide-react";
import { toast } from "sonner";

import {
  createDraftFixture,
} from "~/components/router-editor-state";
import {
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
} from "@vectra/contracts";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "~/components/ui/alert-dialog";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "~/components/ui/sheet";
import { Textarea } from "~/components/ui/textarea";
import { EmptyState } from "~/components/vectra/empty-state";
import { ToneBadge } from "~/components/vectra/tone-badge";
import { ConfigEditor } from "~/features/config-editor";
import type { RouterOutputs } from "~/trpc/react";
import { api } from "~/trpc/react";

type Workspace = RouterOutputs["update"]["profilesAndGroupsWorkspace"];
type ProfileItem = Workspace["profiles"][number];
type GroupItem = Workspace["groups"][number];

interface Artifact {
  id: string;
  name: string;
  type: string;
  channel: string;
  version: string;
}
interface FirmwareManifest {
  channel: string;
  version: string;
  boardName: string;
  layoutFamily: string;
}

export interface UpdatesV2Props {
  initialWorkspace: Workspace;
  artifacts: Artifact[];
  manifests: FirmwareManifest[];
}

const UNASSIGNED = "__unassigned__";

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Неизвестная ошибка";
}

interface EditingProfile {
  profileId: string | null;
  name: string;
  description: string;
  config: PasswallDesiredConfig;
}

export function UpdatesV2({
  initialWorkspace,
  artifacts,
  manifests,
}: UpdatesV2Props) {
  const router = useRouter();
  const utils = api.useUtils();
  const workspaceQuery = api.update.profilesAndGroupsWorkspace.useQuery(
    undefined,
    { initialData: initialWorkspace, refetchOnWindowFocus: false },
  );
  const workspace = workspaceQuery.data ?? initialWorkspace;

  const [editing, setEditing] = useState<EditingProfile | null>(null);
  const [rolloutGroup, setRolloutGroup] = useState<GroupItem | null>(null);

  const saveProfile = api.update.saveRolloutProfile.useMutation();
  const deleteProfile = api.update.deleteRolloutProfile.useMutation();
  const saveGroup = api.update.saveRouterGroup.useMutation();
  const assignRouters = api.update.assignRoutersToGroup.useMutation();
  const queueRollout = api.update.queueGroupProfileRollout.useMutation();

  const busy =
    saveProfile.isPending ||
    deleteProfile.isPending ||
    saveGroup.isPending ||
    assignRouters.isPending ||
    queueRollout.isPending;

  const invalidate = async () => {
    await Promise.all([
      utils.update.profilesAndGroupsWorkspace.invalidate(),
      utils.fleet.monitoring.invalidate(),
    ]);
    router.refresh();
  };

  const openNewProfile = () =>
    setEditing({
      profileId: null,
      name: "",
      description: "",
      config: createDraftFixture(),
    });

  const openEditProfile = (profile: ProfileItem) =>
    setEditing({
      profileId: profile.id,
      name: profile.name,
      description: profile.description ?? "",
      config: passwallDesiredConfigSchema.parse(profile.rolloutConfig),
    });

  const handleSaveProfile = async () => {
    if (!editing || !editing.name.trim()) {
      toast.error("Укажите название профиля");
      return;
    }
    try {
      await saveProfile.mutateAsync({
        profileId: editing.profileId ?? undefined,
        name: editing.name.trim(),
        description: editing.description.trim() || undefined,
        rolloutConfig: editing.config,
      });
      await invalidate();
      setEditing(null);
      toast.success("Профиль сохранён");
    } catch (error) {
      toast.error("Не удалось сохранить профиль", {
        description: errorMessage(error),
      });
    }
  };

  const handleDeleteProfile = async (profile: ProfileItem) => {
    if (!window.confirm(`Удалить профиль «${profile.name}»?`)) {
      return;
    }
    try {
      await deleteProfile.mutateAsync({ profileId: profile.id });
      await invalidate();
      toast.success("Профиль удалён");
    } catch (error) {
      toast.error("Не удалось удалить профиль", {
        description: errorMessage(error),
      });
    }
  };

  const handleCreateGroup = async () => {
    const name = window.prompt("Название новой группы");
    if (!name?.trim()) {
      return;
    }
    try {
      await saveGroup.mutateAsync({ name: name.trim() });
      await invalidate();
      toast.success("Группа создана");
    } catch (error) {
      toast.error("Не удалось создать группу", {
        description: errorMessage(error),
      });
    }
  };

  const handleGroupProfile = async (group: GroupItem, profileId: string) => {
    try {
      await saveGroup.mutateAsync({
        groupId: group.id,
        name: group.name,
        description: group.description ?? undefined,
        rolloutProfileId: profileId === UNASSIGNED ? null : profileId,
      });
      await invalidate();
      toast.success("Профиль группы обновлён");
    } catch (error) {
      toast.error("Не удалось обновить группу", {
        description: errorMessage(error),
      });
    }
  };

  const handleAssignRouter = async (routerId: string, groupId: string) => {
    try {
      await assignRouters.mutateAsync({
        routerIds: [routerId],
        groupId: groupId === UNASSIGNED ? null : groupId,
      });
      await invalidate();
      toast.success("Роутер переназначен");
    } catch (error) {
      toast.error("Не удалось переназначить роутер", {
        description: errorMessage(error),
      });
    }
  };

  const handleRollout = async (mode: "draft_only" | "queue_apply") => {
    if (!rolloutGroup) {
      return;
    }
    try {
      await queueRollout.mutateAsync({ groupId: rolloutGroup.id, mode });
      await invalidate();
      setRolloutGroup(null);
      toast.success(
        mode === "queue_apply"
          ? "Раскатка с применением поставлена в очередь"
          : "Черновики профиля созданы для группы",
      );
    } catch (error) {
      toast.error("Не удалось раскатать", { description: errorMessage(error) });
    }
  };

  return (
    <section className="mx-auto w-full max-w-6xl space-y-4">
      <header className="space-y-1">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
          Обновления
        </p>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">
          Профили и раскатка
        </h1>
        <p className="text-sm text-muted-foreground">
          Соберите профиль визуально, назначьте группе роутеров и раскатайте —
          без JSON.
        </p>
      </header>

      {/* Профили */}
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3 pb-3">
          <div className="space-y-1.5">
            <CardTitle className="flex items-center gap-2 text-base">
              <Layers className="h-4 w-4" strokeWidth={1.75} />
              Профили
            </CardTitle>
            <CardDescription>
              Готовые конфигурации PassWall для раскатки на группы.
            </CardDescription>
          </div>
          <Button
            size="sm"
            variant="outline"
            className="shrink-0"
            onClick={openNewProfile}
            disabled={busy}
          >
            <Plus className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
            Профиль
          </Button>
        </CardHeader>
        <CardContent className="space-y-2">
          {workspace.profiles.length === 0 ? (
            <EmptyState
              icon={Layers}
              title="Профилей пока нет"
              description="Создайте первый профиль, чтобы раскатывать одинаковую конфигурацию на парк."
            />
          ) : (
            workspace.profiles.map((profile) => (
              <div
                key={profile.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border/50 bg-card/40 px-3 py-2.5"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground">
                    {profile.name}
                  </p>
                  {profile.description ? (
                    <p className="truncate text-xs text-muted-foreground">
                      {profile.description}
                    </p>
                  ) : null}
                  <div className="mt-1 flex flex-wrap gap-1.5 text-[11px] text-muted-foreground">
                    <ToneBadge tone="neutral">
                      {profile.managedNodeCount} узлов
                    </ToneBadge>
                    <ToneBadge tone="neutral">
                      {profile.shuntRuleCount} правил
                    </ToneBadge>
                    <ToneBadge tone={profile.groupCount > 0 ? "info" : "neutral"}>
                      {profile.groupCount} групп
                    </ToneBadge>
                  </div>
                </div>
                <div className="flex gap-1">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => openEditProfile(profile)}
                    disabled={busy}
                  >
                    <Pencil className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
                    Редактировать
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-8 w-8 text-destructive hover:text-destructive"
                    onClick={() => handleDeleteProfile(profile)}
                    disabled={busy}
                    aria-label="Удалить профиль"
                  >
                    <Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />
                  </Button>
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      {/* Группы */}
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3 pb-3">
          <div className="space-y-1.5">
            <CardTitle className="flex items-center gap-2 text-base">
              <Users className="h-4 w-4" strokeWidth={1.75} />
              Группы
            </CardTitle>
            <CardDescription>
              Каждой группе — свой профиль. Раскатка применяет профиль ко всем
              роутерам группы.
            </CardDescription>
          </div>
          <Button
            size="sm"
            variant="outline"
            className="shrink-0"
            onClick={handleCreateGroup}
            disabled={busy}
          >
            <Plus className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
            Группа
          </Button>
        </CardHeader>
        <CardContent className="space-y-2">
          {workspace.groups.length === 0 ? (
            <EmptyState
              icon={Users}
              title="Групп пока нет"
              description="Создайте группу и назначьте ей профиль."
            />
          ) : (
            workspace.groups.map((group) => (
              <div
                key={group.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border/50 bg-card/40 px-3 py-2.5"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground">
                    {group.name}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {group.routerCount} роутеров
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Select
                    value={group.rolloutProfileId ?? UNASSIGNED}
                    onValueChange={(value) => handleGroupProfile(group, value)}
                    disabled={busy}
                  >
                    <SelectTrigger className="h-9 w-48">
                      <SelectValue placeholder="Профиль" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={UNASSIGNED}>— без профиля —</SelectItem>
                      {workspace.profiles.map((profile) => (
                        <SelectItem key={profile.id} value={profile.id}>
                          {profile.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button
                    size="sm"
                    onClick={() => setRolloutGroup(group)}
                    disabled={
                      busy ||
                      !group.rolloutProfileId ||
                      group.routerCount === 0
                    }
                  >
                    <Rocket className="mr-1 h-3.5 w-3.5" strokeWidth={1.75} />
                    Раскатать
                  </Button>
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      {/* Назначение роутеров */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Server className="h-4 w-4" strokeWidth={1.75} />
            Роутеры по группам
          </CardTitle>
          <CardDescription>
            Назначьте каждому роутеру группу — он получит её профиль при раскатке.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          {workspace.routers.length === 0 ? (
            <EmptyState
              icon={Server}
              title="Роутеров нет"
              description="Зарегистрируйте роутеры через enrollment."
            />
          ) : (
            workspace.routers.map((r) => (
              <div
                key={r.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border/50 bg-card/40 px-3 py-2.5"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground">
                    {r.displayName}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {r.hostname ?? r.deviceIdentifier}
                  </p>
                </div>
                <Select
                  value={r.rolloutGroupId ?? UNASSIGNED}
                  onValueChange={(value) => handleAssignRouter(r.id, value)}
                  disabled={busy}
                >
                  <SelectTrigger className="h-9 w-48">
                    <SelectValue placeholder="Группа" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={UNASSIGNED}>— без группы —</SelectItem>
                    {workspace.groups.map((group) => (
                      <SelectItem key={group.id} value={group.id}>
                        {group.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      {/* Каналы релизов — справка */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldCheck className="h-4 w-4" strokeWidth={1.75} />
            Каналы релизов
          </CardTitle>
          <CardDescription>
            Текущие опубликованные версии (справка). Обновление конкретного
            роутера — на его странице, вкладка «Обновления».
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-3">
          <ReleaseRef
            icon={CircuitBoard}
            lane="Controller"
            version={
              artifacts.find((a) => a.type === "controller")?.version ??
              "не опубликовано"
            }
          />
          <ReleaseRef
            icon={Boxes}
            lane="PassWall2"
            version={
              (
                artifacts.find((a) => a.type === "passwall_bundle") ??
                artifacts.find((a) => a.type === "passwall_package")
              )?.version ?? "не опубликовано"
            }
          />
          <ReleaseRef
            icon={ShieldCheck}
            lane="Firmware"
            version={manifests[0]?.version ?? "нет манифеста"}
          />
        </CardContent>
      </Card>

      <ProfileEditorSheet
        editing={editing}
        onClose={() => setEditing(null)}
        onChange={setEditing}
        onSave={handleSaveProfile}
        saving={saveProfile.isPending}
      />

      <AlertDialog
        open={rolloutGroup !== null}
        onOpenChange={(open) => !open && setRolloutGroup(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Раскатать на «{rolloutGroup?.name}»?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Профиль «{rolloutGroup?.rolloutProfileName}» уйдёт на{" "}
              {rolloutGroup?.routerCount} роутеров группы. «Черновики» только
              подготовят ревизии, «Применить» поставит apply в очередь.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter className="flex-col gap-2 sm:flex-row">
            <AlertDialogCancel>Отмена</AlertDialogCancel>
            <Button
              variant="outline"
              onClick={() => handleRollout("draft_only")}
              disabled={busy}
            >
              Только черновики
            </Button>
            <Button onClick={() => handleRollout("queue_apply")} disabled={busy}>
              Сохранить и применить
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function ReleaseRef({
  icon: Icon,
  lane,
  version,
}: {
  icon: typeof CircuitBoard;
  lane: string;
  version: string;
}) {
  return (
    <div className="rounded-md border border-border/40 bg-card/40 px-3 py-2.5">
      <p className="flex items-center gap-2 text-sm font-medium text-foreground">
        <Icon className="h-3.5 w-3.5 text-muted-foreground" strokeWidth={1.75} />
        {lane}
      </p>
      <code className="mt-1 block truncate rounded bg-muted/40 px-1.5 py-0.5 font-mono text-xs text-foreground">
        {version}
      </code>
    </div>
  );
}

function ProfileEditorSheet({
  editing,
  onClose,
  onChange,
  onSave,
  saving,
}: {
  editing: EditingProfile | null;
  onClose: () => void;
  onChange: (next: EditingProfile) => void;
  onSave: () => void;
  saving: boolean;
}) {
  return (
    <Sheet open={editing !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 overflow-y-auto p-0 sm:max-w-2xl"
      >
        {editing ? (
          <>
            <SheetHeader className="border-b border-border/40 p-4">
              <SheetTitle>
                {editing.profileId ? "Профиль" : "Новый профиль"}
              </SheetTitle>
              <SheetDescription>
                Визуальная конфигурация PassWall. Сохранится как профиль для
                раскатки.
              </SheetDescription>
            </SheetHeader>
            <div className="flex-1 space-y-4 p-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="profile-name" className="text-sm font-medium">
                    Название
                  </Label>
                  <Input
                    id="profile-name"
                    value={editing.name}
                    onChange={(e) =>
                      onChange({ ...editing, name: e.target.value })
                    }
                    placeholder="напр. RU-baseline"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label
                    htmlFor="profile-desc"
                    className="text-sm font-medium"
                  >
                    Описание
                  </Label>
                  <Textarea
                    id="profile-desc"
                    rows={1}
                    value={editing.description}
                    onChange={(e) =>
                      onChange({ ...editing, description: e.target.value })
                    }
                    placeholder="необязательно"
                  />
                </div>
              </div>
              <ConfigEditor
                config={editing.config}
                onChange={(config) => onChange({ ...editing, config })}
                disabled={saving}
              />
            </div>
            <SheetFooter className="border-t border-border/40 p-4">
              <Button variant="outline" onClick={onClose} disabled={saving}>
                Отмена
              </Button>
              <Button onClick={onSave} disabled={saving}>
                Сохранить профиль
              </Button>
            </SheetFooter>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
