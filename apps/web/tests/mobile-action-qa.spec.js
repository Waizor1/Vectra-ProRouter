// @ts-nocheck
import { expect, test } from "@playwright/test";

const BASE_URL = (process.env.VECTRA_MOBILE_QA_BASE_URL ?? "http://127.0.0.1:3400").replace(/\/$/, "");
const ROUTER_ID =
  process.env.VECTRA_MOBILE_QA_ROUTER_ID ??
  "16a23b6a-d21e-425d-abe6-0447030e2f50";
const USERNAME = process.env.VECTRA_MOBILE_QA_USERNAME ?? "operator";
const PASSWORD = process.env.VECTRA_MOBILE_QA_PASSWORD ?? "change-me";

const VIEWPORTS = [
  { name: "phone-390x844", width: 390, height: 844 },
  { name: "tablet-820x1180", width: 820, height: 1180 },
];

const ROUTER_TABS = [
  "Node List",
  "Node Subscribe",
  "App Update",
  "Rule Manage",
  "Geo View",
  "Watch Logs",
];

function buildUrl(pathname) {
  return `${BASE_URL}${pathname}`;
}

async function settlePage(page) {
  await page.waitForLoadState("domcontentloaded");
  await page.waitForTimeout(700);
  await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => {});
}

async function assertNoHorizontalOverflow(page, name) {
  const metrics = await page.evaluate(() => {
    const doc = document.documentElement;
    const body = document.body;

    return {
      href: window.location.href,
      documentClientWidth: doc.clientWidth,
      documentScrollWidth: doc.scrollWidth,
      bodyClientWidth: body?.clientWidth ?? 0,
      bodyScrollWidth: body?.scrollWidth ?? 0,
    };
  });

  expect(
    metrics.documentScrollWidth,
    `${name} overflowed document width on ${metrics.href}`,
  ).toBeLessThanOrEqual(metrics.documentClientWidth);

  if (metrics.bodyClientWidth > 0) {
    expect(
      metrics.bodyScrollWidth,
      `${name} overflowed body width on ${metrics.href}`,
    ).toBeLessThanOrEqual(metrics.bodyClientWidth);
  }
}

async function gotoAndAssert(page, name, pathname) {
  await page.goto(buildUrl(pathname), { waitUntil: "domcontentloaded" });
  await settlePage(page);
  await assertNoHorizontalOverflow(page, name);
}

async function findFirstActionable(locators) {
  for (const locator of locators) {
    const count = await locator.count();

    for (let index = 0; index < count; index += 1) {
      const candidate = locator.nth(index);
      const isVisible = await candidate.isVisible().catch(() => false);

      if (!isVisible) {
        continue;
      }

      const isEnabled = await candidate.isEnabled().catch(() => false);
      if (!isEnabled) {
        continue;
      }

      return candidate;
    }
  }

  return null;
}

async function clickFirstVisible(locators) {
  const candidate = await findFirstActionable(locators);
  if (!candidate) {
    return false;
  }

  await candidate.click();
  return true;
}

async function openNamedControl(page, name) {
  return clickFirstVisible([
    page.getByRole("button", { name }),
    page.getByRole("link", { name }),
  ]);
}

async function login(page, viewportName) {
  await gotoAndAssert(page, `${viewportName}: login`, "/login");

  const usernameField = page.locator('input[name="username"]');
  if (await usernameField.count()) {
    await usernameField.fill(USERNAME);
    await page.locator('input[name="password"]').fill(PASSWORD);
    await page.getByRole("button", { name: "Войти" }).click();

    await page.waitForURL((url) => !url.pathname.endsWith("/login"), {
      timeout: 15000,
    });
  }

  await settlePage(page);
  await assertNoHorizontalOverflow(page, `${viewportName}: after-login`);
}

async function exerciseFleet(page, viewportName) {
  await gotoAndAssert(page, `${viewportName}: fleet`, "/fleet");

  const search = page.locator('input[name="fleet-operations-search"]');
  await expect(search).toBeVisible();
  await search.fill("OpenWrt");
  await page.waitForTimeout(300);
  await assertNoHorizontalOverflow(page, `${viewportName}: fleet-search`);

  const clearButton = page.getByRole("button", { name: "Сбросить поиск" });
  if (await clearButton.count()) {
    await clearButton.first().click();
    await page.waitForTimeout(200);
    await assertNoHorizontalOverflow(page, `${viewportName}: fleet-search-reset`);
  }
}

async function exerciseRouterDetail(page, viewportName) {
  await gotoAndAssert(
    page,
    `${viewportName}: router-basic-main`,
    `/routers/${ROUTER_ID}?tab=basic-settings&section=main`,
  );

  const noteField = page.getByLabel("Комментарий к черновику");
  if (await noteField.count()) {
    await noteField.fill(`mobile qa ${viewportName}`);
    await assertNoHorizontalOverflow(page, `${viewportName}: router-note-filled`);
  }

  const saveButton = page.getByRole("button", {
    name: /Сохранить только в панели|Сохранить черновик/i,
  });
  if (await saveButton.count()) {
    const actionableSaveButton = await findFirstActionable([saveButton]);
    if (actionableSaveButton) {
      await actionableSaveButton.click();
      await page.waitForTimeout(1200);
      await assertNoHorizontalOverflow(page, `${viewportName}: router-after-save`);
    }
  }

  for (const tabName of ROUTER_TABS) {
    const opened = await openNamedControl(page, tabName);
    if (!opened) {
      continue;
    }

    await settlePage(page);
    await assertNoHorizontalOverflow(page, `${viewportName}: router-tab-${tabName}`);
  }

  if (await page.getByText("Терминал роутера").count()) {
    await assertNoHorizontalOverflow(page, `${viewportName}: router-terminal-history`);
  }
}

async function exerciseUpdates(page, viewportName) {
  await gotoAndAssert(page, `${viewportName}: updates`, "/updates");

  for (const tabName of ["Группы и профили", "Контроллер версий"]) {
    const opened = await openNamedControl(page, tabName);
    if (!opened) {
      continue;
    }

    await settlePage(page);
    await assertNoHorizontalOverflow(page, `${viewportName}: updates-${tabName}`);
  }

  const firstTargetCheckbox = page.locator('input[aria-label^="Выбрать "]');
  if (await firstTargetCheckbox.count()) {
    await firstTargetCheckbox.first().check();
    await page.waitForTimeout(300);
    await assertNoHorizontalOverflow(page, `${viewportName}: updates-target-selected`);
  }

  const rebootButton = page.getByRole("button", {
    name: "Перезагрузить выбранные роутеры",
  });
  if (await rebootButton.count()) {
    const disabled = await rebootButton.first().isDisabled();
    if (!disabled) {
      await rebootButton.first().click();
      await settlePage(page);
      await assertNoHorizontalOverflow(
        page,
        `${viewportName}: updates-reboot-dialog-open`,
      );

      const closeDialog = await clickFirstVisible([
        page.getByRole("button", { name: "Отмена" }),
        page.getByRole("button", { name: /Закрыть/i }),
      ]);

      if (closeDialog) {
        await page.waitForTimeout(300);
        await assertNoHorizontalOverflow(
          page,
          `${viewportName}: updates-reboot-dialog-close`,
        );
      }
    }
  }
}

async function exerciseStaticRoutes(page, viewportName) {
  const routes = [
    { name: "drafts", path: `/drafts?routerId=${ROUTER_ID}` },
    { name: "rescue", path: "/rescue" },
    { name: "enrollment", path: "/enrollment" },
    { name: "install", path: "/install" },
  ];

  for (const route of routes) {
    await gotoAndAssert(page, `${viewportName}: ${route.name}`, route.path);
  }
}

for (const viewport of VIEWPORTS) {
  test(`vectra responsive route matrix (${viewport.name})`, async ({ page }) => {
    test.setTimeout(180000);

    await page.setViewportSize({
      width: viewport.width,
      height: viewport.height,
    });

    await login(page, viewport.name);
    await exerciseFleet(page, viewport.name);
    await exerciseRouterDetail(page, viewport.name);
    await exerciseUpdates(page, viewport.name);
    await exerciseStaticRoutes(page, viewport.name);
  });
}
