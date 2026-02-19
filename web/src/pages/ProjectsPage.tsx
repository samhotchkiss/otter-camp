// Cache bust: 2026-02-05-11:15
import { useState, useEffect, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import { ErrorFallback } from "../components/ErrorBoundary";
import { NoProjectsEmpty } from "../components/EmptyState";
import { SkeletonList } from "../components/Skeleton";
import LabelFilter from "../components/LabelFilter";
import LabelPill, { type LabelOption } from "../components/LabelPill";
import api, { API_URL } from "../lib/api";
import { isDemoMode } from "../lib/demo";
import { formatProjectTaskSummary } from "../lib/projectTaskSummary";
// Filters removed - use magic bar (Cmd+K) for search

type Project = {
  id: string;
  name: string;
  description?: string | null;
  repo?: string | null;
  githubSync?: boolean;
  openIssues?: number;
  inProgress?: number;
  needsApproval?: number;
  techStack?: string[];
  taskCount?: number;
  completedCount?: number;
  color?: string;
  emoji?: string;
  status?: string;
  assignee?: string | null;
  priority?: string | null;
  updatedAt?: string;
  updated_at?: string;
  createdAt?: string;
  created_at?: string;
  labels?: LabelOption[];
};

const SAMPLE_PROJECTS: Project[] = [
  {
    id: "1",
    name: "Pearl Proxy",
    description: "Memory and routing infrastructure",
    repo: "ottercamp/pearl-proxy",
    githubSync: true,
    openIssues: 12,
    inProgress: 5,
    needsApproval: 2,
    techStack: ["TypeScript", "Redis", "Postgres"],
    taskCount: 12,
    completedCount: 5,
    color: "amber",
    emoji: "🔮",
    status: "active",
    assignee: "Derek",
    priority: "urgent",
    updatedAt: "2026-02-04T01:30:00Z",
  },
  {
    id: "2",
    name: "Otter Camp",
    description: "Project and issue tracking for AI agent teams",
    repo: "ottercamp/otter-camp",
    githubSync: true,
    openIssues: 24,
    inProgress: 6,
    needsApproval: 3,
    techStack: ["Go", "React", "Postgres"],
    taskCount: 24,
    completedCount: 18,
    color: "amber",
    emoji: "🦦",
    status: "active",
    assignee: "Derek",
    priority: "high",
    updatedAt: "2026-02-03T18:12:00Z",
  },
  {
    id: "3",
    name: "ItsAlive",
    description: "Static site deployment platform",
    repo: "ottercamp/itsalive",
    githubSync: true,
    openIssues: 8,
    inProgress: 0,
    needsApproval: 0,
    techStack: ["Node.js", "Docker", "Cloudflare"],
    taskCount: 8,
    completedCount: 8,
    color: "amber",
    emoji: "⚡",
    status: "completed",
    assignee: "Ivy",
    priority: "medium",
    updatedAt: "2026-01-29T10:00:00Z",
  },
  {
    id: "4",
    name: "Three Stones",
    description: "Educational content and presentations",
    repo: "local/three-stones",
    githubSync: false,
    openIssues: 15,
    inProgress: 2,
    needsApproval: 1,
    techStack: ["Markdown", "Astro", "Vite"],
    taskCount: 15,
    completedCount: 10,
    color: "amber",
    emoji: "🪨",
    status: "archived",
    assignee: "Stone",
    priority: "low",
    updatedAt: "2025-12-18T15:45:00Z",
  },
];

const DEFAULT_PROJECTS_ENDPOINT = `${API_URL}/api/projects`;

// All projects use gold/amber accent color per DESIGN-SPEC.md
const colorClasses: Record<string, { bg: string; text: string; progress: string }> = {
  sky: {
    bg: "bg-amber-100/50 dark:bg-amber-900/20",
    text: "text-amber-700 dark:text-amber-300",
    progress: "bg-[#C9A86C]",
  },
  emerald: {
    bg: "bg-amber-100/50 dark:bg-amber-900/20",
    text: "text-amber-700 dark:text-amber-300",
    progress: "bg-[#C9A86C]",
  },
  amber: {
    bg: "bg-amber-100/50 dark:bg-amber-900/20",
    text: "text-amber-700 dark:text-amber-300",
    progress: "bg-[#C9A86C]",
  },
  violet: {
    bg: "bg-amber-100/50 dark:bg-amber-900/20",
    text: "text-amber-700 dark:text-amber-300",
    progress: "bg-[#C9A86C]",
  },
};

const getUpdatedAtMs = (project: Project) => {
  const raw =
    project.updatedAt ??
    project.updated_at ??
    project.createdAt ??
    project.created_at;
  if (!raw) return 0;
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? 0 : ms;
};

type ProjectActivityItem = {
  id: string;
  projectName: string;
  summary: string;
  status: "in-progress" | "needs-approval" | "todo" | "done";
  priority: "critical" | "high" | "medium" | "low";
  assignee: string;
};

const buildRecentActivity = (projects: Project[]): ProjectActivityItem[] => {
  return projects.slice(0, 5).map((project) => {
    const taskCount = project.taskCount ?? 0;
    const completedCount = project.completedCount ?? 0;
    const openIssues = project.openIssues ?? taskCount;
    const inProgress = project.inProgress ?? Math.max(taskCount - completedCount, 0);
    const needsApproval = project.needsApproval ?? 0;

    if (needsApproval > 0) {
      return {
        id: `${project.id}-review`,
        projectName: project.name,
        summary: `${needsApproval} item${needsApproval === 1 ? "" : "s"} waiting for approval`,
        status: "needs-approval",
        priority: "high",
        assignee: project.assignee || "Unassigned",
      };
    }

    if (inProgress > 0) {
      return {
        id: `${project.id}-active`,
        projectName: project.name,
        summary: `${inProgress} issue${inProgress === 1 ? "" : "s"} actively in progress`,
        status: "in-progress",
        priority: "medium",
        assignee: project.assignee || "Unassigned",
      };
    }

    if (openIssues > 0) {
      return {
        id: `${project.id}-todo`,
        projectName: project.name,
        summary: `${openIssues} open issue${openIssues === 1 ? "" : "s"} remaining`,
        status: "todo",
        priority: project.priority === "urgent" ? "critical" : "low",
        assignee: project.assignee || "Unassigned",
      };
    }

    return {
      id: `${project.id}-done`,
      projectName: project.name,
      summary: "No open issues",
      status: "done",
      priority: "low",
      assignee: project.assignee || "Unassigned",
    };
  });
};

const normalizeProjectLabel = (label: LabelOption): LabelOption | null => {
  const id = typeof label.id === "string" ? label.id.trim() : "";
  const name = typeof label.name === "string" ? label.name.trim() : "";
  const color = typeof label.color === "string" ? label.color.trim() : "";
  if (!id || !name) {
    return null;
  }
  return {
    id,
    name,
    color: color || "#6b7280",
  };
};

const mergeLabelCatalog = (
  existing: LabelOption[],
  projects: Project[],
  replaceExisting: boolean,
): LabelOption[] => {
  const catalog = new Map<string, LabelOption>();
  if (!replaceExisting) {
    for (const label of existing) {
      const normalized = normalizeProjectLabel(label);
      if (normalized) {
        catalog.set(normalized.id, normalized);
      }
    }
  }
  for (const project of projects) {
    for (const label of project.labels ?? []) {
      const normalized = normalizeProjectLabel(label);
      if (normalized) {
        catalog.set(normalized.id, normalized);
      }
    }
  }
  return [...catalog.values()].sort((a, b) => a.name.localeCompare(b.name));
};

function ProjectCard({ project, onClick }: { project: Project; onClick: () => void }) {
  const taskCount = project.taskCount ?? 0;
  const completedCount = project.completedCount ?? 0;
  const progress = taskCount > 0 ? Math.round((completedCount / taskCount) * 100) : 0;
  const taskSummary = formatProjectTaskSummary(completedCount, taskCount);
  const colors = colorClasses[project.color ?? "sky"] ?? colorClasses.sky;
  const updatedAt = project.updatedAt ?? project.updated_at ?? project.createdAt ?? project.created_at;
  const isArchived = (project.status || "").toLowerCase() === "archived";
  const fallbackRepo = `local/${project.name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "")}`;
  const repoName = (project.repo || "").trim() || fallbackRepo;
  const githubSync = project.githubSync ?? !repoName.startsWith("local/");
  const openIssues = project.openIssues ?? taskCount;
  const inProgress = project.inProgress ?? Math.max(taskCount - completedCount, 0);
  const needsApproval = project.needsApproval ?? 0;

  return (
    <div
      data-testid={`project-card-${project.id}`}
      onClick={onClick}
      className={`group cursor-pointer rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 shadow-sm backdrop-blur transition hover:border-[var(--accent)]/30 hover:shadow-md ${
        isArchived ? "opacity-70" : ""
      }`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1 overflow-hidden">
          <h3 className="truncate text-lg font-semibold text-[var(--text)]">
            {project.name}
          </h3>
          <p className="mt-1 flex items-center gap-1 truncate text-xs font-mono text-[var(--text-muted)]">
            <span aria-hidden="true">⑂</span>
            {repoName}
          </p>
        </div>

        <div
          className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${
            githubSync
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-500"
              : "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]"
          }`}
        >
          {githubSync ? "Synced" : "Local"}
        </div>
      </div>

      <div className="mt-4 grid grid-cols-3 gap-2">
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg)]/40 px-2 py-2">
          <p className="text-base font-semibold text-[var(--text)]">{openIssues}</p>
          <p className="text-[10px] uppercase tracking-wide text-[var(--text-muted)]">Open</p>
        </div>
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg)]/40 px-2 py-2">
          <p className="text-base font-semibold text-amber-500">{inProgress}</p>
          <p className="text-[10px] uppercase tracking-wide text-[var(--text-muted)]">Active</p>
        </div>
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg)]/40 px-2 py-2">
          <p className="text-base font-semibold text-orange-500">{needsApproval}</p>
          <p className="text-[10px] uppercase tracking-wide text-[var(--text-muted)]">Review</p>
        </div>
      </div>

      <p className="mt-3 text-sm text-[var(--text-muted)]">
        {project.description ?? ""}
      </p>

      <div className="mt-3 flex items-center justify-between">
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          className="rounded-lg p-2 text-[var(--text-muted)] opacity-0 transition hover:bg-[var(--surface-alt)] hover:text-[var(--text)] group-hover:opacity-100"
          aria-label="Project options"
        >
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 5v.01M12 12v.01M12 19v.01M12 6a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2z" />
          </svg>
        </button>
        <div className={`flex h-9 w-9 items-center justify-center rounded-lg text-base ${colors.bg}`}>
          {project.emoji || "📁"}
        </div>
      </div>

      {(project.labels ?? []).length > 0 && (
        <div className="mt-3 flex flex-wrap gap-2">
          {(project.labels ?? []).map((label) => (
            <LabelPill key={label.id} label={label} />
          ))}
        </div>
      )}

      {(project.status || project.priority || project.assignee) && (
        <div className="mt-3 flex flex-wrap gap-2">
          {project.status && (
            <span
              className={`rounded-full px-3 py-1 text-xs font-medium ${
                isArchived
                  ? "border border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text)]"
                  : "bg-[var(--surface-alt)] text-[var(--text-muted)]"
              }`}
            >
              {isArchived ? "Archived" : `Status: ${project.status}`}
            </span>
          )}
          {project.priority && (
            <span className="rounded-full bg-amber-100 px-3 py-1 text-xs font-medium text-amber-800 dark:bg-amber-900/30 dark:text-amber-200">
              Priority: {project.priority}
            </span>
          )}
          {project.assignee && (
            <span className="rounded-full bg-sky-100 px-3 py-1 text-xs font-medium text-sky-800 dark:bg-sky-900/30 dark:text-sky-200">
              Assignee: {project.assignee}
            </span>
          )}
        </div>
      )}

      <div className="mt-4">
        <div className="flex items-center justify-between text-sm">
          <span className="text-[var(--text-muted)]">Progress</span>
          <span className={`font-medium ${colors.text}`}>
            {taskSummary}
          </span>
        </div>
        <div
          role="progressbar"
          aria-label={`Progress for ${project.name}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={progress}
          className="mt-2 h-2 overflow-hidden rounded-full border border-[var(--border)] bg-[var(--bg)]/70"
        >
          <div
            data-testid={`project-progress-fill-${project.id}`}
            className={`h-full rounded-full transition-all ${colors.progress}`}
            style={{ width: `${progress}%` }}
          />
        </div>
        {updatedAt && (
          <p className="mt-3 text-xs text-[var(--text-muted)]">
            Updated {new Date(updatedAt).toLocaleString()}
          </p>
        )}
        {(project.techStack ?? []).length > 0 && (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {(project.techStack ?? []).map((tech) => (
              <span
                key={`${project.id}-${tech}`}
                className="rounded bg-[var(--surface-alt)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]"
              >
                {tech}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export type ProjectsPageProps = {
  apiEndpoint?: string;
};

export default function ProjectsPage({
  apiEndpoint = DEFAULT_PROJECTS_ENDPOINT,
}: ProjectsPageProps) {
  const navigate = useNavigate();
  const [projects, setProjects] = useState<Project[]>([]);
  const [labelCatalog, setLabelCatalog] = useState<LabelOption[]>([]);
  const [selectedLabelIDs, setSelectedLabelIDs] = useState<string[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Fetch projects from API
  const fetchProjects = useCallback(async () => {
    try {
      if (apiEndpoint === DEFAULT_PROJECTS_ENDPOINT) {
        const data = await api.projects(selectedLabelIDs);
        return (data.projects || []) as Project[];
      }

      const url = new URL(apiEndpoint, window.location.origin);
      for (const labelID of selectedLabelIDs) {
        const normalized = labelID.trim();
        if (!normalized) {
          continue;
        }
        url.searchParams.append("label", normalized);
      }
      const response = await fetch(url.toString());
      if (!response.ok) {
        throw new Error("Failed to fetch projects");
      }
      const data = await response.json();
      return (data.projects || data || []) as Project[];
    } catch (err) {
      throw err instanceof Error ? err : new Error("Failed to load projects");
    }
  }, [apiEndpoint, selectedLabelIDs]);

  // Initial fetch
  useEffect(() => {
    const loadProjects = async () => {
      setIsLoading(true);
      setError(null);
      try {
        const fetchedProjects = await fetchProjects();
        if (fetchedProjects.length === 0 && isDemoMode()) {
          setProjects(SAMPLE_PROJECTS);
        } else {
          setProjects(fetchedProjects);
          setLabelCatalog((existing) =>
            mergeLabelCatalog(existing, fetchedProjects, selectedLabelIDs.length === 0),
          );
        }
      } catch (err) {
        if (isDemoMode()) {
          setProjects(SAMPLE_PROJECTS);
          setLabelCatalog([]);
        } else {
          setProjects([]);
          setError(err instanceof Error ? err.message : "Failed to load projects");
        }
      } finally {
        setIsLoading(false);
      }
    };

    loadProjects();
  }, [fetchProjects, selectedLabelIDs.length]);

  const handleCreateProject = () => {
    window.alert("Project creation coming soon!");
  };

  // Simple sort by updated date (most recent first)
  const sortedProjects = useMemo(() => {
    return [...projects].sort((a, b) => getUpdatedAtMs(b) - getUpdatedAtMs(a));
  }, [projects]);
  const recentActivity = useMemo(() => buildRecentActivity(sortedProjects), [sortedProjects]);

  if (isLoading) {
    return (
      <div className="w-full min-w-0">
        <div className="mb-6 flex flex-col items-start gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)]">
              Projects
            </h1>
            <p className="mt-1 text-sm text-[var(--text-muted)]">
              Loading projects...
            </p>
          </div>
          <LoadingSpinner size="md" />
        </div>
        <SkeletonList count={4} variant="project" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="w-full min-w-0">
        <div className="mb-6">
          <h1 className="text-2xl font-semibold text-[var(--text)]">
            Projects
          </h1>
        </div>
        <ErrorFallback
          error={error}
          message="Failed to load projects"
          onRetry={() => window.location.reload()}
        />
      </div>
    );
  }

  if (projects.length === 0 && selectedLabelIDs.length === 0) {
    return (
      <div className="w-full">
        <div className="mb-6">
          <h1 className="text-2xl font-semibold text-[var(--text)]">
            Projects
          </h1>
          <p className="mt-1 text-sm text-[var(--text-muted)]">
            Manage your workspaces and track progress
          </p>
        </div>
        <NoProjectsEmpty onCreate={handleCreateProject} />
      </div>
    );
  }

  if (projects.length === 0 && selectedLabelIDs.length > 0) {
    return (
      <div className="w-full min-w-0">
        <div className="mb-6 flex flex-col items-start gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)]">
              Projects
            </h1>
            <p className="mt-1 text-sm text-[var(--text-muted)]">
              No projects match the selected labels.
            </p>
          </div>
          <button
            type="button"
            onClick={() => setSelectedLabelIDs([])}
            className="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 sm:w-auto dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            Clear filters
          </button>
        </div>
        {labelCatalog.length > 0 && (
          <LabelFilter
            labels={labelCatalog}
            selectedLabelIDs={selectedLabelIDs}
            onChange={setSelectedLabelIDs}
          />
        )}
      </div>
    );
  }

  return (
    <div className="w-full min-w-0">
      <div className="mb-6 flex flex-col items-start gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-[var(--text)]">
            Projects
          </h1>
          <p className="mt-1 text-sm text-[var(--text-muted)]">
            Manage your workspaces and track progress
          </p>
        </div>
        <button
          type="button"
          onClick={handleCreateProject}
          className="inline-flex w-full items-center justify-center gap-2 rounded-xl bg-[#C9A86C] px-4 py-2.5 text-sm font-medium text-[#1a1a1a] shadow-sm transition hover:bg-[#B8975B] focus:outline-none focus:ring-2 focus:ring-[#C9A86C] focus:ring-offset-2 focus:ring-offset-[var(--bg)] sm:w-auto"
        >
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          New Project
        </button>
      </div>

      {/* Use Cmd+K (magic bar) to search projects */}
      <div className="mb-4 flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm text-[var(--text-muted)]">
          {projects.length} projects • Press <kbd className="rounded bg-[var(--surface-alt)] px-1.5 py-0.5 text-xs">⌘K</kbd> to search
        </p>
      </div>

      {labelCatalog.length > 0 && (
        <div className="mb-4">
          <LabelFilter
            labels={labelCatalog}
            selectedLabelIDs={selectedLabelIDs}
            onChange={setSelectedLabelIDs}
          />
        </div>
      )}

      <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
        {sortedProjects.map((project) => (
          <ProjectCard
            key={project.id}
            project={project}
            onClick={() => navigate(`/projects/${project.id}`)}
          />
        ))}

        {/* Empty state placeholder card */}
        <button
          type="button"
          onClick={handleCreateProject}
          className="flex min-h-[200px] flex-col items-center justify-center rounded-2xl border-2 border-dashed border-[var(--border)] bg-[var(--surface)]/50 p-5 text-[var(--text-muted)] transition hover:border-[#C9A86C] hover:bg-[#C9A86C]/10 hover:text-[#C9A86C]"
        >
          <svg className="h-10 w-10" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 4v16m8-8H4" />
          </svg>
          <span className="mt-3 text-sm font-medium">Create a new project</span>
        </button>
      </div>

      <section className="mt-6 overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-sm">
        <div className="flex flex-col items-start gap-2 border-b border-[var(--border)] bg-[var(--surface-alt)]/40 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <h2 className="text-sm font-semibold text-[var(--text)]">Recent Activity</h2>
          <button
            type="button"
            className="text-xs font-medium text-[var(--accent)] transition hover:opacity-80"
          >
            View All
          </button>
        </div>
        <div className="divide-y divide-[var(--border)]/60">
          {recentActivity.map((item) => (
            <div
              key={item.id}
              className="flex flex-col items-start gap-2 px-5 py-3 transition hover:bg-[var(--surface-alt)]/30 sm:flex-row sm:items-center sm:justify-between"
            >
              <div className="min-w-0">
                <p className="truncate text-sm font-medium text-[var(--text)]">{item.summary}</p>
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  {item.projectName} • {item.assignee}
                </p>
              </div>
              <span
                className={`self-start rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide sm:self-auto ${
                  item.status === "needs-approval"
                    ? "border-orange-500/30 bg-orange-500/10 text-orange-500"
                    : item.status === "in-progress"
                      ? "border-amber-500/30 bg-amber-500/10 text-amber-500"
                      : item.status === "todo"
                        ? "border-slate-500/30 bg-slate-500/10 text-slate-500"
                        : "border-emerald-500/30 bg-emerald-500/10 text-emerald-500"
                }`}
              >
                {item.status.replace("-", " ")}
              </span>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
