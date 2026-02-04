import { useCallback } from "react";
import { useNavigate } from "react-router-dom";
import {
  useNotifications,
  type Notification,
  type NotificationFilter,
  type NotificationType,
} from "../contexts/NotificationContext";

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const NOTIFICATION_ICONS: Record<NotificationType, string> = {
  task_assigned: "📋",
  task_completed: "✅",
  task_updated: "📝",
  comment: "💬",
  mention: "@",
  agent_update: "🤖",
  system: "🔔",
};

const NOTIFICATION_TYPE_LABELS: Record<NotificationType, string> = {
  task_assigned: "Task Assigned",
  task_completed: "Task Completed",
  task_updated: "Task Updated",
  comment: "Comments",
  mention: "Mentions",
  agent_update: "Agent Updates",
  system: "System",
};

const FILTER_OPTIONS: Array<{ value: NotificationFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "unread", label: "Unread" },
  { value: "task_assigned", label: "Task Assigned" },
  { value: "task_completed", label: "Task Completed" },
  { value: "task_updated", label: "Task Updated" },
  { value: "comment", label: "Comments" },
  { value: "mention", label: "Mentions" },
  { value: "agent_update", label: "Agent Updates" },
  { value: "system", label: "System" },
];

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const formatDateTime = (date: Date): string => {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  const diffMin = Math.floor(diffSec / 60);
  const diffHr = Math.floor(diffMin / 60);
  const diffDay = Math.floor(diffHr / 24);

  if (diffSec < 60) return "just now";
  if (diffMin < 60) return `${diffMin} minute${diffMin !== 1 ? "s" : ""} ago`;
  if (diffHr < 24) return `${diffHr} hour${diffHr !== 1 ? "s" : ""} ago`;
  if (diffDay < 7) return `${diffDay} day${diffDay !== 1 ? "s" : ""} ago`;

  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    year: now.getFullYear() !== date.getFullYear() ? "numeric" : undefined,
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
};

const groupNotificationsByDate = (
  notifications: Notification[]
): Map<string, Notification[]> => {
  const groups = new Map<string, Notification[]>();
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today.getTime() - 24 * 60 * 60 * 1000);
  const thisWeek = new Date(today.getTime() - 7 * 24 * 60 * 60 * 1000);

  for (const notification of notifications) {
    const date = notification.createdAt;
    let group: string;

    if (date >= today) {
      group = "Today";
    } else if (date >= yesterday) {
      group = "Yesterday";
    } else if (date >= thisWeek) {
      group = "This Week";
    } else {
      group = "Earlier";
    }

    const existing = groups.get(group) ?? [];
    existing.push(notification);
    groups.set(group, existing);
  }

  return groups;
};

// ─────────────────────────────────────────────────────────────────────────────
// Components
// ─────────────────────────────────────────────────────────────────────────────

interface NotificationCardProps {
  notification: Notification;
  onMarkAsRead: () => void;
  onMarkAsUnread: () => void;
  onDelete: () => void;
  onClick: () => void;
}

function NotificationCard({
  notification,
  onMarkAsRead,
  onMarkAsUnread,
  onDelete,
  onClick,
}: NotificationCardProps) {
  return (
    <div
      className={`group relative overflow-hidden rounded-xl border transition hover:shadow-md ${
        notification.read
          ? "border-otter-border bg-white dark:border-otter-dark-border dark:bg-otter-dark-bg"
          : "border-sky-200 bg-sky-50/50 dark:border-sky-800/50 dark:bg-sky-900/10"
      }`}
    >
      <button
        type="button"
        onClick={onClick}
        className="w-full text-left p-4"
      >
        <div className="flex items-start gap-4">
          {/* Icon */}
          <div
            className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl text-lg ${
              notification.read
                ? "bg-otter-surface-alt dark:bg-otter-dark-surface"
                : "bg-sky-100 dark:bg-sky-900/50"
            }`}
          >
            {NOTIFICATION_ICONS[notification.type]}
          </div>

          {/* Content */}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h3
                className={`font-medium ${
                  notification.read
                    ? "text-otter-text dark:text-otter-dark-text"
                    : "text-otter-text dark:text-white"
                }`}
              >
                {notification.title}
              </h3>
              {!notification.read && (
                <span className="h-2 w-2 shrink-0 rounded-full bg-sky-500" />
              )}
            </div>

            <p className="mt-1 text-sm text-otter-muted dark:text-otter-dark-muted">
              {notification.message}
            </p>

            <div className="mt-2 flex items-center gap-3 text-xs text-otter-muted dark:text-otter-dark-muted">
              <span>{formatDateTime(notification.createdAt)}</span>
              <span>•</span>
              <span className="capitalize">
                {NOTIFICATION_TYPE_LABELS[notification.type]}
              </span>
              {notification.actorName && (
                <>
                  <span>•</span>
                  <span>{notification.actorName}</span>
                </>
              )}
            </div>
          </div>
        </div>
      </button>

      {/* Actions */}
      <div className="absolute right-2 top-2 flex items-center gap-1 opacity-0 transition group-hover:opacity-100">
        {notification.read ? (
          <button
            type="button"
            onClick={onMarkAsUnread}
            className="rounded-lg p-1.5 text-otter-muted transition hover:bg-otter-surface-alt hover:text-otter-muted dark:hover:bg-otter-dark-surface-alt dark:hover:text-otter-dark-muted"
            title="Mark as unread"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
            </svg>
          </button>
        ) : (
          <button
            type="button"
            onClick={onMarkAsRead}
            className="rounded-lg p-1.5 text-otter-muted transition hover:bg-otter-surface-alt hover:text-otter-muted dark:hover:bg-otter-dark-surface-alt dark:hover:text-otter-dark-muted"
            title="Mark as read"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
            </svg>
          </button>
        )}
        <button
          type="button"
          onClick={onDelete}
          className="rounded-lg p-1.5 text-otter-muted transition hover:bg-red-100 hover:text-red-600 dark:hover:bg-red-900/30 dark:hover:text-red-400"
          title="Delete notification"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        </button>
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Main Page
// ─────────────────────────────────────────────────────────────────────────────

export default function NotificationsPage() {
  const navigate = useNavigate();
  const {
    filteredNotifications,
    unreadCount,
    loading,
    filter,
    setFilter,
    markAsRead,
    markAsUnread,
    markAllAsRead,
    deleteNotification,
  } = useNotifications();

  const groupedNotifications = groupNotificationsByDate(filteredNotifications);

  const handleNotificationClick = useCallback(
    (notification: Notification) => {
      if (!notification.read) {
        markAsRead(notification.id);
      }
      if (notification.sourceUrl) {
        navigate(notification.sourceUrl);
      }
    },
    [markAsRead, navigate]
  );

  return (
    <div className="mx-auto max-w-4xl">
      {/* Header */}
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-otter-text dark:text-white">
            🔔 Notifications
          </h1>
          <p className="mt-1 text-sm text-otter-muted dark:text-otter-dark-muted">
            {unreadCount > 0
              ? `You have ${unreadCount} unread notification${unreadCount !== 1 ? "s" : ""}`
              : "You're all caught up!"}
          </p>
        </div>

        {unreadCount > 0 && (
          <button
            type="button"
            onClick={markAllAsRead}
            className="inline-flex items-center gap-2 rounded-xl bg-sky-100 px-4 py-2 text-sm font-medium text-sky-700 transition hover:bg-sky-200 dark:bg-otter-dark-accent/15 dark:text-sky-300 dark:hover:bg-sky-900/50"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
            </svg>
            Mark all as read
          </button>
        )}
      </div>

      {/* Filter Tabs */}
      <div className="mb-6 flex flex-wrap gap-2">
        {FILTER_OPTIONS.map((option) => (
          <button
            key={option.value}
            type="button"
            onClick={() => setFilter(option.value)}
            className={`rounded-xl px-4 py-2 text-sm font-medium transition ${
              filter === option.value
                ? "bg-otter-dark-surface text-white dark:bg-white dark:text-otter-text"
                : "bg-otter-surface-alt text-otter-muted hover:bg-otter-surface-alt dark:bg-otter-dark-surface dark:text-otter-dark-muted dark:hover:bg-otter-dark-surface-alt"
            }`}
          >
            {option.label}
          </button>
        ))}
      </div>

      {/* Notifications List */}
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-otter-border border-t-sky-500" />
        </div>
      ) : filteredNotifications.length === 0 ? (
        <div className="rounded-2xl border border-otter-border bg-white p-12 text-center dark:border-otter-dark-border dark:bg-otter-dark-bg">
          <span className="text-5xl">🦦</span>
          <h3 className="mt-4 text-lg font-medium text-otter-text dark:text-white">
            No notifications
          </h3>
          <p className="mt-1 text-sm text-otter-muted dark:text-otter-dark-muted">
            {filter !== "all"
              ? "Try selecting a different filter"
              : "When something happens, you'll see it here"}
          </p>
        </div>
      ) : (
        <div className="space-y-8">
          {Array.from(groupedNotifications.entries()).map(([group, notifications]) => (
            <div key={group}>
              <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-otter-muted dark:text-otter-dark-muted">
                {group}
              </h2>
              <div className="space-y-3">
                {notifications.map((notification) => (
                  <NotificationCard
                    key={notification.id}
                    notification={notification}
                    onMarkAsRead={() => markAsRead(notification.id)}
                    onMarkAsUnread={() => markAsUnread(notification.id)}
                    onDelete={() => deleteNotification(notification.id)}
                    onClick={() => handleNotificationClick(notification)}
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
