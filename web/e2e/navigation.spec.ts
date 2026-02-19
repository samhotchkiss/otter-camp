import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
  });

  test("figma-parity-shell baseline scaffold renders", async ({ page }) => {
    await page.goto("/inbox");

    await expect(page.getByTestId("shell-layout")).toBeVisible();
    await expect(page.getByTestId("shell-sidebar")).toBeVisible();
    await expect(page.getByTestId("shell-header")).toBeVisible();
    await expect(page.getByTestId("shell-workspace")).toBeVisible();
    await expect(page.getByTestId("shell-chat-slot")).toBeVisible();
    await expect(page.getByText("Otter Camp")).toBeVisible();
    await expect(page.getByText("Agent Ops")).toBeVisible();
    await expect(page.getByPlaceholder("Search...")).toBeVisible();
  });

  test("figma-parity-inbox baseline content and filters render", async ({ page }) => {
    await page.goto("/inbox");

    await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "All (6)" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Unread (3)" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Starred (1)" })).toBeVisible();
    await expect(page.getByText("PR #234 awaiting approval")).toBeVisible();

    await page.getByRole("tab", { name: "Starred (1)" }).click();
    await expect(page.getByText("Critical: API rate limit exceeded")).toBeVisible();
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

  test("smoke inbox projects issue review chat journey continuity", async ({ page }) => {
    const projectID = "project-1";
    const projectName = "Design System Refresh";
    const issueID = "issue-1";
    const issueTitle = "Cross-route continuity issue";

    await page.route("**/api/projects**", async (route) => {
      const url = route.request().url();
      if (url.includes(`/api/projects/${projectID}`)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: projectID,
            name: projectName,
            status: "active",
            description: "Redesign hardening",
          }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          projects: [
            {
              id: projectID,
              name: projectName,
              status: "active",
              description: "Redesign hardening",
            },
          ],
        }),
      });
    });

    await page.route("**/api/issues**", async (route) => {
      const url = route.request().url();
      if (url.includes(`/api/issues/${issueID}`)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            issue: {
              id: issueID,
              issue_number: 41,
              title: issueTitle,
              project_id: projectID,
              status: "open",
              work_status: "in_progress",
              priority: "P1",
              document_path: "",
            },
            participants: [],
            comments: [],
          }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          items: [
            {
              id: issueID,
              issue_number: 41,
              title: issueTitle,
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "in_progress",
              priority: "P1",
            },
          ],
        }),
      });
    });

    await page.goto("/inbox");
    await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();

    await page.getByRole("link", { name: "Projects" }).click();
    await expect(page).toHaveURL(/\/projects$/);

    await expect(page.getByTestId("project-card-project-1")).toBeVisible();
    await page.getByTestId("project-card-project-1").click();
    await expect(page).toHaveURL(/\/projects\/project-1$/);
    await expect(page.getByRole("heading", { name: projectName })).toBeVisible();

    await page.getByRole("tab", { name: "List" }).click();
    await page.getByRole("button", { name: /cross-route continuity issue/i }).click();
    await expect(page).toHaveURL(/\/projects\/project-1\/issues\/issue-1$/);
    await expect(page.getByRole("heading", { name: "Issue #issue-1" })).toBeVisible();

    await page.goto("/review/docs%2Fplaybook.md");
    await expect(page).toHaveURL(/\/review\/docs%2Fplaybook\.md$/);
    await expect(page.getByRole("heading", { name: "Content Review" })).toBeVisible();
    await expect(page.getByTestId("content-review-route-path")).toContainText("docs/playbook.md");

    await page.getByRole("button", { name: "Open global chat" }).click();
    await expect(page.getByRole("heading", { name: "Global Chat" })).toBeVisible();
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
