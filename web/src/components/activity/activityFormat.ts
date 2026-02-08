export type ActivityTypeConfig = {
  label: string;
  icon: string;
};

const DEFAULT_TYPE_CONFIG: ActivityTypeConfig = {
  label: "Activity",
  icon: "📝",
};

const TYPE_CONFIG: Record<string, ActivityTypeConfig> = {
  commit: { label: "Commit", icon: "🔨" },
  comment: { label: "Comment", icon: "💬" },
  message: { label: "Message", icon: "✉️" },
  task_created: { label: "Task Created", icon: "✨" },
  task_update: { label: "Task Updated", icon: "✏️" },
  task_updated: { label: "Task Updated", icon: "✏️" },
  task_status_changed: { label: "Status Change", icon: "🔄" },
  dispatch: { label: "Dispatch", icon: "📤" },
  assignment: { label: "Assignment", icon: "👤" },
  "git.push": { label: "Git Push", icon: "⬆️" },
};

export function getTypeConfig(type: string): ActivityTypeConfig {
  return TYPE_CONFIG[type] ?? {
    ...DEFAULT_TYPE_CONFIG,
    label: humanizeType(type) || DEFAULT_TYPE_CONFIG.label,
  };
}

export function humanizeType(type: string): string {
  const cleaned = String(type || "")
    .trim()
    .replace(/[._-]+/g, " ")
    .replace(/\s+/g, " ");

  if (!cleaned) return "";

  return cleaned
    .split(" ")
    .map((word) => word.slice(0, 1).toUpperCase() + word.slice(1))
    .join(" ");
}

export function formatRelativeTime(date: Date): string {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;

  return date.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: now.getFullYear() === date.getFullYear() ? undefined : "numeric",
  });
}

export function truncate(text: string, maxLen: number): string {
  const value = String(text ?? "");
  if (value.length <= maxLen) return value;
  return value.slice(0, Math.max(0, maxLen - 1)) + "…";
}

export function normalizeMetadata(value: unknown): Record<string, unknown> {
  if (!value) return {};
  if (typeof value !== "object") return { value };
  if (Array.isArray(value)) return { items: value };
  return value as Record<string, unknown>;
}

export function getMetadataString(metadata: Record<string, unknown>, key: string): string {
  const value = metadata[key];
  return typeof value === "string" ? value : "";
}

export function stripActorPrefix(summary: string, actorName: string): string {
  const rawSummary = String(summary ?? "").trim();
  const rawActor = String(actorName ?? "").trim();
  if (!rawSummary || !rawActor) return rawSummary;

  const summaryLower = rawSummary.toLowerCase();
  const actorLower = rawActor.toLowerCase();

  if (!summaryLower.startsWith(actorLower)) return rawSummary;

  let rest = rawSummary.slice(rawActor.length).trimStart();
  rest = rest.replace(/^[:,-]\s*/, "");
  return rest || rawSummary;
}

function normalizeSummaryToken(value: string): string {
  return String(value ?? "")
    .trim()
    .toLowerCase()
    .replace(/[._-]+/g, " ")
    .replace(/\s+/g, " ");
}

type DescriptionInput = {
  type: string;
  actorName?: string;
  taskTitle?: string;
  summary?: string;
  metadata?: Record<string, unknown>;
};

export function getActivityDescription(input: DescriptionInput): string {
  const summary = String(input.summary ?? "").trim();
  const type = String(input.type ?? "");

  if (summary) {
    const resolvedSummary = input.actorName ? stripActorPrefix(summary, input.actorName) : summary;
    const normalizedSummary = normalizeSummaryToken(resolvedSummary);
    const normalizedType = normalizeSummaryToken(type);
    const normalizedHumanizedType = normalizeSummaryToken(humanizeType(type));
    if (
      normalizedSummary &&
      normalizedSummary !== normalizedType &&
      normalizedSummary !== normalizedHumanizedType
    ) {
      return resolvedSummary;
    }
  }

  const taskTitle = String(input.taskTitle ?? "").trim();
  const metadata = normalizeMetadata(input.metadata);

  switch (type) {
    case "task_created":
      return taskTitle ? `created task "${taskTitle}"` : "created a task";
    case "task_update":
    case "task_updated":
      return taskTitle ? `updated task "${taskTitle}"` : "updated a task";
    case "task_status_changed": {
      const status = getMetadataString(metadata, "new_status") || getMetadataString(metadata, "status");
      if (taskTitle && status) return `changed task "${taskTitle}" to ${status}`;
      if (taskTitle) return `changed status of "${taskTitle}"`;
      return status ? `changed status to ${status}` : "changed a task status";
    }
    case "comment": {
      const text =
        getMetadataString(metadata, "text") ||
        getMetadataString(metadata, "comment") ||
        getMetadataString(metadata, "content") ||
        getMetadataString(metadata, "preview");
      if (taskTitle && text) return `commented on "${taskTitle}": "${truncate(text, 80)}"`;
      if (taskTitle) return `commented on "${taskTitle}"`;
      return text ? `commented: "${truncate(text, 80)}"` : "added a comment";
    }
    case "commit": {
      const repo = getMetadataString(metadata, "repo");
      const msg = getMetadataString(metadata, "message");
      if (repo && msg) return `committed to ${repo}: "${truncate(msg, 80)}"`;
      if (repo) return `committed to ${repo}`;
      return msg ? `committed: "${truncate(msg, 80)}"` : "made a commit";
    }
    case "git.push": {
      const project = getMetadataString(metadata, "project_name") || getMetadataString(metadata, "project_id");
      const branch =
        getMetadataString(metadata, "branch") ||
        getMetadataString(metadata, "ref") ||
        getMetadataString(metadata, "ref_name");
      const commitMessage =
        getMetadataString(metadata, "commit_message") ||
        getMetadataString(metadata, "head_commit_message") ||
        getMetadataString(metadata, "message");

      if (branch && commitMessage) return `pushed to ${branch}: "${truncate(commitMessage, 80)}"`;
      if (project && commitMessage) return `pushed to ${project}: "${truncate(commitMessage, 80)}"`;
      if (project && branch) return `pushed to ${project} (${branch})`;
      if (branch) return `pushed to ${branch}`;
      if (project) return `pushed to ${project}`;
      return "pushed changes";
    }
    case "message": {
      const preview = getMetadataString(metadata, "preview");
      return preview ? `"${truncate(preview, 90)}"` : "sent a message";
    }
    case "dispatch":
      return taskTitle ? `dispatched "${taskTitle}"` : "dispatched a task";
    case "assignment":
      return taskTitle ? `was assigned to "${taskTitle}"` : "received an assignment";
    default:
      return taskTitle ? `${humanizeType(type)} on "${taskTitle}"` : humanizeType(type) || "activity";
  }
}
