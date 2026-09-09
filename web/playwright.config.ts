import { defineConfig } from "@playwright/test";

// The console's smoke suite runs against a PANEL THAT IS ALREADY RUNNING — it does
// not start one. A panel needs a data directory, an Xray binary and a first-run
// admin, none of which belongs inside a test runner; `e2e/README.md` says how to
// bring one up in one command.
//
// PANEL_URL points at it (secret path included) and PANEL_PASS carries the admin
// password, so the same suite runs against a scratch install or a staging box.
export default defineConfig({
  testDir: "./e2e",
  timeout: 90_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: process.env.PANEL_URL ?? "http://127.0.0.1:8099/rospanel/",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
});
