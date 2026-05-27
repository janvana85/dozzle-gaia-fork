import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("http://dozzle:8080/notifications");
});

test("persists quiet-hours settings through the notifications api", async ({ page }) => {
  await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();

  const enabled = page.getByLabel("Enable Quiet Hours");
  await enabled.check();

  const start = page.getByLabel("Start");
  const end = page.getByLabel("End");
  const timezone = page.getByLabel("Timezone (used for quiet hours)");

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
