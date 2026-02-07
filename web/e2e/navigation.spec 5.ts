import { test, expect } from "@playwright/test";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }) => {
    // Set up authenticated state
    await page.goto("/");
    await page.evaluate(() => {
      const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QGV4YW1wbGUuY29tIiwibmFtZSI6IlRlc3QgVXNlciIsImV4cCI6OTk5OTk5OTk5OX0.test";
      const user = { id: "1", email: "test@example.com", name: "Test User" };
      localStorage.setItem("otter_camp_token", token);
      localStorage.setItem("otter_camp_user", JSON.stringify(user));
    });
  });

  test.describe("Sidebar Navigation", () => {
    test("should display sidebar with navigation links", async ({ page }) => {
      await page.goto("/");

      // Check for main navigation elements
      const nav = page.getByRole("navigation");
      await expect(nav).toBeVisible();

      // Check for common navigation links
      await expect(page.getByRole("link", { name: /dashboard|home/i })).toBeVisible();
      await expect(page.getByRole("link", { name: /tasks/i })).toBeVisible();
      await expect(page.getByRole("link", { name: /agents/i })).toBeVisible();
      await expect(page.getByRole("link", { name: /settings/i })).toBeVisible();
    });

    test("should highlight active navigation item", async ({ page }) => {
      await page.goto("/tasks");

      const tasksLink = page.getByRole("link", { name: /tasks/i });
      await expect(tasksLink).toHaveClass(/active|bg-|text-sky/);
    });

    test("should navigate to different pages via sidebar", async ({ page }) => {
      await page.goto("/");

      // Navigate to Agents
      await page.getByRole("link", { name: /agents/i }).click();
      await expect(page).toHaveURL(/\/agents/);
      await expect(page.getByRole("heading", { name: /agents/i })).toBeVisible();

      // Navigate to Projects
      await page.getByRole("link", { name: /projects/i }).click();
      await expect(page).toHaveURL(/\/projects/);
      await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();

      // Navigate to Settings
      await page.getByRole("link", { name: /settings/i }).click();
      await expect(page).toHaveURL(/\/settings/);
      await expect(page.getByRole("heading", { name: /settings/i })).toBeVisible();
    });

    test("should collapse/expand sidebar on mobile", async ({ page }) => {
      // Set mobile viewport
      await page.setViewportSize({ width: 375, height: 667 });
      await page.goto("/");

      // Look for hamburger menu button
      const menuButton = page.getByRole("button", { name: /menu|toggle/i });

      if (await menuButton.isVisible()) {
        // Sidebar should be hidden initially on mobile
        const sidebar = page.getByRole("navigation");
        await expect(sidebar).not.toBeVisible();

        // Click to open
        await menuButton.click();
        await expect(sidebar).toBeVisible();

        // Click to close
        await menuButton.click();
        await expect(sidebar).not.toBeVisible();
      }
    });
  });

  test.describe("Routing", () => {
    test("should load dashboard on root URL", async ({ page }) => {
      await page.goto("/");

      // Dashboard content should be visible
      const dashboardHeading = page.getByRole("heading", { level: 1 });
      await expect(dashboardHeading).toBeVisible();
    });

    test("should load tasks page", async ({ page }) => {
      await page.goto("/tasks");

      await expect(page.getByRole("heading", { name: /camp tasks|tasks/i })).toBeVisible();
    });

    test("should load agents page", async ({ page }) => {
      await page.goto("/agents");

      await expect(page.getByRole("heading", { name: /agents/i })).toBeVisible();
    });

    test("should load feed page", async ({ page }) => {
      await page.goto("/feed");

      await expect(page.getByRole("heading", { name: /feed/i })).toBeVisible();
    });

    test("should load projects page", async ({ page }) => {
      await page.goto("/projects");

      await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();
    });

    test("should load notifications page", async ({ page }) => {
      await page.goto("/notifications");

      await expect(page.getByRole("heading", { name: /notifications/i })).toBeVisible();
    });

    test("should load settings page", async ({ page }) => {
      await page.goto("/settings");

      await expect(page.getByRole("heading", { name: /settings/i })).toBeVisible();
    });

    test("should handle browser back/forward navigation", async ({ page }) => {
      await page.goto("/");
      await page.goto("/agents");
      await page.goto("/settings");

      // Go back
      await page.goBack();
      await expect(page).toHaveURL(/\/agents/);

      // Go back again
      await page.goBack();
      await expect(page).toHaveURL(/\/$/);

      // Go forward
      await page.goForward();
      await expect(page).toHaveURL(/\/agents/);
    });

    test("should preserve state during navigation", async ({ page }) => {
      await page.goto("/tasks");

      // Interact with the page (e.g., select a task)
      const firstTask = page.locator("article[role='listitem']").first();
      if (await firstTask.isVisible()) {
        await firstTask.click();
      }

      // Navigate away and back
      await page.goto("/settings");
      await page.goBack();

      // Page should load correctly
      await expect(page).toHaveURL(/\/tasks/);
    });
  });

  test.describe("404 Page", () => {
    test("should show 404 page for non-existent routes", async ({ page }) => {
      await page.goto("/this-page-does-not-exist");

      // Should show 404 content
      await expect(page.getByRole("heading", { name: /404|not found/i })).toBeVisible();
    });

    test("should display helpful message on 404 page", async ({ page }) => {
      await page.goto("/random-invalid-url");

      // Should have helpful text
      await expect(page.getByText(/page.*not found|doesn't exist|couldn't find/i)).toBeVisible();
    });

    test("should have link to return home on 404 page", async ({ page }) => {
      await page.goto("/nonexistent-route");

      // Should have a link to go back home
      const homeLink = page.getByRole("link", { name: /home|back|dashboard/i });
      await expect(homeLink).toBeVisible();

      await homeLink.click();
      await expect(page).toHaveURL(/\/$/);
    });

    test("should handle deeply nested invalid routes", async ({ page }) => {
      await page.goto("/foo/bar/baz/qux");

      await expect(page.getByRole("heading", { name: /404|not found/i })).toBeVisible();
    });
  });

  test.describe("Skip Link", () => {
    test("should have skip link for keyboard users", async ({ page }) => {
      await page.goto("/");

      // Tab to reveal skip link
      await page.keyboard.press("Tab");

      const skipLink = page.getByRole("link", { name: /skip to/i });
      if (await skipLink.isVisible()) {
        await expect(skipLink).toBeFocused();
      }
    });

    test("should skip to main content when activated", async ({ page }) => {
      await page.goto("/");

      // Tab to skip link
      await page.keyboard.press("Tab");

      const skipLink = page.getByRole("link", { name: /skip to/i });
      if (await skipLink.isVisible()) {
        await page.keyboard.press("Enter");

        // Main content should be focused
        const mainContent = page.locator("#main-content");
        await expect(mainContent).toBeFocused();
      }
    });
  });

  test.describe("Breadcrumbs", () => {
    test("should show breadcrumbs on nested pages", async ({ page }) => {
      await page.goto("/settings");

      const breadcrumbs = page.getByRole("navigation", { name: /breadcrumb/i });
      if (await breadcrumbs.isVisible()) {
        await expect(breadcrumbs.getByText(/home|dashboard/i)).toBeVisible();
        await expect(breadcrumbs.getByText(/settings/i)).toBeVisible();
      }
    });
  });
});
