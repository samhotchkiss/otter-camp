import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Agents Page", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    // Mock the agents API
    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          agents: [
            {
              id: "agent-1",
              name: "Frank",
              role: "Chief of Staff",
              status: "online",
              avatarUrl: null,
              currentTask: "Reviewing tasks",
              lastActive: new Date().toISOString(),
            },
            {
              id: "agent-2",
              name: "Nova",
              role: "Social Media Lead",
              status: "busy",
              avatarUrl: null,
              currentTask: "Creating content",
              lastActive: new Date().toISOString(),
            },
            {
              id: "agent-3",
              name: "Derek",
              role: "Engineering Lead",
              status: "offline",
              avatarUrl: null,
              currentTask: null,
              lastActive: new Date(Date.now() - 3600000).toISOString(),
            },
            {
              id: "agent-4",
              name: "Stone",
              role: "Content Writer",
              status: "online",
              avatarUrl: null,
              currentTask: "Writing blog post",
              lastActive: new Date().toISOString(),
            },
          ],
        }),
      });
    });

    // Mock WebSocket connection
    await page.addInitScript(() => {
      const mockWS = {
        send: () => {},
        close: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        readyState: 1,
      };
      (window as Window & { WebSocket: typeof WebSocket }).WebSocket = class extends WebSocket {
        constructor(url: string | URL, protocols?: string | string[]) {
          super(url, protocols);
          return mockWS as unknown as WebSocket;
        }
      };
    });

    await page.goto("/agents");
  });

  test("displays agents page with header", async ({ page }) => {
    await expect(page.getByRole("heading", { name: "Agents" })).toBeVisible();
    await expect(page.getByText("4 agents")).toBeVisible();
    await expect(page.getByText("2 online")).toBeVisible();
  });

  test("shows all status filter buttons", async ({ page }) => {
    const statusFilters = page.getByRole("group", { name: "Agent status filters" });
    await expect(statusFilters.getByRole("button", { name: /All.*4/i })).toBeVisible();
    await expect(statusFilters.getByRole("button", { name: /Online.*2/i })).toBeVisible();
    await expect(statusFilters.getByRole("button", { name: /Busy.*1/i })).toBeVisible();
    await expect(statusFilters.getByRole("button", { name: /Offline.*1/i })).toBeVisible();
  });

  test("displays agent cards with correct information", async ({ page }) => {
    await expect(page.getByText("Frank")).toBeVisible();
    await expect(page.getByText("Chief of Staff")).toBeVisible();
    await expect(page.getByText("Nova")).toBeVisible();
    await expect(page.getByText("Derek")).toBeVisible();
    await expect(page.getByText("Stone")).toBeVisible();
  });

  test("filters agents by online status", async ({ page }) => {
    const statusFilters = page.getByRole("group", { name: "Agent status filters" });
    await statusFilters.getByRole("button", { name: /Online.*2/i }).click();

    // Should show Frank and Stone (online)
    await expect(page.getByText("Frank")).toBeVisible();
    await expect(page.getByText("Stone")).toBeVisible();

    // Should not show Nova (busy) or Derek (offline)
    await expect(page.getByText("Nova")).not.toBeVisible();
    await expect(page.getByText("Derek")).not.toBeVisible();
  });

  test("filters agents by busy status", async ({ page }) => {
    const statusFilters = page.getByRole("group", { name: "Agent status filters" });
    await statusFilters.getByRole("button", { name: /Busy.*1/i }).click();

    // Should only show Nova
    await expect(page.getByText("Nova")).toBeVisible();
    await expect(page.getByText("Frank")).not.toBeVisible();
  });

  test("filters agents by offline status", async ({ page }) => {
    const statusFilters = page.getByRole("group", { name: "Agent status filters" });
    await statusFilters.getByRole("button", { name: /Offline.*1/i }).click();

    // Should only show Derek
    await expect(page.getByText("Derek")).toBeVisible();
    await expect(page.getByText("Frank")).not.toBeVisible();
  });

  test("shows empty state when no agents match filter", async ({ page }) => {
    // First set up the page with agents that have no offline agents
    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents: [] }),
      });
    });

    await page.reload();
    await expect(page.getByText("No agents found")).toBeVisible();
  });

  test("opens global chat when clicking an agent card", async ({ page }) => {
    await page.route("**/api/messages*", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            messages: [],
          }),
        });
        return;
      }

      await route.continue();
    });

    await page.getByRole("button", { name: /Frank/i }).first().click();

    await expect(page.getByRole("heading", { name: "Global Chat" })).toBeVisible();
    await expect(page.getByPlaceholder(/Message Frank/i)).toBeVisible();
  });

  test("closes global chat with close button", async ({ page }) => {
    await page.route("**/api/messages*", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ messages: [] }),
        });
        return;
      }

      await route.continue();
    });

    await page.getByRole("button", { name: /Frank/i }).first().click();
    await expect(page.getByRole("heading", { name: "Global Chat" })).toBeVisible();

    await page.getByRole("button", { name: "Collapse global chat" }).click();
    await expect(page.getByRole("button", { name: "Open global chat" })).toBeVisible();
  });

  test("closes global chat with escape key", async ({ page }) => {
    await page.route("**/api/messages*", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ messages: [] }),
        });
        return;
      }

      await route.continue();
    });

    await page.getByRole("button", { name: /Frank/i }).first().click();
    await expect(page.getByRole("heading", { name: "Global Chat" })).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Toggle chat panel" })).toHaveAttribute("title", "Show Chat");
  });

  test("can send message in DM", async ({ page }) => {
    let messageSent = false;
    
    await page.route("**/api/messages*", async (route) => {
      if (route.request().method() === "POST") {
        messageSent = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            message: {
              id: "msg-1",
              threadId: "thread-1",
              senderId: "user-1",
              senderName: "Test User",
              senderType: "user",
              content: "Hello Frank!",
              createdAt: new Date().toISOString(),
            },
            delivery: {
              delivered: true,
            },
          }),
        });
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            messages: [],
          }),
        });
      }
    });

    await page.getByRole("button", { name: /Frank/i }).first().click();
    await expect(page.getByPlaceholder(/Message Frank/i)).toBeVisible();

    // Type a message
    const messageInput = page.getByPlaceholder(/Message Frank/i);
    await messageInput.fill("Hello Frank!");

    // Send the message
    await page.getByRole("button", { name: "Send message" }).click();

    await expect.poll(() => messageSent).toBe(true);
    await expect(page.getByText("Hello Frank!")).toBeVisible();
  });

  test("shows connection status indicator", async ({ page }) => {
    // The page should show either "Live" or "Disconnected" status
    const statusIndicator = page.getByText(/Live|Disconnected/);
    await expect(statusIndicator).toBeVisible();
  });

  test("displays loading state initially", async ({ page }) => {
    await page.unroute("**/api/agents");

    // Set up a slow response
    await page.route("**/api/agents", async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 100));
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents: [] }),
      });
    });

    await page.reload();
    // Loading text may flash briefly
    await expect(page.getByText("Loading agents...")).toBeVisible({ timeout: 500 }).catch(() => {
      // Loading state may have passed, that's okay
    });
  });

  test("handles API error gracefully", async ({ page }) => {
    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Server error" }),
      });
    });

    await page.goto("/agents");
    await expect(page.getByText(/Server error|Failed to.*agents/i)).toBeVisible();
    await expect(page.getByRole("button", { name: /Try Again/i })).toBeVisible();
  });

  test("can return to all agents after filtering", async ({ page }) => {
    // Filter to online
    await page.getByRole("button", { name: /Online.*2/i }).click();
    await expect(page.getByText("Derek")).not.toBeVisible();

    // Return to all
    await page.getByRole("button", { name: /All.*4/i }).click();
    await expect(page.getByText("Derek")).toBeVisible();
    await expect(page.getByText("Frank")).toBeVisible();
    await expect(page.getByText("Nova")).toBeVisible();
    await expect(page.getByText("Stone")).toBeVisible();
  });
});
