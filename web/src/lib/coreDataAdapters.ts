type UnknownRecord = Record<string, unknown>;

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export type CoreInboxItem = {
  id: string;
  issueId?: string;
  type: "approval" | "issue" | "notification" | "mention";
  title: string;
  description: string;
  project: string;
  from: string;
  timestamp: string;
  priority: "critical" | "high" | "medium" | "low";
  read: boolean;
  starred: boolean;
  status: string;
};

export type CoreProjectCard = {
  id: string;
  name: string;
  repo: string;
  status: "active";
  openIssues: number;
  inProgress: number;
  needsApproval: number;
  githubSync: boolean;
  techStack: string[];
};

export type CoreProjectDetailProject = {
  id: string;
  name: string;
  description: string;
  repo: string;
  lastSync: string;
  stats: {
    openIssues: number;
    branches: number;
    commits: number;
    contributors: number;
  };
};

export type CoreProjectDetailIssue = {
  id: string;
  title: string;
  status: "approval-needed" | "in-progress" | "blocked" | "review" | "open";
  priority: "critical" | "high" | "medium" | "low";
  assignee: string | null;
  created: string;
};

function asRecord(value: unknown): UnknownRecord | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as UnknownRecord;
}

function asArray(value: unknown): unknown[] {
  if (Array.isArray(value)) {
    return value;
  }
  return [];
}

function readString(record: UnknownRecord, ...keys: string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (trimmed) {
        return trimmed;
      }
    }
  }
  return "";
}

function readNumber(record: UnknownRecord, ...keys: string[]): number | null {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    if (typeof value === "string" && value.trim() !== "") {
      const parsed = Number(value);
      if (Number.isFinite(parsed)) {
        return parsed;
      }
    }
  }
  return null;
}

function readBoolean(record: UnknownRecord, ...keys: string[]): boolean | null {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "boolean") {
      return value;
    }
    if (typeof value === "string") {
      const normalized = value.trim().toLowerCase();
      if (normalized === "true") {
        return true;
      }
      if (normalized === "false") {
        return false;
      }
    }
  }
  return null;
}

function toCount(value: number | null): number {
  if (value === null || Number.isNaN(value)) {
    return 0;
  }
  return Math.max(0, Math.floor(value));
}

function toRelativeTimestamp(input: string, now: Date): string {
  const parsed = new Date(input);
  if (Number.isNaN(parsed.getTime())) {
    return "Unknown";
  }

  const diffMs = now.getTime() - parsed.getTime();
  if (diffMs <= 0) {
    return "Just now";
  }

  const diffMinutes = Math.floor(diffMs / 60000);
  if (diffMinutes < 1) {
    return "Just now";
  }
  if (diffMinutes < 60) {
    return `${diffMinutes}m ago`;
  }

  const diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) {
    return `${diffHours}h ago`;
  }

  const diffDays = Math.floor(diffHours / 24);
  if (diffDays < 7) {
    return `${diffDays}d ago`;
  }

  return parsed.toISOString().slice(0, 10);
}

function normalizeRepoLabel(rawRepo: string, fallbackID: string): string {
  const repo = rawRepo.trim();
  if (!repo) {
    return `local/${fallbackID || "project"}`;
  }

  const withoutProtocol = repo.replace(/^https?:\/\//i, "").replace(/\.git$/i, "");
  if (withoutProtocol.startsWith("github.com/")) {
    return withoutProtocol;
  }
  if (withoutProtocol.startsWith("git@github.com:")) {
    return withoutProtocol.replace(/^git@github\.com:/i, "github.com/");
  }
  return withoutProtocol;
}

function inferIssueLabel(record: UnknownRecord, fallbackIndex?: number): string | undefined {
  const issueNumber = readNumber(record, "issue_number", "issueNumber", "github_number");
  if (issueNumber !== null && issueNumber > 0) {
    return `ISS-${Math.floor(issueNumber)}`;
  }

  const issueID = readString(record, "issue_id", "issueId");
  if (issueID && !UUID_PATTERN.test(issueID)) {
    return issueID;
  }

  const command = readString(record, "command", "title", "description", "message");
  const match = command.match(/\bISS-\d+\b/i);
  if (match) {
    return match[0].toUpperCase();
  }

  if (typeof fallbackIndex === "number" && fallbackIndex > 0) {
    return `ISS-${fallbackIndex}`;
  }
  return undefined;
}

function mapInboxPriority(status: string): CoreInboxItem["priority"] {
  const normalized = status.trim().toLowerCase();
  if (normalized === "pending") {
    return "high";
  }
  if (normalized === "processing") {
    return "medium";
  }
  if (normalized === "denied" || normalized === "cancelled" || normalized === "expired") {
    return "low";
  }
  return "medium";
}

function mapDetailStatus(record: UnknownRecord): CoreProjectDetailIssue["status"] {
  const approvalState = readString(record, "approval_state", "approvalState").toLowerCase();
  if (approvalState === "ready_for_review") {
    return "approval-needed";
  }
  if (approvalState === "needs_changes") {
    return "blocked";
  }
  if (approvalState === "approved") {
    return "review";
  }

  const workStatus = readString(record, "work_status", "workStatus", "status").toLowerCase();
  if (workStatus.includes("block")) {
    return "blocked";
  }
  if (workStatus.includes("review")) {
    return "review";
  }
  if (workStatus.includes("progress") || workStatus.includes("active")) {
    return "in-progress";
  }
  if (workStatus.includes("approval")) {
    return "approval-needed";
  }

  const state = readString(record, "state").toLowerCase();
  if (state === "closed") {
    return "review";
  }
  return "open";
}

function mapDetailPriority(record: UnknownRecord): CoreProjectDetailIssue["priority"] {
  const raw = readString(record, "priority").toLowerCase();
  if (raw.includes("critical") || raw === "p0") {
    return "critical";
  }
  if (raw.includes("high") || raw === "p1") {
    return "high";
  }
  if (raw.includes("medium") || raw === "p2") {
    return "medium";
  }
  return "low";
}

function normalizeAssignee(record: UnknownRecord): string | null {
  const direct = readString(record, "assignee", "owner_agent_name", "agent");
  if (direct) {
    return direct;
  }
  const ownerID = readString(record, "owner_agent_id", "ownerAgentId", "agent_id");
  if (!ownerID || UUID_PATTERN.test(ownerID)) {
    return null;
  }
  return ownerID;
}

function readRowsFromPayload(payload: unknown, keys: string[]): unknown[] {
  if (Array.isArray(payload)) {
    return payload;
  }

  const record = asRecord(payload);
  if (!record) {
    return [];
  }

  for (const key of keys) {
    if (Array.isArray(record[key])) {
      return record[key] as unknown[];
    }
  }

  return [];
}

export function mapInboxPayloadToCoreItems(payload: unknown, now = new Date()): CoreInboxItem[] {
  const rows = readRowsFromPayload(payload, ["items", "requests"]);
  return rows
    .map<CoreInboxItem | null>((row, index) => {
      const record = asRecord(row);
      if (!record) {
        return null;
      }

      const id = readString(record, "id") || `inbox-${index + 1}`;
      const status = readString(record, "status").toLowerCase() || "pending";
      const command = readString(record, "command");
      const title = readString(record, "title") || "Approval request";
      const description = readString(record, "description", "message") || (command ? `Run: ${command}` : "Awaiting response");
      const timestampSource = readString(record, "createdAt", "created_at");

      return {
        id,
        issueId: inferIssueLabel(record),
        type: "approval",
        title,
        description,
        project: readString(record, "project", "project_name") || "Exec approvals",
        from: readString(record, "agent", "agent_name", "agent_id") || "Unknown",
        timestamp: toRelativeTimestamp(timestampSource, now),
        priority: mapInboxPriority(status),
        read: !(status === "pending" || status === "processing"),
        starred: false,
        status,
      } satisfies CoreInboxItem;
    })
    .filter((item): item is CoreInboxItem => item !== null);
}

export function mapProjectsPayloadToCoreCards(payload: unknown): CoreProjectCard[] {
  const rows = readRowsFromPayload(payload, ["projects"]);
  return rows
    .map((row, index) => {
      const record = asRecord(row);
      if (!record) {
        return null;
      }

      const id = readString(record, "id") || `project-${index + 1}`;
      const name = readString(record, "name") || "Untitled Project";
      const repoRaw = readString(record, "repo", "repo_url", "repoUrl");
      const taskCount = toCount(readNumber(record, "taskCount", "task_count"));
      const completedCount = toCount(readNumber(record, "completedCount", "completed_count"));
      const openIssues =
        toCount(readNumber(record, "openIssues", "open_issues")) ||
        Math.max(taskCount - completedCount, 0);
      const inProgress = toCount(readNumber(record, "inProgress", "in_progress"));
      const needsApprovalRaw = readNumber(record, "needsApproval", "needs_approval");
      const requireHumanReview = readBoolean(record, "require_human_review", "requireHumanReview");
      const needsApproval =
        needsApprovalRaw !== null ? toCount(needsApprovalRaw) : requireHumanReview ? 1 : 0;

      const techStack = asArray(record.techStack ?? record.tech_stack)
        .filter((value): value is string => typeof value === "string")
        .map((value) => value.trim())
        .filter((value) => value !== "");

      const githubSync =
        readBoolean(record, "githubSync", "github_sync") ??
        (!!repoRaw && !repoRaw.startsWith("local/"));

      return {
        id,
        name,
        repo: normalizeRepoLabel(repoRaw, id),
        status: "active",
        openIssues,
        inProgress,
        needsApproval,
        githubSync,
        techStack,
      } satisfies CoreProjectCard;
    })
    .filter((item): item is CoreProjectCard => item !== null);
}

export function mapProjectPayloadToCoreDetailProject(
  payload: unknown,
  issueCountFallback = 0,
  now = new Date(),
): CoreProjectDetailProject {
  const record = asRecord(payload) ?? {};
  const id = readString(record, "id") || "project";
  const name = readString(record, "name") || "Untitled Project";
  const repoRaw = readString(record, "repo", "repo_url", "repoUrl");
  const taskCount = toCount(readNumber(record, "taskCount", "task_count"));
  const completedCount = toCount(readNumber(record, "completedCount", "completed_count"));
  const openIssues =
    toCount(readNumber(record, "openIssues", "open_issues")) ||
    Math.max(taskCount - completedCount, 0) ||
    Math.max(issueCountFallback, 0);
  const lastSyncSource = readString(record, "last_sync", "workflow_last_run_at", "updated_at", "created_at");

  return {
    id,
    name,
    description: readString(record, "description") || "No description provided.",
    repo: normalizeRepoLabel(repoRaw, id),
    lastSync: toRelativeTimestamp(lastSyncSource, now),
    stats: {
      openIssues,
      branches: toCount(readNumber(record, "branches")),
      commits: toCount(readNumber(record, "commits")),
      contributors: toCount(readNumber(record, "contributors")),
    },
  };
}

export function mapProjectIssuesPayloadToCoreDetailIssues(
  payload: unknown,
  now = new Date(),
): CoreProjectDetailIssue[] {
  const rows = readRowsFromPayload(payload, ["items"]);
  return rows
    .map((row, index) => {
      const record = asRecord(row);
      if (!record) {
        return null;
      }

      const id = inferIssueLabel(record, index + 1) || `ISS-${index + 1}`;
      const title = readString(record, "title") || "Untitled issue";
      const created = toRelativeTimestamp(readString(record, "last_activity_at", "created_at"), now);

      return {
        id,
        title,
        status: mapDetailStatus(record),
        priority: mapDetailPriority(record),
        assignee: normalizeAssignee(record),
        created,
      } satisfies CoreProjectDetailIssue;
    })
    .filter((item): item is CoreProjectDetailIssue => item !== null);
}
