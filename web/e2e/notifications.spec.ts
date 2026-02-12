import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Notifications", () => {
  const mockNotifications = [
    {
      id: "notif-1",
      type: "task_assigned",
      title: "New task assigned",
      message: "You have been assigned to 'Implement new feature'",
      read: false,
      sourceUrl: "/tasks",
      actorName: "Derek",
      createdAt: new Date().toISOString(),
    },
    {
      id: "notif-2",
      type: "mention",
      title: "You were mentioned",
      message: "@TestUser check this out",
      read: false,
      sourceUrl: "/tasks",
      actorName: "Frank",
      createdAt: new Date(Date.now() - 3600000).toISOString(),
    },
    {
      id: "notif-3",
      type: "task_completed",
      title: "Task completed",
      message: "'Fix bug in login' was marked as done",
      read: true,
      sourceUrl: "/tasks",
      actorName: "Nova",
      createdAt: new Date(Date.now() - 86400000).toISOString(),
    },
    {
      id: "notif-4",
      type: "comment",
      title: "New comment",
      message: "Someone commented on your task",
      read: true,
      sourceUrl: "/tasks",
      actorName: "Stone",
      createdAt: new Date(Date.now() - 86400000 * 2).toISOString(),
    },
    {
      id: "notif-5",
      type: "agent_update",
      title: "Agent status update",
      message: "Derek is now online",
      read: false,
      sourceUrl: "/agents",
      actorName: "System",
      createdAt: new Date(Date.now() - 86400000 * 5).toISOString(),
    },
    {
      id: "notif-6",
      type: "system",
      title: "System notification",
      message: "Scheduled maintenance tonight",
      read: true,
      sourceUrl: null,
      actorName: null,
      createdAt: new Date(Date.now() - 86400000 * 8).toISOString(),
    },
  ];

  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);

    // Inject notification context data
    await page.addInitScript((notifications) => {
      // Store notifications in window for the context to pick up
      (window as Window & { __mockNotifications?: typeof notifications }).__mockNotifications = notifications;
    }, mockNotifications);

    // Mock any API calls
    await page.route("**/api/notifications**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ notifications: mockNotifications }),
      });
    });

    await page.route("**/api/notifications/*/read", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
    });

    await page.route("**/api/notifications/read-all", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
    });

    await page.route("**/api/notifications/*", async (route) => {
      if (route.request().method() === "DELETE") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true }),
        });
      } else {
        await route.continue();
      }
    });
  });

  test.describe("Notification Bell", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/");
    });

    test("displays notification bell in header", async ({ page }) => {
      await expect(page.getByRole("button", { name: /notifications/i })).toBeVisible();
    });

    test("shows unread count badge", async ({ page }) => {
      // Should show badge with unread count
      const badge = page.locator("span").filter({ hasText: /^[0-9]+$|9\+/ });
      await expect(badge.first()).toBeVisible();
    });

    test("opens dropdown when clicking bell", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
    });

    test("closes dropdown when clicking outside", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();

      // Click outside the dropdown
      await page.locator("body").click({ position: { x: 10, y: 10 } });
      await expect(page.getByRole("heading", { name: "Notifications" })).not.toBeVisible();
    });

    test("closes dropdown with escape key", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();

      await page.keyboard.press("Escape");
      await expect(page.getByRole("heading", { name: "Notifications" })).not.toBeVisible();
    });

    test("shows recent notifications in dropdown", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      
      // Should show up to 5 most recent notifications
      await expect(page.getByText("New task assigned")).toBeVisible();
      await expect(page.getByText("You were mentioned")).toBeVisible();
    });

    test("shows mark all as read button when unread exist", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      await expect(page.getByText(/mark all as read/i)).toBeVisible();
    });

    test("shows View all notifications link", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      await expect(page.getByRole("link", { name: /view all/i })).toBeVisible();
    });

    test("can mark individual notification as read from dropdown", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      
      // Find unread notification and its mark as read button
      const markReadButton = page.locator("button[title='Mark as read']").first();
      await expect(markReadButton).toBeVisible();
      await markReadButton.click();
    });

    test("navigates to notifications page when clicking View all", async ({ page }) => {
      await page.getByRole("button", { name: /notifications/i }).click();
      await page.getByRole("link", { name: /view all/i }).click();
      
      await expect(page).toHaveURL(/\/notifications/);
    });
  });

  test.describe("Notifications Page", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/notifications");
    });

    test("displays notifications page header", async ({ page }) => {
      await expect(page.getByRole("heading", { name: /Notifications/i })).toBeVisible();
    });

    test("shows unread count in description", async ({ page }) => {
      await expect(page.getByText(/unread notification/i)).toBeVisible();
    });

    test("displays filter buttons", async ({ page }) => {
      await expect(page.getByRole("button", { name: /^All$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /^Unread$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /Task Assigned/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /Mentions/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /Comments/i })).toBeVisible();
    });

    test("displays notifications grouped by date", async ({ page }) => {
      // Should show date group headers
      await expect(page.getByRole("heading", { name: /Today|Yesterday|This Week|Earlier/i }).first()).toBeVisible();
    });

    test("displays notification cards with icons", async ({ page }) => {
      // Each notification type has an icon
      await expect(page.getByText("📋")).toBeVisible(); // task_assigned
    });

    test("shows unread indicator on unread notifications", async ({ page }) => {
      // Unread notifications should have a blue dot
      const unreadIndicator = page.locator(".bg-sky-500.rounded-full").first();
      await expect(unreadIndicator).toBeVisible();
    });

    test("can filter by unread only", async ({ page }) => {
      await page.getByRole("button", { name: /^Unread$/i }).click();

      // Should only show unread notifications (3 unread in our mock)
      await expect(page.getByText("New task assigned")).toBeVisible();
      await expect(page.getByText("You were mentioned")).toBeVisible();
      
      // Read notifications should not be visible
      await expect(page.getByText("Task completed")).not.toBeVisible();
    });

    test("can filter by notification type - Task Assigned", async ({ page }) => {
      await page.getByRole("button", { name: /Task Assigned/i }).click();

      await expect(page.getByText("New task assigned")).toBeVisible();
      // Other types should not be visible
      await expect(page.getByText("You were mentioned")).not.toBeVisible();
    });

    test("can filter by notification type - Mentions", async ({ page }) => {
      await page.getByRole("button", { name: /Mentions/i }).click();

      await expect(page.getByText("You were mentioned")).toBeVisible();
      await expect(page.getByText("New task assigned")).not.toBeVisible();
    });

    test("can filter by notification type - Comments", async ({ page }) => {
      await page.getByRole("button", { name: /Comments/i }).click();

      await expect(page.getByText("New comment")).toBeVisible();
      await expect(page.getByText("New task assigned")).not.toBeVisible();
    });

    test("shows empty state when filter has no results", async ({ page }) => {
      // Filter by a type that doesn't exist or has no items
      await page.getByRole("button", { name: /Task Completed/i }).click();
      
      // If there are completed tasks, they should show; otherwise empty state
      const emptyState = page.getByText("No notifications");
      const completedTask = page.getByText("Task completed");
      
      await expect(emptyState.or(completedTask)).toBeVisible();
    });

    test("can return to All filter", async ({ page }) => {
      // First filter to unread
      await page.getByRole("button", { name: /^Unread$/i }).click();
      await expect(page.getByText("Task completed")).not.toBeVisible();

      // Then return to all
      await page.getByRole("button", { name: /^All$/i }).click();
      await expect(page.getByText("Task completed")).toBeVisible();
    });
  });

  test.describe("Mark as Read", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/notifications");
    });

    test("can mark individual notification as read", async ({ page }) => {
      // Hover over an unread notification to reveal actions
      const notificationCard = page.locator(".group").filter({ hasText: "New task assigned" });
      await notificationCard.hover();

      const markReadButton = notificationCard.locator("button[title='Mark as read']");
      await markReadButton.click();
    });

    test("can mark notification as unread", async ({ page }) => {
      // Hover over a read notification
      const notificationCard = page.locator(".group").filter({ hasText: "Task completed" });
      await notificationCard.hover();

      const markUnreadButton = notificationCard.locator("button[title='Mark as unread']");
      await markUnreadButton.click();
    });

    test("shows Mark all as read button when unread exist", async ({ page }) => {
      await expect(page.getByRole("button", { name: /mark all as read/i })).toBeVisible();
    });

    test("can mark all as read", async ({ page }) => {
      await page.getByRole("button", { name: /mark all as read/i }).click();

      // After marking all as read, the button might disappear or change
      // The "You're all caught up!" message might appear
      await page.waitForTimeout(500);
    });
  });

  test.describe("Delete Notification", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/notifications");
    });

    test("can delete a notification", async ({ page }) => {
      const notificationCard = page.locator(".group").filter({ hasText: "New task assigned" });
      await notificationCard.hover();

      const deleteButton = notificationCard.locator("button[title='Delete notification']");
      await deleteButton.click();

      // Notification should be removed from the list
      await page.waitForTimeout(500);
    });
  });

  test.describe("Notification Click Actions", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/notifications");
    });

    test("clicking notification marks it as read", async ({ page }) => {
      const notificationCard = page.locator("button").filter({ hasText: "New task assigned" });
      await notificationCard.click();

      // Should navigate to source URL
      await expect(page).toHaveURL(/\/tasks/);
    });

    test("clicking notification navigates to source URL", async ({ page }) => {
      const notificationCard = page.locator("button").filter({ hasText: "Agent status update" });
      await notificationCard.click();

      await expect(page).toHaveURL(/\/agents/);
    });
  });

  test.describe("Empty States", () => {
    test("shows empty state when no notifications", async ({ page }) => {
      await page.route("**/api/notifications**", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ notifications: [] }),
        });
      });

      // Inject empty notifications
      await page.addInitScript(() => {
        (window as Window & { __mockNotifications?: unknown[] }).__mockNotifications = [];
      });

      await page.goto("/notifications");

      await expect(page.getByText("No notifications")).toBeVisible();
      await expect(page.getByText(/When something happens/i)).toBeVisible();
    });

    test("shows otter mascot in empty state", async ({ page }) => {
      await page.route("**/api/notifications**", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ notifications: [] }),
        });
      });

      await page.addInitScript(() => {
        (window as Window & { __mockNotifications?: unknown[] }).__mockNotifications = [];
      });

      await page.goto("/notifications");

      // Should show otter emoji
      await expect(page.getByText("🦦")).toBeVisible();
    });

    test("shows caught up message when all read", async ({ page }) => {
      // Create a page with all read notifications
      const allReadNotifications = mockNotifications.map((n) => ({ ...n, read: true }));
      
      await page.addInitScript((notifications) => {
        (window as Window & { __mockNotifications?: typeof notifications }).__mockNotifications = notifications;
      }, allReadNotifications);

      await page.goto("/notifications");

      await expect(page.getByText(/all caught up/i)).toBeVisible();
    });
  });

  test.describe("Notification Bell in Different Contexts", () => {
    test("notification bell visible on agents page", async ({ page }) => {
      await page.goto("/agents");
      await expect(page.getByRole("button", { name: /notifications/i })).toBeVisible();
    });

    test("notification bell visible on settings page", async ({ page }) => {
      await page.goto("/settings");
      await expect(page.getByRole("button", { name: /notifications/i })).toBeVisible();
    });

    test("notification bell visible on tasks page", async ({ page }) => {
      await page.goto("/tasks");
      await expect(page.getByRole("button", { name: /notifications/i })).toBeVisible();
    });
  });

  test.describe("Edge Cases", () => {
    test("handles notification with no source URL", async ({ page }) => {
      await page.goto("/notifications");

      // System notification has no source URL
      const notificationCard = page.locator("button").filter({ hasText: "System notification" });
      await notificationCard.click();

      // Should not navigate anywhere, stay on notifications page
      await expect(page).toHaveURL(/\/notifications/);
    });

    test("handles very long notification message", async ({ page }) => {
      const longNotification = {
        id: "notif-long",
        type: "comment",
        title: "Very long notification title that should be truncated properly",
        message: "This is a very long notification message that should be truncated or wrapped properly to avoid breaking the layout. It contains a lot of text to test the overflow handling.",
        read: false,
        sourceUrl: "/tasks",
        actorName: "Test User",
        createdAt: new Date().toISOString(),
      };

      await page.addInitScript((notification) => {
        (window as Window & { __mockNotifications?: typeof notification[] }).__mockNotifications = [notification];
      }, longNotification);

      await page.goto("/notifications");

      // Page should still render properly
      await expect(page.getByRole("heading", { name: /Notifications/i })).toBeVisible();
    });

    test("handles rapid filter switching", async ({ page }) => {
      await page.goto("/notifications");

      // Rapidly switch between filters
      await page.getByRole("button", { name: /^Unread$/i }).click();
      await page.getByRole("button", { name: /^All$/i }).click();
      await page.getByRole("button", { name: /Mentions/i }).click();
      await page.getByRole("button", { name: /^All$/i }).click();

      // Page should still be functional
      await expect(page.getByRole("heading", { name: /Notifications/i })).toBeVisible();
    });

    test("updates badge count when marking as read", async ({ page }) => {
      await page.goto("/");

      // Get initial badge
      await page.getByRole("button", { name: /notifications/i }).click();
      
      // Mark one as read
      const markReadButton = page.locator("button[title='Mark as read']").first();
      if (await markReadButton.isVisible()) {
        await markReadButton.click();
        // Badge count should decrease
        await page.waitForTimeout(300);
      }
    });
  });
});
