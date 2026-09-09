import { expect, test } from "@playwright/test";
import { audit, goto, ROUTES, signIn } from "./panel";

// The console is one layout at four widths: a phone, a small phone, a tablet-ish
// window and a desktop. Every screen is checked at each — the redesign's bugs were
// all "fine at 1440, broken at 390" or the reverse.
const WIDTHS = [
  { name: "phone", width: 390, height: 900 },
  { name: "small phone", width: 320, height: 900 },
  { name: "narrow window", width: 700, height: 900 },
  { name: "desktop", width: 1440, height: 950 },
];

for (const size of WIDTHS) {
  test.describe(`${size.name} (${size.width}px)`, () => {
    test.use({ viewport: { width: size.width, height: size.height } });

    test("every screen lays out cleanly", async ({ page }) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await signIn(page);

      for (const route of ROUTES) {
        await goto(page, route);
        const l = await audit(page);
        expect(l.pageOverflow, `${route}: the page scrolls sideways`).toBe(0);
        expect(l.bleeding, `${route}: a block sticks out of its container`).toEqual([]);
        expect(l.colliding, `${route}: a value lands on its own label`).toEqual([]);
        expect(l.clipped, `${route}: text is cut off with no ellipsis`).toEqual([]);
      }
      expect(errors, "the walk raised page errors").toEqual([]);
    });
  });
}
