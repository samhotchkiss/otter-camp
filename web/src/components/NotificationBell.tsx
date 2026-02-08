import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  useNotifications,
  type Notification,
  type NotificationType,
} from "../contexts/NotificationContext";
import type { Emission } from "../hooks/useEmissions";
import useEmissions from "../hooks/useEmissions";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
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

const formatTimeAgo = (date: Date): string => {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  const diffMin = Math.floor(diffSec / 60);
  const diffHr = Math.floor(diffMin / 60);
  const diffDay = Math.floor(diffHr / 24);

  if (diffSec < 60) return "just now";
  if (diffMin < 60) return `${diffMin}m ago`;
  if (diffHr < 24) return `${diffHr}h ago`;
  if (diffDay < 7) return `${diffDay}d ago`;
  return date.toLocaleDateString();
};

const ACTIONABLE_KINDS = new Set(["error", "milestone", "progress"]);
const ACTIONABLE_WINDOW_MS = 2 * 60 * 1000;

function isActionableRecentEmission(emission: Emission, nowMs: number): boolean {
  if (!ACTIONABLE_KINDS.has(emission.kind)) {
    return false;
  }
  const emittedMs = new Date(emission.timestamp).getTime();
  if (Number.isNaN(emittedMs)) {
    return false;
  }
  return nowMs - emittedMs <= ACTIONABLE_WINDOW_MS;
}

// ─────────────────────────────────────────────────────────────────────────────
// NotificationItem
// ─────────────────────────────────────────────────────────────────────────────

interface NotificationItemProps {
  notification: Notification;
  onMarkAsRead: () => void;
  onClick: () => void;
}

function NotificationItem({
  notification,
  onMarkAsRead,
  onClick,
}: NotificationItemProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-full text-left px-4 py-3 transition hover:bg-slate-50 dark:hover:bg-slate-800 ${
        !notification.read ? "bg-sky-50/50 dark:bg-sky-900/10" : ""
      }`}
    >
      <div className="flex items-start gap-3">
        {/* Icon */}
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-slate-100 text-sm dark:bg-slate-700">
          {NOTIFICATION_ICONS[notification.type]}
        </span>

        {/* Content */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p
              className={`text-sm font-medium truncate ${
                notification.read
                  ? "text-slate-600 dark:text-slate-300"
                  : "text-slate-900 dark:text-white"
              }`}
            >
              {notification.title}
            </p>
            {!notification.read && (
              <span className="h-2 w-2 shrink-0 rounded-full bg-sky-500" />
            )}
          </div>
          <p className="mt-0.5 text-sm text-slate-500 dark:text-slate-400 truncate">
            {notification.message}
          </p>
          <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
            {formatTimeAgo(notification.createdAt)}
          </p>
        </div>

        {/* Mark as read button */}
        {!notification.read && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onMarkAsRead();
            }}
            className="shrink-0 rounded-lg p-1.5 text-slate-400 transition hover:bg-slate-200 hover:text-slate-600 dark:hover:bg-slate-700 dark:hover:text-slate-300"
            title="Mark as read"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
            </svg>
          </button>
        )}
      </div>
    </button>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// NotificationBell
// ─────────────────────────────────────────────────────────────────────────────

export default function NotificationBell() {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  const {
    notifications,
    unreadCount,
    markAsRead,
    markAllAsRead,
  } = useNotifications();
  const { emissions } = useEmissions({ limit: 50 });

  // Get the 5 most recent notifications for the dropdown
  const recentNotifications = notifications.slice(0, 5);
  const actionableEmissionCount = useMemo(() => {
    const nowMs = Date.now();
    return emissions.reduce((count, emission) => (
      isActionableRecentEmission(emission, nowMs) ? count + 1 : count
    ), 0);
  }, [emissions]);
  const badgeCount = unreadCount + actionableEmissionCount;

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
    }

    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
    };
  }, [isOpen]);

  // Close on escape key
  useEffect(() => {
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener("keydown", handleEscape);
    }

    return () => {
      document.removeEventListener("keydown", handleEscape);
    };
  }, [isOpen]);

  const handleNotificationClick = useCallback(
    (notification: Notification) => {
      if (!notification.read) {
        markAsRead(notification.id);
      }
      setIsOpen(false);
      if (notification.sourceUrl) {
        navigate(notification.sourceUrl);
      }
    },
    [markAsRead, navigate]
  );

  const handleMarkAllAsRead = useCallback(() => {
    markAllAsRead();
  }, [markAllAsRead]);

  const handleViewAll = useCallback(() => {
    setIsOpen(false);
  }, []);

  return (
    <div className="relative" ref={dropdownRef}>
      {/* Bell Button */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="relative rounded-xl p-2.5 text-slate-500 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
        aria-label={`Notifications${badgeCount > 0 ? ` (${badgeCount} alerts)` : ""}`}
        aria-expanded={isOpen}
        aria-haspopup="true"
      >
        <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"
          />
        </svg>

        {/* Badge */}
        {badgeCount > 0 && (
          <span
            className="absolute right-1 top-1 flex h-5 w-5 items-center justify-center rounded-full bg-red-500 text-xs font-semibold text-white"
            data-testid="notification-bell-badge"
          >
            {badgeCount > 9 ? "9+" : badgeCount}
          </span>
        )}
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div className="absolute right-0 top-full z-50 mt-2 w-80 overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-900 sm:w-96">
          {/* Header */}
          <div className="flex items-center justify-between border-b border-slate-200 px-4 py-3 dark:border-slate-700">
            <h3 className="font-semibold text-slate-900 dark:text-white">
              Notifications
            </h3>
            {unreadCount > 0 && (
              <button
                type="button"
                onClick={handleMarkAllAsRead}
                className="text-sm font-medium text-sky-600 transition hover:text-sky-700 dark:text-sky-400 dark:hover:text-sky-300"
              >
                Mark all as read
              </button>
            )}
          </div>

          {/* Notification List */}
          <div className="max-h-80 divide-y divide-slate-100 overflow-y-auto dark:divide-slate-800">
            {recentNotifications.length > 0 ? (
              recentNotifications.map((notification) => (
                <NotificationItem
                  key={notification.id}
                  notification={notification}
                  onMarkAsRead={() => markAsRead(notification.id)}
                  onClick={() => handleNotificationClick(notification)}
                />
              ))
            ) : (
              <div className="px-4 py-8 text-center">
                <span className="text-3xl">🦦</span>
                <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
                  No notifications yet
                </p>
                <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
                  You're all caught up!
                </p>
              </div>
            )}
          </div>

          {/* Footer */}
          {notifications.length > 0 && (
            <div className="border-t border-slate-200 dark:border-slate-700">
              <Link
                to="/notifications"
                onClick={handleViewAll}
                className="block px-4 py-3 text-center text-sm font-medium text-sky-600 transition hover:bg-slate-50 dark:text-sky-400 dark:hover:bg-slate-800"
              >
                View all notifications
              </Link>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
