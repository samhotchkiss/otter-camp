import { useEffect, useMemo, useState } from "react";
import { NavLink } from "react-router-dom";

import api from "../lib/api";
import { mapProjectsPayloadToCoreCards, type CoreProjectCard } from "../lib/coreDataAdapters";

type ProjectActivity = {
  id: string;
  title: string;
  status: "in-progress" | "needs-approval" | "todo";
  priority: "critical" | "high" | "medium" | "low";
  assignee: string;
  project: string;
};

function activityStatusClass(status: ProjectActivity["status"]): string {
  if (status === "in-progress") {
    return "bg-amber-500/10 text-amber-400 border-amber-500/20";
  }
  if (status === "needs-approval") {
    return "bg-orange-500/10 text-orange-400 border-orange-500/20";
  }
  return "bg-stone-800 text-stone-400 border-stone-700";
}

function activityPriorityDot(priority: ProjectActivity["priority"]): string {
  if (priority === "critical") {
    return "bg-rose-500";
  }
  if (priority === "high") {
    return "bg-orange-500";
  }
  if (priority === "medium") {
    return "bg-amber-500";
  }
  return "bg-stone-500";
}

function normalizeErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "Failed to load projects";
}

function buildProjectActivity(projects: CoreProjectCard[]): ProjectActivity[] {
  return projects.map((project) => {
    if (project.needsApproval > 0) {
      return {
        id: `${project.id}-review`,
        title: `${project.needsApproval} item(s) awaiting review`,
        status: "needs-approval",
        priority: project.needsApproval > 2 ? "critical" : "high",
        assignee: "Review queue",
        project: project.name,
      };
    }

    if (project.inProgress > 0) {
      return {
        id: `${project.id}-active`,
        title: `${project.inProgress} issue(s) in progress`,
        status: "in-progress",
        priority: project.inProgress > 4 ? "high" : "medium",
        assignee: "Active agents",
        project: project.name,
      };
    }

    return {
      id: `${project.id}-todo`,
      title: `${project.openIssues} open issue(s) queued`,
      status: "todo",
      priority: "low",
      assignee: "Unassigned",
      project: project.name,
    };
  });
}

export default function ProjectsPage() {
  const [projects, setProjects] = useState<CoreProjectCard[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError(null);

    void api
      .projects()
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setProjects(mapProjectsPayloadToCoreCards(payload));
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        setProjects([]);
        setLoadError(normalizeErrorMessage(error));
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const activityRows = useMemo(() => buildProjectActivity(projects), [projects]);

  return (
    <div className="min-w-0 space-y-6">
      <header className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-semibold text-stone-100">Projects</h2>
          <p className="text-sm text-stone-500">Git-backed repositories & tracking</p>
        </div>
        <button
          type="button"
          className="flex items-center gap-2 rounded-md bg-amber-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-amber-500"
        >
          <span aria-hidden="true">+</span>
          <span>New Project</span>
        </button>
      </header>

      <section
        className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3"
        aria-label="Projects grid"
      >
        {loading ? (
          <div className="rounded-lg border border-stone-800 bg-stone-900 p-5 text-sm text-stone-400">
            Loading projects...
          </div>
        ) : null}

        {!loading && loadError ? (
          <div className="space-y-3 rounded-lg border border-rose-500/40 bg-rose-500/10 p-5">
            <p className="text-sm text-rose-300">{loadError}</p>
            <button
              type="button"
              onClick={() => setRefreshKey((current) => current + 1)}
              className="rounded border border-rose-400/40 px-3 py-1.5 text-xs font-semibold text-rose-200 transition-colors hover:bg-rose-500/10"
            >
              Retry
            </button>
          </div>
        ) : null}

        {!loading && !loadError && projects.length === 0 ? (
          <div className="rounded-lg border border-stone-800 bg-stone-900 p-5 text-sm text-stone-400">
            No projects found.
          </div>
        ) : null}

        {!loading && !loadError
          ? projects.map((project) => (
              <NavLink
                key={project.id}
                to={`/projects/${encodeURIComponent(project.id)}`}
                className="group rounded-lg border border-stone-800 bg-stone-900 p-5 transition-all hover:border-amber-500/50 hover:shadow-[0_0_20px_rgba(245,158,11,0.1)]"
                data-testid={`project-card-${project.id}`}
                aria-label={project.name}
              >
                <div className="mb-4 flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <h3 className="truncate font-semibold text-stone-200">{project.name}</h3>
                    <p className="mt-1 truncate font-mono text-xs text-stone-500">{project.repo}</p>
                  </div>
                  {project.githubSync ? (
                    <span className="rounded border border-lime-500/20 bg-lime-500/10 px-2 py-0.5 text-[10px] text-lime-400">
                      Synced
                    </span>
                  ) : (
                    <span className="rounded border border-stone-700 bg-stone-800 px-2 py-0.5 text-[10px] text-stone-400">
                      Local
                    </span>
                  )}
                </div>

                <div className="mb-4 grid grid-cols-3 gap-2">
                  <div className="rounded border border-stone-800/50 bg-stone-950/50 p-2">
                    <p className="text-lg font-bold text-stone-200">{project.openIssues}</p>
                    <p className="text-[10px] uppercase tracking-wide text-stone-500">Open</p>
                  </div>
                  <div className="rounded border border-stone-800/50 bg-stone-950/50 p-2">
                    <p className="text-lg font-bold text-amber-400">{project.inProgress}</p>
                    <p className="text-[10px] uppercase tracking-wide text-stone-500">Active</p>
                  </div>
                  <div className="rounded border border-stone-800/50 bg-stone-950/50 p-2">
                    <p className="text-lg font-bold text-orange-400">{project.needsApproval}</p>
                    <p className="text-[10px] uppercase tracking-wide text-stone-500">Review</p>
                  </div>
                </div>

                <div className="flex items-center justify-between border-t border-stone-800 pt-4">
                  <div className="flex flex-wrap gap-2">
                    {project.techStack.map((tech) => (
                      <span key={tech} className="rounded bg-stone-800 px-1.5 py-0.5 text-[10px] text-stone-400">
                        {tech}
                      </span>
                    ))}
                  </div>
                  <span className="text-stone-500 transition-colors group-hover:text-amber-400" aria-hidden="true">
                    ↗
                  </span>
                </div>
              </NavLink>
            ))
          : null}
      </section>

      <section className="overflow-hidden rounded-lg border border-stone-800 bg-stone-900" aria-label="Recent activity">
        <div className="flex items-center justify-between border-b border-stone-800 bg-stone-900/50 px-5 py-4">
          <h3 className="text-sm font-semibold text-stone-200">Recent Activity</h3>
          <button type="button" className="text-xs text-amber-400 hover:text-amber-300" aria-label="View all recent activity">
            View All
          </button>
        </div>

        <div className="divide-y divide-stone-800/50">
          {!loading && !loadError && activityRows.length === 0 ? (
            <div className="px-5 py-4 text-xs text-stone-500">No recent activity.</div>
          ) : null}

          {!loading && !loadError
            ? activityRows.map((activity) => (
                <div key={activity.id} className="group flex items-center justify-between gap-4 px-5 py-3 hover:bg-stone-800/50">
                  <div className="min-w-0 flex flex-1 items-center gap-4">
                    <span className={`h-2 w-2 shrink-0 rounded-full ${activityPriorityDot(activity.priority)}`} />
                    <div className="min-w-0 flex-1">
                      <div className="mb-0.5 flex min-w-0 items-center gap-2">
                        <p className="truncate text-sm font-medium text-stone-200 transition-colors group-hover:text-amber-400">
                          {activity.title}
                        </p>
                      </div>
                      <p className="text-xs text-stone-500">
                        {activity.project} • {activity.assignee}
                      </p>
                    </div>
                  </div>
                  <span className={`shrink-0 rounded border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${activityStatusClass(activity.status)}`}>
                    {activity.status.replace("-", " ")}
                  </span>
                </div>
              ))
            : null}
        </div>
      </section>
    </div>
  );
}
