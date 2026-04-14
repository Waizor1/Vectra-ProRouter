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
    expect(script).toContain(
      `ARCH="$(awk -F"'" '/^DISTRIB_ARCH=/{print $2; exit}' /etc/openwrt_release 2>/dev/null || true)"`,
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
    expect(script).toContain("run_preflight_checks() {");
    expect(script).toContain("detect_bootstrap_classification() {");
    expect(script).toContain("simulate_overlay_step() {");
    expect(script).toContain("require_remote_ipk tcping");
    expect(script).toContain(
      `OPENWRT_FEED_PREREQS='${AX3000T_OPENWRT_FEED_PROVIDED_DEPENDENCIES.join(" ")}'`,
    );
    expect(script).toContain("require_feed_packages $OPENWRT_FEED_PREREQS");
    expect(script).toContain("require_feed_packages $REQUIRED_RUNTIME_PACKAGES");
    expect(script).toContain("require_vectra_package vectra-controller-agent");
    expect(script).toContain("download_and_install_managed_ipk() {");
    expect(script).toContain("append_preserved_passwall_sections() {");
    expect(script).toContain("apply_passwall_baseline() {");
    expect(script).toContain("refresh_passwall_managed_stack() {");
    expect(script).toContain("install_controller_packages() {");
    expect(script).toContain("install_dnsmasq_full()");
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
    expect(script).toContain("find_node_by_remark()");
    expect(script).toContain(
      "for id in $(uci show passwall2 2>/dev/null | awk -F'[.=]' '/=nodes$/{print $2}')",
    );
    expect(script).toContain("apply_slot 'WorldProxy' '🇩🇪⚡Германия YouTube 🚫Ad🚫'");
    expect(script).toContain("apply_slot 'YouTube' '🇷🇺⚡Россия YouTube 🚫Ad🚫'");
    expect(script).toContain("apply_slot 'Special' '🇫🇮 ⚡⚡ Финляндия Xhttp Gaming'");
    expect(script).toContain("apply_slot 'Tiktok' '🇧🇾 Беларусь'");
    expect(script).toContain('uci set "passwall2.$SHUNT_ID.$slot=$node_id"');
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

  it("fails early with exact overlay diagnostics when a fresh install cannot fit", () => {
    const plan = planAx3000tManagedPackageOperations({
      overlayFreeBytes: 5_000_000,
      stageFreeBytes: 80_000_000,
      packageStates: {},
    });

    expect(plan.storageCheck.ok).toBe(false);
    expect(plan.storageCheck.reason).toBe("overlay");
    expect(plan.storageCheck.blockingPackageName).toBe("xray-core");
    expect(plan.storageCheck.message).toContain("/overlay");
    expect(plan.storageCheck.message).toContain("xray-core");
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
