import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type ReactNode,
} from "react";
import TaskThread from "./TaskThread";
import { useWS } from "../contexts/WebSocketContext";
import { useFocusTrap } from "../hooks/useFocusTrap";

// =============================================================================
// Types
// =============================================================================

export type TaskStatus = "todo" | "in-progress" | "done";
export type TaskPriority = "low" | "medium" | "high";

export type TaskLabel = {
  id: string;
  name: string;
  color: string;
};

export type TaskAssignee = {
  id: string;
  name: string;
  avatarUrl?: string;
};

export type TaskAttachment = {
  id: string;
  filename: string;
  size_bytes: number;
  mime_type: string;
  url: string;
  thumbnail_url?: string;
  uploadedAt: string;
  uploadedBy: string;
};

export type TaskActivity = {
  id: string;
  type: "created" | "status_changed" | "assigned" | "priority_changed" | "edited" | "commented";
  actor: string;
  timestamp: string;
  oldValue?: string;
  newValue?: string;
  description?: string;
};

export type TaskDetailData = {
  id: string;
  title: string;
  description?: string;
  status: TaskStatus;
  priority?: TaskPriority;
  assignee?: TaskAssignee;
  dueDate?: string;
  labels?: TaskLabel[];
  attachments?: TaskAttachment[];
  activities?: TaskActivity[];
  createdAt: string;
  updatedAt?: string;
};

export type TaskDetailProps = {
  taskId: string;
  isOpen: boolean;
  onClose: () => void;
  onTaskUpdated?: (task: TaskDetailData) => void;
  onTaskDeleted?: (taskId: string) => void;
  apiEndpoint?: string;
};

// =============================================================================
// Constants
// =============================================================================

const STATUS_OPTIONS: { value: TaskStatus; label: string; emoji: string }[] = [
  { value: "todo", label: "To Do", emoji: "📋" },
  { value: "in-progress", label: "In Progress", emoji: "🚀" },
  { value: "done", label: "Done", emoji: "✅" },
];

const PRIORITY_OPTIONS: { value: TaskPriority; label: string; color: string }[] = [
  { value: "low", label: "Low", color: "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300" },
  { value: "medium", label: "Medium", color: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300" },
  { value: "high", label: "High", color: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300" },
];

type TabId = "comments" | "activity" | "attachments";

const TABS: { id: TabId; label: string; icon: string }[] = [
  { id: "comments", label: "Comments", icon: "💬" },
  { id: "activity", label: "Activity", icon: "📜" },
  { id: "attachments", label: "Attachments", icon: "📎" },
];

// =============================================================================
// Utility Functions
// =============================================================================

function formatDate(isoString: string): string {
  const date = new Date(isoString);
  return date.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

function formatRelativeTime(isoString: string): string {
  const date = new Date(isoString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return formatDate(isoString);
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function getFileIcon(mimeType: string): string {
  if (mimeType.startsWith("image/")) return "🖼️";
  if (mimeType.startsWith("video/")) return "🎬";
  if (mimeType.startsWith("audio/")) return "🎵";
  if (mimeType.includes("pdf")) return "📄";
  if (mimeType.includes("word") || mimeType.includes("document")) return "📝";
  if (mimeType.includes("sheet") || mimeType.includes("excel")) return "📊";
  if (mimeType.includes("zip") || mimeType.includes("archive")) return "📦";
  return "📎";
}

function getInitials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/**
 * Simple markdown renderer for task descriptions.
 * Supports: bold, italic, code, links, lists, headers
 */
function renderMarkdown(text: string): ReactNode {
  const lines = text.split("\n");
  const elements: ReactNode[] = [];

  const processInline = (line: string, key: string): ReactNode => {
    // Process inline elements: bold, italic, code, links
    const parts: ReactNode[] = [];
    let remaining = line;
    let partKey = 0;

    while (remaining) {
      // Code (backticks)
      const codeMatch = remaining.match(/^`([^`]+)`/);
      if (codeMatch) {
        parts.push(
          <code
            key={`${key}-${partKey++}`}
            className="rounded bg-slate-200 px-1.5 py-0.5 text-sm dark:bg-slate-700"
          >
            {codeMatch[1]}
          </code>
        );
        remaining = remaining.slice(codeMatch[0].length);
        continue;
      }

      // Bold
      const boldMatch = remaining.match(/^\*\*([^*]+)\*\*/);
      if (boldMatch) {
        parts.push(<strong key={`${key}-${partKey++}`}>{boldMatch[1]}</strong>);
        remaining = remaining.slice(boldMatch[0].length);
        continue;
      }

      // Italic
      const italicMatch = remaining.match(/^\*([^*]+)\*/);
      if (italicMatch) {
        parts.push(<em key={`${key}-${partKey++}`}>{italicMatch[1]}</em>);
        remaining = remaining.slice(italicMatch[0].length);
        continue;
      }

      // Link
      const linkMatch = remaining.match(/^\[([^\]]+)\]\(([^)]+)\)/);
      if (linkMatch) {
        parts.push(
          <a
            key={`${key}-${partKey++}`}
            href={linkMatch[2]}
            target="_blank"
            rel="noopener noreferrer"
            className="text-sky-600 underline hover:text-sky-500 dark:text-sky-400"
          >
            {linkMatch[1]}
          </a>
        );
        remaining = remaining.slice(linkMatch[0].length);
        continue;
      }

      // Regular text - consume one character at a time until we hit a special character
      const nextSpecial = remaining.search(/[`*\[]/);
      if (nextSpecial === -1) {
        parts.push(remaining);
        break;
      } else if (nextSpecial === 0) {
        parts.push(remaining[0]);
        remaining = remaining.slice(1);
      } else {
        parts.push(remaining.slice(0, nextSpecial));
        remaining = remaining.slice(nextSpecial);
      }
    }

    return parts.length === 1 ? parts[0] : parts;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const key = `line-${i}`;

    // Headers
    const headerMatch = line.match(/^(#{1,6})\s+(.+)/);
    if (headerMatch) {
      const level = headerMatch[1].length;
      const content = processInline(headerMatch[2], key);
      const className = level === 1
        ? "text-xl font-bold text-slate-900 dark:text-white"
        : level === 2
        ? "text-lg font-semibold text-slate-900 dark:text-white"
        : "text-base font-medium text-slate-900 dark:text-white";
      elements.push(
        <p key={key} className={`${className} mt-3 first:mt-0`}>
          {content}
        </p>
      );
      continue;
    }

    // Unordered list
    if (line.match(/^[-*]\s+/)) {
      const content = processInline(line.replace(/^[-*]\s+/, ""), key);
      elements.push(
        <li key={key} className="ml-4 text-slate-700 dark:text-slate-300">
          {content}
        </li>
      );
      continue;
    }

    // Ordered list
    const orderedMatch = line.match(/^(\d+)\.\s+(.+)/);
    if (orderedMatch) {
      const content = processInline(orderedMatch[2], key);
      elements.push(
        <li key={key} className="ml-4 list-decimal text-slate-700 dark:text-slate-300">
          {content}
        </li>
      );
      continue;
    }

    // Empty line
    if (!line.trim()) {
      elements.push(<br key={key} />);
      continue;
    }

    // Regular paragraph
    elements.push(
      <p key={key} className="text-slate-700 dark:text-slate-300">
        {processInline(line, key)}
      </p>
    );
  }

  return <div className="space-y-2">{elements}</div>;
}

// =============================================================================
// Sub-Components
// =============================================================================

export function StatusBadge({ status }: { status: TaskStatus }) {
  const option = STATUS_OPTIONS.find((o) => o.value === status);
  if (!option) return null;

  const colors = {
    todo: "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300",
    "in-progress": "bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300",
    done: "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300",
  };

  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium ${colors[status]}`}>
      <span>{option.emoji}</span>
      {option.label}
    </span>
  );
}

export function PriorityBadge({ priority }: { priority: TaskPriority }) {
  const option = PRIORITY_OPTIONS.find((o) => o.value === priority);
  if (!option) return null;

  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium ${option.color}`}>
      {option.label}
    </span>
  );
}

function AssigneeAvatar({ assignee }: { assignee: TaskAssignee }) {
  if (assignee.avatarUrl) {
    return (
      <img
        src={assignee.avatarUrl}
        alt={assignee.name}
        loading="lazy"
        decoding="async"
        className="h-8 w-8 rounded-full object-cover ring-2 ring-white dark:ring-slate-800"
      />
    );
  }

  return (
    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-emerald-400 text-xs font-semibold text-white ring-2 ring-white dark:ring-slate-800">
      {getInitials(assignee.name)}
    </div>
  );
}

function LabelBadge({ label }: { label: TaskLabel }) {
  return (
    <span
      className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
      style={{
        backgroundColor: `${label.color}20`,
        color: label.color,
      }}
    >
      {label.name}
    </span>
  );
}

function ActivityItem({ activity }: { activity: TaskActivity }) {
  const getActivityDescription = (): string => {
    switch (activity.type) {
      case "created":
        return "created this task";
      case "status_changed":
        return `changed status from ${activity.oldValue} to ${activity.newValue}`;
      case "assigned":
        return activity.newValue
          ? `assigned to ${activity.newValue}`
          : "unassigned";
      case "priority_changed":
        return `changed priority from ${activity.oldValue} to ${activity.newValue}`;
      case "edited":
        return "edited the description";
      case "commented":
        return "added a comment";
      default:
        return activity.description || "performed an action";
    }
  };

  const getActivityIcon = (): string => {
    switch (activity.type) {
      case "created":
        return "✨";
      case "status_changed":
        return "🔄";
      case "assigned":
        return "👤";
      case "priority_changed":
        return "🎯";
      case "edited":
        return "✏️";
      case "commented":
        return "💬";
      default:
        return "📝";
    }
  };

  return (
    <div className="flex items-start gap-3 py-2">
      <span className="mt-0.5 text-base">{getActivityIcon()}</span>
      <div className="min-w-0 flex-1">
        <p className="text-sm text-slate-700 dark:text-slate-300">
          <span className="font-medium text-slate-900 dark:text-white">
            {activity.actor}
          </span>{" "}
          {getActivityDescription()}
        </p>
        <p className="text-xs text-slate-500 dark:text-slate-400">
          {formatRelativeTime(activity.timestamp)}
        </p>
      </div>
    </div>
  );
}

function AttachmentCard({
  attachment,
  onDelete,
}: {
  attachment: TaskAttachment;
  onDelete?: (id: string) => void;
}) {
  const isImage = attachment.mime_type.startsWith("image/");

  return (
    <div className="group relative overflow-hidden rounded-xl border border-slate-200 bg-white transition hover:border-slate-300 dark:border-slate-700 dark:bg-slate-800 dark:hover:border-slate-600">
      {isImage ? (
        <a
          href={attachment.url}
          target="_blank"
          rel="noopener noreferrer"
          className="block"
        >
          <img
            src={attachment.thumbnail_url || attachment.url}
            alt={attachment.filename}
            loading="lazy"
            decoding="async"
            className="h-32 w-full object-cover"
          />
          <div className="p-3">
            <p className="truncate text-sm font-medium text-slate-900 dark:text-white">
              {attachment.filename}
            </p>
            <p className="text-xs text-slate-500 dark:text-slate-400">
              {formatFileSize(attachment.size_bytes)}
            </p>
          </div>
        </a>
      ) : (
        <a
          href={attachment.url}
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-3 p-4"
        >
          <span className="text-2xl">{getFileIcon(attachment.mime_type)}</span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium text-slate-900 dark:text-white">
              {attachment.filename}
            </p>
            <p className="text-xs text-slate-500 dark:text-slate-400">
              {formatFileSize(attachment.size_bytes)} • {formatRelativeTime(attachment.uploadedAt)}
            </p>
          </div>
        </a>
      )}

      {onDelete && (
        <button
          type="button"
          onClick={() => onDelete(attachment.id)}
          className="absolute right-2 top-2 rounded-lg bg-white/90 p-1.5 text-slate-500 opacity-0 shadow-sm transition hover:bg-red-50 hover:text-red-600 group-hover:opacity-100 dark:bg-slate-900/90 dark:hover:bg-red-900/50 dark:hover:text-red-400"
        >
          <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      )}
    </div>
  );
}

// =============================================================================
// Main Component
// =============================================================================

export default function TaskDetail({
  taskId,
  isOpen,
  onClose,
  onTaskUpdated,
  onTaskDeleted,
  apiEndpoint = "https://api.otter.camp/api/tasks",
}: TaskDetailProps) {
  const [task, setTask] = useState<TaskDetailData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isEditing, setIsEditing] = useState(false);
  const [editedTask, setEditedTask] = useState<Partial<TaskDetailData>>({});
  const [isSaving, setIsSaving] = useState(false);
  const [activeTab, setActiveTab] = useState<TabId>("comments");
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const { lastMessage } = useWS();
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  // Focus trap for modal accessibility
  const { containerRef } = useFocusTrap({
    isActive: isOpen,
    onEscape: () => {
      if (showDeleteConfirm) {
        setShowDeleteConfirm(false);
      } else if (isEditing) {
        setIsEditing(false);
        setEditedTask({});
      } else {
        onClose();
      }
    },
    returnFocusOnClose: true,
    initialFocusRef: closeButtonRef,
  });

  // Fetch task details
  useEffect(() => {
    if (!isOpen || !taskId) return;

    const fetchTask = async () => {
      setIsLoading(true);
      setError(null);

      try {
        const response = await fetch(`${apiEndpoint}/${taskId}`);
        if (!response.ok) {
          throw new Error("Failed to fetch task details");
        }
        const data = await response.json();
        setTask(data.task || data);
        setEditedTask({});
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load task");
      } finally {
        setIsLoading(false);
      }
    };

    fetchTask();
  }, [apiEndpoint, isOpen, taskId]);

  // Handle WebSocket updates
  useEffect(() => {
    if (!lastMessage || !task) return;

    if (
      lastMessage.type === "TaskUpdated" &&
      (lastMessage.data as { id?: string })?.id === task.id
    ) {
      const updatedData = lastMessage.data as Partial<TaskDetailData>;
      setTask((prev) => (prev ? { ...prev, ...updatedData } : prev));
    }
  }, [lastMessage, task]);

  // Reset state when panel closes
  useEffect(() => {
    if (!isOpen) {
      setIsEditing(false);
      setEditedTask({});
      setShowDeleteConfirm(false);
      setActiveTab("comments");
    }
  }, [isOpen]);

  // Note: Escape key handling is done by useFocusTrap hook

  // Save edited task
  const handleSave = useCallback(async () => {
    if (!task || Object.keys(editedTask).length === 0) {
      setIsEditing(false);
      return;
    }

    setIsSaving(true);

    try {
      const response = await fetch(`${apiEndpoint}/${task.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(editedTask),
      });

      if (!response.ok) {
        throw new Error("Failed to update task");
      }

      const data = await response.json();
      const updatedTask = data.task || { ...task, ...editedTask };
      setTask(updatedTask);
      setEditedTask({});
      setIsEditing(false);
      onTaskUpdated?.(updatedTask);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save changes");
    } finally {
      setIsSaving(false);
    }
  }, [apiEndpoint, editedTask, onTaskUpdated, task]);

  // Update task field
  const updateTask = useCallback(
    async (field: keyof TaskDetailData, value: unknown) => {
      if (!task) return;

      // Optimistic update
      setTask((prev) => (prev ? { ...prev, [field]: value } : prev));

      try {
        const response = await fetch(`${apiEndpoint}/${task.id}`, {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ [field]: value }),
        });

        if (!response.ok) {
          throw new Error("Failed to update task");
        }

        const data = await response.json();
        const updatedTask = data.task || { ...task, [field]: value };
        onTaskUpdated?.(updatedTask);
      } catch (err) {
        // Revert on error
        setTask((prev) => (prev ? { ...prev, [field]: task[field] } : prev));
        setError(err instanceof Error ? err.message : "Failed to update");
      }
    },
    [apiEndpoint, onTaskUpdated, task]
  );

  // Delete task
  const handleDelete = useCallback(async () => {
    if (!task) return;

    try {
      const response = await fetch(`${apiEndpoint}/${task.id}`, {
        method: "DELETE",
      });

      if (!response.ok) {
        throw new Error("Failed to delete task");
      }

      onTaskDeleted?.(task.id);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete task");
    }
  }, [apiEndpoint, onClose, onTaskDeleted, task]);

  // Current values (edited or original)
  const currentTitle = useMemo(
    () => editedTask.title ?? task?.title ?? "",
    [editedTask.title, task?.title]
  );
  const currentDescription = useMemo(
    () => editedTask.description ?? task?.description ?? "",
    [editedTask.description, task?.description]
  );

  if (!isOpen) return null;

  return (
    <>
      {/* Backdrop */}
      <div
        className="fixed inset-0 z-40 bg-slate-950/50 backdrop-blur-sm transition-opacity"
        onClick={onClose}
        aria-hidden="true"
      />

      {/* Slide-over panel */}
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="task-detail-title"
        className="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col bg-white shadow-2xl dark:bg-slate-900"
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-200 px-6 py-4 dark:border-slate-800">
          <div className="flex items-center gap-3">
            <span className="text-xl" aria-hidden="true">🦦</span>
            <h2 id="task-detail-title" className="text-lg font-semibold text-slate-900 dark:text-white">
              Task Details
            </h2>
          </div>
          <div className="flex items-center gap-2">
            {!isEditing && task && (
              <button
                type="button"
                onClick={() => setIsEditing(true)}
                aria-label="Edit task"
                className="rounded-lg px-3 py-1.5 text-sm font-medium text-slate-600 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
              >
                <span aria-hidden="true">✏️</span> Edit
              </button>
            )}
            <button
              ref={closeButtonRef}
              type="button"
              onClick={onClose}
              className="rounded-lg p-2 text-slate-500 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
              aria-label="Close task details"
            >
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="flex h-64 items-center justify-center">
              <div className="flex items-center gap-3 text-slate-500">
                <div className="h-5 w-5 animate-spin rounded-full border-2 border-slate-300 border-t-sky-500" />
                <span>Loading task...</span>
              </div>
            </div>
          ) : error ? (
            <div className="m-6 rounded-xl bg-red-50 p-4 dark:bg-red-900/20">
              <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
              <button
                type="button"
                onClick={() => setError(null)}
                className="mt-2 text-sm font-medium text-red-700 underline hover:no-underline dark:text-red-300"
              >
                Dismiss
              </button>
            </div>
          ) : task ? (
            <div className="p-6">
              {/* Title */}
              <div className="mb-4">
                {isEditing ? (
                  <input
                    type="text"
                    value={currentTitle}
                    onChange={(e: ChangeEvent<HTMLInputElement>) =>
                      setEditedTask((prev) => ({ ...prev, title: e.target.value }))
                    }
                    className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-xl font-semibold text-slate-900 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 dark:border-slate-600 dark:bg-slate-800 dark:text-white"
                    placeholder="Task title"
                  />
                ) : (
                  <h1 className="text-xl font-semibold text-slate-900 dark:text-white">
                    {task.title}
                  </h1>
                )}
              </div>

              {/* Status, Priority, Assignee row */}
              <div className="mb-6 flex flex-wrap items-center gap-3">
                {/* Status Dropdown */}
                <div className="relative">
                  <select
                    value={task.status}
                    onChange={(e) => updateTask("status", e.target.value as TaskStatus)}
                    className="appearance-none rounded-full border border-slate-200 bg-white py-1 pl-3 pr-8 text-sm font-medium text-slate-700 transition hover:border-slate-300 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
                  >
                    {STATUS_OPTIONS.map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.emoji} {opt.label}
                      </option>
                    ))}
                  </select>
                  <svg
                    className="pointer-events-none absolute right-2 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                  >
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                  </svg>
                </div>

                {/* Priority Dropdown */}
                <div className="relative">
                  <select
                    value={task.priority || ""}
                    onChange={(e) =>
                      updateTask("priority", e.target.value as TaskPriority || undefined)
                    }
                    className="appearance-none rounded-full border border-slate-200 bg-white py-1 pl-3 pr-8 text-sm font-medium text-slate-700 transition hover:border-slate-300 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
                  >
                    <option value="">No priority</option>
                    {PRIORITY_OPTIONS.map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                  <svg
                    className="pointer-events-none absolute right-2 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                  >
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                  </svg>
                </div>

                {/* Assignee */}
                {task.assignee ? (
                  <div className="flex items-center gap-2">
                    <AssigneeAvatar assignee={task.assignee} />
                    <span className="text-sm font-medium text-slate-700 dark:text-slate-300">
                      {task.assignee.name}
                    </span>
                  </div>
                ) : (
                  <button
                    type="button"
                    className="rounded-full border border-dashed border-slate-300 px-3 py-1 text-sm text-slate-500 transition hover:border-slate-400 hover:text-slate-600 dark:border-slate-600 dark:text-slate-400"
                  >
                    + Assign
                  </button>
                )}
              </div>

              {/* Due Date & Labels */}
              <div className="mb-6 flex flex-wrap items-center gap-4 text-sm">
                {task.dueDate && (
                  <div className="flex items-center gap-2 text-slate-600 dark:text-slate-400">
                    <span>📅</span>
                    <span>Due {formatDate(task.dueDate)}</span>
                  </div>
                )}

                {task.labels && task.labels.length > 0 && (
                  <div className="flex flex-wrap gap-1">
                    {task.labels.map((label) => (
                      <LabelBadge key={label.id} label={label} />
                    ))}
                  </div>
                )}
              </div>

              {/* Description */}
              <div className="mb-6">
                <h3 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                  Description
                </h3>
                {isEditing ? (
                  <textarea
                    value={currentDescription}
                    onChange={(e: ChangeEvent<HTMLTextAreaElement>) =>
                      setEditedTask((prev) => ({ ...prev, description: e.target.value }))
                    }
                    rows={6}
                    className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-700 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300"
                    placeholder="Add a description... (Markdown supported)"
                  />
                ) : task.description ? (
                  <div className="prose prose-sm max-w-none dark:prose-invert">
                    {renderMarkdown(task.description)}
                  </div>
                ) : (
                  <p className="text-sm italic text-slate-400 dark:text-slate-500">
                    No description provided.
                  </p>
                )}
              </div>

              {/* Edit Actions */}
              {isEditing && (
                <div className="mb-6 flex items-center gap-3">
                  <button
                    type="button"
                    onClick={handleSave}
                    disabled={isSaving}
                    className="rounded-lg bg-sky-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2 disabled:opacity-50 dark:focus:ring-offset-slate-900"
                  >
                    {isSaving ? "Saving..." : "Save Changes"}
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setIsEditing(false);
                      setEditedTask({});
                    }}
                    className="rounded-lg px-4 py-2 text-sm font-medium text-slate-600 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
                  >
                    Cancel
                  </button>
                </div>
              )}

              {/* Tabs */}
              <div className="border-t border-slate-200 pt-6 dark:border-slate-800">
                <div className="mb-4 flex gap-1 border-b border-slate-200 dark:border-slate-800">
                  {TABS.map((tab) => (
                    <button
                      key={tab.id}
                      type="button"
                      onClick={() => setActiveTab(tab.id)}
                      className={`flex items-center gap-2 border-b-2 px-4 py-2 text-sm font-medium transition ${
                        activeTab === tab.id
                          ? "border-sky-500 text-sky-600 dark:text-sky-400"
                          : "border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300"
                      }`}
                    >
                      <span>{tab.icon}</span>
                      {tab.label}
                      {tab.id === "attachments" && task.attachments && (
                        <span className="ml-1 rounded-full bg-slate-200 px-1.5 text-xs dark:bg-slate-700">
                          {task.attachments.length}
                        </span>
                      )}
                    </button>
                  ))}
                </div>

                {/* Tab Content */}
                <div className="min-h-[300px]">
                  {activeTab === "comments" && (
                    <TaskThread
                      taskId={taskId}
                      apiEndpoint="/api/messages"
                    />
                  )}

                  {activeTab === "activity" && (
                    <div className="space-y-1">
                      {task.activities && task.activities.length > 0 ? (
                        task.activities.map((activity) => (
                          <ActivityItem key={activity.id} activity={activity} />
                        ))
                      ) : (
                        <div className="flex flex-col items-center justify-center py-12 text-center">
                          <span className="text-3xl">📜</span>
                          <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
                            No activity yet
                          </p>
                        </div>
                      )}
                    </div>
                  )}

                  {activeTab === "attachments" && (
                    <div>
                      {task.attachments && task.attachments.length > 0 ? (
                        <div className="grid gap-4 sm:grid-cols-2">
                          {task.attachments.map((attachment) => (
                            <AttachmentCard
                              key={attachment.id}
                              attachment={attachment}
                            />
                          ))}
                        </div>
                      ) : (
                        <div className="flex flex-col items-center justify-center py-12 text-center">
                          <span className="text-3xl">📎</span>
                          <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
                            No attachments yet
                          </p>
                          <button
                            type="button"
                            className="mt-3 rounded-lg border border-slate-200 px-4 py-2 text-sm font-medium text-slate-600 transition hover:border-slate-300 hover:bg-slate-50 dark:border-slate-700 dark:text-slate-400 dark:hover:border-slate-600 dark:hover:bg-slate-800"
                          >
                            Upload files
                          </button>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              </div>
            </div>
          ) : null}
        </div>

        {/* Footer with actions */}
        {task && !isEditing && (
          <div className="border-t border-slate-200 px-6 py-4 dark:border-slate-800">
            <div className="flex items-center justify-between">
              <div className="text-xs text-slate-500 dark:text-slate-400">
                Created {formatRelativeTime(task.createdAt)}
                {task.updatedAt && task.updatedAt !== task.createdAt && (
                  <> • Updated {formatRelativeTime(task.updatedAt)}</>
                )}
              </div>

              <div className="flex items-center gap-2">
                {showDeleteConfirm ? (
                  <>
                    <span className="text-sm text-slate-600 dark:text-slate-400">
                      Delete this task?
                    </span>
                    <button
                      type="button"
                      onClick={handleDelete}
                      className="rounded-lg bg-red-600 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-red-500"
                    >
                      Confirm
                    </button>
                    <button
                      type="button"
                      onClick={() => setShowDeleteConfirm(false)}
                      className="rounded-lg px-3 py-1.5 text-sm font-medium text-slate-600 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
                    >
                      Cancel
                    </button>
                  </>
                ) : (
                  <button
                    type="button"
                    onClick={() => setShowDeleteConfirm(true)}
                    className="rounded-lg px-3 py-1.5 text-sm font-medium text-red-600 transition hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
                  >
                    🗑️ Delete
                  </button>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
