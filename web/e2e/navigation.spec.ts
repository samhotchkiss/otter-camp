import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";
import { installCoreDataApiMocks } from "./helpers/coreDataRoutes";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
    await installCoreDataApiMocks(page);
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
    await expect(page.getByRole("tab", { name: "All (3)" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Unread (2)" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Starred (0)" })).toBeVisible();
    await expect(page.getByText("Deploy frontend")).toBeVisible();

    await page.getByRole("button", { name: "Toggle star for Deploy frontend" }).click();
    await expect(page.getByRole("tab", { name: "Starred (1)" })).toBeVisible();
    await page.getByRole("tab", { name: "Starred (1)" }).click();
    await expect(page.getByText("Deploy frontend")).toBeVisible();
  });

  test("figma-parity-projects baseline cards and activity render", async ({ page }) => {
    await page.goto("/projects");

    await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
    await expect(page.getByText("Git-backed repositories & tracking")).toBeVisible();
    await expect(page.getByRole("button", { name: "New Project" })).toBeVisible();
    const customerPortalCard = page.getByTestId("project-card-project-1");
    await expect(customerPortalCard).toBeVisible();
    await expect(customerPortalCard.getByText("Customer Portal")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Recent Activity" })).toBeVisible();

    await customerPortalCard.click();
    await expect(page).toHaveURL(/\/projects\/project-1$/);
  });

  test("figma-parity-project-detail baseline right rail and explorer render", async ({ page }) => {
    await page.goto("/projects/project-2");

    await expect(page.getByRole("heading", { name: "API Gateway" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Open Issues" })).toBeVisible();
    await expect(page.getByTestId("project-detail-right-rail")).toBeVisible();
    await expect(page.getByTestId("project-detail-file-explorer")).toBeVisible();
    const readmeLink = page.getByRole("link", { name: /README\.md/i });
    await expect(readmeLink).toBeVisible();

    await readmeLink.click();
    await expect(page).toHaveURL(/\/review\/docs%2FREADME\.md$/);
  });

  test("figma-parity-issue route renders baseline issue detail surface", async ({ page }) => {
    await page.goto("/issue/ISS-209");

    const issueShell = page.getByTestId("issue-detail-shell");
    const header = issueShell.locator("header").first();
    await expect(page.getByRole("heading", { name: "Fix API rate limiting", exact: true })).toBeVisible();
    await expect(issueShell.getByText("Issue #209")).toBeVisible();
    await expect(issueShell.getByText(/^Ready for Review$/).first()).toBeVisible();
    await expect(header.getByRole("button", { name: "Approve", exact: true })).toBeVisible();
    await expect(header.getByRole("button", { name: "Request Changes", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Issue context" })).toBeVisible();
    await expect(page.getByTestId("issue-thread-shell")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Linked document" })).toBeVisible();
  });

  test("figma-parity-review route renders baseline content review surface", async ({ page }) => {
    await page.goto("/review/docs%2Frate-limiting-implementation.md");

    await expect(page.getByRole("heading", { name: "Content Review" })).toBeVisible();
    await expect(page.getByTestId("content-review-route-path")).toContainText("docs/rate-limiting-implementation.md");
    await expect(page.getByRole("button", { name: "Mark Ready for Review" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Markdown Review Session" })).toBeVisible();
    await expect(page.getByTestId("review-line-lane")).toBeVisible();
    await expect(page.getByTestId("review-comment-sidebar")).toBeVisible();
  });

  test("issue-wiring route uses API issue context and approval actions", async ({ page }) => {
    await page.goto("/issue/ISS-209");

    const issueShell = page.getByTestId("issue-detail-shell");
    const header = issueShell.locator("header").first();
    await expect(issueShell.getByRole("heading", { name: "Fix API rate limiting", exact: true })).toBeVisible();
    await expect(issueShell.getByText("Issue #209")).toBeVisible();
    await expect(issueShell.getByText(/^Ready for Review$/).first()).toBeVisible();

    await header.getByRole("button", { name: "Approve", exact: true }).click();
    await expect(issueShell.getByRole("status")).toContainText("Issue approved.");
  });

  test("review-wiring linked review route loads issue context and transitions state", async ({ page }) => {
    await page.goto("/review/posts%2Frate-limiting-implementation.md?project_id=project-2&issue_id=issue-209");

    await expect(page.getByTestId("content-review-linked-issue")).toContainText("Linked issue: issue-209");
    await expect(page.getByTestId("content-review-linked-issue")).toContainText("Project: project-2");
    await expect(page.getByTestId("content-review-linked-issue")).toContainText("Comments: 0");

    await page.getByRole("button", { name: "Mark Ready for Review" }).click();
    await page.getByRole("button", { name: "Request Changes" }).click();
    await expect(page.getByText("Changes requested.")).toBeVisible();
  });

  test("file-explorer markdown links open review route with encoded path continuity", async ({ page }) => {
    await page.goto("/projects/project-2");

    const readmeLink = page.getByRole("link", { name: /README\.md/i });
    await expect(readmeLink).toBeVisible();
    await readmeLink.click();

    await expect(page).toHaveURL(/\/review\/docs%2FREADME\.md$/);
    await expect(page.getByRole("heading", { name: "Content Review" })).toBeVisible();
  });

  test("shows primary shell navigation links", async ({ page }) => {
    await page.goto("/");

    const sidebar = page.getByTestId("shell-sidebar");
    await expect(sidebar).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Inbox" })).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Projects", exact: true })).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Memory quick nav" })).toBeVisible();
    await expect(sidebar.getByRole("link", { name: "Operations quick nav" })).toBeVisible();
  });

  test("navigates between available primary routes", async ({ page }) => {
    await page.goto("/");

    await page.getByRole("link", { name: "Projects", exact: true }).click();
    await expect(page).toHaveURL(/\/projects$/);

    await page.getByRole("link", { name: "Inbox" }).click();
    await expect(page).toHaveURL(/\/inbox$/);

    await page.getByRole("link", { name: "Memory quick nav" }).click();
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

  test("figma-parity-chat dock open and collapse states render", async ({ page }) => {
    await page.goto("/inbox");

    await expect(page.getByRole("button", { name: "Open global chat" })).toBeVisible();
    await page.getByRole("button", { name: "Open global chat" }).click();
    await expect(page.getByRole("heading", { name: "Global Chat" })).toBeVisible();
    await expect(page.getByTestId("global-chat-context-cue")).toContainText("Main context");

    await page.getByRole("button", { name: "Collapse global chat" }).click();
    await expect(page.getByRole("button", { name: "Open global chat" })).toBeVisible();
  });

  test("figma-parity-secondary routes render baseline surfaces", async ({ page }) => {
    await page.goto("/agents");
    await expect(page.getByRole("heading", { name: "Agent Status" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Chameleon Agents (On-Demand)" })).toBeVisible();

    await page.goto("/knowledge");
    await expect(page.getByRole("heading", { name: "Memory System" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Stream: Conversation Extraction" })).toBeVisible();

    await page.goto("/connections");
    await expect(page.getByRole("heading", { name: "Operations" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "OpenClaw Bridge" })).toBeVisible();
  });

  test("core-data-wiring-inbox approve and reject actions update API-backed rows", async ({ page }) => {
    const decisions: string[] = [];
    await page.route("**/api/approvals/exec/*/respond**", async (route) => {
      const body = route.request().postDataJSON() as { action?: string } | null;
      const action = typeof body?.action === "string" ? body.action : "";
      decisions.push(action);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
    });

    await page.goto("/inbox");
    await expect(page.getByText("Deploy frontend")).toBeVisible();
    await expect(page.getByText("Publish package")).toBeVisible();

    await page.getByRole("button", { name: "Approve Deploy frontend" }).click();
    await expect(page.getByText("Deploy frontend")).not.toBeVisible();

    await page.getByRole("button", { name: "Reject Publish package" }).click();
    await expect(page.getByText("Publish package")).not.toBeVisible();
    await expect(page.getByText("Nightly sync complete")).toBeVisible();
    expect(decisions).toEqual(["approve", "reject"]);
  });

  test("figma-parity-core projects and project-detail render API-backed baseline surfaces", async ({ page }) => {
    await page.goto("/projects");

    await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
    await expect(page.getByTestId("project-card-project-1")).toBeVisible();
    await expect(page.getByTestId("project-card-project-2")).toBeVisible();
    await expect(page.getByText("2 item(s) awaiting review")).toBeVisible();

    await page.getByTestId("project-card-project-2").click();
    await expect(page).toHaveURL(/\/projects\/project-2$/);
    await expect(page.getByRole("heading", { name: "API Gateway" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Open Issues" })).toBeVisible();
    await expect(page.getByTestId("shell-workspace").getByText("Fix API rate limiting")).toBeVisible();
    await expect(page.getByTestId("project-detail-right-rail")).toBeVisible();
  });
});
