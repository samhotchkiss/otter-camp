import { test, expect } from "@playwright/test";

test.describe("Global Search / Command Palette", () => {
  test.beforeEach(async ({ page }) => {
    // Set up authenticated state
    await page.goto("/");
    await page.evaluate(() => {
      const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZW1haWwiOiJ0ZXN0QGV4YW1wbGUuY29tIiwibmFtZSI6IlRlc3QgVXNlciIsImV4cCI6OTk5OTk5OTk5OX0.test";
      const user = { id: "1", email: "test@example.com", name: "Test User" };
      localStorage.setItem("otter_camp_token", token);
      localStorage.setItem("otter_camp_user", JSON.stringify(user));
    });
    await page.goto("/");
  });

  test.describe("Opening Command Palette", () => {
    test("should open command palette with Cmd/Ctrl+K", async ({ page }) => {
      // Use Cmd+K on Mac, Ctrl+K on Windows/Linux
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const commandPalette = page.getByRole("dialog", { name: /command palette/i });
      await expect(commandPalette).toBeVisible();
    });

    test("should open command palette via search button", async ({ page }) => {
      const searchButton = page.getByRole("button", { name: /search|command/i });

      if (await searchButton.isVisible()) {
        await searchButton.click();

        const commandPalette = page.getByRole("dialog", { name: /command palette/i });
        await expect(commandPalette).toBeVisible();
      }
    });

    test("should close command palette with Escape", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const commandPalette = page.getByRole("dialog", { name: /command palette/i });
      await expect(commandPalette).toBeVisible();

      await page.keyboard.press("Escape");
      await expect(commandPalette).not.toBeVisible();
    });

    test("should close command palette by clicking overlay", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const commandPalette = page.getByRole("dialog", { name: /command palette/i });
      await expect(commandPalette).toBeVisible();

      // Click the overlay (outside the dialog)
      await page.locator(".command-palette-overlay").click({ position: { x: 10, y: 10 } });
      await expect(commandPalette).not.toBeVisible();
    });

    test("should auto-focus search input when opened", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await expect(searchInput).toBeFocused();
    });
  });

  test.describe("Search Functionality", () => {
    test("should filter commands as user types", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("settings");

      // Should show settings-related results
      const results = page.getByRole("option");
      const resultsCount = await results.count();
      expect(resultsCount).toBeGreaterThan(0);

      // Results should contain "settings"
      await expect(results.first()).toContainText(/settings/i);
    });

    test("should show no results message for invalid search", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("xyznonexistent123");

      // Should show no results message
      await expect(page.getByText(/no matches|no results/i)).toBeVisible();
    });

    test("should display commands grouped by category", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      // Should show category headings
      await expect(page.getByText(/navigation/i)).toBeVisible();
    });

    test("should execute navigation command and close palette", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("agents");

      const agentsOption = page.getByRole("option").filter({ hasText: /agents/i }).first();
      await agentsOption.click();

      // Palette should close and navigate to agents
      const commandPalette = page.getByRole("dialog", { name: /command palette/i });
      await expect(commandPalette).not.toBeVisible();
      await expect(page).toHaveURL(/\/agents/);
    });
  });

  test.describe("Keyboard Navigation", () => {
    test("should navigate options with arrow keys", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      // Wait for palette to be visible
      const commandPalette = page.getByRole("dialog", { name: /command palette/i });
      await expect(commandPalette).toBeVisible();

      // Press down arrow to move selection
      await page.keyboard.press("ArrowDown");

      // Second option should be selected
      const options = page.getByRole("option");
      const secondOption = options.nth(1);
      await expect(secondOption).toHaveAttribute("aria-selected", "true");
    });

    test("should navigate up with arrow up key", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      // Move down first
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowDown");

      // Then move up
      await page.keyboard.press("ArrowUp");

      // Second option should be selected
      const options = page.getByRole("option");
      const secondOption = options.nth(1);
      await expect(secondOption).toHaveAttribute("aria-selected", "true");
    });

    test("should not go past first option when pressing up", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      // Press up multiple times
      await page.keyboard.press("ArrowUp");
      await page.keyboard.press("ArrowUp");

      // First option should still be selected
      const firstOption = page.getByRole("option").first();
      await expect(firstOption).toHaveAttribute("aria-selected", "true");
    });

    test("should execute selected command with Enter", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("settings");

      // Press Enter to execute first result
      await page.keyboard.press("Enter");

      // Should navigate to settings
      await expect(page).toHaveURL(/\/settings/);
    });

    test("should maintain focus within palette (focus trap)", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      // Tab multiple times
      await page.keyboard.press("Tab");
      await page.keyboard.press("Tab");
      await page.keyboard.press("Tab");

      // Focus should still be within the palette
      const commandPalette = page.getByRole("dialog", { name: /command palette/i });
      const focusedElement = page.locator(":focus");
      await expect(commandPalette).toContainText(await focusedElement.textContent() || "");
    });
  });

  test.describe("Recent Searches", () => {
    test("should show recent commands section", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";

      // Execute a command first
      await page.keyboard.press(`${modifier}+KeyK`);
      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("settings");
      await page.keyboard.press("Enter");

      // Wait for navigation
      await expect(page).toHaveURL(/\/settings/);

      // Open palette again
      await page.keyboard.press(`${modifier}+KeyK`);

      // Should show recent section
      await expect(page.getByText(/recent/i)).toBeVisible();
    });

    test("should show recently executed commands at top", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";

      // Execute a command
      await page.keyboard.press(`${modifier}+KeyK`);
      let searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("agents");
      await page.keyboard.press("Enter");
      await expect(page).toHaveURL(/\/agents/);

      // Open palette again
      await page.keyboard.press(`${modifier}+KeyK`);

      // Recent section should contain the previously executed command
      const recentSection = page.locator("section").filter({ hasText: /recent/i });
      if (await recentSection.isVisible()) {
        await expect(recentSection.getByText(/agents/i)).toBeVisible();
      }
    });

    test("should filter recent commands when searching", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";

      // Execute some commands to populate recent
      await page.keyboard.press(`${modifier}+KeyK`);
      await page.getByRole("combobox", { name: /search commands/i }).fill("settings");
      await page.keyboard.press("Enter");
      await expect(page).toHaveURL(/\/settings/);

      // Open palette and search
      await page.keyboard.press(`${modifier}+KeyK`);
      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("agents");

      // Recent section should not show (search results take over)
      const resultsSection = page.locator("section").filter({ hasText: /results/i });
      await expect(resultsSection).toBeVisible();
    });

    test("should clear recent when search input has text", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";

      // Open palette
      await page.keyboard.press(`${modifier}+KeyK`);

      // Type something
      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await searchInput.fill("test");

      // Recent section should be replaced with results
      const recentHeading = page.getByText(/^recent$/i);
      await expect(recentHeading).not.toBeVisible();
    });
  });

  test.describe("Accessibility", () => {
    test("should have proper ARIA attributes", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const dialog = page.getByRole("dialog", { name: /command palette/i });
      await expect(dialog).toHaveAttribute("aria-modal", "true");

      const searchInput = page.getByRole("combobox", { name: /search commands/i });
      await expect(searchInput).toHaveAttribute("aria-expanded", "true");
      await expect(searchInput).toHaveAttribute("aria-autocomplete", "list");
    });

    test("should announce active descendant to screen readers", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      const searchInput = page.getByRole("combobox", { name: /search commands/i });

      // Navigate to an option
      await page.keyboard.press("ArrowDown");

      // Active descendant should be set
      const activeDescendant = await searchInput.getAttribute("aria-activedescendant");
      expect(activeDescendant).toBeTruthy();
    });

    test("should have descriptive text for screen readers", async ({ page }) => {
      const modifier = process.platform === "darwin" ? "Meta" : "Control";
      await page.keyboard.press(`${modifier}+KeyK`);

      // Should have description
      const description = page.locator("#command-palette-description");
      await expect(description).toBeAttached();
    });
  });
});
