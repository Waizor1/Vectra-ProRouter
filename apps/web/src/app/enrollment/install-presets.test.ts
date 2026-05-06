import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import {
  AX3000T_OPTIONAL_MIRRORED_PACKAGES,
  AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES,
  AX3000T_BASELINE_PATH,
  AX3000T_BOOTSTRAP_PATH,
  AX3000T_SHUNT_REBIND_PATH,
  AX3000T_REQUIRED_MIRRORED_PACKAGES,
  AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER,
  ax3000tEnrollmentPreset,
  buildAx3000tBaselineUrl,
  buildAx3000tBootstrapCommand,
  buildAx3000tPasswallMirrorUrl,
  buildAx3000tBootstrapScript,
  buildAx3000tBootstrapScriptUrl,
  buildAx3000tFeedUrl,
  buildAx3000tShuntRebindCommand,
  buildAx3000tShuntRebindScript,
  buildAx3000tShuntRebindScriptUrl,
  classifyFilogicPasswallInstallState,
  DEFAULT_PASSWALL2_RELEASE_TAG,
  planAx3000tManagedPackageOperations,
} from "~/app/enrollment/install-presets";

const baselinePath = fileURLToPath(
  new URL("../../../public/install/ax3000t-passwall2-baseline.uci", import.meta.url),
);
const controlFixturePath = fileURLToPath(
  new URL(
    "./__fixtures__/luci-app-passwall2-26.4.5-r1.control.txt",
    import.meta.url,
  ),
);
const bundleFixturePath = fileURLToPath(
  new URL(
    "./__fixtures__/passwall-bundle-26.4.5-1-aarch64_cortex-a53.txt",
    import.meta.url,
  ),
);
const manifestFixturePath = fileURLToPath(
  new URL(
    "./__fixtures__/passwall-mirror-manifest-26.4.5-1-aarch64_cortex-a53.json",
    import.meta.url,
  ),
);

function parseDependencyList(controlFixture: string) {
  const dependsLine = controlFixture
    .split(/\r?\n/)
    .find((line) => line.startsWith("Depends: "));

  if (!dependsLine) {
    throw new Error("Depends line not found in control fixture");
  }

  return dependsLine
    .replace("Depends: ", "")
    .split(",")
    .map((dependency) => dependency.trim())
    .filter(Boolean);
}

function parseBundleListing(bundleFixture: string) {
  return bundleFixture
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function parseManifestFixture() {
  return JSON.parse(readFileSync(manifestFixturePath, "utf8")) as {
    requiredPackages: typeof AX3000T_REQUIRED_MIRRORED_PACKAGES;
    optionalPackages: typeof AX3000T_OPTIONAL_MIRRORED_PACKAGES;
  };
}

describe("enrollment install preset", () => {
  it("builds stable URLs for the AX3000T enrollment assets", () => {
    expect(buildAx3000tBaselineUrl("https://router.vectra-pro.net")).toBe(
      `https://router.vectra-pro.net${AX3000T_BASELINE_PATH}`,
    );
    expect(buildAx3000tBootstrapScriptUrl("https://router.vectra-pro.net")).toBe(
      `https://router.vectra-pro.net${AX3000T_BOOTSTRAP_PATH}`,
    );
    expect(
      buildAx3000tShuntRebindScriptUrl("https://router.vectra-pro.net"),
    ).toBe(`https://router.vectra-pro.net${AX3000T_SHUNT_REBIND_PATH}`);
    expect(buildAx3000tFeedUrl("https://api.vectra-pro.net/artifacts")).toBe(
      `https://api.vectra-pro.net/artifacts/openwrt/stable/${ax3000tEnrollmentPreset.architecture}/`,
    );
    expect(buildAx3000tPasswallMirrorUrl("https://api.vectra-pro.net/artifacts")).toBe(
      `https://api.vectra-pro.net/artifacts/bootstrap/passwall2/${DEFAULT_PASSWALL2_RELEASE_TAG}/${ax3000tEnrollmentPreset.architecture}/`,
    );
  });

  it("renders a bootstrap command and script aligned to the live control-plane URLs", () => {
    const controlDomain = "https://router.vectra-pro.net";
    const routerApiBase = "https://api.vectra-pro.net";
    const artifactBase = "https://api.vectra-pro.net/artifacts";

    expect(buildAx3000tBootstrapCommand(controlDomain)).toContain(
      `https://router.vectra-pro.net${AX3000T_BOOTSTRAP_PATH}`,
    );
    expect(buildAx3000tShuntRebindCommand(controlDomain)).toContain(
      `https://router.vectra-pro.net${AX3000T_SHUNT_REBIND_PATH}`,
    );

    const script = buildAx3000tBootstrapScript({
      controlDomain,
      routerApiBase,
      artifactBase,
    });

    expect(script).toContain(`EXPECTED_ARCH='${ax3000tEnrollmentPreset.architecture}'`);
    expect(script).toContain("EXPECTED_TARGET='mediatek/filogic'");
    expect(script).toContain(
      `ARCH="$(awk -F"'" '/^DISTRIB_ARCH=/{print $2; exit}' /etc/openwrt_release 2>/dev/null || true)"`,
    );
    expect(script).toContain(
      `TARGET="$(awk -F"'" '/^DISTRIB_TARGET=/{print $2; exit}' /etc/openwrt_release 2>/dev/null || true)"`,
    );
    expect(script).toContain("ensure_opkg_architecture() {");
    expect(script).toContain("ensure_arch_line \"$arch_name\" 100");
    expect(script).toContain(
      "[ -f /etc/opkg/arch.conf ] && cp /etc/opkg/arch.conf \"$BACKUP_DIR/opkg-arch.conf\" || true",
    );
    expect(script).toContain("ensure_opkg_architecture \"$ARCH\"");
    expect(script).toContain(
      "opkg arch.conf: добавил архитектуру $name",
    );
    expect(script).toContain(`CONTROL_URL='${routerApiBase}'`);
    expect(script).toContain(`PASSWALL_RELEASE_TAG='${DEFAULT_PASSWALL2_RELEASE_TAG}'`);
    expect(script).toContain(
      `PASSWALL_MIRROR_URL='https://api.vectra-pro.net/artifacts/bootstrap/passwall2/${DEFAULT_PASSWALL2_RELEASE_TAG}/${ax3000tEnrollmentPreset.architecture}/'`,
    );
    expect(script).toContain("STAGE_MARGIN_BYTES='1048576'");
    expect(script).toContain("WORKDIR='/tmp/vectra-bootstrap-work'");
    expect(script).toContain("OPKG_TMP_DIR=\"$WORKDIR/opkg-tmp\"");
    expect(script).toContain(
      `REQUIRED_MANAGED_PACKAGES='${AX3000T_REQUIRED_MIRRORED_INSTALL_ORDER.join(" ")}'`,
    );
    expect(script).toContain(
      `BASELINE_URL='https://router.vectra-pro.net${AX3000T_BASELINE_PATH}'`,
    );
    expect(script).toContain("run_opkg() {");
    expect(script).toContain("if ! run_opkg update; then");
    expect(script).toContain("package-specific preflight проверит нужные пакеты отдельно");
    expect(script).toContain("run_preflight_checks() {");
    expect(script).toContain("detect_bootstrap_classification() {");
    expect(script).toContain("simulate_overlay_step() {");
    expect(script).toContain("require_remote_ipk tcping");
    expect(script).toContain(
      `OPENWRT_FEED_PREREQS='${AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES.join(" ")}'`,
    );
    expect(script).toContain("require_feed_packages $OPENWRT_FEED_PREREQS");
    expect(script).toContain(
      "require_feed_packages_with_storage $REQUIRED_RUNTIME_PACKAGES",
    );
    const feedPrereqFunctionStart = script.indexOf("require_feed_package() {");
    const feedPrereqFunctionEnd = script.indexOf(
      "require_feed_package_storage_metadata() {",
    );
    const feedPrereqFunction = script.slice(
      feedPrereqFunctionStart,
      feedPrereqFunctionEnd,
    );
    expect(script).toContain("openwrt_base_prereq_present() {");
    expect(script).toContain("[ -e /lib/libc.so ] || ls /lib/ld-musl-*.so.1");
    expect(feedPrereqFunction).toContain(
      'if openwrt_base_prereq_present "$pkg"; then',
    );
    expect(
      feedPrereqFunction.indexOf('openwrt_base_prereq_present "$pkg"'),
    ).toBeLessThan(feedPrereqFunction.indexOf('package_installed "$pkg"'));
    expect(feedPrereqFunction).toContain('if package_installed "$pkg"; then');
    expect(feedPrereqFunction).toContain("    return 0");
    expect(feedPrereqFunction.indexOf('package_installed "$pkg"')).toBeLessThan(
      feedPrereqFunction.indexOf('package_available "$pkg"'),
    );
    expect(feedPrereqFunction).toContain(
      "Обязательный пакет не установлен и не найден в доступных OpenWrt feeds: $pkg",
    );
    expect(script).toContain("require_vectra_package vectra-controller-agent");
    expect(script).not.toContain(
      'Не удалось определить Installed-Size для пакета Vectra $pkg. Storage-aware preflight остановлен.',
    );
    expect(script).toContain("whatdepends_packages() {");
    expect(script).toContain(
      "awk '/^[[:space:]]+[[:alnum:]_.+-]+([[:space:]]|$)/ { print $1 }'",
    );
    expect(script).toContain('whatdepends_packages "$pkg" | grep -qx \'luci-app-passwall2\'');
    expect(script).not.toContain(
      `opkg whatdepends "$pkg" 2>/dev/null | awk '/^[[:alnum:]_.+-]+[[:space:]]/ { print $1 }'`,
    );
    expect(script).toContain('value="$(feed_package_field "$1" "$2" || true)"');
    expect(script).toContain('package_index_field "$1" "$2"');
    expect(script).toContain("feed_package_storage_budget_bytes() {");
    expect(script).toContain('feed_package_download_size_bytes "$pkg"');
    expect(script).toContain("require_feed_package_storage_metadata() {");
    expect(script).toContain("vectra_package_storage_budget_bytes() {");
    expect(script).toContain('vectra_package_download_size_bytes "$pkg"');
    expect(script).toContain("download_and_install_managed_ipk() {");
    expect(script).toContain("append_preserved_passwall_sections() {");
    expect(script).toContain("apply_passwall_baseline() {");
    expect(script).toContain("refresh_passwall_managed_stack() {");
    expect(script).toContain("install_controller_packages() {");
    expect(script).toContain("install_dnsmasq_full()");
    expect(script).toContain("passwall_runtime_ready_for_reuse() {");
    expect(script).toContain("passwall_config_exists() {");
    expect(script).toContain("REUSE_EXISTING_PASSWALL_STACK='0'");
    expect(script).toContain("CONTROLLER_ONLY_BOOTSTRAP='0'");
    expect(script).toContain("mark_controller_only_bootstrap() {");
    expect(script).toContain("detect_fresh_passwall_overlay_shortage() {");
    expect(script).toContain("is_passwall_bootstrap_package() {");
    expect(script).toContain(
      "Продолжаю controller-only bootstrap: ставлю контроллер Vectra без PassWall2/Xray.",
    );
    expect(script).toContain(
      "PassWall2/Xray обновим позже из панели Vectra через контроллер после первого check-in.",
    );
    expect(script).toContain(
      "if [ \"$CONTROLLER_ONLY_BOOTSTRAP\" != '1' ]; then",
    );
    expect(script).toContain(
      "Controller-only bootstrap: пропускаю установку/настройку PassWall2, baseline, подписки и запуск сервиса.",
    );
    expect(script).toContain(
      "PassWall2/Xray не менялись во время установки из-за малого /overlay",
    );
    expect(script).toContain("Reuse lane: существующий PassWall2 уже выглядит рабочим");
    expect(script).toContain("install_missing_managed_ipk() {");
    expect(script).toContain("refresh_managed_package_package_based() {");
    expect(script).toContain("сначала пробую in-place package install");
    expect(script).toContain("passwall_component_updater_available() {");
    expect(script).toContain("refresh_passwall_component_via_builtin_updater() {");
    expect(script).toContain("refresh_reuse_lane_heavy_component() {");
    expect(script).toContain("refresh_bootstrap_xray_runtime_via_builtin_updater() {");
    expect(script).toContain("package_upgrade_impossible_for_overlay() {");
    expect(script).toContain("extract_xray_binary_from_ipk() {");
    expect(script).toContain("refresh_xray_binary_from_ipk_payload() {");
    expect(script).toContain("refresh_reuse_lane_passwall_state() {");
    expect(script).toContain("luci.passwall2.api");
    expect(script).toContain("local download_size_kb = tonumber(data.size or 0)");
    expect(script).toContain("download_size_kb = download_size_kb / 1024");
    expect(script).toContain(
      "local download = api.to_download(component, data.browser_download_url, download_size_kb)",
    );
    expect(script).toContain("отсутствует, доустанавливаю");
    expect(script).toContain('cp /etc/config/dhcp "$BACKUP_DIR/dhcp"');
    expect(script).toContain('rm -f /etc/config/dhcp /etc/config/dhcp-opkg');
    expect(script).toContain('/etc/init.d/dnsmasq restart >/dev/null 2>&1 || true');
    expect(script).toContain("install_required_openwrt_packages kmod-nft-socket kmod-nft-tproxy");
    expect(script).toContain(
      "download_and_install_managed_ipk \"$pkg\"",
    );
    expect(script).toContain(
      "[luci-app-passwall2] устанавливаю $(managed_package_field luci-app-passwall2 version) последним шагом...",
    );
    expect(script).toContain(
      'if ! uci -c "$WORKDIR/uci" show passwall2 >/dev/null 2>"$WORKDIR/baseline-validate.stderr"; then',
    );
    expect(script).toContain(
      "Не удалось проверить baseline PassWall2 перед применением в /etc/config/passwall2",
    );
    expect(script).toContain(
      'append_preserved_passwall_sections "$BACKUP_DIR/passwall2" "$WORKDIR/uci/passwall2"',
    );
    expect(script).toContain("refresh_existing_subscriptions");
    expect(script).toContain("rebind_myshunt_from_remarks");
    expect(script).toContain(
      "apply_shunt_slot 'default_node' '🇩🇪⚡Германия YouTube 🚫Ad🚫'",
    );
    expect(script).toContain("passwall_bootstrap_ready_to_start() {");
    expect(script).toContain(
      "shunt Default/default_node='$default_node' не привязан к серверу",
    );
    expect(script).toContain(
      "  log 'Устанавливаю пакеты контроллера Vectra до запуска PassWall2...'\n  install_controller_packages\n  start_passwall_after_bootstrap",
    );
    expect(script).toContain("normalize_remark() {");
    expect(script).toContain("repair broken PassWall config");
    expect(script).toContain("пытаюсь salvage подписки и ноды из raw backup");
    expect(script).toContain('cp "$WORKDIR/uci/passwall2" "$WORKDIR/uci/passwall2.pristine"');
    expect(script).toContain('пытаюсь salvage подписки и ноды из raw backup');
    expect(script).toContain('Raw salvage из повреждённого passwall2 успешно добавил recoverable подписки и ноды.');
    expect(script).toContain('Raw salvage из повреждённого passwall2 не прошёл валидацию; продолжаю с чистым baseline.');
    expect(script).toContain("Shunt-автопривязка неполная; сохраняю прежний рабочий узел");
    expect(script).toContain("install_missing_managed_ipk \"$pkg\"");
    expect(script).toContain("Reuse lane: довожу PassWall app и тяжёлые компоненты до результата адаптивным update-path...");
    expect(script).toContain("встроенный PassWall App Update");
    expect(script).toContain("Проверяю, нужен ли runtime-апдейт Xray через встроенный PassWall App Update");
    expect(script).toContain("runtime доведён через встроенный PassWall App Update");
    expect(script).toContain("переключаюсь на package-refresh fallback");
    expect(script).toContain("package-refresh физически не помещается в overlay; сохраняю low-storage runtime-convergence как допустимый результат.");
    expect(script).toContain("runtime binary обновлён напрямую из target IPK payload");
    expect(script).toContain(
      "Сохраняю существующие подписки и ноды PassWall2 из текущего конфига...",
    );
    expect(script).toContain(
      "Не удалось объединить baseline PassWall2 с существующими подписками и нодами",
    );
    expect(script).toContain("Bootstrap прерван до любых изменений.");
    expect(script).toContain("run_opkg install \"$pkg\" || {");
    expect(script).toContain("После первого check-in откройте веб-панель");
    expect(script).toContain(
      'log "3. После первого check-in откройте веб-панель и примите импорт как эталон"',
    );
    expect(script).not.toContain("passwall_packages.zip");
    expect(script).not.toContain("unzip -");
    expect(script).not.toContain("fetch_vectra_package");
    expect(script).not.toContain("download_and_install_ipk()");
    expect(script).not.toContain(
      "REQUIRED_MANAGED_PACKAGES='xray-core v2ray-geoip v2ray-geosite geoview chinadns-ng tcping sing-box hysteria luci-app-passwall2'",
    );
    expect(script.lastIndexOf("run_preflight_checks")).toBeLessThan(
      script.indexOf("log 'Подготавливаю dnsmasq-full для PassWall2...'"),
    );
  });

  it("preserves existing PassWall subscriptions and imported nodes during baseline apply", () => {
    const script = buildAx3000tBootstrapScript({
      controlDomain: "https://router.vectra-pro.net",
      routerApiBase: "https://api.vectra-pro.net",
      artifactBase: "https://api.vectra-pro.net/artifacts",
    });

    expect(script).toContain("awk -v allowed_types='nodes subscribe_list' -f - \"$source\" >> \"$destination\" <<'AWK'");
    expect(script).toContain(
      'capture = is_allowed(section_type) && !(section_type == "nodes" && section_name == "myshunt")',
    );
    expect(script).toContain(
      'if ! uci -c "$WORKDIR/uci" show passwall2 >/dev/null 2>"$WORKDIR/preserved-passwall-validate.stderr"; then',
    );
  });

  it("renders a myshunt rebind helper that restores slots by remark", () => {
    const controlDomain = "https://router.vectra-pro.net";
    const script = buildAx3000tShuntRebindScript({ controlDomain });

    expect(script).toContain("SHUNT_ID='myshunt'");
    expect(script).toContain(`PANEL_URL='${controlDomain}'`);
    expect(script).toContain("find_unique_node_by_remark()");
    expect(script).toContain("count_nodes_by_remark()");
    expect(script).toContain(
      "for id in $(uci show passwall2 2>/dev/null | awk -F'[.=]' '/=nodes$/{print $2}')",
    );
    expect(script).toContain("apply_slot 'WorldProxy' '🇩🇪⚡Германия YouTube 🚫Ad🚫'");
    expect(script).toContain("apply_slot 'YouTube' '🇷🇺⚡Россия YouTube 🚫Ad🚫'");
    expect(script).toContain("apply_slot 'Special' '🇫🇮 ⚡⚡ Финляндия Xhttp Gaming'");
    expect(script).toContain("apply_slot 'Tiktok' '🇧🇾 Беларусь'");
    expect(script).toContain("apply_slot 'default_node' '🇩🇪⚡Германия YouTube 🚫Ad🚫'");
    expect(script).toContain('uci set "passwall2.$SHUNT_ID.$slot=$node_id"');
    expect(script).toContain("найдено несколько нод с remark");
    expect(script).toContain("Привязки myshunt обновлены и PassWall2 перезапущен.");
  });

  it("covers luci-app-passwall2 dependencies with mirrored packages or OpenWrt feed allowlist", () => {
    const controlFixture = readFileSync(controlFixturePath, "utf8");
    const coveredDependencies = new Set<string>([
      ...AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES,
      ...AX3000T_REQUIRED_MIRRORED_PACKAGES.map(({ name }) => name),
    ]);
    const missingDependencies = parseDependencyList(controlFixture).filter(
      (dependency) => !coveredDependencies.has(dependency),
    );

    expect(missingDependencies).toEqual([]);
  });

  it("keeps every required mirrored package present in the checked-in upstream bundle fixture", () => {
    const bundleFixture = readFileSync(bundleFixturePath, "utf8");
    const bundleEntries = new Set(parseBundleListing(bundleFixture));
    const missingEntries = AX3000T_REQUIRED_MIRRORED_PACKAGES.filter(
      ({ name, filename }) =>
        name !== "luci-app-passwall2" && !bundleEntries.has(filename),
    ).map(({ filename }) => filename);

    expect(missingEntries).toEqual([]);
  });

  it("keeps repo-side mirrored package metadata aligned with the checked-in manifest fixture", () => {
    const manifest = parseManifestFixture();

    expect(AX3000T_REQUIRED_MIRRORED_PACKAGES).toEqual(manifest.requiredPackages);
    expect(AX3000T_OPTIONAL_MIRRORED_PACKAGES).toEqual(manifest.optionalPackages);
  });

  it("plans reclaim-first replacement for a low-overlay managed upgrade", () => {
    const plan = planAx3000tManagedPackageOperations({
      overlayFreeBytes: 6_000_000,
      stageFreeBytes: 20_000_000,
      packageStates: {
        "luci-app-passwall2": {
          installed: true,
          version: "26.4.5-r1",
          installedSizeBytes: 1_300_480,
        },
        "xray-core": {
          installed: true,
          version: "26.2.6-r1",
          installedSizeBytes: 30_320_640,
        },
        geoview: {
          installed: true,
          version: "0.2.5-r1",
          installedSizeBytes: 7_208_960,
        },
        "v2ray-geoip": {
          installed: true,
          version: "202603260032.1",
          installedSizeBytes: 19_773_440,
        },
        "v2ray-geosite": {
          installed: true,
          version: "202603292224.1",
          installedSizeBytes: 10_536_960,
        },
        tcping: {
          installed: true,
          version: "0.3-r1",
          installedSizeBytes: 71_680,
        },
        "chinadns-ng": {
          installed: true,
          version: "2025.08.09-r1",
          installedSizeBytes: 522_240,
        },
      },
    });

    expect(plan.storageCheck.ok).toBe(true);
    expect(plan.classification).toBe("upgrade existing PassWall stack");
    expect(plan.passwallAppRemovalRequired).toBe(true);
    expect(
      plan.steps.filter((step) => step.action !== "skip").map((step) => step.packageName),
    ).toEqual(["xray-core", "luci-app-passwall2"]);
  });

  it("skips current-version managed packages", () => {
    const packageStates = Object.fromEntries(
      AX3000T_REQUIRED_MIRRORED_PACKAGES.map((pkg) => [
        pkg.name,
        {
          installed: true,
          version: pkg.version,
          installedSizeBytes: pkg.installedSizeBytes,
        },
      ]),
    );

    const plan = planAx3000tManagedPackageOperations({
      overlayFreeBytes: 80_000_000,
      stageFreeBytes: 80_000_000,
      packageStates,
    });

    expect(plan.storageCheck.ok).toBe(true);
    expect(plan.passwallAppRemovalRequired).toBe(false);
    expect(plan.steps.every((step) => step.action === "skip")).toBe(true);
  });

  it("fails early with exact stage diagnostics when staging-space is too small", () => {
    const plan = planAx3000tManagedPackageOperations({
      overlayFreeBytes: 80_000_000,
      stageFreeBytes: 1_000_000,
      packageStates: {},
    });

    expect(plan.storageCheck.ok).toBe(false);
    expect(plan.storageCheck.reason).toBe("stage");
    expect(plan.storageCheck.blockingPackageName).toBe("xray-core");
    expect(plan.storageCheck.message).toContain("staging-space");
    expect(plan.storageCheck.message).toContain("xray-core");
  });

  it("marks controller-only fallback when a fresh install cannot fit PassWall", () => {
    const plan = planAx3000tManagedPackageOperations({
      overlayFreeBytes: 5_000_000,
      stageFreeBytes: 80_000_000,
      packageStates: {},
    });

    expect(plan.controllerOnlyFallback).toBe(true);
    expect(plan.storageCheck.ok).toBe(false);
    expect(plan.storageCheck.reason).toBe("overlay");
    expect(plan.storageCheck.blockingPackageName).toBe("xray-core");
    expect(plan.storageCheck.message).toContain("/overlay");
    expect(plan.storageCheck.message).toContain("xray-core");
  });

  it("classifies broken parseable state as repair-broken-config before other lanes", () => {
    expect(
      classifyFilogicPasswallInstallState({
        hasAnyManagedPackagesInstalled: true,
        hasOutdatedManagedPackages: false,
        hasMissingManagedPackages: false,
        hasPasswallConfigFile: true,
        passwallConfigParseable: false,
      }),
    ).toBe("repair broken PassWall config");
  });

  it("classifies empty Filogic state as fresh install", () => {
    expect(
      classifyFilogicPasswallInstallState({
        hasAnyManagedPackagesInstalled: false,
        hasOutdatedManagedPackages: false,
        hasMissingManagedPackages: true,
        hasPasswallConfigFile: false,
        passwallConfigParseable: false,
      }),
    ).toBe("fresh install");
  });

  it("classifies partially missing managed stack as repair drifted packages", () => {
    expect(
      classifyFilogicPasswallInstallState({
        hasAnyManagedPackagesInstalled: true,
        hasOutdatedManagedPackages: false,
        hasMissingManagedPackages: true,
        hasPasswallConfigFile: true,
        passwallConfigParseable: true,
      }),
    ).toBe("repair drifted managed packages");
  });

  it("blocks destructive reclaim when an external package depends on xray-core", () => {
    const plan = planAx3000tManagedPackageOperations({
      overlayFreeBytes: 80_000_000,
      stageFreeBytes: 80_000_000,
      packageStates: {
        "xray-core": {
          installed: true,
          version: "26.2.6-r1",
          installedSizeBytes: 30_320_640,
          unexpectedDependents: ["custom-proxy-addon"],
        },
      },
    });

    expect(plan.storageCheck.ok).toBe(false);
    expect(plan.storageCheck.reason).toBe("unexpected_dependents");
    expect(plan.storageCheck.blockingPackageName).toBe("xray-core");
    expect(plan.storageCheck.message).toContain("custom-proxy-addon");
  });

  it("keeps the published baseline sanitized", () => {
    const baseline = readFileSync(baselinePath, "utf8");

    expect(baseline).toContain("config nodes 'myshunt'");
    expect(baseline).toContain("config shunt_rules 'WorldProxy'");
    expect(baseline).not.toContain("config subscribe_list");
    expect(baseline).not.toContain("option url ");
    expect(baseline).not.toContain("option password ");
    expect(baseline).not.toContain("option address ");
    expect(baseline).not.toContain("\\t");
    expect(baseline).not.toContain("\r");
    expect(baseline).toContain("option remarks 'Маршрутизатор BloopCat'");
    expect(baseline).toContain("option default_fakedns '0'");
    expect(baseline).toContain("option direct_fakedns '0'");
    expect(baseline).not.toContain("option default_fakedns '1'");
    expect(baseline).not.toContain("option direct_fakedns '1'");
  });
});
