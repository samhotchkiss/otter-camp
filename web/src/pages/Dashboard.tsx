import { useMemo, useState, useEffect } from "react";
import CommandPalette from "../components/CommandPalette";
import OnboardingTour from "../components/OnboardingTour";
import TaskDetail from "../components/TaskDetail";
import NewTaskModal from "../components/NewTaskModal";
import type { Command } from "../hooks/useCommandPalette";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import {
  api,
  type ActionItem,
  type FeedApiItem,
  type FeedItem,
  type Project,
  type RecentActivityApiItem,
} from "../lib/api";
import { formatProjectTaskSummary } from "../lib/projectTaskSummary";
import { getActivityDescription, formatRelativeTime, getTypeConfig, normalizeMetadata } from "../components/activity/activityFormat";
import { getInitials } from "../components/messaging/utils";
import { isDemoMode } from "../lib/demo";
import useEmissions from "../hooks/useEmissions";
import EmissionTicker from "../components/EmissionTicker";
import { useOptionalWS } from "../contexts/WebSocketContext";

/**
 * Dashboard - Two-column layout matching Jeff G's mockups
 * 
 * Layout:
 * - Primary (left): "NEEDS YOU" action items + "YOUR FEED" activity
 * - Secondary (right): Quick actions + Projects list
 */

// Demo data (only used on demo host)
const DEMO_ACTION_ITEMS: ActionItem[] = [
  {
    id: "1",
    icon: "🚀",
    project: "ItsAlive",
    time: "5 min ago",
    agent: "Ivy",
    message: "is waiting on your approval to deploy v2.1.0 with the new onboarding flow.",
    primaryAction: "Approve Deploy",
    secondaryAction: "View Details",
  },
  {
    id: "2",
    icon: "✍️",
    project: "Content",
    time: "1 hour ago",
    agent: "Stone",
    message: 'finished a blog post for you to review: "Why I Run 12 AI Agents"',
    primaryAction: "Review Post",
    secondaryAction: "Later",
  },
];

const DEMO_FEED_ITEMS: FeedItem[] = [
  {
    id: "summary",
    avatar: "✓",
    avatarBg: "var(--green)",
    title: "4 projects active",
    text: "Derek pushed 4 commits to Pearl, Jeff G finished mockups, Nova scheduled tweets",
    meta: "Last 6 hours • 14 updates total",
    type: null,
  },
  {
    id: "email",
    avatar: "P",
    avatarBg: "var(--blue)",
    title: "Important email",
    text: 'from investor@example.com — "Follow up on our conversation"',
    meta: "30 min ago",
    type: { label: "Penny • Email", className: "insight" },
  },
  {
    id: "markets",
    avatar: "B",
    avatarBg: "var(--orange)",
    title: "Market Summary",
    text: "S&P up 0.8%, your watchlist +1.2%. No alerts triggered.",
    meta: "2 hours ago",
    type: { label: "Beau H • Markets", className: "progress" },
  },
  {
    id: "social",
    avatar: "N",
    avatarBg: "#ec4899",
    title: "@samhotchkiss",
    text: "got 3 replies worth reading. One potential lead.",
    meta: "45 min ago",
    type: { label: "Nova • Twitter", className: "insight" },
  },
];

// Demo projects for sidebar
const DEMO_PROJECTS = [
  { id: "itsalive", name: "ItsAlive", desc: "Waiting on deploy approval", status: "blocked", time: "5m" },
  { id: "pearl", name: "Pearl", desc: "Derek pushing commits", status: "working", time: "2m" },
  { id: "otter-camp", name: "Otter Camp", desc: "Design + architecture in progress", status: "working", time: "now" },
  { id: "three-stones", name: "Three Stones", desc: "Presentation shipped", status: "idle", time: "3h" },
  { id: "content", name: "Content", desc: "Tweets scheduled", status: "idle", time: "1h" },
];

const FEED_AVATAR_COLORS = [
  "var(--accent)",
  "var(--blue)",
  "var(--green)",
  "var(--orange)",
  "#ec4899",
  "var(--surface-alt)",
];

const FEED_TYPE_CLASS_MAP: Record<string, string> = {
  message: "insight",
  comment: "insight",
  approval: "insight",
  decision: "insight",
  commit: "progress",
  task_created: "progress",
  task_update: "progress",
  task_updated: "progress",
  task_status_changed: "progress",
  dispatch: "progress",
  assignment: "progress",
};

const UUID_PATTERN = /\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b/gi;
const UUID_TEST_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function hashString(value: string): number {
  let hash = 0;
  for (let i = 0; i < value.length; i += 1) {
    hash = (hash << 5) - hash + value.charCodeAt(i);
    hash |= 0;
  }
  return Math.abs(hash);
}

function resolveAvatarColor(name: string): string {
  if (!name) return "var(--accent)";
  const index = hashString(name) % FEED_AVATAR_COLORS.length;
  return FEED_AVATAR_COLORS[index];
}

function resolveFeedBadgeClass(type: string, priority?: string | null): string {
  if (priority && ["urgent", "high", "critical"].includes(priority)) {
    return "insight";
  }
  return FEED_TYPE_CLASS_MAP[type] || "progress";
}

function metadataActorCandidate(metadata: Record<string, unknown>, key: string): string {
  const value = metadata[key];
  return typeof value === "string" ? value.trim() : "";
}

function looksLikeUUID(value: string): boolean {
  return UUID_TEST_PATTERN.test(value.trim());
}

function titleCaseFromSlug(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) {
    return "";
  }
  if (trimmed.includes(" ")) {
    return trimmed;
  }
  return trimmed
    .replace(/[._-]+/g, " ")
    .split(/\s+/)
    .filter(Boolean)
    .map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
    .join(" ");
}

function parseSessionAgentAlias(sessionKey: string): string {
  const trimmed = sessionKey.trim();
  if (!trimmed) {
    return "";
  }
  const match = /^agent:([^:]+):/i.exec(trimmed);
  if (!match?.[1]) {
    return "";
  }
  return match[1].trim();
}

function setAliasName(map: Map<string, string>, alias: string, name: string) {
  const aliasTrimmed = alias.trim();
  const nameTrimmed = name.trim();
  if (!aliasTrimmed || !nameTrimmed) {
    return;
  }
  map.set(aliasTrimmed, nameTrimmed);
  map.set(aliasTrimmed.toLowerCase(), nameTrimmed);
}

type RecentFormattingContext = {
  projectNamesByID: Map<string, string>;
  agentNamesByAlias: Map<string, string>;
};

function buildProjectNameMap(projects: Project[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const project of projects) {
    const id = (project.id || "").trim();
    const name = (project.name || "").trim();
    if (!id || !name) {
      continue;
    }
    out.set(id, name);
    out.set(id.toLowerCase(), name);
  }
  return out;
}

function buildAgentAliasNameMap(rawAgents: unknown): Map<string, string> {
  const out = new Map<string, string>();
  const agents =
    Array.isArray(rawAgents)
      ? rawAgents
      : rawAgents && typeof rawAgents === "object" && Array.isArray((rawAgents as Record<string, unknown>).agents)
        ? ((rawAgents as Record<string, unknown>).agents as unknown[])
        : [];

  for (const raw of agents) {
    if (!raw || typeof raw !== "object") {
      continue;
    }
    const record = raw as Record<string, unknown>;
    const name =
      (typeof record.name === "string" ? record.name.trim() : "") ||
      (typeof record.displayName === "string" ? record.displayName.trim() : "") ||
      (typeof record.display_name === "string" ? record.display_name.trim() : "");
    if (!name) {
      continue;
    }

    const sessionKey =
      (typeof record.sessionKey === "string" ? record.sessionKey.trim() : "") ||
      (typeof record.session_key === "string" ? record.session_key.trim() : "");

    const aliases = [
      typeof record.id === "string" ? record.id : "",
      typeof record.slug === "string" ? record.slug : "",
      typeof record.slot === "string" ? record.slot : "",
      typeof record.role === "string" ? record.role : "",
      sessionKey,
      parseSessionAgentAlias(sessionKey),
    ];
    for (const alias of aliases) {
      setAliasName(out, alias, name);
    }
  }

  return out;
}

function resolveFeedActorName(item: FeedApiItem): string {
  if (item.agent_name?.trim()) {
    return item.agent_name.trim();
  }

  const metadata = normalizeMetadata(item.metadata);
  const actorCandidates = [
    metadataActorCandidate(metadata, "actor"),
    metadataActorCandidate(metadata, "user"),
    metadataActorCandidate(metadata, "agentName"),
    metadataActorCandidate(metadata, "agent_name"),
    metadataActorCandidate(metadata, "pusher_name"),
    metadataActorCandidate(metadata, "pusher"),
    metadataActorCandidate(metadata, "sender_login"),
    metadataActorCandidate(metadata, "sender_name"),
    metadataActorCandidate(metadata, "sender"),
    metadataActorCandidate(metadata, "author_name"),
    metadataActorCandidate(metadata, "author"),
  ];

  for (const candidate of actorCandidates) {
    if (candidate && candidate.toLowerCase() !== "unknown") {
      return candidate;
    }
  }

  return "System";
}

function mapActivityToFeedItems(items: FeedApiItem[]): FeedItem[] {
  return items.map((item) => {
    const actorName = resolveFeedActorName(item);
    const type = item.type || "activity";
    const typeConfig = getTypeConfig(type);
    const metadata = normalizeMetadata(item.metadata);
    const description = getActivityDescription({
      type,
      actorName,
      taskTitle: item.task_title || undefined,
      summary: item.summary || undefined,
      metadata,
    });

    return {
      id: item.id,
      avatar: getInitials(actorName),
      avatarBg: resolveAvatarColor(actorName),
      title: actorName,
      text: description,
      meta: formatRelativeTime(new Date(item.created_at)),
      type: {
        label: typeConfig.label,
        className: resolveFeedBadgeClass(type, item.priority),
      },
    };
  });
}

function resolveRecentActivityActorName(
  item: RecentActivityApiItem,
  context: RecentFormattingContext,
): string {
  const rawAgentID = (item.agent_id || "").trim();
  const sessionAgentAlias = parseSessionAgentAlias(item.session_key || "");
  const aliases = [rawAgentID, sessionAgentAlias];
  for (const alias of aliases) {
    if (!alias) {
      continue;
    }
    const resolved =
      context.agentNamesByAlias.get(alias) ||
      context.agentNamesByAlias.get(alias.toLowerCase()) ||
      "";
    if (resolved) {
      return resolved;
    }
  }

  if (!rawAgentID || rawAgentID.toLowerCase() === "system") {
    return "System";
  }

  if (looksLikeUUID(rawAgentID)) {
    return "Agent";
  }

  return titleCaseFromSlug(rawAgentID) || "Agent";
}

function resolveRecentProjectLabel(
  item: RecentActivityApiItem,
  context: RecentFormattingContext,
): string {
  const projectID = (item.project_id || "").trim();
  if (projectID) {
    return context.projectNamesByID.get(projectID) || context.projectNamesByID.get(projectID.toLowerCase()) || "";
  }
  const sessionKey = (item.session_key || "").trim();
  if (!sessionKey) {
    return "";
  }
  const match = /:project:([0-9a-f-]{36})/i.exec(sessionKey);
  const fromSession = match?.[1]?.trim() || "";
  if (!fromSession) {
    return "";
  }
  return context.projectNamesByID.get(fromSession) || context.projectNamesByID.get(fromSession.toLowerCase()) || "";
}

function sanitizeRecentActivityText(
  input: string,
  item: RecentActivityApiItem,
  context: RecentFormattingContext,
): string {
  let output = input.trim();
  if (!output) {
    return "";
  }

  const projectLabel = resolveRecentProjectLabel(item, context);
  const sessionKey = (item.session_key || "").trim();
  if (sessionKey) {
    const agentAlias = parseSessionAgentAlias(sessionKey);
    const actor =
      (agentAlias && (context.agentNamesByAlias.get(agentAlias) || context.agentNamesByAlias.get(agentAlias.toLowerCase()))) ||
      titleCaseFromSlug(agentAlias) ||
      "Agent";
    const scope = projectLabel ? `Project ${projectLabel}` : "Project";
    output = output.replace(new RegExp(sessionKey.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "g"), `${actor} • ${scope}`);
  }
  output = output.replace(/agent:([^:\s)]+):project:([^)\s]+)/gi, (_match, agentPart: string) => {
    const agentAlias = String(agentPart || "").trim();
    const actor =
      (context.agentNamesByAlias.get(agentAlias) || context.agentNamesByAlias.get(agentAlias.toLowerCase())) ||
      titleCaseFromSlug(agentAlias) ||
      "Agent";
    const scope = projectLabel ? `Project ${projectLabel}` : "Project";
    return `${actor} • ${scope}`;
  });

  const projectID = (item.project_id || "").trim();
  if (projectID && projectLabel) {
    output = output.replace(new RegExp(projectID, "ig"), projectLabel);
  }

  const issueID = (item.issue_id || "").trim();
  if (issueID) {
    const issueLabel = item.issue_number ? `Issue #${item.issue_number}` : "Issue";
    output = output.replace(new RegExp(issueID, "ig"), issueLabel);
  }

  const fallbackLabel =
    item.trigger.toLowerCase().includes("issue")
      ? "issue"
      : item.trigger.toLowerCase().includes("project")
        ? "project"
        : "item";
  output = output.replace(UUID_PATTERN, `this ${fallbackLabel}`);

  return output.replace(/\s+/g, " ").trim();
}

function buildRecentActivityDescription(
  item: RecentActivityApiItem,
  context: RecentFormattingContext,
): string {
  const trigger = (item.trigger || "").trim().toLowerCase();
  const projectLabel = resolveRecentProjectLabel(item, context);
  const summary = sanitizeRecentActivityText(item.summary || "", item, context);
  const detail = sanitizeRecentActivityText(item.detail || "", item, context);

  if (trigger === "dispatch.project_chat") {
    if (projectLabel) {
      return `dispatched project chat for ${projectLabel}`;
    }
    return "dispatched project chat";
  }

  if (trigger === "system.event") {
    if (/^session activity\b/i.test(summary)) {
      return projectLabel ? `recorded session activity for ${projectLabel}` : "recorded session activity";
    }
    if (summary && !summary.toLowerCase().includes("system.event")) {
      return summary;
    }
    if (detail) {
      return detail;
    }
    return projectLabel ? `recorded system event for ${projectLabel}` : "recorded system event";
  }

  if (summary) {
    return summary;
  }
  if (detail) {
    return detail;
  }

  const status = (item.status || "").trim().toLowerCase();
  if (status === "failed") {
    return `failed ${humanizeType(item.trigger) || "activity"}`.trim();
  }
  if (status === "started") {
    return `started ${humanizeType(item.trigger) || "activity"}`.trim();
  }
  return humanizeType(item.trigger) || "activity event";
}

function mapRecentActivityToFeedItems(
  items: RecentActivityApiItem[],
  context: RecentFormattingContext,
): FeedItem[] {
  return items.map((item) => {
    const actorName = resolveRecentActivityActorName(item, context);
    const trigger = (item.trigger || "activity").trim() || "activity";
    const typeConfig = getTypeConfig(trigger);
    const description = buildRecentActivityDescription(item, context);

    return {
      id: item.id,
      avatar: getInitials(actorName),
      avatarBg: resolveAvatarColor(actorName),
      title: actorName,
      text: description,
      meta: formatRelativeTime(new Date(item.created_at)),
      type: {
        label: typeConfig.label,
        className: resolveFeedBadgeClass(trigger, null),
      },
    };
  });
}

function parseRealtimeActivityEvent(payload: unknown): RecentActivityApiItem | null {
  if (!payload || typeof payload !== "object") {
    return null;
  }
  const envelope = payload as Record<string, unknown>;
  const record =
    envelope.event && typeof envelope.event === "object"
      ? (envelope.event as Record<string, unknown>)
      : envelope;

  const summary = typeof record.summary === "string" ? record.summary.trim() : "";
  const trigger = typeof record.trigger === "string" ? record.trigger.trim() : "";
  if (!summary || !trigger) {
    return null;
  }

  const idRaw = typeof record.id === "string" ? record.id.trim() : "";
  const createdAtRaw = typeof record.created_at === "string" ? record.created_at.trim() : "";
  const createdAt = createdAtRaw || new Date().toISOString();
  const agentID = typeof record.agent_id === "string" ? record.agent_id.trim() : "";
  const orgID = typeof record.org_id === "string" ? record.org_id.trim() : "";

  return {
    id: idRaw || `realtime-${trigger}-${createdAt}`,
    org_id: orgID,
    agent_id: agentID || "System",
    trigger,
    summary,
    detail: typeof record.detail === "string" ? record.detail : undefined,
    channel: typeof record.channel === "string" ? record.channel : undefined,
    session_key: typeof record.session_key === "string" ? record.session_key : undefined,
    project_id: typeof record.project_id === "string" ? record.project_id : undefined,
    issue_id: typeof record.issue_id === "string" ? record.issue_id : undefined,
    issue_number: typeof record.issue_number === "number" ? record.issue_number : undefined,
    thread_id: typeof record.thread_id === "string" ? record.thread_id : undefined,
    status: typeof record.status === "string" ? record.status : undefined,
    created_at: createdAt,
  };
}

export default function Dashboard() {
  const {
    isCommandPaletteOpen,
    closeCommandPalette,
    openCommandPalette,
    selectedTaskId,
    closeTaskDetail,
    isNewTaskOpen,
    closeNewTask,
  } = useKeyboardShortcutsContext();

  // API state
  const [actionItems, setActionItems] = useState<ActionItem[]>(isDemoMode() ? DEMO_ACTION_ITEMS : []);
  const [feedItems, setFeedItems] = useState<FeedItem[]>(isDemoMode() ? DEMO_FEED_ITEMS : []);
  const [projects, setProjects] = useState<Project[]>(isDemoMode() ? (DEMO_PROJECTS as unknown as Project[]) : []);
  const [agentNamesByAlias, setAgentNamesByAlias] = useState<Map<string, string>>(() => new Map());
  const [lastSync, setLastSync] = useState<Date | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { emissions } = useEmissions({ limit: 30 });
  const ws = useOptionalWS();
  const lastMessage = ws?.lastMessage ?? null;

  const liveProjectStatus = useMemo(() => {
    const now = Date.now();
    const statusByProject = new Map<string, "live-active" | "live-recent">();

    for (const emission of emissions) {
      const projectID = emission.scope?.project_id?.trim();
      if (!projectID) {
        continue;
      }

      const emittedAt = new Date(emission.timestamp).getTime();
      if (Number.isNaN(emittedAt)) {
        continue;
      }

      const ageSeconds = Math.floor((now - emittedAt) / 1000);
      if (ageSeconds <= 60) {
        statusByProject.set(projectID, "live-active");
        continue;
      }
      if (ageSeconds <= 300 && !statusByProject.has(projectID)) {
        statusByProject.set(projectID, "live-recent");
      }
    }

    return statusByProject;
  }, [emissions]);

  // Fetch feed data from API
  useEffect(() => {
    let cancelled = false;

    async function fetchFeed() {
      try {
        setIsLoading(true);
        setError(null);
        const [activityResult, projectsResult, syncResult] = await Promise.allSettled([
          api.activityRecent(50),
          api.projects(),
          api.syncAgents(),
        ]);
        let feedFallbackFailed = false;

        if (cancelled) return;

        const nextProjects = projectsResult.status === "fulfilled"
          ? (projectsResult.value.projects || [])
          : [];
        const projectNamesByID = buildProjectNameMap(nextProjects);

        const nextAgentNamesByAlias =
          syncResult.status === "fulfilled"
            ? buildAgentAliasNameMap(syncResult.value)
            : new Map<string, string>();
        setAgentNamesByAlias(nextAgentNamesByAlias);

        if (activityResult.status === "fulfilled" && (activityResult.value.items || []).length > 0) {
          setActionItems([]);
          setFeedItems(
            mapRecentActivityToFeedItems(activityResult.value.items || [], {
              projectNamesByID,
              agentNamesByAlias: nextAgentNamesByAlias,
            }),
          );
        } else {
          const feedResult = await Promise.resolve(api.feed()).then(
            (value) => ({ status: "fulfilled" as const, value }),
            (reason) => ({ status: "rejected" as const, reason }),
          );
          if (feedResult.status === "fulfilled") {
            const feedValue = feedResult.value;
            if ("feedItems" in feedValue) {
              setActionItems(feedValue.actionItems || []);
              setFeedItems(feedValue.feedItems || []);
            } else {
              setActionItems([]);
              setFeedItems(mapActivityToFeedItems(feedValue.items || []));
            }
          } else if (!isDemoMode()) {
            feedFallbackFailed = true;
            setActionItems([]);
            setFeedItems([]);
          }
        }

        if (projectsResult.status === "fulfilled") {
          setProjects(nextProjects);
        } else if (!isDemoMode()) {
          setProjects([]);
        }

        if (syncResult.status === "fulfilled" && syncResult.value?.last_sync) {
          setLastSync(new Date(syncResult.value.last_sync));
        } else if (!isDemoMode()) {
          setLastSync(null);
        }

        if (
          (activityResult.status === "rejected" && feedFallbackFailed) ||
          projectsResult.status === "rejected" ||
          syncResult.status === "rejected"
        ) {
          console.warn("API unavailable:", {
            recentActivity: activityResult.status === "rejected" ? activityResult.reason : null,
            projects: projectsResult.status === "rejected" ? projectsResult.reason : null,
            sync: syncResult.status === "rejected" ? syncResult.reason : null,
          });
          setError("Unable to connect to API");
        }
      } catch (err) {
        if (!cancelled) {
          console.warn("API unavailable:", err);
          setError("Unable to connect to API");
          if (!isDemoMode()) {
            setActionItems([]);
            setFeedItems([]);
            setProjects([]);
            setLastSync(null);
          }
        }
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    }

    fetchFeed();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!lastMessage || lastMessage.type !== "ActivityEventReceived") {
      return;
    }

    const realtimeEvent = parseRealtimeActivityEvent(lastMessage.data);
    if (!realtimeEvent) {
      return;
    }

    const nextFeedItem = mapRecentActivityToFeedItems([realtimeEvent], {
      projectNamesByID: buildProjectNameMap(projects),
      agentNamesByAlias,
    })[0];
    if (!nextFeedItem) {
      return;
    }

    setFeedItems((prev) => {
      const withoutExisting = prev.filter((entry) => entry.id !== nextFeedItem.id);
      return [nextFeedItem, ...withoutExisting].slice(0, 100);
    });
    setLastSync(new Date(realtimeEvent.created_at));
    setError(null);
  }, [agentNamesByAlias, lastMessage, projects]);

  const commands = useMemo<Command[]>(
    () => [
      {
        id: "nav-projects",
        label: "Go to Projects",
        category: "Navigation",
        keywords: ["projects", "boards"],
        action: () => (window.location.href = "/projects"),
      },
      {
        id: "nav-agents",
        label: "Go to Agents",
        category: "Navigation",
        keywords: ["agents", "ai"],
        action: () => (window.location.href = "/agents"),
      },
      {
        id: "nav-feed",
        label: "Go to Feed",
        category: "Navigation",
        keywords: ["feed", "activity"],
        action: () => (window.location.href = "/feed"),
      },
      {
        id: "task-create",
        label: "Create New Task",
        category: "Tasks",
        keywords: ["new", "task", "create"],
        action: () => window.alert("Task creation coming soon"),
      },
      {
        id: "settings-theme",
        label: "Toggle Dark Mode",
        category: "Settings",
        keywords: ["dark", "light", "theme"],
        action: () => document.documentElement.classList.toggle("dark"),
      },
    ],
    []
  );

  return (
    <OnboardingTour>
      <div className="two-column-layout">
      {/* ========== PRIMARY COLUMN ========== */}
      <div className="primary">
        {lastSync && (
          <div className="last-sync">Last updated {lastSync.toLocaleString()}</div>
        )}
        {!lastSync && !isLoading && (
          <div className="last-sync">No data here yet.</div>
        )}
        {/* NEEDS YOU Section */}
        <section className="section" data-tour="needs-you">
          <header className="section-header">
            <h2 className="section-title">⚡ NEEDS YOU</h2>
            <span className="section-count">{actionItems.length}</span>
            {isLoading && <span className="loading-indicator">⏳</span>}
          </header>

          {error && (
            <div className="api-notice">
              <span>📡 Using cached data</span>
            </div>
          )}

          {actionItems.length === 0 && !isLoading && (
            <div className="empty-state">No approvals waiting.</div>
          )}

          {actionItems.map((item) => (
            <div key={item.id} className="action-card">
              <div className="action-header">
                <span className="action-icon">{item.icon}</span>
                <span className="action-project">{item.project}</span>
                <span className="action-time">{item.time}</span>
              </div>
              <p className="action-text">
                <strong>{item.agent}</strong> {item.message}
              </p>
              <div className="action-buttons">
                <button type="button" className="btn btn-primary">
                  {item.primaryAction}
                </button>
                <button type="button" className="btn btn-secondary">
                  {item.secondaryAction}
                </button>
              </div>
            </div>
          ))}
        </section>

        {/* YOUR FEED Section */}
        <section className="section" data-tour="your-feed">
          <header className="section-header">
            <h2 className="section-title">📡 YOUR FEED</h2>
            <span className="section-count muted">{feedItems.length}</span>
          </header>

          <div className="live-ticker">
            <span className="live-pill">LIVE</span>
            <EmissionTicker
              emissions={emissions}
              limit={5}
              emptyText="No live emissions yet"
            />
          </div>

          <div className="card">
            {feedItems.length === 0 && !isLoading && (
              <div className="empty-state">No activity yet.</div>
            )}
            {feedItems.map((item) => (
              <div key={item.id} className="feed-item">
                <div 
                  className="feed-avatar" 
                  style={{ background: item.avatarBg }}
                >
                  {item.avatar}
                </div>
                <div className="feed-content">
                  <p className="feed-text">
                    <strong>{item.title}</strong> {item.text}
                  </p>
                  <p className="feed-meta">
                    {item.type && (
                      <span className={`feed-type ${item.type.className}`}>
                        {item.type.label}
                      </span>
                    )}
                    {item.type && " • "}
                    {item.meta}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>

      {/* ========== SECONDARY COLUMN (SIDEBAR) ========== */}
      <aside className="secondary">
        {/* Otter Illustration */}
        <div className="otter-illustration">
          <img 
            src="/images/otters-sailing.png" 
            alt="Otters sailing together" 
            className="otter-woodcut"
          />
          <p className="otter-caption">Your otters, working together</p>
        </div>

        {/* Quick Action - Drop a thought */}
        <div 
          className="add-button" 
          onClick={openCommandPalette}
          role="button"
          tabIndex={0}
          onKeyDown={(e) => e.key === "Enter" && openCommandPalette()}
        >
          <div className="add-otter">🦦💭</div>
          <div className="add-text">Drop a thought</div>
          <div className="add-hint">Press / to open command bar</div>
        </div>

        {/* Projects List */}
        <div className="projects-card">
          <div className="projects-header">Projects</div>
          {projects.length === 0 && !isLoading && (
            <div className="empty-state">No projects yet.</div>
          )}
          {projects.map((project) => {
            const total = project.taskCount ?? 0;
            const done = project.completedCount ?? 0;
            const desc = formatProjectTaskSummary(done, total);
            const projectStatusClass =
              liveProjectStatus.get(project.id) || project.status || "idle";
            return (
              <div key={project.id} className="project-item">
                <div className={`project-status ${projectStatusClass}`}></div>
                <div className="project-info">
                  <div className="project-name">{project.name}</div>
                  <div className="project-desc">{desc}</div>
                </div>
                <div className="project-time">&nbsp;</div>
              </div>
            );
          })}
        </div>
      </aside>
      </div>

      {/* Command Palette */}
      <CommandPalette
        commands={commands}
        isOpen={isCommandPaletteOpen}
        onOpenChange={(open) => !open && closeCommandPalette()}
      />

      {/* Task Detail Slide-over */}
      {selectedTaskId && (
        <TaskDetail
          taskId={selectedTaskId}
          isOpen={!!selectedTaskId}
          onClose={closeTaskDetail}
        />
      )}

      {/* New Task Modal */}
      <NewTaskModal isOpen={isNewTaskOpen} onClose={closeNewTask} />

      <style>{`
        /* Loading and API states */
        .loading-indicator {
          margin-left: 8px;
          animation: pulse 1s ease-in-out infinite;
        }
        
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.5; }
        }
        
        .api-notice {
          background: var(--surface-alt);
          border: 1px solid var(--border);
          border-radius: 8px;
          padding: 8px 12px;
          margin-bottom: 12px;
          font-size: 12px;
          color: var(--text-muted);
          display: flex;
          align-items: center;
          gap: 8px;
        }

        .empty-state {
          color: var(--text-muted);
          font-size: 13px;
          padding: 12px;
        }

        .live-ticker {
          display: flex;
          align-items: center;
          gap: 10px;
          margin-bottom: 10px;
          padding: 10px 12px;
          border: 1px solid var(--border);
          border-radius: 10px;
          background: var(--surface);
        }

        .live-pill {
          border-radius: 999px;
          padding: 2px 8px;
          font-size: 11px;
          font-weight: 700;
          letter-spacing: 0.05em;
          background: #ef4444;
          color: #fff;
        }

        .last-sync {
          color: var(--text-muted);
          font-size: 12px;
          margin-bottom: 10px;
        }
        
        /* Otter illustration */
        .otter-illustration {
          background: var(--surface);
          border: 1px solid var(--border);
          border-radius: 16px;
          padding: 20px;
          text-align: center;
          overflow: hidden;
        }
        
        .otter-woodcut {
          width: 100%;
          max-width: 280px;
          height: auto;
          border-radius: 8px;
          opacity: 0.9;
          filter: sepia(20%) contrast(1.1);
          transition: all 0.3s;
        }
        
        .otter-illustration:hover .otter-woodcut {
          opacity: 1;
          transform: scale(1.02);
        }
        
        .otter-caption {
          margin-top: 12px;
          font-size: 13px;
          color: var(--text-muted);
          font-style: italic;
        }
        
        /* Section count badges */
        .section-count {
          background: var(--red);
          color: white;
          font-size: 12px;
          font-weight: 700;
          padding: 2px 10px;
          border-radius: 10px;
        }
        
        .section-count.muted {
          background: var(--text-muted);
        }
        
        /* Action card enhancements */
        .action-header {
          display: flex;
          align-items: center;
          gap: 12px;
          margin-bottom: 12px;
        }
        
        .action-icon {
          font-size: 24px;
        }
        
        .action-project {
          font-weight: 700;
          font-size: 18px;
        }
        
        .action-time {
          margin-left: auto;
          font-size: 12px;
          color: var(--text-muted);
        }
        
        .action-text {
          color: var(--text-muted);
          margin-bottom: 16px;
        }
        
        .action-text strong {
          color: var(--text);
        }
        
        .action-buttons {
          display: flex;
          gap: 12px;
        }
        
        /* Feed type badges */
        .feed-type {
          font-size: 11px;
          font-weight: 600;
          text-transform: uppercase;
          letter-spacing: 0.5px;
          padding: 2px 8px;
          border-radius: 4px;
          background: var(--surface-alt);
          color: var(--text-muted);
        }
        
        .feed-type.insight {
          background: rgba(74, 109, 124, 0.15);
          color: var(--blue);
        }
        
        .feed-type.progress {
          background: rgba(90, 122, 92, 0.15);
          color: var(--green);
        }
        
        /* Add button */
        .add-button {
          background: var(--surface);
          border: 2px dashed var(--border);
          border-radius: 12px;
          padding: 24px;
          text-align: center;
          cursor: pointer;
          transition: all 0.2s;
        }
        
        .add-button:hover {
          border-color: var(--accent);
          border-style: solid;
          transform: translateY(-2px);
          box-shadow: 0 4px 12px rgba(0,0,0,0.15);
        }
        
        .add-otter {
          font-size: 44px;
          margin-bottom: 8px;
          transition: transform 0.2s;
        }
        
        .add-button:hover .add-otter {
          transform: scale(1.1);
        }
        
        .add-text {
          font-weight: 600;
          color: var(--text-muted);
          font-size: 15px;
        }
        
        .add-hint {
          font-size: 12px;
          color: var(--text-muted);
          margin-top: 4px;
          opacity: 0.7;
        }
        
        /* Projects card */
        .projects-card {
          background: var(--surface);
          border: 1px solid var(--border);
          border-radius: 12px;
          overflow: hidden;
        }
        
        .projects-header {
          background: var(--surface-alt);
          padding: 14px 20px;
          border-bottom: 1px solid var(--border);
          font-weight: 700;
          font-size: 14px;
        }
        
        .project-item {
          padding: 14px 20px;
          border-bottom: 1px solid var(--border);
          display: flex;
          align-items: center;
          gap: 12px;
          cursor: pointer;
          transition: all 0.15s;
        }
        
        .project-item:last-child {
          border-bottom: none;
        }
        
        .project-item:hover {
          background: var(--surface-alt);
          transform: translateX(4px);
        }
        
        .project-status {
          width: 10px;
          height: 10px;
          border-radius: 50%;
          flex-shrink: 0;
        }
        
        .project-status.blocked {
          background: var(--red);
        }
        
        .project-status.working {
          background: var(--green);
        }
        
        .project-status.idle {
          background: var(--text-muted);
        }

        .project-status.live-active {
          background: var(--green);
          box-shadow: 0 0 0 4px rgba(74, 222, 128, 0.2);
        }

        .project-status.live-recent {
          background: #f59e0b;
        }
        
        .project-info {
          flex: 1;
          min-width: 0;
        }
        
        .project-name {
          font-weight: 600;
          font-size: 14px;
        }
        
        .project-desc {
          font-size: 12px;
          color: var(--text-muted);
          white-space: nowrap;
          overflow: hidden;
          text-overflow: ellipsis;
        }
        
        .project-time {
          font-size: 11px;
          color: var(--text-muted);
          white-space: nowrap;
        }
      `}</style>
    </OnboardingTour>
  );
}
