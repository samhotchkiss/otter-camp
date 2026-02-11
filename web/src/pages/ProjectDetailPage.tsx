// Cache bust: 2026-02-05-11:15
import { useState, useEffect, useMemo, useRef, useCallback, type FormEvent } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import ProjectFileBrowser from "../components/project/ProjectFileBrowser";
import ProjectIssuesList from "../components/project/ProjectIssuesList";
import IssueThreadPanel from "../components/project/IssueThreadPanel";
import PipelineMiniProgress from "../components/issues/PipelineMiniProgress";
import EmissionStream from "../components/EmissionStream";
import ProjectSettingsPage from "./project/ProjectSettingsPage";
import WorkflowConfig, {
  defaultWorkflowConfigState,
  type WorkflowConfigState,
} from "../components/project/WorkflowConfig";
import { useGlobalChat } from "../contexts/GlobalChatContext";
import { useOptionalWS } from "../contexts/WebSocketContext";
import { getActivityDescription, normalizeMetadata } from "../components/activity/activityFormat";
import useEmissions from "../hooks/useEmissions";
import useNowTicker from "../hooks/useNowTicker";
import { API_URL } from "../lib/api";

// Agent color mappings
const agentColors: Record<string, string> = {
  "Derek": "var(--blue, #4A6D7C)",
  "Ivy": "var(--green, #5A7A5C)",
  "Jeff G": "var(--orange, #C87941)",
  "Stone": "#ec4899",
  "Josh S": "var(--blue, #4A6D7C)",
  "Frank": "var(--accent, #C9A86C)",
  "Nova": "#a855f7",
  "Max": "#06b6d4",
  "Penny": "#f59e0b",
  "Beau H": "#10b981",
  "Jeremy H": "#6366f1",
  "Claudette": "#ec4899",
};

// Project emoji mappings  
const projectEmojis: Record<string, string> = {
  "Pearl Proxy": "🔮",
  "Otter Camp": "🦦",
  "ItsAlive": "⚡",
  "Three Stones": "🪨",
  "OpenClaw": "🦀",
};

type Project = {
  id: string;
  name: string;
  description?: string;
  status?: string;
  lead?: string;
  repo_url?: string;
  require_human_review?: boolean;
  primary_agent_id?: string;
  workflow_enabled?: boolean;
  workflow_schedule?: {
    kind?: string;
    expr?: string;
    tz?: string;
    everyMs?: number;
    at?: string;
  } | null;
  workflow_template?: {
    title_pattern?: string;
    body?: string;
    priority?: "P0" | "P1" | "P2" | "P3";
    labels?: string[];
    auto_close?: boolean;
    pipeline?: "none" | "auto_close" | "standard";
  } | null;
  workflow_agent_id?: string | null;
};

type AgentOption = {
  id: string;
  name: string;
};

const UUID_REGEX =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const USER_NAME_STORAGE_KEY = "otter-camp-user-name";

type ApiIssue = {
  id: string;
  title: string;
  issue_number?: number;
  owner_agent_id?: string;
  work_status?: string;
  priority?: string;
};

type Task = {
  id: string;
  issueNumber?: number;
  title: string;
  status:
    | "queued"
    | "in_progress"
    | "review"
    | "done"
    | "blocked"
    | "dispatched"
    | "cancelled"
    | "ready"
    | "planning"
    | "ready_for_work"
    | "flagged";
  priority: "P0" | "P1" | "P2" | "P3";
  assignee: string;
  avatarColor: string;
  blocked?: boolean;
  isActive?: boolean;
};

type Activity = {
  id: string;
  agent: string;
  avatarColor: string;
  text: string;
  highlight: string;
  timeAgo: string;
};

function getCurrentAuthorName(): string {
  try {
    const candidate = (localStorage.getItem(USER_NAME_STORAGE_KEY) ?? "").trim();
    if (candidate !== "") {
      return candidate;
    }
  } catch {
    // ignore localStorage failures
  }
  return "You";
}

function buildProjectIssueRequestMessage(projectName: string, issueTitle: string): string {
  return [
    "New issue request",
    `Project: ${projectName}`,
    `Title: ${issueTitle}`,
    "",
    "Please create a new project issue for this request.",
  ].join("\n");
}

function workflowConfigFromProject(project: Project): WorkflowConfigState {
  const base = defaultWorkflowConfigState();
  const schedule = project.workflow_schedule || undefined;
  const template = project.workflow_template || undefined;
  const kind = schedule?.kind === "every" || schedule?.kind === "at" ? schedule.kind : "cron";
  return {
    ...base,
    enabled: project.workflow_enabled === true,
    scheduleKind: kind,
    cronExpr: schedule?.expr || base.cronExpr,
    tz: schedule?.tz || base.tz,
    everyMs: typeof schedule?.everyMs === "number" ? String(schedule.everyMs) : base.everyMs,
    at: schedule?.at || "",
    titlePattern: template?.title_pattern || "",
    body: template?.body || "",
    priority: template?.priority || base.priority,
    labels: Array.isArray(template?.labels) ? template?.labels.join(", ") : base.labels,
    pipeline: template?.pipeline || base.pipeline,
    autoClose: template?.auto_close ?? base.autoClose,
    workflowAgentID: project.workflow_agent_id || "",
  };
}

function workflowSchedulePayloadFromConfig(config: WorkflowConfigState): Record<string, unknown> {
  if (config.scheduleKind === "every") {
    const everyMs = Number.parseInt(config.everyMs.trim(), 10);
    return {
      kind: "every",
      everyMs: Number.isFinite(everyMs) && everyMs > 0 ? everyMs : 900000,
    };
  }
  if (config.scheduleKind === "at") {
    return {
      kind: "at",
      at: config.at.trim(),
    };
  }
  return {
    kind: "cron",
    expr: config.cronExpr.trim(),
    tz: config.tz.trim(),
  };
}

function workflowTemplatePayloadFromConfig(config: WorkflowConfigState): Record<string, unknown> {
  const labels = config.labels
    .split(",")
    .map((label) => label.trim())
    .filter((label) => label !== "");
  return {
    title_pattern: config.titlePattern.trim(),
    body: config.body.trim(),
    priority: config.priority,
    labels,
    auto_close: config.autoClose,
    pipeline: config.pipeline,
  };
}

type TaskColumn = {
  key: string;
  title: string;
  statuses: Task["status"][];
};

const COLUMNS: TaskColumn[] = [
  { key: "planning", title: "Planning", statuses: ["planning"] },
  { key: "queue", title: "Queue", statuses: ["queued", "dispatched", "ready", "ready_for_work"] },
  { key: "in_progress", title: "In Progress", statuses: ["in_progress"] },
  { key: "review", title: "Review", statuses: ["review", "blocked", "flagged"] },
  { key: "done", title: "Done", statuses: ["done", "cancelled"] },
];

const LIST_STATUS_BADGE: Record<Task["status"], { label: string; className: string }> = {
  queued: {
    label: "Queued",
    className: "bg-[var(--surface-alt)] text-[var(--text-muted)]",
  },
  ready: {
    label: "Ready",
    className: "bg-[var(--surface-alt)] text-[var(--text-muted)]",
  },
  planning: {
    label: "Planning",
    className: "bg-[var(--surface-alt)] text-[var(--text-muted)]",
  },
  ready_for_work: {
    label: "Ready for Work",
    className: "bg-[var(--surface-alt)] text-[var(--text-muted)]",
  },
  dispatched: {
    label: "Dispatched",
    className: "bg-[var(--surface-alt)] text-[var(--text-muted)]",
  },
  in_progress: {
    label: "In Progress",
    className: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300",
  },
  review: {
    label: "Review",
    className: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
  },
  blocked: {
    label: "Blocked",
    className: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
  },
  flagged: {
    label: "Flagged",
    className: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
  },
  done: {
    label: "Done",
    className: "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300",
  },
  cancelled: {
    label: "Cancelled",
    className: "bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-300",
  },
};

const ACTIVE_EMISSION_WINDOW_MS = 45_000;

function formatTimeAgo(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);
  
  if (diffMins < 1) return "just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${diffDays}d ago`;
}

function TaskCard({ task, onClick }: { task: Task; onClick?: () => void }) {
  const priorityClasses: Record<string, string> = {
    P0: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
    P1: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400",
    P2: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
    P3: "bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400",
  };

  return (
    <div
      onClick={onClick}
      className={`cursor-pointer rounded-xl border bg-[var(--surface)] p-4 transition hover:-translate-y-0.5 hover:shadow-md ${
        task.blocked
          ? "border-l-4 border-l-[#C9A86C] border-t-[var(--border)] border-r-[var(--border)] border-b-[var(--border)]"
          : "border-[var(--border)] hover:border-[#C9A86C]/50"
      } ${task.status === "done" ? "opacity-70" : ""}`}
    >
      {typeof task.issueNumber === "number" && (
        <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">
          #{task.issueNumber}
        </p>
      )}
      {task.isActive ? (
        <p className="mb-2 inline-flex items-center gap-1 rounded-full border border-emerald-400/40 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-300">
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-300" />
          Working
        </p>
      ) : null}
      <h4 className="mb-3 text-sm font-semibold text-[var(--text)]">
        {task.title}
      </h4>
      <div className="mb-3">
        <div data-testid={`project-board-mini-${task.id}`}>
          <PipelineMiniProgress status={task.status} />
        </div>
      </div>
      <div className="flex items-center justify-between text-xs">
        <div className="flex items-center gap-2">
          <div
            className="flex h-6 w-6 items-center justify-center rounded-full text-[10px] font-semibold text-white"
            style={{ backgroundColor: task.avatarColor }}
          >
            {task.assignee[0]}
          </div>
          <span className="text-[var(--text-muted)]">{task.assignee}</span>
        </div>
        {task.status !== "done" && (
          <span className={`rounded px-2 py-0.5 text-[10px] font-semibold ${priorityClasses[task.priority] || priorityClasses.P2}`}>
            {task.priority}{task.blocked ? " • Blocked" : ""}
          </span>
        )}
      </div>
    </div>
  );
}

function BoardColumn({
  columnKey,
  title,
  tasks,
  onTaskClick,
}: {
  columnKey: string;
  title: string;
  tasks: Task[];
  onTaskClick?: (task: Task) => void;
}) {
  return (
    <div
      className="flex w-80 flex-shrink-0 flex-col rounded-xl bg-[var(--surface-alt)]"
      data-testid={`board-column-${columnKey}`}
    >
      <div className="flex items-center gap-3 border-b border-[var(--border)] px-5 py-4">
        <span className="text-sm font-bold text-[var(--text)]">{title}</span>
        <span className="rounded-full bg-[var(--surface)] px-2.5 py-0.5 text-xs font-semibold text-[var(--text-muted)]">
          {tasks.length}
        </span>
      </div>
      <div className="flex flex-1 flex-col gap-3 overflow-y-auto p-3" style={{ maxHeight: "calc(100vh - 340px)" }}>
        {tasks.map((task) => (
          <TaskCard key={task.id} task={task} onClick={() => onTaskClick?.(task)} />
        ))}
        {tasks.length === 0 && (
          <div className="py-8 text-center text-sm text-[var(--text-muted)]">
            No issues
          </div>
        )}
      </div>
    </div>
  );
}

function ActivityItem({ activity }: { activity: Activity }) {
  return (
    <div className="rounded-lg p-3 transition hover:bg-[var(--surface-alt)]">
      <div className="mb-1.5 flex items-center gap-2.5">
        <div
          className="flex h-7 w-7 items-center justify-center rounded-full text-xs font-semibold text-white"
          style={{ backgroundColor: activity.avatarColor }}
        >
          {activity.agent[0]}
        </div>
        <span className="text-sm font-semibold text-[var(--text)]">
          {activity.agent}
        </span>
        <span className="ml-auto text-xs text-[var(--text-muted)]">
          {activity.timeAgo}
        </span>
      </div>
      <p className="text-sm text-[var(--text-muted)]">
        {activity.text}
        <strong className="text-[var(--text)]">{activity.highlight}</strong>
      </p>
    </div>
  );
}

type TabKey = "board" | "list" | "activity" | "files" | "issues" | "settings";

export default function ProjectDetailPage() {
  const { id, issueId } = useParams<{ id: string; issueId?: string }>();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState<TabKey>(issueId ? "issues" : "board");
  const [project, setProject] = useState<Project | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activity, setActivity] = useState<Activity[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [availableAgents, setAvailableAgents] = useState<AgentOption[]>([]);
  const [selectedPrimaryAgentID, setSelectedPrimaryAgentID] = useState<string>("");
  const [newIssueDraft, setNewIssueDraft] = useState("");
  const [isSubmittingIssueRequest, setIsSubmittingIssueRequest] = useState(false);
  const [newIssueError, setNewIssueError] = useState<string | null>(null);
  const [newIssueSuccess, setNewIssueSuccess] = useState<string | null>(null);
  const [isSavingSettings, setIsSavingSettings] = useState(false);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [settingsSuccess, setSettingsSuccess] = useState<string | null>(null);
  const [workflowConfig, setWorkflowConfig] = useState<WorkflowConfigState>(defaultWorkflowConfigState());
  const agentIdToNameRef = useRef<Record<string, string>>({});
  const ws = useOptionalWS();
  const lastMessage = ws?.lastMessage ?? null;
  const nowMs = useNowTicker();
  const { upsertConversation, openConversation } = useGlobalChat();
  const { emissions: projectEmissions } = useEmissions({
    projectId: id,
    issueId: issueId ?? undefined,
    limit: 20,
  });

  const refreshProjectIssues = useCallback(async (projectID: string) => {
    const orgId = localStorage.getItem("otter-camp-org-id");
    const issuesUrl = orgId
      ? `${API_URL}/api/issues?org_id=${encodeURIComponent(orgId)}&project_id=${encodeURIComponent(projectID)}&limit=200`
      : `${API_URL}/api/issues?project_id=${encodeURIComponent(projectID)}&limit=200`;
    const issuesRes = await fetch(issuesUrl);
    if (!issuesRes.ok) {
      return;
    }
    const payload = await issuesRes.json() as
      | { items?: ApiIssue[] }
      | ApiIssue[];
    const apiIssues = Array.isArray(payload)
      ? payload
      : Array.isArray(payload.items)
        ? payload.items
        : [];
    const transformedTasks: Task[] = apiIssues.map((raw) => {
      const issue = raw as ApiIssue;
      const ownerAgentID = issue.owner_agent_id;
      const issueStatusRaw = issue.work_status ?? "queued";
      const normalizedStatus = issueStatusRaw.trim().toLowerCase();
      const status = (
        normalizedStatus === "queued" ||
        normalizedStatus === "ready" ||
        normalizedStatus === "planning" ||
        normalizedStatus === "ready_for_work" ||
        normalizedStatus === "in_progress" ||
        normalizedStatus === "review" ||
        normalizedStatus === "blocked" ||
        normalizedStatus === "flagged" ||
        normalizedStatus === "done" ||
        normalizedStatus === "cancelled" ||
        normalizedStatus === "dispatched"
      )
        ? normalizedStatus
        : "queued";
      const priorityRaw = (issue.priority ?? "P2").toUpperCase();
      const priority = (priorityRaw === "P0" || priorityRaw === "P1" || priorityRaw === "P2" || priorityRaw === "P3")
        ? priorityRaw
        : "P2";
      const agentName = ownerAgentID
        ? (agentIdToNameRef.current[ownerAgentID] || "Unassigned")
        : "Unassigned";
      return {
        id: raw.id,
        issueNumber: issue.issue_number,
        title: raw.title,
        status,
        priority,
        assignee: agentName,
        avatarColor: agentColors[agentName] || "var(--accent, #C9A86C)",
        blocked: status === "blocked" || status === "flagged",
      };
    });
    setTasks(transformedTasks);
  }, []);

  const refreshProjectActivity = useCallback(async () => {
    const orgId = localStorage.getItem("otter-camp-org-id");
    const activityUrl = orgId
      ? `${API_URL}/api/feed?org_id=${orgId}&limit=10`
      : `${API_URL}/api/feed?limit=10`;
    const activityRes = await fetch(activityUrl);
    if (!activityRes.ok) {
      return;
    }
    const activityData = await activityRes.json();
    const items = activityData.items || [];
    const transformedActivity: Activity[] = items.slice(0, 5).map((item: {
      id: string;
      agent_name?: string;
      type?: string;
      summary?: string;
      task_title?: string;
      metadata?: unknown;
      created_at?: string;
    }) => {
      const agentName = item.agent_name?.trim() || "System";
      const type = item.type?.trim() || "activity";
      const highlight = getActivityDescription({
        type,
        actorName: agentName,
        taskTitle: item.task_title,
        summary: item.summary,
        metadata: normalizeMetadata(item.metadata),
      });
      return {
        id: item.id,
        agent: agentName,
        avatarColor: agentColors[agentName] || "var(--accent, #C9A86C)",
        text: "",
        highlight,
        timeAgo: item.created_at ? formatTimeAgo(item.created_at) : "",
      };
    });
    setActivity(transformedActivity);
  }, []);

  // Fetch project and issue work items
  useEffect(() => {
    async function fetchData() {
      if (!id) return;
      
      setIsLoading(true);
      setError(null);
      agentIdToNameRef.current = {};
      
      try {
        const orgId = localStorage.getItem('otter-camp-org-id');
        
        // Fetch project
        const projectUrl = orgId
          ? `${API_URL}/api/projects/${id}?org_id=${encodeURIComponent(orgId)}`
          : `${API_URL}/api/projects/${id}`;
        const projectRes = await fetch(projectUrl);
        if (!projectRes.ok) {
          throw new Error('Project not found');
        }
        const projectData = await projectRes.json();
        setProject(projectData);
        setSelectedPrimaryAgentID(projectData.primary_agent_id || "");
        setWorkflowConfig(workflowConfigFromProject(projectData));
        
        // Fetch canonical workspace agents (UUID-backed) for mapping and settings.
        const agentsUrl = orgId
          ? `${API_URL}/api/agents?org_id=${encodeURIComponent(orgId)}`
          : `${API_URL}/api/agents`;
        const agentsRes = await fetch(agentsUrl);
        if (agentsRes.ok) {
          const agentsData = await agentsRes.json();
          const parsedAgents: AgentOption[] = [];
          for (const raw of (agentsData.agents || [])) {
            const agent = raw as { id?: string; name?: string; display_name?: string };
            if (!agent.id || typeof agent.id !== "string") continue;
            if (!UUID_REGEX.test(agent.id.trim())) continue;
            const agentName =
              (typeof agent.name === "string" && agent.name.trim()) ||
              (typeof agent.display_name === "string" && agent.display_name.trim()) ||
              agent.id;
            const agentID = agent.id.trim();
            agentIdToNameRef.current[agentID] = agentName;
            parsedAgents.push({ id: agentID, name: agentName });
          }
          parsedAgents.sort((a, b) => a.name.localeCompare(b.name));
          setAvailableAgents(parsedAgents);
        }
        await Promise.all([
          refreshProjectIssues(id),
          refreshProjectActivity(),
        ]);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load project');
      } finally {
        setIsLoading(false);
      }
    }
    
    fetchData();
  }, [id, refreshProjectActivity, refreshProjectIssues]);

  useEffect(() => {
    if (!id || !lastMessage || !lastMessage.data || typeof lastMessage.data !== "object") {
      return;
    }
    const payload = lastMessage.data as Record<string, unknown>;

    if (lastMessage.type === "IssueCreated") {
      const issueRecord =
        payload.issue && typeof payload.issue === "object"
          ? (payload.issue as Record<string, unknown>)
          : payload;
      const eventProjectID =
        (typeof issueRecord.project_id === "string" && issueRecord.project_id.trim()) ||
        (typeof issueRecord.projectId === "string" && issueRecord.projectId.trim()) ||
        "";
      if (eventProjectID === id) {
        void refreshProjectIssues(id);
        void refreshProjectActivity();
      }
      return;
    }

    if (lastMessage.type === "ProjectChatMessageCreated") {
      const messageRecord =
        payload.message && typeof payload.message === "object"
          ? (payload.message as Record<string, unknown>)
          : payload;
      const eventProjectID =
        (typeof messageRecord.project_id === "string" && messageRecord.project_id.trim()) ||
        (typeof messageRecord.projectId === "string" && messageRecord.projectId.trim()) ||
        "";
      if (eventProjectID === id) {
        void refreshProjectActivity();
      }
      return;
    }

    if (lastMessage.type === "ActivityEventReceived") {
      void refreshProjectActivity();
    }
  }, [id, lastMessage, refreshProjectActivity, refreshProjectIssues]);

  useEffect(() => {
    if (!project) {
      return;
    }
    upsertConversation({
      type: "project",
      projectId: project.id,
      title: project.name,
      contextLabel: `Project • ${project.name}`,
      subtitle: "Project chat",
    });
  }, [project, upsertConversation]);

  useEffect(() => {
    if (!project || !issueId) {
      return;
    }
    upsertConversation({
      type: "issue",
      issueId,
      title: `Issue ${issueId.slice(0, 8)}`,
      contextLabel: `Issue • ${project.name}`,
      subtitle: "Issue conversation",
    });
  }, [issueId, project, upsertConversation]);

  useEffect(() => {
    if (!project || !issueId || activeTab !== "issues") {
      return;
    }
    openConversation(
      {
        type: "issue",
        issueId,
        title: `Issue ${issueId.slice(0, 8)}`,
        contextLabel: `Issue • ${project.name}`,
        subtitle: "Issue conversation",
      },
      { focus: true, openDock: true },
    );
  }, [activeTab, issueId, openConversation, project]);

  const tasksByColumn = useMemo(() => {
    const activeIssueIDs = new Set<string>();
    for (const emission of projectEmissions) {
      const issueID = emission.scope?.issue_id?.trim() ?? "";
      if (!issueID) {
        continue;
      }
      const emittedAtMs = Date.parse(emission.timestamp);
      if (Number.isNaN(emittedAtMs)) {
        continue;
      }
      const ageMs = nowMs - emittedAtMs;
      if (ageMs < -ACTIVE_EMISSION_WINDOW_MS || ageMs > ACTIVE_EMISSION_WINDOW_MS) {
        continue;
      }
      activeIssueIDs.add(issueID);
    }

    const decoratedTasks = tasks.map((task) => ({
      ...task,
      isActive: activeIssueIDs.has(task.id),
    }));

    const grouped: Record<string, Task[]> = {};
    for (const col of COLUMNS) {
      grouped[col.key] = decoratedTasks.filter((t) => col.statuses.includes(t.status));
    }
    return grouped;
  }, [nowMs, projectEmissions, tasks]);

  const waitingCount = useMemo(() => {
    return tasks.filter((t) => t.blocked).length;
  }, [tasks]);

  const activeTaskCount = useMemo(() => {
    return tasks.filter((t) => t.status !== "done" && t.status !== "cancelled").length;
  }, [tasks]);

  useEffect(() => {
    if (issueId) {
      setActiveTab("issues");
    }
  }, [issueId]);

  if (isLoading) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <LoadingSpinner size="lg" />
        <p className="mt-4 text-[var(--text-muted)]">Loading project...</p>
      </div>
    );
  }

  if (error || !project) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <div className="text-6xl">🦦</div>
        <h1 className="mt-4 text-2xl font-bold text-[var(--text)]">
          Project Not Found
        </h1>
        <p className="mt-2 text-[var(--text-muted)]">
          {error || "This project doesn't exist or may have been deleted."}
        </p>
        <button
          type="button"
          onClick={() => navigate("/projects")}
          className="mt-6 rounded-xl bg-amber-600 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-amber-700"
        >
          ← Back to Projects
        </button>
      </div>
    );
  }

  const emoji = projectEmojis[project.name] || "📁";
  const status = project.status || "active";
  const statusColors: Record<string, { dot: string; text: string }> = {
    active: { dot: "bg-[var(--green)]", text: "Active" },
    completed: { dot: "bg-[var(--text-muted)]", text: "Completed" },
    archived: { dot: "bg-[var(--text-muted)]", text: "Archived" },
    blocked: { dot: "bg-amber-500", text: "Blocked" },
  };
  const statusDisplay = statusColors[status] || statusColors.active;

  const tabs: { key: TabKey; label: string; badge?: number }[] = [
    { key: "board", label: "Board" },
    { key: "list", label: "List" },
    { key: "activity", label: "Activity" },
    { key: "files", label: "Files" },
    { key: "issues", label: "Issues" },
    { key: "settings", label: "Settings" },
  ];

  const handleTaskClick = (task: Task) => {
    if (!id) {
      return;
    }
    navigate(`/projects/${id}/issues/${task.id}`);
  };

  const primaryAgentName =
    project.lead ||
    (project.primary_agent_id
      ? availableAgents.find((agent) => agent.id === project.primary_agent_id)?.name ||
        agentIdToNameRef.current[project.primary_agent_id]
      : undefined);

  const refreshProjectConfiguration = async (projectID: string) => {
    const orgId = localStorage.getItem("otter-camp-org-id");
    const projectUrl = orgId
      ? `${API_URL}/api/projects/${projectID}?org_id=${encodeURIComponent(orgId)}`
      : `${API_URL}/api/projects/${projectID}`;
    const projectResponse = await fetch(projectUrl);
    if (!projectResponse.ok) {
      throw new Error("Failed to reload project state");
    }
    const projectData = await projectResponse.json();
    setProject(projectData);
    setSelectedPrimaryAgentID(projectData.primary_agent_id || "");
    setWorkflowConfig(workflowConfigFromProject(projectData));
  };

  const handleSaveSettings = async () => {
    if (!id) return;
    setSettingsError(null);
    setSettingsSuccess(null);
    setIsSavingSettings(true);
    let settingsPatched = false;
    try {
      const orgId = localStorage.getItem('otter-camp-org-id');
      const settingsUrl = orgId
        ? `${API_URL}/api/projects/${id}/settings?org_id=${encodeURIComponent(orgId)}`
        : `${API_URL}/api/projects/${id}/settings`;
      const response = await fetch(settingsUrl, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          primary_agent_id: selectedPrimaryAgentID || null,
        }),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(
          (payload && (payload.error || payload.message)) || "Failed to save project settings",
        );
      }
      const updatedSettingsProject = await response.json();
      settingsPatched = true;

      const projectPatchUrl = orgId
        ? `${API_URL}/api/projects/${id}?org_id=${encodeURIComponent(orgId)}`
        : `${API_URL}/api/projects/${id}`;
      const workflowResponse = await fetch(projectPatchUrl, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          workflow_enabled: workflowConfig.enabled,
          workflow_schedule: workflowSchedulePayloadFromConfig(workflowConfig),
          workflow_template: workflowTemplatePayloadFromConfig(workflowConfig),
          workflow_agent_id: workflowConfig.workflowAgentID || null,
        }),
      });
      if (!workflowResponse.ok) {
        const payload = await workflowResponse.json().catch(() => null);
        throw new Error(
          (payload && (payload.error || payload.message)) || "Failed to save workflow settings",
        );
      }
      const updatedWorkflowProject = await workflowResponse.json();

      const mergedProject = {
        ...updatedSettingsProject,
        ...updatedWorkflowProject,
      };
      setProject(mergedProject);
      setSelectedPrimaryAgentID(mergedProject.primary_agent_id || "");
      setWorkflowConfig(workflowConfigFromProject(mergedProject));
      setSettingsSuccess("Project settings saved.");
    } catch (err) {
      if (settingsPatched) {
        try {
          await refreshProjectConfiguration(id);
        } catch {
          // Keep original save error as primary message.
        }
      }
      setSettingsError(err instanceof Error ? err.message : "Failed to save project settings");
    } finally {
      setIsSavingSettings(false);
    }
  };

  const openProjectChat = () => {
    if (!project) {
      return;
    }
    openConversation(
      {
        type: "project",
        projectId: project.id,
        title: project.name,
        contextLabel: `Project • ${project.name}`,
        subtitle: "Project chat",
      },
      { focus: true, openDock: true },
    );
  };

  const handleSubmitIssueRequest = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!project || !id) {
      return;
    }
    const issueTitle = newIssueDraft.trim();
    if (issueTitle === "") {
      return;
    }

    setIsSubmittingIssueRequest(true);
    setNewIssueError(null);
    setNewIssueSuccess(null);
    try {
      const orgId = localStorage.getItem("otter-camp-org-id");
      const createMessageUrl = orgId
        ? `${API_URL}/api/projects/${id}/chat/messages?org_id=${encodeURIComponent(orgId)}`
        : `${API_URL}/api/projects/${id}/chat/messages`;
      const response = await fetch(createMessageUrl, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          author: getCurrentAuthorName(),
          body: buildProjectIssueRequestMessage(project.name, issueTitle),
        }),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to submit issue request");
      }

      const payload = await response.json().catch(() => null);
      const delivery = payload?.delivery as
        | { delivered?: boolean; error?: string }
        | undefined;
      const deliveryError =
        typeof delivery?.error === "string" ? delivery.error.trim() : "";
      if (deliveryError) {
        setNewIssueSuccess("Issue request saved; bridge delivery pending.");
      } else if (delivery?.delivered) {
        setNewIssueSuccess("Issue request sent to the project agent.");
      } else {
        setNewIssueSuccess("Issue request saved.");
      }
      setNewIssueDraft("");
      openProjectChat();
    } catch (err) {
      setNewIssueError(err instanceof Error ? err.message : "Failed to submit issue request");
    } finally {
      setIsSubmittingIssueRequest(false);
    }
  };

  return (
    <div className="flex min-h-full flex-col">
      {/* Breadcrumb */}
      <nav className="mb-4 flex items-center gap-2 text-sm text-[var(--text-muted)]">
        <Link to="/projects" className="hover:text-[var(--text)]">
          Projects
        </Link>
        <span>›</span>
        <span className="font-medium text-[var(--text)]">{project.name}</span>
      </nav>

      {/* Project Header */}
      <header className="mb-6 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
        <div className="flex items-center gap-5">
          <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-[var(--surface-alt)] text-4xl">
            {emoji}
          </div>
          <div className="flex-1">
            <h1 className="text-2xl font-bold text-[var(--text)]">
              {project.name}
            </h1>
            <div className="mt-1 flex flex-wrap items-center gap-4 text-sm text-[var(--text-muted)]">
              <div className="flex items-center gap-1.5">
                <span className={`h-2.5 w-2.5 rounded-full ${statusDisplay.dot}`} />
                {waitingCount > 0 ? (
                  <span>{waitingCount} item{waitingCount !== 1 ? "s" : ""} waiting on you</span>
                ) : (
                  <span>{statusDisplay.text}</span>
                )}
              </div>
              <span>•</span>
              <span>{activeTaskCount} active issue{activeTaskCount !== 1 ? "s" : ""}</span>
              {primaryAgentName && (
                <>
                  <span>•</span>
                  <span>Lead: {primaryAgentName}</span>
                </>
              )}
            </div>
            {project.description && (
              <p className="mt-2 text-sm text-[var(--text-muted)]">{project.description}</p>
            )}
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              onClick={openProjectChat}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-sm font-medium text-[var(--text)] transition hover:bg-[var(--surface-alt)]"
            >
              Chat
            </button>
            <button
              type="button"
              onClick={() => setActiveTab("settings")}
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-sm font-medium text-[var(--text)] transition hover:bg-[var(--surface-alt)]"
            >
              Settings
            </button>
            <form onSubmit={handleSubmitIssueRequest} className="flex items-center gap-2">
              <label htmlFor="new-issue-title" className="sr-only">
                New issue
              </label>
              <input
                id="new-issue-title"
                type="text"
                value={newIssueDraft}
                onChange={(event) => {
                  setNewIssueDraft(event.target.value);
                  if (newIssueError) {
                    setNewIssueError(null);
                  }
                  if (newIssueSuccess) {
                    setNewIssueSuccess(null);
                  }
                }}
                placeholder="New issue..."
                className="w-56 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-muted)]"
              />
              <button
                type="submit"
                disabled={isSubmittingIssueRequest || newIssueDraft.trim() === ""}
                className="rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-medium text-white transition hover:bg-[#B8975B] disabled:cursor-not-allowed disabled:opacity-60"
              >
                {isSubmittingIssueRequest ? "Sending..." : "New Issue"}
              </button>
            </form>
          </div>
        </div>
        {(newIssueError || newIssueSuccess) && (
          <div
            className={`mt-4 rounded-lg border px-3 py-2 text-sm ${
              newIssueError
                ? "border-red-500/40 bg-red-500/10 text-red-300"
                : "border-emerald-500/40 bg-emerald-500/10 text-emerald-300"
            }`}
          >
            {newIssueError || newIssueSuccess}
          </div>
        )}
      </header>

      {/* Tabs */}
      <div className="mb-6 flex gap-1 border-b border-[var(--border)]">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            type="button"
            onClick={() => setActiveTab(tab.key)}
            className={`relative px-5 py-3 text-sm font-medium transition ${
              activeTab === tab.key
                ? "text-amber-600 dark:text-amber-400"
                : "text-[var(--text-muted)] hover:text-[var(--text)]"
            }`}
          >
            {tab.label}
            {tab.badge && (
              <span className="ml-1.5 rounded-full bg-red-500 px-2 py-0.5 text-[10px] font-bold text-white">
                {tab.badge}
              </span>
            )}
            {activeTab === tab.key && (
              <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-amber-600 dark:bg-amber-400" />
            )}
          </button>
        ))}
      </div>

      {/* Tab Content */}
      {activeTab === "board" && (
        <div className="flex flex-1 gap-6 overflow-x-auto pb-4">
          {/* Kanban Board */}
          <div className="flex flex-1 gap-5 overflow-x-auto">
            {COLUMNS.map((col) => (
              <BoardColumn
                key={col.key}
                columnKey={col.key}
                title={col.title}
                tasks={tasksByColumn[col.key] || []}
                onTaskClick={handleTaskClick}
              />
            ))}
          </div>

          {/* Activity Sidebar */}
          <aside className="hidden w-80 flex-shrink-0 rounded-2xl border border-[var(--border)] bg-[var(--surface)] xl:flex xl:flex-col">
            <div className="flex items-center gap-2 border-b border-[var(--border)] px-5 py-4">
              <span className="text-sm">📡</span>
              <span className="text-sm font-bold text-[var(--text)]">
                Recent Activity
              </span>
            </div>
            <div className="flex-1 overflow-y-auto p-3">
              <div className="mb-3 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2">
                <p className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                  Live Emissions
                </p>
                <EmissionStream
                  emissions={projectEmissions}
                  projectId={project.id}
                  issueId={issueId ?? undefined}
                  limit={4}
                  emptyText="No live emissions yet"
                  className="mt-2 text-xs text-[var(--text-muted)]"
                />
              </div>
              {activity.length > 0 ? (
                activity.map((a) => (
                  <ActivityItem key={a.id} activity={a} />
                ))
              ) : (
                <div className="py-8 text-center text-sm text-[var(--text-muted)]">
                  No recent activity
                </div>
              )}
            </div>
          </aside>
        </div>
      )}

      {activeTab === "list" && (
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
          <div className="space-y-3">
            {tasks.filter(t => t.status !== "done" && t.status !== "cancelled").length > 0 ? (
              <>
                <div className="grid grid-cols-[minmax(0,1fr)_120px_120px_90px] items-center gap-3 px-3 text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                  <span>Issue</span>
                  <span>Assignee</span>
                  <span>Status</span>
                  <span>Priority</span>
                </div>
                {tasks.filter(t => t.status !== "done" && t.status !== "cancelled").map((task) => (
                  <button
                    key={task.id}
                    type="button"
                    onClick={() => handleTaskClick(task)}
                    className="grid w-full cursor-pointer grid-cols-[minmax(0,1fr)_120px_120px_90px] items-center gap-3 rounded-xl border border-[var(--border)] p-4 text-left transition hover:border-[#C9A86C]/50 hover:bg-[var(--surface-alt)]"
                  >
                    <span className="truncate text-sm font-medium text-[var(--text)]">
                      {typeof task.issueNumber === "number" ? `#${task.issueNumber} ` : ""}
                      {task.title}
                    </span>
                    <span className="text-xs text-[var(--text-muted)]">
                      {task.assignee}
                    </span>
                    <div className="flex flex-col items-start gap-1">
                      <span className={`w-fit rounded-full px-2 py-0.5 text-[10px] font-semibold ${LIST_STATUS_BADGE[task.status].className}`}>
                        {LIST_STATUS_BADGE[task.status].label}
                      </span>
                      <div data-testid={`project-list-mini-${task.id}`}>
                        <PipelineMiniProgress status={task.status} />
                      </div>
                    </div>
                    <span className={`w-fit rounded px-2 py-0.5 text-[10px] font-semibold ${
                      task.priority === "P0" ? "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400" :
                      task.priority === "P1" ? "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400" :
                      task.priority === "P2" ? "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400" :
                      "bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400"
                    }`}>
                      {task.priority}
                    </span>
                  </button>
                ))}
              </>
            ) : (
              <div className="py-8 text-center text-sm text-[var(--text-muted)]">
                No active issues
              </div>
            )}
          </div>
        </div>
      )}

      {activeTab === "activity" && (
        <div className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
          <div className="mb-4 rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] p-3">
            <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">
              Live Emissions
            </p>
            <EmissionStream
              emissions={projectEmissions}
              projectId={project.id}
              issueId={issueId ?? undefined}
              limit={8}
              emptyText="No live emissions yet"
              className="mt-2 text-sm text-[var(--text-muted)]"
            />
          </div>
          <div className="space-y-2">
            {activity.length > 0 ? (
              activity.map((a) => (
                <ActivityItem key={a.id} activity={a} />
              ))
            ) : (
              <div className="py-8 text-center text-sm text-[var(--text-muted)]">
                No recent activity
              </div>
            )}
          </div>
        </div>
      )}

      {activeTab === "files" && <ProjectFileBrowser projectId={project.id} />}

      {activeTab === "issues" && (
        <div className={`grid gap-4 ${issueId ? "xl:grid-cols-[minmax(320px,420px)_1fr]" : "grid-cols-1"}`}>
          <ProjectIssuesList
            projectId={project.id}
            selectedIssueID={issueId ?? null}
            onSelectIssue={(selectedIssueID) =>
              navigate(`/projects/${project.id}/issues/${selectedIssueID}`)
            }
          />
          {issueId && <IssueThreadPanel issueID={issueId} projectID={project.id} />}
        </div>
      )}

      {activeTab === "settings" && (
        <div className="space-y-5">
          <ProjectSettingsPage
            projectID={project.id}
            availableAgents={availableAgents}
            selectedPrimaryAgentID={selectedPrimaryAgentID}
            onPrimaryAgentChange={setSelectedPrimaryAgentID}
            onSaveGeneralSettings={handleSaveSettings}
            isSavingGeneralSettings={isSavingSettings}
            generalError={settingsError}
            generalSuccess={settingsSuccess}
            initialRequireHumanReview={Boolean(project.require_human_review)}
            onRequireHumanReviewSaved={(value) =>
              setProject((prev) => (prev ? { ...prev, require_human_review: value } : prev))
            }
          />

          <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
            <h2 className="text-base font-semibold text-[var(--text)]">Workflow</h2>
            <p className="mt-1 text-sm text-[var(--text-muted)]">
              Configure recurring issue creation for this project.
            </p>
            <div className="mt-4">
            <WorkflowConfig
              value={workflowConfig}
              onChange={setWorkflowConfig}
              agents={availableAgents}
            />
            </div>
          </section>
          </div>
      )}
    </div>
  );
}
