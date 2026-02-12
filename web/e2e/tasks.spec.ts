import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Tasks Page - Task Detail View", () => {
  const mockTask = {
    id: "task-1",
    title: "Implement new feature",
    description: "Add the **new feature** with the following requirements:\n\n- Feature A\n- Feature B\n- Feature C",
    status: "in-progress",
    priority: "high",
    assignee: {
      id: "user-1",
      name: "Derek",
      avatarUrl: null,
    },
    dueDate: new Date(Date.now() + 86400000 * 3).toISOString(), // 3 days from now
    labels: [
      { id: "label-1", name: "feature", color: "#3b82f6" },
      { id: "label-2", name: "priority", color: "#ef4444" },
    ],
    attachments: [
      {
        id: "att-1",
        filename: "design-spec.pdf",
        size_bytes: 1024000,
        mime_type: "application/pdf",
        url: "/files/design-spec.pdf",
        uploadedAt: new Date().toISOString(),
        uploadedBy: "Test User",
      },
    ],
    activities: [
      {
        id: "act-1",
        type: "created",
        actor: "Test User",
        timestamp: new Date(Date.now() - 86400000).toISOString(),
      },
      {
        id: "act-2",
        type: "status_changed",
        actor: "Derek",
        timestamp: new Date(Date.now() - 3600000).toISOString(),
        oldValue: "todo",
        newValue: "in-progress",
      },
    ],
    createdAt: new Date(Date.now() - 86400000).toISOString(),
    updatedAt: new Date().toISOString(),
  };

  const mockTasks = [
    mockTask,
    {
      id: "task-2",
      title: "Fix bug in login",
      description: "Users report login issues",
      status: "todo",
      priority: "medium",
      createdAt: new Date().toISOString(),
    },
    {
      id: "task-3",
      title: "Write documentation",
      description: "Document the API endpoints",
      status: "done",
      priority: "low",
      createdAt: new Date().toISOString(),
    },
  ];

  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    // Mock tasks list API
    await page.route("**/api/tasks", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ tasks: mockTasks }),
        });
      } else {
        await route.continue();
      }
    });

    // Mock individual task API
    await page.route("**/api/tasks/*", async (route) => {
      const url = route.request().url();
      const taskId = url.split("/").pop();

      if (route.request().method() === "GET") {
        const task = mockTasks.find((t) => t.id === taskId) || mockTask;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ task }),
        });
      } else if (route.request().method() === "PATCH") {
        const body = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ task: { ...mockTask, ...body } }),
        });
      } else if (route.request().method() === "DELETE") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true }),
        });
      }
    });

    // Mock messages API for task thread
    await page.route("**/api/messages**", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            messages: [
              {
                id: "msg-1",
                taskId: "task-1",
                content: "Let me know if you need help!",
                authorId: "user-2",
                authorName: "Team Member",
                createdAt: new Date().toISOString(),
              },
            ],
            pagination: { hasMore: false },
          }),
        });
      } else if (route.request().method() === "POST") {
        const body = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            message: {
              id: "msg-new",
              taskId: "task-1",
              content: body?.content || "",
              authorId: "user-1",
              authorName: "Test User",
              createdAt: new Date().toISOString(),
            },
          }),
        });
      }
    });

    // Mock WebSocket
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

    await page.goto("/tasks");
  });

  test("displays tasks page with kanban board", async ({ page }) => {
    // Look for the main page content
    await expect(page.getByText("Implement new feature")).toBeVisible();
    await expect(page.getByText("Fix bug in login")).toBeVisible();
    await expect(page.getByText("Write documentation")).toBeVisible();
  });

  test.describe("Task Detail Panel", () => {
    test.beforeEach(async ({ page }) => {
      // Click on a task to open detail panel
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("displays task detail header", async ({ page }) => {
      await expect(page.getByRole("heading", { name: "Task Details" })).toBeVisible();
      await expect(page.getByRole("button", { name: /close/i })).toBeVisible();
    });

    test("displays task title", async ({ page }) => {
      await expect(page.getByRole("heading", { name: "Implement new feature" })).toBeVisible();
    });

    test("displays task description with markdown", async ({ page }) => {
      await expect(page.getByText("new feature")).toBeVisible();
      await expect(page.getByText("Feature A")).toBeVisible();
      await expect(page.getByText("Feature B")).toBeVisible();
    });

    test("displays status dropdown with current status", async ({ page }) => {
      const statusSelect = page.locator("select").filter({ hasText: "In Progress" });
      await expect(statusSelect).toBeVisible();
    });

    test("displays priority dropdown", async ({ page }) => {
      const prioritySelect = page.locator("select").filter({ hasText: /High|Medium|Low/i });
      await expect(prioritySelect.first()).toBeVisible();
    });

    test("displays assignee information", async ({ page }) => {
      await expect(page.getByText("Derek")).toBeVisible();
    });

    test("displays labels", async ({ page }) => {
      await expect(page.getByText("feature")).toBeVisible();
      await expect(page.getByText("priority")).toBeVisible();
    });

    test("displays due date", async ({ page }) => {
      await expect(page.getByText(/Due/)).toBeVisible();
    });

    test("can close detail panel with button", async ({ page }) => {
      await page.getByRole("button", { name: /close/i }).click();
      await expect(page.getByRole("dialog")).not.toBeVisible();
    });

    test("can close detail panel with escape key", async ({ page }) => {
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).not.toBeVisible();
    });
  });

  test.describe("Edit Task", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("shows edit button", async ({ page }) => {
      await expect(page.getByRole("button", { name: /edit/i })).toBeVisible();
    });

    test("can enter edit mode", async ({ page }) => {
      await page.getByRole("button", { name: /edit/i }).click();
      
      // In edit mode, should see input fields
      const titleInput = page.getByRole("textbox").filter({ hasText: /Implement new feature/i }).or(
        page.locator("input[value='Implement new feature']")
      );
      // Edit mode should show editable fields
      await expect(page.getByRole("button", { name: /save/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /cancel/i })).toBeVisible();
    });

    test("can edit task title", async ({ page }) => {
      await page.getByRole("button", { name: /edit/i }).click();

      const titleInput = page.locator("input").first();
      await titleInput.clear();
      await titleInput.fill("Updated task title");
      await expect(titleInput).toHaveValue("Updated task title");
    });

    test("can edit task description", async ({ page }) => {
      await page.getByRole("button", { name: /edit/i }).click();

      const descriptionInput = page.locator("textarea").first();
      await descriptionInput.clear();
      await descriptionInput.fill("Updated description");
      await expect(descriptionInput).toHaveValue("Updated description");
    });

    test("can save edited task", async ({ page }) => {
      let savedTask: Record<string, unknown> | null = null;

      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "PATCH") {
          savedTask = route.request().postDataJSON() as Record<string, unknown>;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ task: { ...mockTask, ...savedTask } }),
          });
        } else {
          await route.continue();
        }
      });

      await page.getByRole("button", { name: /edit/i }).click();

      const titleInput = page.locator("input").first();
      await titleInput.clear();
      await titleInput.fill("Updated task");

      await page.getByRole("button", { name: /save/i }).click();

      await page.waitForTimeout(500);
      expect(savedTask?.title).toBe("Updated task");
    });

    test("can cancel edit mode", async ({ page }) => {
      await page.getByRole("button", { name: /edit/i }).click();

      const titleInput = page.locator("input").first();
      await titleInput.clear();
      await titleInput.fill("This will be cancelled");

      await page.getByRole("button", { name: /cancel/i }).click();

      // Should exit edit mode
      await expect(page.getByRole("button", { name: /edit/i })).toBeVisible();
      // Original title should still be visible
      await expect(page.getByText("Implement new feature")).toBeVisible();
    });
  });

  test.describe("Change Status", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("can change status to todo", async ({ page }) => {
      let updatedStatus: string | null = null;

      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "PATCH") {
          const body = route.request().postDataJSON() as { status?: string };
          updatedStatus = body?.status || null;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ task: { ...mockTask, status: updatedStatus } }),
          });
        } else {
          await route.continue();
        }
      });

      const statusSelect = page.locator("select").first();
      await statusSelect.selectOption("todo");

      await page.waitForTimeout(500);
      expect(updatedStatus).toBe("todo");
    });

    test("can change status to done", async ({ page }) => {
      let updatedStatus: string | null = null;

      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "PATCH") {
          const body = route.request().postDataJSON() as { status?: string };
          updatedStatus = body?.status || null;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ task: { ...mockTask, status: updatedStatus } }),
          });
        } else {
          await route.continue();
        }
      });

      const statusSelect = page.locator("select").first();
      await statusSelect.selectOption("done");

      await page.waitForTimeout(500);
      expect(updatedStatus).toBe("done");
    });
  });

  test.describe("Change Priority", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("can change priority to low", async ({ page }) => {
      let updatedPriority: string | null = null;

      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "PATCH") {
          const body = route.request().postDataJSON() as { priority?: string };
          updatedPriority = body?.priority || null;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ task: { ...mockTask, priority: updatedPriority } }),
          });
        } else {
          await route.continue();
        }
      });

      // Find the priority select (second select after status)
      const prioritySelect = page.locator("select").nth(1);
      await prioritySelect.selectOption("low");

      await page.waitForTimeout(500);
      expect(updatedPriority).toBe("low");
    });

    test("can change priority to medium", async ({ page }) => {
      let updatedPriority: string | null = null;

      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "PATCH") {
          const body = route.request().postDataJSON() as { priority?: string };
          updatedPriority = body?.priority || null;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ task: { ...mockTask, priority: updatedPriority } }),
          });
        } else {
          await route.continue();
        }
      });

      const prioritySelect = page.locator("select").nth(1);
      await prioritySelect.selectOption("medium");

      await page.waitForTimeout(500);
      expect(updatedPriority).toBe("medium");
    });
  });

  test.describe("Comments Tab", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("shows comments tab by default", async ({ page }) => {
      await expect(page.getByRole("button", { name: /comments/i })).toBeVisible();
    });

    test("displays existing comments", async ({ page }) => {
      await expect(page.getByText("Let me know if you need help!")).toBeVisible();
    });

    test("can add a comment", async ({ page }) => {
      let commentSent = false;

      await page.route("**/api/messages**", async (route) => {
        if (route.request().method() === "POST") {
          commentSent = true;
          const body = route.request().postDataJSON();
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              message: {
                id: "msg-new",
                content: body?.content || "",
                authorId: "user-1",
                authorName: "Test User",
                createdAt: new Date().toISOString(),
              },
            }),
          });
        } else {
          await route.continue();
        }
      });

      const commentInput = page.getByPlaceholder(/comment|message/i);
      await commentInput.fill("This is a new comment");
      await page.getByRole("button", { name: /send|post/i }).click();

      await page.waitForTimeout(500);
      expect(commentSent).toBe(true);
    });
  });

  test.describe("Activity Tab", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("can switch to activity tab", async ({ page }) => {
      await page.getByRole("button", { name: /activity/i }).click();
      // Should show activity items
      await expect(page.getByText(/created this task/i)).toBeVisible();
    });

    test("displays activity history", async ({ page }) => {
      await page.getByRole("button", { name: /activity/i }).click();
      await expect(page.getByText(/changed status/i)).toBeVisible();
    });
  });

  test.describe("Attachments Tab", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("can switch to attachments tab", async ({ page }) => {
      await page.getByRole("button", { name: /attachments/i }).click();
      // Should show attachment
      await expect(page.getByText("design-spec.pdf")).toBeVisible();
    });

    test("displays attachment count in tab", async ({ page }) => {
      // Attachment tab should show count
      await expect(page.getByText("1")).toBeVisible();
    });
  });

  test.describe("Delete Task", () => {
    test.beforeEach(async ({ page }) => {
      await page.getByText("Implement new feature").click();
      await expect(page.getByRole("dialog")).toBeVisible();
    });

    test("shows delete button", async ({ page }) => {
      await expect(page.getByRole("button", { name: /delete/i })).toBeVisible();
    });

    test("shows confirmation before deleting", async ({ page }) => {
      await page.getByRole("button", { name: /delete/i }).click();
      await expect(page.getByText(/Delete this task/i)).toBeVisible();
      await expect(page.getByRole("button", { name: /confirm/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /cancel/i })).toBeVisible();
    });

    test("can cancel delete", async ({ page }) => {
      await page.getByRole("button", { name: /delete/i }).click();
      await page.getByRole("button", { name: /cancel/i }).click();

      // Confirmation should disappear
      await expect(page.getByText(/Delete this task/i)).not.toBeVisible();
    });

    test("can confirm delete", async ({ page }) => {
      let deleteRequested = false;

      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "DELETE") {
          deleteRequested = true;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true }),
          });
        } else {
          await route.continue();
        }
      });

      await page.getByRole("button", { name: /delete/i }).click();
      await page.getByRole("button", { name: /confirm/i }).click();

      await page.waitForTimeout(500);
      expect(deleteRequested).toBe(true);
    });
  });

  test.describe("Edge Cases", () => {
    test("handles task load error gracefully", async ({ page }) => {
      await page.route("**/api/tasks/task-1", async (route) => {
        if (route.request().method() === "GET") {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({ error: "Server error" }),
          });
        } else {
          await route.continue();
        }
      });

      await page.getByText("Implement new feature").click();
      await expect(page.getByText(/failed/i)).toBeVisible();
    });

    test("handles task without description", async ({ page }) => {
      await page.route("**/api/tasks/task-2", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            task: {
              id: "task-2",
              title: "Fix bug in login",
              status: "todo",
              createdAt: new Date().toISOString(),
            },
          }),
        });
      });

      await page.getByText("Fix bug in login").click();
      await expect(page.getByText("No description provided")).toBeVisible();
    });

    test("handles task without assignee", async ({ page }) => {
      await page.route("**/api/tasks/task-2", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            task: {
              id: "task-2",
              title: "Fix bug in login",
              status: "todo",
              createdAt: new Date().toISOString(),
            },
          }),
        });
      });

      await page.getByText("Fix bug in login").click();
      await expect(page.getByText(/Assign/i)).toBeVisible();
    });
  });
});
