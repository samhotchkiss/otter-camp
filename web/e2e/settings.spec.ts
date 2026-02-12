import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Settings Page", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    let apiKeys = [
      {
        id: "key-1",
        name: "API Key 1",
        prefix: "oc_live_",
        createdAt: new Date().toISOString(),
      },
    ];

    await page.route("**/api/settings/profile", async (route) => {
      if (route.request().method() === "PUT") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: route.request().postData() ?? "{}",
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          name: "Test User",
          email: "test@example.com",
          avatarUrl: null,
        }),
      });
    });

    await page.route("**/api/settings/notifications", async (route) => {
      if (route.request().method() === "PUT") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: route.request().postData() ?? "{}",
        });
        return;
      }

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
    });

    await page.route("**/api/settings/workspace", async (route) => {
      if (route.request().method() === "PUT") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: route.request().postData() ?? "{}",
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          name: "Otter Camp Workspace",
          slug: "otter-camp",
          members: [
            { id: "u1", name: "Test User", email: "test@example.com", role: "owner" },
            { id: "u2", name: "Teammate", email: "teammate@example.com", role: "member" },
          ],
        }),
      });
    });

    await page.route("**/api/settings/integrations", async (route) => {
      if (route.request().method() === "PUT") {
        await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          openclawWebhookUrl: "https://example.com/webhook",
          apiKeys,
        }),
      });
    });

    await page.route("**/api/settings/integrations/api-keys", async (route) => {
      const newKey = {
        id: "key-2",
        name: "API Key 2",
        prefix: "oc_live_",
        createdAt: new Date().toISOString(),
      };
      apiKeys = [...apiKeys, newKey];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(newKey),
      });
    });

    await page.route("**/api/settings/integrations/api-keys/*", async (route) => {
      const keyID = route.request().url().split("/").at(-1) ?? "";
      apiKeys = apiKeys.filter((key) => key.id !== keyID);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
    });

    await page.route("**/api/labels**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ labels: [] }),
      });
    });

    await page.route("**/api/github/integration/status**", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({}) });
    });

    await page.route("**/api/github/integration/repos**", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ repositories: [] }) });
    });

    await page.route("**/api/github/integration/settings**", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({}) });
    });

    await page.goto("/settings");
  });

  test("renders core settings sections", async ({ page }) => {
    await expect(page.getByRole("heading", { name: /settings/i })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Profile" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Workspace" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Integrations" })).toBeVisible();
  });

  test("shows workspace slug and member roles", async ({ page }) => {
    await expect(page.getByLabel("Organization Slug")).toHaveValue("otter-camp");
    await expect(page.getByLabel("Workspace Name")).toHaveValue("Otter Camp Workspace");
    await expect(page.locator("span", { hasText: /^Owner$/ }).first()).toBeVisible();
    await expect(page.locator("span", { hasText: /^Member$/ }).first()).toBeVisible();
  });

  test("shows existing API keys", async ({ page }) => {
    await expect(page.getByText("API Key 1")).toBeVisible();
    await expect(page.getByText(/oc_live_/i)).toBeVisible();
  });

  test("generates a new API key", async ({ page }) => {
    await page.getByRole("button", { name: /generate new key/i }).click();
    await expect(page.getByText("API Key 2")).toBeVisible();
  });

  test("revokes API key from list", async ({ page }) => {
    await expect(page.getByText("API Key 1")).toBeVisible();

    await page.getByRole("button", { name: "Revoke" }).first().click();
    await expect(page.getByText("API Key 1")).not.toBeVisible();
  });
});
