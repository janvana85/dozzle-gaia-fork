import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("http://dozzle:8080/notifications");
});

test("shows quiet-hours settings copy", async ({ page }) => {
  await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
  await expect(page.getByLabel("Enable Quiet Hours")).toBeVisible();
  await expect(page.getByLabel("Timezone (used for quiet hours)")).toBeVisible();
  await expect(page.getByPlaceholder("Europe/Prague")).toBeVisible();
  await expect(page.getByText("Server now")).toBeVisible();
  await expect(page.getByText("Quiet hours active now")).toBeVisible();
});
