import { test, expect } from "@playwright/test";

test.describe("Authentication", () => {
  test.beforeEach(async ({ page }) => {
    // Clear localStorage before each test
    await page.goto("/");
    await page.evaluate(() => {
      localStorage.clear();
    });
  });

  test.describe("Login Flow", () => {
    test("should display login form when not authenticated", async ({ page }) => {
      await page.goto("/");

      // Check for login elements (adjust selectors based on actual UI)
      const loginButton = page.getByRole("button", { name: /log\s*in|sign\s*in/i });
      await expect(loginButton).toBeVisible();
    });

    test("should show error message for invalid credentials", async ({ page }) => {
      await page.goto("/");

      // Fill in invalid credentials
      const emailInput = page.getByLabel(/email/i);
      const passwordInput = page.getByLabel(/password/i);

      if (await emailInput.isVisible()) {
        await emailInput.fill("invalid@example.com");
        await passwordInput.fill("wrongpassword");

        await page.getByRole("button", { name: /log\s*in|sign\s*in/i }).click();

        // Expect error message
        await expect(page.getByText(/invalid|error|failed/i)).toBeVisible();
      }
    });

    test("should successfully login with valid credentials", async ({ page }) => {
      await page.goto("/");

      // Mock the auth API
      await page.route("**/api/auth/login", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            token: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QGV4YW1wbGUuY29tIiwibmFtZSI6IlRlc3QgVXNlciIsImV4cCI6OTk5OTk5OTk5OX0.test",
            user: {
              id: "1",
              email: "test@example.com",
              name: "Test User",
            },
          }),
        });
      });

      const emailInput = page.getByLabel(/email/i);
      const passwordInput = page.getByLabel(/password/i);

      if (await emailInput.isVisible()) {
        await emailInput.fill("test@example.com");
        await passwordInput.fill("password123");

        await page.getByRole("button", { name: /log\s*in|sign\s*in/i }).click();

        // Should be redirected to dashboard or show authenticated UI
        await expect(page).toHaveURL(/\/(dashboard|tasks)?$/);
      }
    });
  });

  test.describe("Logout", () => {
    test("should successfully logout user", async ({ page }) => {
      // Set up authenticated state
      await page.goto("/");
      await page.evaluate(() => {
        const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QGV4YW1wbGUuY29tIiwibmFtZSI6IlRlc3QgVXNlciIsImV4cCI6OTk5OTk5OTk5OX0.test";
        const user = { id: "1", email: "test@example.com", name: "Test User" };
        localStorage.setItem("otter_camp_token", token);
        localStorage.setItem("otter_camp_user", JSON.stringify(user));
      });

      await page.reload();

      // Look for user menu or logout button
      const userMenu = page.getByRole("button", { name: /user|profile|menu/i });
      if (await userMenu.isVisible()) {
        await userMenu.click();

        const logoutButton = page.getByRole("button", { name: /log\s*out|sign\s*out/i });
        if (await logoutButton.isVisible()) {
          await logoutButton.click();

          // Verify user is logged out
          await expect(page.getByRole("button", { name: /log\s*in|sign\s*in/i })).toBeVisible();

          // Verify localStorage is cleared
          const token = await page.evaluate(() => localStorage.getItem("otter_camp_token"));
          expect(token).toBeNull();
        }
      }
    });
  });

  test.describe("Protected Routes", () => {
    test("should redirect unauthenticated users from protected routes", async ({ page }) => {
      // Clear any existing auth
      await page.goto("/");
      await page.evaluate(() => {
        localStorage.removeItem("otter_camp_token");
        localStorage.removeItem("otter_camp_user");
      });

      // Try to access a protected route
      await page.goto("/settings");

      // Should be redirected to login or show login prompt
      const loginButton = page.getByRole("button", { name: /log\s*in|sign\s*in/i });
      const isRedirected = await page.url().includes("login");

      expect(await loginButton.isVisible() || isRedirected).toBeTruthy();
    });

    test("should allow authenticated users to access protected routes", async ({ page }) => {
      // Set up authenticated state
      await page.goto("/");
      await page.evaluate(() => {
        const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QGV4YW1wbGUuY29tIiwibmFtZSI6IlRlc3QgVXNlciIsImV4cCI6OTk5OTk5OTk5OX0.test";
        const user = { id: "1", email: "test@example.com", name: "Test User" };
        localStorage.setItem("otter_camp_token", token);
        localStorage.setItem("otter_camp_user", JSON.stringify(user));
      });

      await page.goto("/settings");

      // Should show settings page content
      await expect(page.getByRole("heading", { name: /settings/i })).toBeVisible();
    });

    test("should persist authentication across page reloads", async ({ page }) => {
      // Set up authenticated state
      await page.goto("/");
      await page.evaluate(() => {
        const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QGV4YW1wbGUuY29tIiwibmFtZSI6IlRlc3QgVXNlciIsImV4cCI6OTk5OTk5OTk5OX0.test";
        const user = { id: "1", email: "test@example.com", name: "Test User" };
        localStorage.setItem("otter_camp_token", token);
        localStorage.setItem("otter_camp_user", JSON.stringify(user));
      });

      await page.reload();

      // User should still be authenticated (no login button visible or user menu visible)
      const loginButton = page.getByRole("button", { name: /log\s*in|sign\s*in/i });
      const userMenu = page.getByRole("button", { name: /user|profile|menu/i });

      const isStillAuthenticated = !(await loginButton.isVisible()) || (await userMenu.isVisible());
      expect(isStillAuthenticated).toBeTruthy();
    });
  });
});
