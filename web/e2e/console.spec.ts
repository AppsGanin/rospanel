import { expect, test } from "@playwright/test";
import { audit, goto, signIn } from "./panel";

// What the console must still be able to do. One spec per thing that broke, or was
// built, during the redesign — the checks are deliberately shallow (does the shape
// exist, does the state change) because they run against real data on a real panel.
test.describe("console", () => {
  test.use({ viewport: { width: 1440, height: 950 } });

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test("a new user can be given a tariff instead of hand-set limits", async ({ page }) => {
    await goto(page, "users");
    await page.locator('main button[title="Добавить пользователя"]').first().click();
    const dialog = page.locator("div.fixed.inset-0").last();
    await expect(dialog.getByText("Тариф", { exact: true })).toBeVisible();
  });

  test("the user card carries its actions as icons and its journal as a table", async ({ page }) => {
    await goto(page, "users");
    await page.locator('main input[placeholder*="Поиск"]').fill("Ольга");
    await page.waitForTimeout(1200);
    await page.getByText("Ольга", { exact: true }).first().click();
    await page.waitForTimeout(1500);

    for (const action of ["Продлить", "Сбросить трафик", "Удалить пользователя"]) {
      await expect(page.locator(`button[title="${action}"]`)).toHaveCount(1);
    }
    await page.locator('button[title="Журнал действий"]').click();
    await page.waitForTimeout(1200);
    // A table, not a stack of cards: the grid template is the tell.
    await expect(
      page.locator('div.fixed.inset-0 [style*="grid-template-columns"]').first(),
    ).toBeVisible();
  });

  test("server settings open as a side drawer with every tab reachable", async ({ page }) => {
    await goto(page, "nodes");
    await page.locator('main button[title="Настройки"]').first().click();
    await page.waitForTimeout(1500);
    const drawer = page.locator("div.fixed.inset-0").last();
    await expect(drawer.locator("div.absolute.top-0")).toBeVisible();

    for (const tab of [
      "Основное",
      "Подключения",
      "Доп. подключения",
      "Роутинг",
      "DNS",
      "Geo",
      "Списки",
      "Домен",
      "Снимки",
    ]) {
      await drawer.getByRole("button", { name: tab, exact: true }).first().click();
      await page.waitForTimeout(500);
      const l = await audit(page);
      expect(l.pageOverflow, `${tab}: the page scrolls sideways`).toBe(0);
      expect(l.bleeding, `${tab}: a block sticks out of the drawer`).toEqual([]);
    }
  });

  test("an egress switch says it is unsaved rather than claiming to be up", async ({ page }) => {
    await goto(page, "nodes");
    await page.locator('main button[title="Настройки"]').first().click();
    await page.waitForTimeout(1500);
    const drawer = page.locator("div.fixed.inset-0").last();
    await drawer.getByRole("button", { name: "Роутинг", exact: true }).click();
    await page.waitForTimeout(1000);

    const warp = drawer.locator("section").filter({ hasText: "Cloudflare WARP" }).first();
    await warp.locator('button[role="switch"]').click();
    await expect(warp.getByText("не сохранено")).toBeVisible();
  });

  test("node logs keep their window when there is nothing to show", async ({ page }) => {
    await goto(page, "nodes");
    const logs = page.locator('main button[title="Логи"]').first();
    await logs.click();
    await page.waitForTimeout(1200);
    const box = page.locator("div.fixed.inset-0").last().locator("div.relative").first();
    const size = await box.boundingBox();
    expect(size?.height ?? 0, "the log window collapsed to its content").toBeGreaterThan(600);
  });

  test("statistics draw traffic as an area chart, the dashboard as columns with a tooltip", async ({
    page,
  }) => {
    await goto(page, "users/stats");
    await expect(page.locator("main .recharts-area")).toHaveCount(2);

    await goto(page, "overview");
    const columns = page.locator("main span.group");
    await columns.nth(2).click();
    const tooltip = page.locator("main span.bg-brand-600").filter({ hasText: "·" });
    await expect(tooltip).toHaveCount(1);
    // A click anywhere else puts it away: the chart is not a dialog.
    await page.locator("main h3").first().click({ force: true });
    await expect(tooltip).toHaveCount(0);
  });
});
