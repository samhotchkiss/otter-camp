import { useState, useEffect, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import LoadingSpinner from "../components/LoadingSpinner";
import { ErrorFallback } from "../components/ErrorBoundary";
import { NoProjectsEmpty } from "../components/EmptyState";
import { SkeletonList } from "../components/Skeleton";
import ProjectsListFilters from "../components/ProjectsListFilters";
import { useProjectListFilters } from "../hooks/useProjectListFilters";

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
    name: "Otter Camp",
    description: "Task management for AI-assisted workflows",
    taskCount: 24,
    completedCount: 18,
    color: "sky",
    emoji: "🦦",
    status: "active",
    assignee: "🦦 Scout Otter",
    priority: "high",
    updatedAt: "2026-02-03T18:12:00Z",
  },
  {
    id: "2",
    name: "Pearl Proxy",
    description: "Memory and routing infrastructure",
    taskCount: 12,
    completedCount: 5,
    color: "emerald",
    emoji: "🔮",
    status: "active",
    assignee: "🦦 Builder Otter",
    priority: "urgent",
    updatedAt: "2026-02-04T01:30:00Z",
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
    assignee: "🦦 Lead Otter",
    priority: "medium",
    updatedAt: "2026-01-29T10:00:00Z",
  },
  {
    id: "4",
    name: "Three Stones",
    description: "Educational content and presentations",
    taskCount: 15,
    completedCount: 10,
    color: "violet",
    emoji: "🪨",
    status: "archived",
    assignee: null,
    priority: "low",
    updatedAt: "2025-12-18T15:45:00Z",
  },
];

const colorClasses: Record<string, { bg: string; text: string; progress: string }> = {
  sky: {
    bg: "bg-sky-100 dark:bg-sky-900/30",
    text: "text-sky-700 dark:text-sky-300",
    progress: "bg-sky-500",
  },
  emerald: {
    bg: "bg-emerald-100 dark:bg-emerald-900/30",
    text: "text-emerald-700 dark:text-emerald-300",
    progress: "bg-emerald-500",
  },
  amber: {
    bg: "bg-amber-100 dark:bg-amber-900/30",
    text: "text-amber-700 dark:text-amber-300",
    progress: "bg-amber-500",
  },
  violet: {
    bg: "bg-violet-100 dark:bg-violet-900/30",
    text: "text-violet-700 dark:text-violet-300",
    progress: "bg-violet-500",
  },
};

const formatLabel = (value: string) => {
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
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

const getPriorityRank = (value: string | null | undefined) => {
  if (!value) return 0;
  const normalized = value.trim().toLowerCase();
  if (normalized === "urgent") return 4;
  if (normalized === "high") return 3;
  if (normalized === "medium") return 2;
  if (normalized === "low") return 1;

  const pMatch = normalized.match(/^p([0-3])$/);
  if (pMatch) {
    const score = 4 - Number(pMatch[1]);
    return Number.isFinite(score) ? score : 0;
  }

  return 0;
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
      className="group cursor-pointer rounded-2xl border border-slate-200 bg-white/80 p-5 shadow-sm backdrop-blur transition hover:border-slate-300 hover:shadow-md dark:border-slate-800 dark:bg-slate-900/80 dark:hover:border-slate-700">
      <div className="flex items-start justify-between">
        <div className={`flex h-12 w-12 items-center justify-center rounded-xl text-2xl ${colors.bg}`}>
          {project.emoji}
        </div>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          className="rounded-lg p-2 text-slate-400 opacity-0 transition hover:bg-slate-100 hover:text-slate-600 group-hover:opacity-100 dark:hover:bg-slate-800 dark:hover:text-slate-300"
          aria-label="Project options"
        >
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 5v.01M12 12v.01M12 19v.01M12 6a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2z" />
          </svg>
        </button>
      </div>

      <h3 className="mt-4 text-lg font-semibold text-slate-900 dark:text-white">
        {project.name}
      </h3>
      <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
        {project.description ?? ""}
      </p>

      {(project.status || project.priority || project.assignee) && (
        <div className="mt-3 flex flex-wrap gap-2">
          {project.status && (
            <span className="rounded-full bg-slate-100 px-3 py-1 text-xs font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-200">
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
          <span className="text-slate-600 dark:text-slate-400">Progress</span>
          <span className={`font-medium ${colors.text}`}>
            {completedCount}/{taskCount} tasks
          </span>
        </div>
        <div className="mt-2 h-2 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700">
          <div
            className={`h-full rounded-full transition-all ${colors.progress}`}
            style={{ width: `${progress}%` }}
          />
        </div>
        {updatedAt && (
          <p className="mt-3 text-xs text-slate-500 dark:text-slate-400">
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
  apiEndpoint = "/api/projects",
}: ProjectsPageProps) {
  const navigate = useNavigate();
  const [projects, setProjects] = useState<Project[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error] = useState<string | null>(null);
  const filterState = useProjectListFilters();
  const { filters, debouncedSearch } = filterState;

  // Fetch projects from API
  const fetchProjects = useCallback(async () => {
    try {
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
      try {
        const fetchedProjects = await fetchProjects();
        // If API returns empty, fall back to sample data for demo
        setProjects(fetchedProjects.length > 0 ? fetchedProjects : SAMPLE_PROJECTS);
      } catch {
        // On error, use sample data for demo purposes
        setProjects(SAMPLE_PROJECTS);
      } finally {
        setIsLoading(false);
      }
    };

    loadProjects();
  }, [fetchProjects]);

  const handleCreateProject = () => {
    window.alert("Project creation coming soon!");
  };

  const statusOptions = useMemo(() => {
    const unique = Array.from(
      new Set(projects.map((p) => p.status).filter(Boolean) as string[])
    );
    unique.sort((a, b) => a.localeCompare(b));
    return unique.map((value) => ({ value, label: formatLabel(value) }));
  }, [projects]);

  const assigneeOptions = useMemo(() => {
    const unique = Array.from(
      new Set(projects.map((p) => p.assignee).filter(Boolean) as string[])
    );
    unique.sort((a, b) => a.localeCompare(b));
    return unique.map((value) => ({ value, label: value }));
  }, [projects]);

  const priorityOptions = useMemo(() => {
    const unique = Array.from(
      new Set(projects.map((p) => p.priority).filter(Boolean) as string[])
    );

    unique.sort((a, b) => {
      const rankA = getPriorityRank(a);
      const rankB = getPriorityRank(b);
      if (rankA !== rankB) return rankB - rankA;
      return a.localeCompare(b);
    });

    return unique.map((value) => ({ value, label: formatLabel(value) }));
  }, [projects]);

  const filteredProjects = useMemo(() => {
    const trimmedQuery = debouncedSearch.trim().toLowerCase();
    const terms = trimmedQuery ? trimmedQuery.split(/\\s+/).filter(Boolean) : [];

    const matchesSearch = (project: Project) => {
      if (terms.length === 0) return true;
      const haystack = `${project.name ?? ""} ${project.description ?? ""}`.toLowerCase();
      return terms.every((term) => haystack.includes(term));
    };

    const matchesStatus = (project: Project) => {
      if (!filters.status) return true;
      return (project.status ?? null) === filters.status;
    };

    const matchesAssignee = (project: Project) => {
      if (!filters.assignee) return true;
      return (project.assignee ?? null) === filters.assignee;
    };

    const matchesPriority = (project: Project) => {
      if (!filters.priority) return true;
      return (project.priority ?? null) === filters.priority;
    };

    const next = projects.filter(
      (project) =>
        matchesSearch(project) &&
        matchesStatus(project) &&
        matchesAssignee(project) &&
        matchesPriority(project)
    );

    const sorted = [...next].sort((a, b) => {
      if (filters.sort === "name") {
        const cmp = a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
        if (cmp !== 0) return cmp;
        return getUpdatedAtMs(b) - getUpdatedAtMs(a);
      }

      if (filters.sort === "priority") {
        const rankDiff = getPriorityRank(b.priority) - getPriorityRank(a.priority);
        if (rankDiff !== 0) return rankDiff;
        const updatedDiff = getUpdatedAtMs(b) - getUpdatedAtMs(a);
        if (updatedDiff !== 0) return updatedDiff;
        return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
      }

      // updated
      const updatedDiff = getUpdatedAtMs(b) - getUpdatedAtMs(a);
      if (updatedDiff !== 0) return updatedDiff;
      return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    });

    return sorted;
  }, [projects, filters.status, filters.assignee, filters.priority, filters.sort, debouncedSearch]);

  if (isLoading) {
    return (
      <div className="w-full">
        <div className="mb-6 flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold text-slate-900 dark:text-white">
              Projects
            </h1>
            <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
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
      <div className="mx-auto max-w-6xl">
        <div className="mb-6">
          <h1 className="text-2xl font-semibold text-slate-900 dark:text-white">
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
      <div className="mx-auto max-w-6xl">
        <div className="mb-6">
          <h1 className="text-2xl font-semibold text-slate-900 dark:text-white">
            Projects
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Manage your workspaces and track progress
          </p>
        </div>
        <NoProjectsEmpty onCreate={handleCreateProject} />
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-6xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-900 dark:text-white">
            Projects
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Manage your workspaces and track progress
          </p>
        </div>
        <button
          type="button"
          onClick={handleCreateProject}
          className="inline-flex items-center gap-2 rounded-xl bg-emerald-600 px-4 py-2.5 text-sm font-medium text-white shadow-sm transition hover:bg-emerald-700 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 dark:focus:ring-offset-slate-900"
        >
          <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          New Project
        </button>
      </div>

      <div className="mb-6 rounded-2xl border border-slate-200 bg-white/80 p-4 backdrop-blur-sm dark:border-slate-800 dark:bg-slate-900/80 sm:p-6">
        <ProjectsListFilters
          statusOptions={statusOptions}
          assigneeOptions={assigneeOptions}
          priorityOptions={priorityOptions}
          filterState={filterState}
        />
      </div>

      <div className="mb-4 flex items-center justify-between">
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Showing {filteredProjects.length} of {projects.length} projects
        </p>
      </div>

      {filteredProjects.length === 0 ? (
        <div className="rounded-2xl border border-slate-200 bg-white/80 p-10 text-center backdrop-blur-sm dark:border-slate-800 dark:bg-slate-900/80">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
            No projects match your filters
          </h2>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Try adjusting your search or clearing filters.
          </p>
          {filterState.hasActiveFilters && (
            <button
              type="button"
              onClick={filterState.clearAllFilters}
              className="mt-4 inline-flex items-center justify-center rounded-xl bg-slate-900 px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-slate-400 focus:ring-offset-2 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-slate-200 dark:focus:ring-offset-slate-900"
            >
              Clear filters
            </button>
          )}
        </div>
      ) : (
        <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {filteredProjects.map((project) => (
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
            className="flex min-h-[200px] flex-col items-center justify-center rounded-2xl border-2 border-dashed border-slate-300 bg-white/50 p-5 text-slate-500 transition hover:border-emerald-400 hover:bg-emerald-50/50 hover:text-emerald-600 dark:border-slate-700 dark:bg-slate-900/50 dark:hover:border-emerald-600 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400"
          >
            <svg className="h-10 w-10" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 4v16m8-8H4" />
            </svg>
            <span className="mt-3 text-sm font-medium">Create a new project</span>
          </button>
        </div>
      )}
    </div>
  );
}
