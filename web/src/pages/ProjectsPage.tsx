// Cache bust: 2026-02-05-11:15
import { useState, useEffect, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import { ErrorFallback } from "../components/ErrorBoundary";
import { NoProjectsEmpty } from "../components/EmptyState";
import { SkeletonList } from "../components/Skeleton";
import api from "../lib/api";
import { isDemoMode } from "../lib/demo";
// Filters removed - use magic bar (Cmd+K) for search

type Project = {
  id: string;
  name: string;
  description?: string | null;
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
};

const SAMPLE_PROJECTS: Project[] = [
  {
    id: "1",
    name: "Pearl Proxy",
    description: "Memory and routing infrastructure",
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
    description: "Task management for AI-assisted workflows",
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

function ProjectCard({ project, onClick }: { project: Project; onClick: () => void }) {
  const taskCount = project.taskCount ?? 0;
  const completedCount = project.completedCount ?? 0;
  const progress = taskCount > 0 ? Math.round((completedCount / taskCount) * 100) : 0;
  const colors = colorClasses[project.color ?? "sky"] ?? colorClasses.sky;
  const updatedAt = project.updatedAt ?? project.updated_at ?? project.createdAt ?? project.created_at;

  return (
    <div
      onClick={onClick}
      className="group cursor-pointer rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 shadow-sm backdrop-blur transition hover:border-[var(--accent)]/30 hover:shadow-md">
      <div className="flex items-start justify-between">
        <div className={`flex h-12 w-12 items-center justify-center rounded-xl text-2xl ${colors.bg}`}>
          {project.emoji}
        </div>
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
      </div>

      <h3 className="mt-4 text-lg font-semibold text-[var(--text)]">
        {project.name}
      </h3>
      <p className="mt-1 text-sm text-[var(--text-muted)]">
        {project.description ?? ""}
      </p>

      {(project.status || project.priority || project.assignee) && (
        <div className="mt-3 flex flex-wrap gap-2">
          {project.status && (
            <span className="rounded-full bg-[var(--surface-alt)] px-3 py-1 text-xs font-medium text-[var(--text-muted)]">
              Status: {project.status}
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
            {completedCount}/{taskCount} tasks
          </span>
        </div>
        <div className="mt-2 h-2 overflow-hidden rounded-full bg-[var(--surface-alt)]">
          <div
            className={`h-full rounded-full transition-all ${colors.progress}`}
            style={{ width: `${progress}%` }}
          />
        </div>
        {updatedAt && (
          <p className="mt-3 text-xs text-[var(--text-muted)]">
            Updated {new Date(updatedAt).toLocaleString()}
          </p>
        )}
      </div>
    </div>
  );
}

export type ProjectsPageProps = {
  apiEndpoint?: string;
};

export default function ProjectsPage({
  apiEndpoint = "https://api.otter.camp/api/projects",
}: ProjectsPageProps) {
  const navigate = useNavigate();
  const [projects, setProjects] = useState<Project[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Fetch projects from API
  const fetchProjects = useCallback(async () => {
    try {
      if (apiEndpoint === "https://api.otter.camp/api/projects") {
        const data = await api.projects();
        return (data.projects || []) as Project[];
      }

      const response = await fetch(apiEndpoint);
      if (!response.ok) {
        throw new Error("Failed to fetch projects");
      }
      const data = await response.json();
      return (data.projects || data || []) as Project[];
    } catch (err) {
      throw err instanceof Error ? err : new Error("Failed to load projects");
    }
  }, [apiEndpoint]);

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
        }
      } catch (err) {
        if (isDemoMode()) {
          setProjects(SAMPLE_PROJECTS);
        } else {
          setProjects([]);
          setError(err instanceof Error ? err.message : "Failed to load projects");
        }
      } finally {
        setIsLoading(false);
      }
    };

    loadProjects();
  }, [fetchProjects]);

  const handleCreateProject = () => {
    window.alert("Project creation coming soon!");
  };

  // Simple sort by updated date (most recent first)
  const sortedProjects = useMemo(() => {
    return [...projects].sort((a, b) => getUpdatedAtMs(b) - getUpdatedAtMs(a));
  }, [projects]);

  if (isLoading) {
    return (
      <div className="w-full">
        <div className="mb-6 flex items-center justify-between">
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
      <div className="w-full">
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

  if (projects.length === 0) {
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

  return (
    <div className="w-full">
      <div className="mb-6 flex items-center justify-between">
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
          className="inline-flex items-center gap-2 rounded-xl bg-[#C9A86C] px-4 py-2.5 text-sm font-medium text-white shadow-sm transition hover:bg-[#B8975B] focus:outline-none focus:ring-2 focus:ring-[#C9A86C] focus:ring-offset-2 focus:ring-offset-[var(--bg)]"
        >
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          New Project
        </button>
      </div>

      {/* Use Cmd+K (magic bar) to search projects */}
      <div className="mb-4 flex items-center justify-between">
        <p className="text-sm text-[var(--text-muted)]">
          {projects.length} projects • Press <kbd className="rounded bg-[var(--surface-alt)] px-1.5 py-0.5 text-xs">⌘K</kbd> to search
        </p>
      </div>

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
    </div>
  );
}
