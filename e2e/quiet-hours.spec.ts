import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("http://dozzle:8080/notifications");
});

test("persists quiet-hours settings through the notifications api", async ({ page }) => {
  await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();

  const enabled = page.getByLabel("Enable Quiet Hours");
  await enabled.check();

  const quietTimeInputs = page.locator('input[type="time"]');
  const start = quietTimeInputs.first();
  const end = quietTimeInputs.nth(1);
  const timezone = page.locator('input[placeholder="Europe/Prague"]');

  await start.fill("21:15");
  await end.fill("06:45");
  await timezone.fill("Europe/Prague");

  await page.waitForResponse((response) => {
    return response.url().includes("/api/notifications/quiet-hours") && response.request().method() === "PUT";
  });

  await page.reload();

  await expect(enabled).toBeChecked();
  await expect(start).toHaveValue("21:15");
  await expect(end).toHaveValue("06:45");
  await expect(timezone).toHaveValue("Europe/Prague");
  await expect(page.getByText("Server now")).toBeVisible();
  await expect(page.getByText("Quiet hours active now")).toBeVisible();
});

test("shows active quiet-hours status from the notifications api", async ({ page }) => {
  await page.route("**/api/notifications/quiet-hours", async (route) => {
    const method = route.request().method();
    if (method === "GET") {
      await route.fulfill({
        json: {
          enabled: true,
          start: "00:00",
          end: "23:59",
          timezone: "Europe/Prague",
          stackThreshold: 3,
          stackWindow: 15,
          stackedPriority: 4,
          quietTopic: "",
          stackedUsesQuietTopic: false,
          serverNow: "2026-05-28T10:30:00+02:00",
          serverNowLabel: "2026-05-28 10:30:00 CEST +0200",
          activeNow: true,
        },
      });
      return;
    }

    await route.fulfill({ json: {} });
  });

  await page.goto("http://dozzle:8080/notifications");

  await expect(page.getByText("Server now: 2026-05-28 10:30:00 CEST +0200")).toBeVisible();
  await expect(page.getByText("Quiet hours active now:")).toBeVisible();
  await expect(page.getByText("Quiet hours active now: Yes", { exact: true })).toBeVisible();
});

test("falls back to server local when timezone is blank", async ({ page }) => {
  await page.route("**/api/notifications/quiet-hours", async (route) => {
    const method = route.request().method();
    if (method === "GET") {
      await route.fulfill({
        json: {
          enabled: true,
          start: "00:00",
          end: "23:59",
          timezone: "",
          stackThreshold: 3,
          stackWindow: 15,
          stackedPriority: 4,
          quietTopic: "",
          stackedUsesQuietTopic: false,
          serverNow: "2026-05-28T10:30:00Z",
          serverNowLabel: "2026-05-28 10:30:00 UTC +0000",
          activeNow: false,
        },
      });
      return;
    }

    await route.fulfill({ json: {} });
  });

  await page.goto("http://dozzle:8080/notifications");

  await expect(page.locator('input[placeholder="Europe/Prague"]')).toHaveValue("");
  await expect(page.getByText("Server now: 2026-05-28 10:30:00 UTC +0000")).toBeVisible();
  await expect(page.getByText("Quiet hours active now: No")).toBeVisible();
});
