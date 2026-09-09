import type { Page } from "@playwright/test";

// Signing in is the preamble of every spec, so it lives here rather than in each of
// them. The agreement is stored, not clicked: it is a modal over the whole app and
// answering it in every test would be a click that tests nothing.
//
// The language is pinned too. It is a per-browser setting the panel remembers, so a
// suite that does not state which one it wants inherits whoever used that profile
// last — and then fails on a button whose label is in the other language.
export async function signIn(page: Page) {
  const lang = process.env.PANEL_LANG ?? "ru";
  await page.goto("./", { waitUntil: "networkidle" });
  await page.evaluate((l) => {
    localStorage.setItem("rospanel.agreement", "1");
    localStorage.setItem("rospanel.lang", l);
  }, lang);
  await page.reload({ waitUntil: "networkidle" });
  const accept = page.getByRole("button", { name: /Принимаю|I accept/ });
  if (await accept.count()) await accept.click();
  if (await page.locator('input[type="password"]').count()) {
    await page.fill('input[type="text"]', process.env.PANEL_USER ?? "admin");
    await page.fill('input[type="password"]', process.env.PANEL_PASS ?? "admin");
    await page.getByRole("button", { name: /Войти|Sign in/ }).click();
  }
  await page.waitForTimeout(3000);
}

// The SPA routes in memory: pushing state and firing popstate is what its own links
// do, and it avoids a full reload (and a second login) per screen.
export async function goto(page: Page, route: string) {
  await page.evaluate((r) => {
    const base = new URL(document.baseURI).pathname;
    history.pushState(null, "", base + r);
    dispatchEvent(new PopStateEvent("popstate"));
  }, route);
  await page.waitForTimeout(1200);
}

export type Layout = {
  pageOverflow: number;
  bleeding: string[];
  colliding: string[];
  clipped: string[];
};

// audit is the layout check every screen gets: does the page scroll sideways, does a
// section stick out of its container, does a value land on top of its own label, is
// text cut off with no ellipsis to say so. These are the four ways the console broke
// during the redesign, each one found by eye — so they are asserted now.
export async function audit(page: Page): Promise<Layout> {
  return page.evaluate(() => {
    const out: Layout = { pageOverflow: 0, bleeding: [], colliding: [], clipped: [] };
    const de = document.documentElement;
    out.pageOverflow = de.scrollWidth - de.clientWidth;
    for (const root of document.querySelectorAll("main, div.fixed.inset-0")) {
      const rb = root.getBoundingClientRect();
      for (const el of root.querySelectorAll("section, div.border-t, table")) {
        const r = el.getBoundingClientRect();
        if (r.width > 0 && (r.right > rb.right + 1 || r.left < rb.left - 1)) {
          out.bleeding.push(`${el.tagName}.${String(el.className).slice(0, 30)}`);
        }
      }
      for (const row of root.querySelectorAll("div.border-t")) {
        const label = row.querySelector("p.text-xs");
        const control = row.querySelector("div.ml-auto");
        if (!label || !control) continue;
        const lr = label.getBoundingClientRect();
        const cr = control.getBoundingClientRect();
        if (Math.abs(lr.top - cr.top) < 8 && lr.right > cr.left + 1) {
          out.colliding.push((label.textContent ?? "").trim().slice(0, 24));
        }
      }
      for (const el of root.querySelectorAll("p, span, h3, code")) {
        if (el.children.length) continue;
        // sr-only text is clipped on purpose: it exists for a screen reader.
        if (el.closest(".sr-only")) continue;
        const cs = getComputedStyle(el);
        if (
          cs.overflow === "hidden" &&
          cs.textOverflow !== "ellipsis" &&
          el.scrollWidth > el.clientWidth + 2
        ) {
          out.clipped.push((el.textContent ?? "").trim().slice(0, 24));
        }
      }
    }
    out.bleeding = [...new Set(out.bleeding)];
    out.colliding = [...new Set(out.colliding)];
    out.clipped = [...new Set(out.clipped)];
    return out;
  });
}

// Every screen the console has, in the order the navigation lists them.
export const ROUTES = [
  "overview",
  "users",
  "users/registrations",
  "users/broadcast",
  "users/payments",
  "users/groups",
  "users/stats",
  "users/events",
  "nodes",
  "admins",
  "settings",
  "settings/branding",
  "settings/subscriptions",
  "settings/telegram",
  "settings/billing",
  "settings/abuse",
  "settings/api",
];
