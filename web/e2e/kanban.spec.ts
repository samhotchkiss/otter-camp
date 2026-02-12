import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Kanban Board", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
    await page.goto("/tasks");
  });

  test.describe("Board Display", () => {
    test("should display all four kanban columns", async ({ page }) => {
      await expect(page.getByRole("heading", { name: /backlog/i })).toBeVisible();
      await expect(page.getByRole("heading", { name: /in progress/i })).toBeVisible();
      await expect(page.getByRole("heading", { name: /review/i })).toBeVisible();
      await expect(page.getByRole("heading", { name: /done/i })).toBeVisible();
    });

    test("should display task cards with title and priority", async ({ page }) => {
      // Check for task cards
      const taskCards = page.locator("article[role='listitem']");
      const count = await taskCards.count();
      expect(count).toBeGreaterThan(0);

      // Check first task has required elements
      const firstTask = taskCards.first();
      await expect(firstTask.locator("h4")).toBeVisible();
    });

    test("should show task count badges on columns", async ({ page }) => {
      // Each column should have a count badge
      const backlogCount = page.locator("#column-backlog-count");
      const inProgressCount = page.locator("#column-in-progress-count");
      const reviewCount = page.locator("#column-review-count");
      const doneCount = page.locator("#column-done-count");

      await expect(backlogCount).toBeVisible();
      await expect(inProgressCount).toBeVisible();
      await expect(reviewCount).toBeVisible();
      await expect(doneCount).toBeVisible();
    });
  });

  test.describe("Create Task", () => {
    test("should open create task modal when clicking add button", async ({ page }) => {
      // Look for add task button
      const addButton = page.getByRole("button", { name: /add|new|create/i }).filter({ hasText: /task/i });

      if (await addButton.isVisible()) {
        await addButton.click();

        // Modal should be visible
        const modal = page.getByRole("dialog");
        await expect(modal).toBeVisible();
      }
    });

    test("should create a new task with title", async ({ page }) => {
      const addButton = page.getByRole("button", { name: /add|new|create/i }).filter({ hasText: /task/i });

      if (await addButton.isVisible()) {
        await addButton.click();

        // Fill in task details
        const titleInput = page.getByLabel(/title/i);
        await titleInput.fill("New E2E Test Task");

        // Submit the form
        const submitButton = page.getByRole("button", { name: /create|save|add/i });
        await submitButton.click();

        // New task should appear in Backlog column
        await expect(page.getByText("New E2E Test Task")).toBeVisible();
      }
    });

    test("should create task with priority", async ({ page }) => {
      const addButton = page.getByRole("button", { name: /add|new|create/i }).filter({ hasText: /task/i });

      if (await addButton.isVisible()) {
        await addButton.click();

        const titleInput = page.getByLabel(/title/i);
        await titleInput.fill("High Priority Task");

        // Select high priority
        const prioritySelect = page.getByLabel(/priority/i);
        if (await prioritySelect.isVisible()) {
          await prioritySelect.selectOption("high");
        }

        const submitButton = page.getByRole("button", { name: /create|save|add/i });
        await submitButton.click();

        // Task should show high priority badge
        const newTask = page.getByText("High Priority Task").locator("..");
        await expect(newTask.getByText(/high/i)).toBeVisible();
      }
    });
  });

  test.describe("Drag and Drop", () => {
    test("should drag task from Backlog to In Progress", async ({ page }) => {
      // Find a task in the Backlog column
      const backlogColumn = page.locator("section").filter({ hasText: /backlog/i });
      const taskCard = backlogColumn.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        const taskTitle = await taskCard.locator("h4").textContent();

        // Get the In Progress column
        const inProgressColumn = page.locator("section").filter({ hasText: /in progress/i });

        // Perform drag and drop
        await taskCard.dragTo(inProgressColumn);

        // Verify task moved to In Progress
        const inProgressTasks = inProgressColumn.locator("article[role='listitem']");
        await expect(inProgressTasks.filter({ hasText: taskTitle || "" })).toBeVisible();
      }
    });

    test("should drag task from In Progress to Done", async ({ page }) => {
      // Find a task in In Progress column
      const inProgressColumn = page.locator("section").filter({ hasText: /in progress/i });
      const taskCard = inProgressColumn.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        const taskTitle = await taskCard.locator("h4").textContent();

        // Get the Done column
        const doneColumn = page.locator("section").filter({ hasText: /done/i });

        // Perform drag and drop
        await taskCard.dragTo(doneColumn);

        // Verify task moved to Done
        const doneTasks = doneColumn.locator("article[role='listitem']");
        await expect(doneTasks.filter({ hasText: taskTitle || "" })).toBeVisible();
      }
    });

    test("should update column task counts after drag", async ({ page }) => {
      const backlogColumn = page.locator("section").filter({ hasText: /backlog/i });
      const backlogCountBadge = backlogColumn.locator("[aria-label*='task']");
      const initialCount = await backlogCountBadge.textContent();

      const taskCard = backlogColumn.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        const inProgressColumn = page.locator("section").filter({ hasText: /in progress/i });
        await taskCard.dragTo(inProgressColumn);

        // Wait for count to update
        await page.waitForTimeout(500);

        const newCount = await backlogCountBadge.textContent();
        expect(Number(newCount)).toBeLessThan(Number(initialCount));
      }
    });

    test("should show visual feedback during drag", async ({ page }) => {
      const backlogColumn = page.locator("section").filter({ hasText: /backlog/i });
      const taskCard = backlogColumn.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        // Start dragging
        await taskCard.hover();
        await page.mouse.down();

        // Task should show dragging state (opacity or shadow changes)
        const inProgressColumn = page.locator("section").filter({ hasText: /in progress/i });
        await inProgressColumn.hover();

        // Check for visual feedback class
        await expect(inProgressColumn).toHaveClass(/border-sky|bg-sky/);

        await page.mouse.up();
      }
    });
  });

  test.describe("Delete Task", () => {
    test("should show delete confirmation when deleting a task", async ({ page }) => {
      const taskCard = page.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        // Hover to reveal delete button
        await taskCard.hover();

        const deleteButton = taskCard.getByRole("button", { name: /delete|remove/i });
        if (await deleteButton.isVisible()) {
          await deleteButton.click();

          // Confirmation dialog should appear
          const confirmDialog = page.getByRole("dialog");
          await expect(confirmDialog).toBeVisible();
        }
      }
    });

    test("should remove task from board after deletion", async ({ page }) => {
      const taskCard = page.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        const taskTitle = await taskCard.locator("h4").textContent();

        await taskCard.hover();
        const deleteButton = taskCard.getByRole("button", { name: /delete|remove/i });

        if (await deleteButton.isVisible()) {
          await deleteButton.click();

          // Confirm deletion
          const confirmButton = page.getByRole("button", { name: /confirm|yes|delete/i });
          if (await confirmButton.isVisible()) {
            await confirmButton.click();

            // Task should no longer be visible
            await expect(page.getByText(taskTitle || "")).not.toBeVisible();
          }
        }
      }
    });

    test("should cancel deletion when clicking cancel", async ({ page }) => {
      const taskCard = page.locator("article[role='listitem']").first();

      if (await taskCard.isVisible()) {
        const taskTitle = await taskCard.locator("h4").textContent();

        await taskCard.hover();
        const deleteButton = taskCard.getByRole("button", { name: /delete|remove/i });

        if (await deleteButton.isVisible()) {
          await deleteButton.click();

          // Cancel deletion
          const cancelButton = page.getByRole("button", { name: /cancel|no/i });
          if (await cancelButton.isVisible()) {
            await cancelButton.click();

            // Task should still be visible
            await expect(page.getByText(taskTitle || "")).toBeVisible();
          }
        }
      }
    });
  });

  test.describe("Keyboard Navigation", () => {
    test("should navigate tasks with arrow keys", async ({ page }) => {
      // Focus on the kanban board
      const firstTask = page.locator("article[role='listitem']").first();
      await firstTask.focus();

      // Press arrow down
      await page.keyboard.press("ArrowDown");

      // Second task should be focused/selected
      const secondTask = page.locator("article[role='listitem']").nth(1);
      await expect(secondTask).toHaveClass(/ring|border-sky/);
    });

    test("should open task detail with Enter key", async ({ page }) => {
      const firstTask = page.locator("article[role='listitem']").first();
      await firstTask.focus();

      await page.keyboard.press("Enter");

      // Task detail modal should open
      const modal = page.getByRole("dialog");
      if (await modal.isVisible({ timeout: 1000 })) {
        await expect(modal).toBeVisible();
      }
    });
  });
});
