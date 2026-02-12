import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Authentication", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/auth/magic", async (route) => {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: "magic login unavailable in test" }),
      });
    });

    await page.goto("/");
    await page.evaluate(() => {
      localStorage.clear();
    });
  });

  test("shows current magic-link login form", async ({ page }) => {
    await page.goto("/");

    await expect(page.getByRole("heading", { name: /choose your setup path/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /generate magic link/i })).toBeVisible();
    await expect(page.getByLabel("Name")).toBeVisible();
    await expect(page.getByLabel("Organization")).toBeVisible();
    await expect(page.getByLabel("Email address")).toBeVisible();
  });

  test("shows API error when magic-link request fails", async ({ page }) => {
    await page.route("**/api/auth/magic", async (route) => {
      await route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({ error: "invalid email domain" }),
      });
    });

    await page.goto("/");
    await page.getByLabel("Name").fill("Test User");
    await page.getByLabel("Organization").fill("Otter Camp");
    await page.getByLabel("Email address").fill("bad@example.com");
    await page.getByRole("button", { name: /generate magic link/i }).click();

    await expect(page.getByText(/invalid email domain/i)).toBeVisible();
  });

  test("redirects when magic-link request succeeds", async ({ page }) => {
    await page.route("**/api/auth/magic", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ url: "/auth/test-token" }),
      });
    });

    await page.goto("/");
    await page.getByLabel("Name").fill("Test User");
    await page.getByLabel("Organization").fill("Otter Camp");
    await page.getByLabel("Email address").fill("test@example.com");
    await page.getByRole("button", { name: /generate magic link/i }).click();

    await expect(page).toHaveURL(/\/auth\/test-token$/);
  });

  test("renders login page for unauthenticated protected route", async ({ page }) => {
    await page.goto("/settings");

    await expect(page.getByRole("heading", { name: /choose your setup path/i })).toBeVisible();
  });

  test("allows authenticated users to access protected route", async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    await page.goto("/settings");
    await expect(page.getByRole("heading", { name: /settings/i })).toBeVisible();
  });
});
