import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import NotificationBell from "./NotificationBell";
import useEmissions from "../hooks/useEmissions";
import { useNotifications, type Notification } from "../contexts/NotificationContext";

vi.mock("../hooks/useEmissions", () => ({
  default: vi.fn(),
}));

vi.mock("../contexts/NotificationContext", async () => {
  const actual = await vi.importActual<typeof import("../contexts/NotificationContext")>("../contexts/NotificationContext");
  return {
    ...actual,
    useNotifications: vi.fn(),
  };
});

describe("NotificationBell", () => {
  const markAsRead = vi.fn();
  const markAllAsRead = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useNotifications).mockReturnValue({
      notifications: [] as Notification[],
      unreadCount: 0,
      loading: false,
      filter: "all",
      setFilter: vi.fn(),
      filteredNotifications: [],
      markAsRead,
      markAsUnread: vi.fn(),
      markAllAsRead,
      deleteNotification: vi.fn(),
      refreshNotifications: vi.fn(),
    });
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });
  });

  it("shows combined badge count for unread notifications and actionable emissions", () => {
    const now = Date.now();
    vi.mocked(useNotifications).mockReturnValue({
      notifications: [
        {
          id: "n-1",
          type: "system",
          title: "System",
          message: "Message",
          read: false,
          createdAt: new Date(now),
        },
      ],
      unreadCount: 2,
      loading: false,
      filter: "all",
      setFilter: vi.fn(),
      filteredNotifications: [],
      markAsRead,
      markAsUnread: vi.fn(),
      markAllAsRead,
      deleteNotification: vi.fn(),
      refreshNotifications: vi.fn(),
    });
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [
        {
          id: "e-1",
          source_type: "agent",
          source_id: "agent-1",
          kind: "error",
          summary: "Error",
          timestamp: new Date(now).toISOString(),
        },
        {
          id: "e-2",
          source_type: "agent",
          source_id: "agent-2",
          kind: "progress",
          summary: "Progress",
          timestamp: new Date(now - 60_000).toISOString(),
        },
        {
          id: "e-3",
          source_type: "agent",
          source_id: "agent-3",
          kind: "status",
          summary: "Status",
          timestamp: new Date(now).toISOString(),
        },
      ],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });

    render(
      <MemoryRouter>
        <NotificationBell />
      </MemoryRouter>,
    );

    expect(screen.getByRole("button", { name: "Notifications (4 alerts)" })).toBeInTheDocument();
    expect(screen.getByTestId("notification-bell-badge")).toHaveTextContent("4");
  });

  it("ignores stale and non-actionable emissions for badge count", () => {
    const now = Date.now();
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [
        {
          id: "e-1",
          source_type: "agent",
          source_id: "agent-1",
          kind: "milestone",
          summary: "Old milestone",
          timestamp: new Date(now - 5 * 60_000).toISOString(),
        },
        {
          id: "e-2",
          source_type: "agent",
          source_id: "agent-2",
          kind: "status",
          summary: "Current status",
          timestamp: new Date(now).toISOString(),
        },
      ],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });

    render(
      <MemoryRouter>
        <NotificationBell />
      </MemoryRouter>,
    );

    expect(screen.getByRole("button", { name: "Notifications" })).toBeInTheDocument();
    expect(screen.queryByTestId("notification-bell-badge")).not.toBeInTheDocument();
  });
});
