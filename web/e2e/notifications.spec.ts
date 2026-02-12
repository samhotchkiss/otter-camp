import { expect, test, type Page } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

type MockNotification = {
  id: string;
  type:
    | "task_assigned"
    | "task_completed"
    | "task_updated"
    | "comment"
    | "mention"
    | "agent_update"
    | "system";
  title: string;
  message: string;
  read: boolean;
  sourceUrl: string | null;
  actorName: string | null;
  createdAt: string;
};

function buildMockNotifications(): MockNotification[] {
  const now = Date.now();
  return [
    {
      id: "notif-1",
      type: "task_assigned",
      title: "New task assigned",
      message: "You have been assigned to Implement new feature",
      read: false,
      sourceUrl: "/tasks",
      actorName: "Derek",
      createdAt: new Date(now).toISOString(),
    },
    {
      id: "notif-2",
      type: "mention",
      title: "You were mentioned",
      message: "@TestUser check this out",
      read: false,
      sourceUrl: "/tasks",
      actorName: "Frank",
      createdAt: new Date(now - 60 * 60 * 1000).toISOString(),
    },
    {
      id: "notif-3",
      type: "task_completed",
      title: "Task completed",
      message: "Fix bug in login was marked as done",
      read: true,
      sourceUrl: "/tasks",
      actorName: "Nova",
      createdAt: new Date(now - 24 * 60 * 60 * 1000).toISOString(),
    },
    {
      id: "notif-4",
      type: "comment",
      title: "New comment",
      message: "Someone commented on your task",
      read: true,
      sourceUrl: "/tasks",
      actorName: "Stone",
      createdAt: new Date(now - 2 * 24 * 60 * 60 * 1000).toISOString(),
    },
    {
      id: "notif-5",
      type: "agent_update",
      title: "Agent status update",
      message: "Derek is now online",
      read: false,
      sourceUrl: "/agents",
      actorName: "System",
      createdAt: new Date(now - 5 * 24 * 60 * 60 * 1000).toISOString(),
    },
    {
      id: "notif-6",
      type: "system",
      title: "System notification",
      message: "Scheduled maintenance tonight",
      read: true,
      sourceUrl: null,
      actorName: null,
      createdAt: new Date(now - 8 * 24 * 60 * 60 * 1000).toISOString(),
    },
  ];
}

async function mockNotificationsApi(
  page: Page,
  initialNotifications: MockNotification[],
): Promise<void> {
  let notifications = initialNotifications.map((item) => ({ ...item }));

  await page.route("**/api/notifications**", async (route) => {
    const url = new URL(route.request().url());
    const method = route.request().method();
    const path = url.pathname;

    const respondJSON = async (status: number, body: unknown) => {
      await route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    };

    const readMatch = path.match(/^\/api\/notifications\/([^/]+)\/read$/);
    const unreadMatch = path.match(/^\/api\/notifications\/([^/]+)\/unread$/);
    const deleteMatch = path.match(/^\/api\/notifications\/([^/]+)$/);

    if (path === "/api/notifications" && method === "GET") {
      await respondJSON(200, notifications);
      return;
    }

    if (path === "/api/notifications/read-all" && method === "POST") {
      notifications = notifications.map((notification) => ({
        ...notification,
        read: true,
      }));
      await respondJSON(200, { success: true });
      return;
    }

    if (readMatch && method === "POST") {
      const targetID = readMatch[1];
      notifications = notifications.map((notification) => (
        notification.id === targetID
          ? { ...notification, read: true }
          : notification
      ));
      await respondJSON(200, { success: true });
      return;
    }

    if (unreadMatch && method === "POST") {
      const targetID = unreadMatch[1];
      notifications = notifications.map((notification) => (
        notification.id === targetID
          ? { ...notification, read: false }
          : notification
      ));
      await respondJSON(200, { success: true });
      return;
    }

    if (deleteMatch && method === "DELETE") {
      const targetID = deleteMatch[1];
      notifications = notifications.filter((notification) => notification.id !== targetID);
      await respondJSON(200, { success: true });
      return;
    }

    await respondJSON(200, { success: true });
  });
}

function notificationCard(page: Page, title: string) {
  return page.locator(".group").filter({ hasText: title }).first();
}

function notificationTitle(page: Page, title: string) {
  return page.locator(".group h3").filter({ hasText: title }).first();
}

test.describe("Notifications", () => {
  test.describe("Notifications Page", () => {
    test.beforeEach(async ({ page }) => {
      await bootstrapAuthenticatedSession(page);
      await mockNotificationsApi(page, buildMockNotifications());
      await page.goto("/notifications");
    });

    test("renders notifications heading and unread summary", async ({ page }) => {
      await expect(page.getByRole("heading", { name: "🔔 Notifications" })).toBeVisible();
      await expect(page.getByText("You have 3 unread notifications")).toBeVisible();
    });

    test("shows filter controls", async ({ page }) => {
      await expect(page.getByRole("button", { name: /^All$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /^Unread$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /^Task Assigned$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /^Task Completed$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /^Comments$/i })).toBeVisible();
      await expect(page.getByRole("button", { name: /^Mentions$/i })).toBeVisible();
    });

    test("shows grouped notification cards", async ({ page }) => {
      await expect(page.getByText(/Today|Yesterday|This Week|Earlier/).first()).toBeVisible();
      await expect(notificationTitle(page, "New task assigned")).toBeVisible();
      await expect(notificationTitle(page, "Task completed")).toBeVisible();
    });

    test("filters unread notifications and can return to all", async ({ page }) => {
      await page.getByRole("button", { name: /^Unread$/i }).click();

      await expect(notificationTitle(page, "New task assigned")).toBeVisible();
      await expect(notificationTitle(page, "You were mentioned")).toBeVisible();
      await expect(notificationTitle(page, "Task completed")).not.toBeVisible();

      await page.getByRole("button", { name: /^All$/i }).click();
      await expect(notificationTitle(page, "Task completed")).toBeVisible();
    });

    test("filters by notification type", async ({ page }) => {
      await page.getByRole("button", { name: /^Task Assigned$/i }).click();
      await expect(notificationTitle(page, "New task assigned")).toBeVisible();
      await expect(notificationTitle(page, "You were mentioned")).not.toBeVisible();

      await page.getByRole("button", { name: /^Mentions$/i }).click();
      await expect(notificationTitle(page, "You were mentioned")).toBeVisible();
      await expect(notificationTitle(page, "New task assigned")).not.toBeVisible();
    });

    test("shows filter empty state copy", async ({ page }) => {
      await page.getByRole("button", { name: /^Task Updated$/i }).click();
      await expect(page.getByRole("heading", { name: "No notifications" })).toBeVisible();
      await expect(page.getByText("Try selecting a different filter")).toBeVisible();
    });

    test("marks a single notification as read", async ({ page }) => {
      const card = notificationCard(page, "New task assigned");
      await card.hover();
      await card.locator("button[title='Mark as read']").click();

      await expect(page.getByText("You have 2 unread notifications")).toBeVisible();
    });

    test("marks a read notification as unread", async ({ page }) => {
      const card = notificationCard(page, "Task completed");
      await card.hover();
      await card.locator("button[title='Mark as unread']").click();

      await expect(page.getByText("You have 4 unread notifications")).toBeVisible();
    });

    test("marks all notifications as read", async ({ page }) => {
      await page.getByRole("button", { name: /Mark all as read/i }).click();
      await expect(page.getByText("You're all caught up!")).toBeVisible();
    });

    test("deletes a notification", async ({ page }) => {
      const card = notificationCard(page, "New task assigned");
      await card.hover();
      await card.locator("button[title='Delete notification']").click();

      await expect(notificationTitle(page, "New task assigned")).not.toBeVisible();
    });

    test("clicking a notification navigates to its source", async ({ page }) => {
      await notificationCard(page, "New task assigned").click();
      await expect(page).toHaveURL(/\/tasks/);
    });

    test("notification without source URL stays on notifications page", async ({ page }) => {
      await notificationCard(page, "System notification").click();
      await expect(page).toHaveURL(/\/notifications/);
    });

    test("handles rapid filter switching", async ({ page }) => {
      await page.getByRole("button", { name: /^Unread$/i }).click();
      await page.getByRole("button", { name: /^All$/i }).click();
      await page.getByRole("button", { name: /^Mentions$/i }).click();
      await page.getByRole("button", { name: /^All$/i }).click();

      await expect(page.getByRole("heading", { name: "🔔 Notifications" })).toBeVisible();
    });
  });

  test.describe("Empty States", () => {
    test.beforeEach(async ({ page }) => {
      await bootstrapAuthenticatedSession(page);
      await mockNotificationsApi(page, []);
      await page.goto("/notifications");
    });

    test("shows empty state content", async ({ page }) => {
      const main = page.locator("main");
      await expect(main.getByRole("heading", { name: "No notifications" })).toBeVisible();
      await expect(main.getByText("When something happens, you'll see it here")).toBeVisible();
      await expect(main.getByText("🦦")).toBeVisible();
    });
  });
});
