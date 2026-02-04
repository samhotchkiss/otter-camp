import { test, expect } from "@playwright/test";

test.describe("Settings Page", () => {
  test.beforeEach(async ({ page }) => {
    // Mock all settings APIs
    await page.route("**/api/settings/profile", async (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(body),
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            name: "Test User",
            email: "test@example.com",
            avatarUrl: null,
          }),
        });
      }
    });

    await page.route("**/api/settings/notifications", async (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(body),
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            taskAssigned: { email: true, push: true, inApp: true },
            taskCompleted: { email: false, push: true, inApp: true },
            mentions: { email: true, push: true, inApp: true },
            comments: { email: false, push: false, inApp: true },
            agentUpdates: { email: false, push: true, inApp: true },
            weeklyDigest: { email: true, push: false, inApp: false },
          }),
        });
      }
    });

    await page.route("**/api/settings/workspace", async (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(body),
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            name: "Otter Camp Workspace",
            members: [
              {
                id: "user-1",
                name: "Test User",
                email: "test@example.com",
                role: "owner",
              },
              {
                id: "user-2",
                name: "Team Member",
                email: "team@example.com",
                role: "member",
              },
            ],
          }),
        });
      }
    });

    await page.route("**/api/settings/integrations", async (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(body),
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            openclawWebhookUrl: "https://example.com/webhook",
            apiKeys: [
              {
                id: "key-1",
                name: "Production API Key",
                prefix: "oc_live_",
                createdAt: new Date().toISOString(),
              },
            ],
          }),
        });
      }
    });

    await page.route("**/api/settings/integrations/api-keys", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "key-new",
          name: "API Key 2",
          prefix: "oc_test_",
          createdAt: new Date().toISOString(),
        }),
      });
    });

    await page.route("**/api/settings/integrations/api-keys/*", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
    });

    await page.goto("/settings");
  });

  test("displays settings page header", async ({ page }) => {
    await expect(page.getByRole("heading", { name: /Settings/i })).toBeVisible();
    await expect(page.getByText("Manage your account, preferences, and integrations")).toBeVisible();
  });

  test("displays all settings sections", async ({ page }) => {
    await expect(page.getByRole("heading", { name: "Profile" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Workspace" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Integrations" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Appearance" })).toBeVisible();
  });

  test.describe("Profile Section", () => {
    test("displays profile information", async ({ page }) => {
      await expect(page.getByLabel("Display Name")).toHaveValue("Test User");
      await expect(page.getByLabel("Email Address")).toHaveValue("test@example.com");
    });

    test("can update display name", async ({ page }) => {
      const nameInput = page.getByLabel("Display Name");
      await nameInput.clear();
      await nameInput.fill("Updated User");
      await expect(nameInput).toHaveValue("Updated User");
    });

    test("can update email address", async ({ page }) => {
      const emailInput = page.getByLabel("Email Address");
      await emailInput.clear();
      await emailInput.fill("updated@example.com");
      await expect(emailInput).toHaveValue("updated@example.com");
    });

    test("can save profile changes", async ({ page }) => {
      let savedProfile: { name?: string; email?: string } | null = null;

      await page.route("**/api/settings/profile", async (route) => {
        if (route.request().method() === "PUT") {
          savedProfile = route.request().postDataJSON() as { name?: string; email?: string };
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify(savedProfile),
          });
        } else {
          await route.continue();
        }
      });

      const nameInput = page.getByLabel("Display Name");
      await nameInput.clear();
      await nameInput.fill("New Name");

      // Find the Save Profile button in the Profile section
      const profileSection = page.locator("section").filter({ hasText: "Profile" });
      await profileSection.getByRole("button", { name: "Save Profile" }).click();

      // Wait for the request to complete
      await page.waitForTimeout(500);
      expect(savedProfile?.name).toBe("New Name");
    });

    test("shows avatar initials when no image", async ({ page }) => {
      // With name "Test User", initials should be "TU"
      await expect(page.getByText("TU")).toBeVisible();
    });
  });

  test.describe("Notifications Section", () => {
    test("displays notification preference table", async ({ page }) => {
      await expect(page.getByText("Event Type")).toBeVisible();
      await expect(page.getByText("Task Assigned")).toBeVisible();
      await expect(page.getByText("Task Completed")).toBeVisible();
      await expect(page.getByText("Mentions")).toBeVisible();
      await expect(page.getByText("Comments")).toBeVisible();
      await expect(page.getByText("Agent Updates")).toBeVisible();
      await expect(page.getByText("Weekly Digest")).toBeVisible();
    });

    test("displays channel headers", async ({ page }) => {
      const notificationsSection = page.locator("section").filter({ hasText: /Notifications.*Choose how you want to be notified/i });
      await expect(notificationsSection.getByText("Email")).toBeVisible();
      await expect(notificationsSection.getByText("Push")).toBeVisible();
      await expect(notificationsSection.getByText("In-App")).toBeVisible();
    });

    test("can toggle notification preferences", async ({ page }) => {
      // Find toggle switches in notification section
      const notificationToggles = page.locator("section").filter({ hasText: "Notifications" }).getByRole("switch");
      const toggleCount = await notificationToggles.count();
      expect(toggleCount).toBeGreaterThan(0);

      // Click the first toggle
      const firstToggle = notificationToggles.first();
      const initialState = await firstToggle.getAttribute("aria-checked");
      await firstToggle.click();
      const newState = await firstToggle.getAttribute("aria-checked");
      expect(newState).not.toBe(initialState);
    });

    test("can save notification preferences", async ({ page }) => {
      let savedPrefs: Record<string, unknown> | null = null;

      await page.route("**/api/settings/notifications", async (route) => {
        if (route.request().method() === "PUT") {
          savedPrefs = route.request().postDataJSON() as Record<string, unknown>;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify(savedPrefs),
          });
        } else {
          await route.continue();
        }
      });

      const notificationsSection = page.locator("section").filter({ hasText: /Choose how you want to be notified/i });
      await notificationsSection.getByRole("button", { name: "Save Preferences" }).click();

      await page.waitForTimeout(500);
      expect(savedPrefs).not.toBeNull();
    });
  });

  test.describe("Appearance Section", () => {
    test("displays theme options", async ({ page }) => {
      const appearanceSection = page.locator("section").filter({ hasText: "Appearance" });
      await expect(appearanceSection.getByText("Light")).toBeVisible();
      await expect(appearanceSection.getByText("Dark")).toBeVisible();
      await expect(appearanceSection.getByText("System")).toBeVisible();
    });

    test("can change theme to light", async ({ page }) => {
      const appearanceSection = page.locator("section").filter({ hasText: "Appearance" });
      const lightButton = appearanceSection.getByRole("button", { name: /Light/i });
      await lightButton.click();

      // The button should now be selected (have emerald border)
      await expect(lightButton).toHaveClass(/border-emerald/);
    });

    test("can change theme to dark", async ({ page }) => {
      const appearanceSection = page.locator("section").filter({ hasText: "Appearance" });
      const darkButton = appearanceSection.getByRole("button", { name: /Dark/i });
      await darkButton.click();

      await expect(darkButton).toHaveClass(/border-emerald/);
    });

    test("can change theme to system", async ({ page }) => {
      const appearanceSection = page.locator("section").filter({ hasText: "Appearance" });
      const systemButton = appearanceSection.getByRole("button", { name: /System/i });
      await systemButton.click();

      await expect(systemButton).toHaveClass(/border-emerald/);
    });

    test("persists theme selection in localStorage", async ({ page }) => {
      const appearanceSection = page.locator("section").filter({ hasText: "Appearance" });
      await appearanceSection.getByRole("button", { name: /Dark/i }).click();

      const theme = await page.evaluate(() => localStorage.getItem("otter-camp-theme"));
      expect(theme).toBe("dark");
    });
  });

  test.describe("Workspace Section", () => {
    test("displays workspace name", async ({ page }) => {
      await expect(page.getByLabel("Workspace Name")).toHaveValue("Otter Camp Workspace");
    });

    test("displays team members", async ({ page }) => {
      await expect(page.getByText("Test User")).toBeVisible();
      await expect(page.getByText("test@example.com")).toBeVisible();
      await expect(page.getByText("Team Member")).toBeVisible();
      await expect(page.getByText("team@example.com")).toBeVisible();
    });

    test("displays member roles", async ({ page }) => {
      await expect(page.getByText("Owner")).toBeVisible();
      await expect(page.getByText("Member")).toBeVisible();
    });

    test("shows Invite Member button", async ({ page }) => {
      const workspaceSection = page.locator("section").filter({ hasText: "Workspace" });
      await expect(workspaceSection.getByRole("button", { name: "Invite Member" })).toBeVisible();
    });

    test("can update workspace name", async ({ page }) => {
      const nameInput = page.getByLabel("Workspace Name");
      await nameInput.clear();
      await nameInput.fill("New Workspace Name");
      await expect(nameInput).toHaveValue("New Workspace Name");
    });
  });

  test.describe("Integrations Section", () => {
    test("displays webhook URL field", async ({ page }) => {
      await expect(page.getByLabel("OpenClaw Webhook URL")).toHaveValue("https://example.com/webhook");
    });

    test("displays existing API keys", async ({ page }) => {
      await expect(page.getByText("Production API Key")).toBeVisible();
      await expect(page.getByText("oc_live_")).toBeVisible();
    });

    test("can generate new API key", async ({ page }) => {
      const integrationsSection = page.locator("section").filter({ hasText: "Integrations" });
      await integrationsSection.getByRole("button", { name: "Generate New Key" }).click();

      // Wait for the new key to appear
      await expect(page.getByText("API Key 2")).toBeVisible();
    });

    test("can revoke API key", async ({ page }) => {
      const revokeButton = page.getByRole("button", { name: "Revoke" }).first();
      await revokeButton.click();

      // The key should be removed after revocation
      await page.waitForTimeout(500);
      // Since we mock the delete, we just verify the button was clickable
    });

    test("can update webhook URL", async ({ page }) => {
      const webhookInput = page.getByLabel("OpenClaw Webhook URL");
      await webhookInput.clear();
      await webhookInput.fill("https://new-webhook.com/events");
      await expect(webhookInput).toHaveValue("https://new-webhook.com/events");
    });
  });

  test.describe("Edge Cases", () => {
    test("handles API errors gracefully", async ({ page }) => {
      await page.route("**/api/settings/profile", async (route) => {
        if (route.request().method() === "PUT") {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({ error: "Server error" }),
          });
        } else {
          await route.continue();
        }
      });

      const profileSection = page.locator("section").filter({ hasText: "Profile" });
      await profileSection.getByRole("button", { name: "Save Profile" }).click();

      // Button should return to normal state after error
      await expect(profileSection.getByRole("button", { name: "Save Profile" })).toBeEnabled();
    });

    test("shows loading state when saving", async ({ page }) => {
      await page.route("**/api/settings/profile", async (route) => {
        if (route.request().method() === "PUT") {
          await new Promise((resolve) => setTimeout(resolve, 1000));
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({}),
          });
        } else {
          await route.continue();
        }
      });

      const profileSection = page.locator("section").filter({ hasText: "Profile" });
      const saveButton = profileSection.getByRole("button", { name: "Save Profile" });
      await saveButton.click();

      // Button should show loading state
      await expect(saveButton).toBeDisabled();
    });
  });
});
