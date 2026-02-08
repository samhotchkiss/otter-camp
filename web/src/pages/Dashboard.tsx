import { useMemo, useState, useEffect } from "react";
import CommandPalette from "../components/CommandPalette";
import OnboardingTour from "../components/OnboardingTour";
import TaskDetail from "../components/TaskDetail";
import NewTaskModal from "../components/NewTaskModal";
import type { Command } from "../hooks/useCommandPalette";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { api, type ActionItem, type FeedApiItem, type FeedItem, type Project } from "../lib/api";
import { formatProjectTaskSummary } from "../lib/projectTaskSummary";
import { getActivityDescription, formatRelativeTime, getTypeConfig, normalizeMetadata } from "../components/activity/activityFormat";
import { getInitials } from "../components/messaging/utils";
import { isDemoMode } from "../lib/demo";

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

function mapActivityToFeedItems(items: FeedApiItem[]): FeedItem[] {
  return items.map((item) => {
    const actorName = item.agent_name?.trim() || "System";
    const type = item.type || "activity";
    const typeConfig = getTypeConfig(type);
    const description = getActivityDescription({
      type,
      actorName,
      taskTitle: item.task_title || undefined,
      summary: item.summary || undefined,
      metadata: normalizeMetadata(item.metadata),
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
  const [lastSync, setLastSync] = useState<Date | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Fetch feed data from API
  useEffect(() => {
    let cancelled = false;

    async function fetchFeed() {
      try {
        setIsLoading(true);
        setError(null);
        const [feedResult, projectsResult, syncResult] = await Promise.allSettled([
          api.feed(),
          api.projects(),
          api.syncAgents(),
        ]);

        if (cancelled) return;

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
          setActionItems([]);
          setFeedItems([]);
        }

        if (projectsResult.status === "fulfilled") {
          setProjects(projectsResult.value.projects || []);
        } else if (!isDemoMode()) {
          setProjects([]);
        }

        if (syncResult.status === "fulfilled" && syncResult.value?.last_sync) {
          setLastSync(new Date(syncResult.value.last_sync));
        } else if (!isDemoMode()) {
          setLastSync(null);
        }

        if (
          feedResult.status === "rejected" ||
          projectsResult.status === "rejected" ||
          syncResult.status === "rejected"
        ) {
          console.warn("API unavailable:", {
            feed: feedResult.status === "rejected" ? feedResult.reason : null,
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
            return (
              <div key={project.id} className="project-item">
                <div className={`project-status ${project.status || "active"}`}></div>
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
