import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Tasks Route", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
  });

  test("renders dashboard sections at /tasks", async ({ page }) => {
    await page.goto("/tasks");

    await expect(page.getByRole("heading", { name: /needs you/i })).toBeVisible();
    await expect(page.getByRole("heading", { name: /your feed/i })).toBeVisible();
    await expect(page.locator("#main-content .projects-header")).toBeVisible();
  });

  test("keeps shell navigation available on /tasks", async ({ page }) => {
    await page.goto("/tasks");

    const sidebar = page.getByTestId("shell-sidebar");
    await expect(sidebar.getByRole("link", { name: "Inbox" })).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Projects", exact: true })).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Memory quick nav" })).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Operations quick nav" })).toBeVisible();
  });
});
