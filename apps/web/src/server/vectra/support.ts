import type { SupportState } from "@vectra/contracts";

type SupportInput = {
  boardName?: string | null;
  target?: string | null;
  architecture?: string | null;
  openwrtRelease?: string | null;
  layoutFamily?: string | null;
};

type InventorySupportPayload = SupportInput & {
  layoutFamily?: unknown;
};

type EffectiveSupportInput = {
  router: SupportInput;
  inventory?: InventorySupportPayload | null;
};

export type SupportDescriptor = {
  state: SupportState;
  title: string;
  reason: string;
};

export function describeRouterSupport(input: SupportInput): SupportDescriptor {
  const boardName = input.boardName?.trim().toLowerCase() ?? "";
  const target = input.target?.trim().toLowerCase() ?? "";
  const architecture = input.architecture?.trim().toLowerCase() ?? "";
  const openwrtRelease = input.openwrtRelease?.trim().toLowerCase() ?? "";
  const layoutFamily = input.layoutFamily?.trim().toLowerCase() ?? "";

  const isFilogicFamily =
    target === "mediatek/filogic" && architecture === "aarch64_cortex-a53";
  const isCertifiedAx3000t =
    boardName === "xiaomi,mi-router-ax3000t" &&
    isFilogicFamily &&
    openwrtRelease.startsWith("24.10") &&
    (layoutFamily === "" || layoutFamily === "stock" || layoutFamily === "stock-layout");

  if (isCertifiedAx3000t) {
    return {
      state: "certified",
      title: "Сертифицировано",
      reason:
        "Поддерживается стабильный контур для Xiaomi AX3000T stock-layout на OpenWrt 24.10.x.",
    };
  }

  if (isFilogicFamily) {
    return {
      state: "pilot",
      title: "Пилот",
      reason:
        "Платформа идёт в пилотном контуре: полный операторский surface разрешён, но stable-гарантий по board/layout пока нет, и решение остаётся на операторе.",
    };
  }

  return {
    state: "blocked",
    title: "Заблокировано",
    reason:
      "Для этой платы пока нет сертифицированного stable-профиля, поэтому destructive-действия запрещены.",
  };
}

export function canRunDestructiveAction(state: SupportState) {
  return state === "certified" || state === "pilot";
}

export function canRunUpdateAction(state: SupportState) {
  return canRunDestructiveAction(state);
}

export function describeEffectiveRouterSupport(input: EffectiveSupportInput) {
  const inventory = input.inventory;
  const layoutFamily =
    typeof inventory?.layoutFamily === "string" ? inventory.layoutFamily : null;

  return describeRouterSupport({
    boardName: inventory?.boardName ?? input.router.boardName,
    layoutFamily,
    target: inventory?.target ?? input.router.target,
    architecture: inventory?.architecture ?? input.router.architecture,
    openwrtRelease: inventory?.openwrtRelease ?? input.router.openwrtRelease,
  });
}

export function evaluateRouterSupport(
  boardName?: string | null,
  layoutFamily?: string | null,
  target = "mediatek/filogic",
  architecture = "aarch64_cortex-a53",
  openwrtRelease = "24.10"
) {
  const descriptor = describeRouterSupport({
    boardName,
    layoutFamily,
    target,
    architecture,
    openwrtRelease,
  });

  return {
    state: descriptor.state,
    label: descriptor.title,
    message: descriptor.reason,
    destructiveActionsAllowed: canRunDestructiveAction(descriptor.state),
    updateActionsAllowed: canRunUpdateAction(descriptor.state),
  };
}
