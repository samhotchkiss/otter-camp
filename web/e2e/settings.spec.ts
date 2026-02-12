import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Settings Page", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    let gitTokens = [
      {
        id: "token-1",
        name: "Git Token 1",
        token_prefix: "oc_git_",
        created_at: new Date().toISOString(),
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
          apiKeys: [],
        }),
      });
    });

    await page.route("**/api/git/tokens", async (route) => {
      const method = route.request().method();
      if (method === "POST") {
        const nextToken = {
          id: "token-2",
          name: "Git Token 2",
          token_prefix: "oc_git_",
          token: "oc_git_secret_value",
          created_at: new Date().toISOString(),
        };
        gitTokens = [nextToken, ...gitTokens];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(nextToken),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ tokens: gitTokens }),
      });
    });

    await page.route("**/api/git/tokens/*/revoke", async (route) => {
      const tokenID = route.request().url().split("/").at(-2) ?? "";
      gitTokens = gitTokens.filter((token) => token.id !== tokenID);
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true }) });
    });

    await page.route("**/api/projects", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ projects: [{ id: "project-1" }, { id: "project-2" }] }),
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

  test("shows workspace name and member roles", async ({ page }) => {
    await expect(page.getByLabel("Workspace Name")).toHaveValue("Otter Camp Workspace");
    await expect(page.locator("span", { hasText: /^Owner$/ }).first()).toBeVisible();
    await expect(page.locator("span", { hasText: /^Member$/ }).first()).toBeVisible();
  });

  test("generates git token and reveals one-time secret", async ({ page }) => {
    await page.getByRole("button", { name: /generate git token/i }).click();

    await expect(page.getByText(/copy this token now/i)).toBeVisible();
    await expect(page.getByText("oc_git_secret_value")).toBeVisible();
    await expect(page.getByText("Git Token 2")).toBeVisible();
  });

  test("shows actionable guidance when no projects exist", async ({ page }) => {
    await page.route("**/api/projects", async (route) => {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ projects: [] }) });
    });

    await page.getByRole("button", { name: /generate git token/i }).click();
    await expect(page.getByText(/create a project before generating a git token/i)).toBeVisible();
  });

  test("revokes git token from list", async ({ page }) => {
    await expect(page.getByText("Git Token 1")).toBeVisible();

    await page.getByRole("button", { name: "Revoke" }).first().click();
    await expect(page.getByText("Git Token 1")).not.toBeVisible();
  });
});
