import { test, expect, type Page } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

async function openGlobalSearchWithShortcut(page: Page) {
  const shortcut = process.platform === "darwin" ? "Meta+KeyK" : "Control+KeyK";
  await page.keyboard.press(shortcut);
  await expect(page.getByRole("dialog", { name: /global search/i })).toBeVisible();
}

async function openGlobalSearchWithButton(page: Page) {
  await page.getByRole("button", { name: /search or command/i }).click();
  await expect(page.getByRole("dialog", { name: /global search/i })).toBeVisible();
}

async function waitForSearchQuery(page: Page, query: string) {
  await page.waitForResponse((response) => {
    if (!response.url().includes("/api/search")) {
      return false;
    }
    const requestURL = new URL(response.url());
    return (requestURL.searchParams.get("q") ?? "").toLowerCase() === query.toLowerCase();
  });
}

test.describe("Global Search", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    await page.route("**/api/search**", async (route) => {
      const requestURL = new URL(route.request().url());
      const query = (requestURL.searchParams.get("q") ?? "").toLowerCase();

      const payload = {
        query,
        results: {
          tasks: [] as Array<Record<string, unknown>>,
          projects: [] as Array<Record<string, unknown>>,
          agents: [] as Array<Record<string, unknown>>,
          messages: [] as Array<Record<string, unknown>>,
        },
      };

      if (query.includes("task")) {
        payload.results.tasks.push({
          id: "task-1",
          number: 1,
          title: "Set up camp perimeter",
          status: "todo",
          priority: "high",
        });
      }

      if (query.includes("project")) {
        payload.results.projects.push({
          id: "project-1",
          name: "Otter Camp Project",
          status: "active",
        });
      }

      if (query.includes("agent")) {
        payload.results.agents.push({
          id: "agent-1",
          slug: "camp-ranger",
          display_name: "Camp Ranger",
          status: "active",
        });
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(payload),
      });
    });

    await page.goto("/");
    await expect(page.getByRole("button", { name: /search or command/i })).toBeVisible();
  });

  test("opens with keyboard shortcut and focuses search input", async ({ page }) => {
    await openGlobalSearchWithShortcut(page);

    const input = page.getByRole("textbox", { name: /^Search$/i });
    await expect(input).toBeFocused();
  });

  test("closes with escape", async ({ page }) => {
    await openGlobalSearchWithButton(page);

    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: /global search/i })).not.toBeVisible();
  });

  test("filters API search results as user types", async ({ page }) => {
    await openGlobalSearchWithButton(page);

    const input = page.getByRole("textbox", { name: /^Search$/i });
    await input.fill("project");
    await waitForSearchQuery(page, "project");

    await expect(page.getByRole("button", { name: /otter camp project/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /camp ranger/i })).not.toBeVisible();
  });

  test("executes a search result navigation", async ({ page }) => {
    await openGlobalSearchWithButton(page);

    const input = page.getByRole("textbox", { name: /^Search$/i });
    await input.fill("project");
    await waitForSearchQuery(page, "project");

    await page.getByRole("button", { name: /otter camp project/i }).click();
    await expect(page).toHaveURL(/\/projects\/project-1$/);
  });

  test("shows recent searches after selecting a result", async ({ page }) => {
    await openGlobalSearchWithButton(page);

    const input = page.getByRole("textbox", { name: /^Search$/i });
    await input.fill("task");
    await waitForSearchQuery(page, "task");

    await page.getByRole("button", { name: /set up camp perimeter/i }).click();
    await expect(page).toHaveURL(/\/tasks\/task-1$/);

    await openGlobalSearchWithButton(page);
    const dialog = page.getByRole("dialog", { name: /global search/i });
    await expect(dialog.getByText(/recent searches/i)).toBeVisible();
    await expect(dialog.getByText(/^task$/i)).toBeVisible();
  });
});
