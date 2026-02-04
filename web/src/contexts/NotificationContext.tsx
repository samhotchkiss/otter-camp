import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useWS } from "./WebSocketContext";

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

export type NotificationType =
  | "task_assigned"
  | "task_completed"
  | "task_updated"
  | "comment"
  | "mention"
  | "agent_update"
  | "system";

export interface Notification {
  id: string;
  type: NotificationType;
  title: string;
  message: string;
  read: boolean;
  createdAt: Date;
  sourceUrl?: string;
  sourceId?: string;
  sourceType?: "task" | "project" | "agent" | "comment";
  actorName?: string;
  actorAvatar?: string;
}

export type NotificationFilter = NotificationType | "all" | "unread";

interface NotificationContextValue {
  notifications: Notification[];
  unreadCount: number;
  loading: boolean;
  filter: NotificationFilter;
  setFilter: (filter: NotificationFilter) => void;
  filteredNotifications: Notification[];
  markAsRead: (id: string) => Promise<void>;
  markAsUnread: (id: string) => Promise<void>;
  markAllAsRead: () => Promise<void>;
  deleteNotification: (id: string) => Promise<void>;
  refreshNotifications: () => Promise<void>;
}

const NotificationContext = createContext<NotificationContextValue | null>(null);

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const parseNotification = (raw: unknown): Notification | null => {
  if (!raw || typeof raw !== "object") return null;
  
  const data = raw as Record<string, unknown>;
  
  return {
    id: String(data.id ?? crypto.randomUUID()),
    type: (data.type as NotificationType) ?? "system",
    title: String(data.title ?? "Notification"),
    message: String(data.message ?? ""),
    read: Boolean(data.read ?? false),
    createdAt: data.createdAt ? new Date(String(data.createdAt)) : new Date(),
    sourceUrl: data.sourceUrl ? String(data.sourceUrl) : undefined,
    sourceId: data.sourceId ? String(data.sourceId) : undefined,
    sourceType: data.sourceType as Notification["sourceType"],
    actorName: data.actorName ? String(data.actorName) : undefined,
    actorAvatar: data.actorAvatar ? String(data.actorAvatar) : undefined,
  };
};

const wsMessageToNotification = (
  type: string,
  data: unknown
): Notification | null => {
  const payload = data as Record<string, unknown> | undefined;
  
  switch (type) {
    case "TaskCreated":
    case "TaskUpdated":
    case "TaskStatusChanged":
      return {
        id: crypto.randomUUID(),
        type: type === "TaskCreated" ? "task_assigned" : "task_updated",
        title: type === "TaskCreated" ? "New Task" : "Task Updated",
        message: payload?.title
          ? `Task "${payload.title}" was ${type === "TaskCreated" ? "created" : "updated"}`
          : "A task was updated",
        read: false,
        createdAt: new Date(),
        sourceId: payload?.id ? String(payload.id) : undefined,
        sourceType: "task",
        sourceUrl: payload?.id ? `/tasks/${payload.id}` : "/",
      };

    case "CommentAdded":
      return {
        id: crypto.randomUUID(),
        type: "comment",
        title: "New Comment",
        message: payload?.preview
          ? String(payload.preview)
          : "Someone commented on a task",
        read: false,
        createdAt: new Date(),
        sourceId: payload?.taskId ? String(payload.taskId) : undefined,
        sourceType: "comment",
        sourceUrl: payload?.taskId ? `/tasks/${payload.taskId}` : "/",
        actorName: payload?.authorName ? String(payload.authorName) : undefined,
      };

    case "AgentStatusUpdated":
      return {
        id: crypto.randomUUID(),
        type: "agent_update",
        title: "Agent Status",
        message: payload?.agentName
          ? `${payload.agentName} is now ${payload.status ?? "updated"}`
          : "An agent's status changed",
        read: false,
        createdAt: new Date(),
        sourceId: payload?.agentId ? String(payload.agentId) : undefined,
        sourceType: "agent",
        sourceUrl: payload?.agentId ? `/agents/${payload.agentId}` : "/agents",
      };

    case "DMMessageReceived":
      return {
        id: crypto.randomUUID(),
        type: "mention",
        title: "New Message",
        message: payload?.preview
          ? String(payload.preview)
          : "You received a new message",
        read: false,
        createdAt: new Date(),
        actorName: payload?.from ? String(payload.from) : undefined,
      };

    default:
      return null;
  }
};

// ─────────────────────────────────────────────────────────────────────────────
// Provider
// ─────────────────────────────────────────────────────────────────────────────

interface NotificationProviderProps {
  children: ReactNode;
}

export function NotificationProvider({ children }: NotificationProviderProps) {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState<NotificationFilter>("all");
  const { lastMessage } = useWS();

  // Calculate unread count
  const unreadCount = useMemo(
    () => notifications.filter((n) => !n.read).length,
    [notifications]
  );

  // Filter notifications based on current filter
  const filteredNotifications = useMemo(() => {
    if (filter === "all") return notifications;
    if (filter === "unread") return notifications.filter((n) => !n.read);
    return notifications.filter((n) => n.type === filter);
  }, [notifications, filter]);

  // Fetch notifications from API
  const refreshNotifications = useCallback(async () => {
    try {
      setLoading(true);
      const response = await fetch("/api/notifications");
      if (response.ok) {
        const data = await response.json();
        const parsed = (data as unknown[])
          .map(parseNotification)
          .filter((n): n is Notification => n !== null);
        setNotifications(parsed);
      }
    } catch (error) {
      console.error("Failed to fetch notifications:", error);
    } finally {
      setLoading(false);
    }
  }, []);

  // Initial load
  useEffect(() => {
    refreshNotifications();
  }, [refreshNotifications]);

  // Handle WebSocket messages
  useEffect(() => {
    if (!lastMessage || lastMessage.type === "Unknown") return;

    const notification = wsMessageToNotification(
      lastMessage.type,
      lastMessage.data
    );

    if (notification) {
      setNotifications((prev) => [notification, ...prev]);
    }
  }, [lastMessage]);

  // Mark single notification as read
  const markAsRead = useCallback(async (id: string) => {
    try {
      await fetch(`/api/notifications/${id}/read`, { method: "POST" });
      setNotifications((prev) =>
        prev.map((n) => (n.id === id ? { ...n, read: true } : n))
      );
    } catch (error) {
      console.error("Failed to mark notification as read:", error);
    }
  }, []);

  // Mark single notification as unread
  const markAsUnread = useCallback(async (id: string) => {
    try {
      await fetch(`/api/notifications/${id}/unread`, { method: "POST" });
      setNotifications((prev) =>
        prev.map((n) => (n.id === id ? { ...n, read: false } : n))
      );
    } catch (error) {
      console.error("Failed to mark notification as unread:", error);
    }
  }, []);

  // Mark all as read
  const markAllAsRead = useCallback(async () => {
    try {
      await fetch("/api/notifications/read-all", { method: "POST" });
      setNotifications((prev) => prev.map((n) => ({ ...n, read: true })));
    } catch (error) {
      console.error("Failed to mark all notifications as read:", error);
    }
  }, []);

  // Delete notification
  const deleteNotification = useCallback(async (id: string) => {
    try {
      await fetch(`/api/notifications/${id}`, { method: "DELETE" });
      setNotifications((prev) => prev.filter((n) => n.id !== id));
    } catch (error) {
      console.error("Failed to delete notification:", error);
    }
  }, []);

  const value: NotificationContextValue = {
    notifications,
    unreadCount,
    loading,
    filter,
    setFilter,
    filteredNotifications,
    markAsRead,
    markAsUnread,
    markAllAsRead,
    deleteNotification,
    refreshNotifications,
  };

  return (
    <NotificationContext.Provider value={value}>
      {children}
    </NotificationContext.Provider>
  );
}

export function useNotifications(): NotificationContextValue {
  const context = useContext(NotificationContext);
  if (!context) {
    throw new Error(
      "useNotifications must be used within a NotificationProvider"
    );
  }
  return context;
}
