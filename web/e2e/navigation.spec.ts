import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
  });

  test("shows primary topbar links", async ({ page }) => {
    await page.goto("/");

    const nav = page.locator("nav.nav-links");
    await expect(nav).toBeVisible();
    await expect(nav.getByRole("link", { name: "Inbox" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Projects" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Workflows" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Knowledge" })).toBeVisible();
  });

  test("navigates between primary routes", async ({ page }) => {
    await page.goto("/");

    await page.getByRole("link", { name: "Projects" }).click();
    await expect(page).toHaveURL(/\/projects$/);

    await page.getByRole("link", { name: "Inbox" }).click();
    await expect(page).toHaveURL(/\/inbox$/);

    await page.getByRole("link", { name: "Workflows" }).click();
    await expect(page).toHaveURL(/\/workflows$/);

    await page.getByRole("link", { name: "Knowledge" }).click();
    await expect(page).toHaveURL(/\/knowledge$/);
  });

  test("opens avatar menu and navigates to settings", async ({ page }) => {
    await page.goto("/");

    await page.getByRole("button", { name: "User menu" }).click();

    const avatarMenu = page.locator(".avatar-dropdown");
    await expect(avatarMenu).toBeVisible();
    await expect(avatarMenu.getByRole("button", { name: "Agents" })).toBeVisible();
    await expect(avatarMenu.getByRole("button", { name: "Connections" })).toBeVisible();
    await expect(avatarMenu.getByRole("button", { name: "Feed" })).toBeVisible();
    await expect(avatarMenu.getByRole("button", { name: "Settings" })).toBeVisible();

    await avatarMenu.getByRole("button", { name: "Settings" }).click();
    await expect(page).toHaveURL(/\/settings$/);
  });

  test("toggles mobile navigation", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 });
    await page.goto("/");

    const toggleButton = page.getByRole("button", { name: "Toggle menu" });
    await expect(toggleButton).toBeVisible();

    await toggleButton.click();
    const mobileNav = page.locator("nav.mobile-nav");
    await expect(mobileNav).toBeVisible();

    await mobileNav.getByRole("link", { name: "Inbox" }).click();
    await expect(page).toHaveURL(/\/inbox$/);
  });

  test("shows not-found page for unknown routes", async ({ page }) => {
    await page.goto("/definitely-not-a-route");

    await expect(page.getByRole("heading", { name: /404|page failed to load/i })).toBeVisible();
  });
});
