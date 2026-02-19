import { useEffect, useState } from "react";
import { NavLink, useParams } from "react-router-dom";

import api, { apiFetch } from "../lib/api";
import {
  mapProjectIssuesPayloadToCoreDetailIssues,
  mapProjectPayloadToCoreDetailProject,
  type CoreProjectDetailIssue,
  type CoreProjectDetailProject,
} from "../lib/coreDataAdapters";

type DetailActivity = {
  user: string;
  action: string;
  target?: string;
  time: string;
};

type ExplorerFile = {
  name: string;
  path: string;
  kind: "markdown" | "code";
};

type RecentActivityPayload = {
  items?: Array<{
    agent_id?: string;
    trigger?: string;
    summary?: string;
    issue_number?: number;
    issue_id?: string;
    created_at?: string;
    project_id?: string;
  }>;
};

type ProjectTreePayload = {
  entries?: Array<{
    name?: string;
    path?: string;
    type?: string;
  }>;
};

type ProjectCommitListPayload = {
  total?: number;
  items?: Array<{
    author_name?: string;
  }>;
};

type ProjectBranchesPayload = {
  branches?: unknown[];
};

function issueStatusClass(status: CoreProjectDetailIssue["status"]): string {
  if (status === "approval-needed") {
    return "text-rose-400 bg-rose-500/10";
  }
  if (status === "in-progress") {
    return "text-amber-400 bg-amber-500/10";
  }
  if (status === "blocked") {
    return "text-orange-400 bg-orange-500/10";
  }
  if (status === "review") {
    return "text-lime-400 bg-lime-500/10";
  }
  return "text-stone-400 bg-stone-500/10";
}

function issuePriorityDot(priority: CoreProjectDetailIssue["priority"]): string {
  if (priority === "critical") {
    return "bg-rose-500";
  }
  if (priority === "high") {
    return "bg-orange-500";
  }
  if (priority === "medium") {
    return "bg-amber-500";
  }
  return "bg-stone-600";
}

function explorerLink(path: string): string {
  return `/review/${encodeURIComponent(path)}`;
}

function normalizeErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "Failed to load project details";
}

function toRelativeTimestamp(input: string, now = new Date()): string {
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

function readOrgID(): string {
  try {
    return (localStorage.getItem("otter-camp-org-id") ?? "").trim();
  } catch {
    return "";
  }
}

function mapRecentActivityRows(payload: RecentActivityPayload, projectID: string): DetailActivity[] {
  const rows = Array.isArray(payload.items) ? payload.items : [];
  return rows
    .filter((row) => (row.project_id ?? "").trim() === projectID)
    .slice(0, 6)
    .map<DetailActivity>((row) => {
      const issueLabel =
        typeof row.issue_number === "number" && row.issue_number > 0
          ? `ISS-${Math.floor(row.issue_number)}`
          : (row.issue_id ?? "").trim();
      return {
        user: (row.agent_id ?? "").trim() || "System",
        action: (row.summary ?? "").trim() || (row.trigger ?? "").trim().replace(/[._]/g, " ") || "recorded activity",
        target: issueLabel || undefined,
        time: toRelativeTimestamp((row.created_at ?? "").trim()),
      };
    });
}

function mapExplorerFiles(payload: ProjectTreePayload): ExplorerFile[] {
  const rows = Array.isArray(payload.entries) ? payload.entries : [];
  return rows
    .filter((row) => (row.type ?? "").trim() === "file")
    .slice(0, 8)
    .map<ExplorerFile>((row) => {
      const path = (row.path ?? "").trim() || (row.name ?? "").trim();
      const name = (row.name ?? "").trim() || path.split("/").pop() || "file";
      const lower = name.toLowerCase();
      const kind = lower.endsWith(".md") || lower.endsWith(".markdown") ? "markdown" : "code";
      return { name, path, kind };
    })
    .filter((row) => row.path !== "");
}

export default function ProjectDetailPage() {
  const { id: projectID = "" } = useParams<{ id?: string }>();
  const [project, setProject] = useState<CoreProjectDetailProject | null>(null);
  const [issues, setIssues] = useState<CoreProjectDetailIssue[]>([]);
  const [recentActivity, setRecentActivity] = useState<DetailActivity[]>([]);
  const [explorerFiles, setExplorerFiles] = useState<ExplorerFile[]>([]);
  const [commitCount, setCommitCount] = useState<number | null>(null);
  const [contributorCount, setContributorCount] = useState<number | null>(null);
  const [branchCount, setBranchCount] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    if (!projectID) {
      setProject(null);
      setIssues([]);
      setLoadError("Missing project identifier");
      setLoading(false);
      return;
    }

    setLoading(true);
    setLoadError(null);

    void Promise.all([
      api.project(projectID),
      api.issues({ projectID, state: "open", limit: 200 }),
    ])
      .then(([projectPayload, issuesPayload]) => {
        if (cancelled) {
          return;
        }

        const mappedIssues = mapProjectIssuesPayloadToCoreDetailIssues(issuesPayload);
        setIssues(mappedIssues);
        setProject(mapProjectPayloadToCoreDetailProject(projectPayload, mappedIssues.length));
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        setProject(null);
        setIssues([]);
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
  }, [projectID, refreshKey]);

  useEffect(() => {
    let cancelled = false;
    if (!projectID) {
      setRecentActivity([]);
      setExplorerFiles([]);
      setCommitCount(null);
      setContributorCount(null);
      setBranchCount(null);
      return;
    }

    const orgID = readOrgID();
    if (!orgID) {
      setRecentActivity([]);
      setExplorerFiles([]);
      setCommitCount(null);
      setContributorCount(null);
      setBranchCount(null);
      return;
    }

    void Promise.allSettled([
      api.activityRecent(80) as Promise<RecentActivityPayload>,
      apiFetch<ProjectTreePayload>(
        `/api/projects/${encodeURIComponent(projectID)}/tree?org_id=${encodeURIComponent(orgID)}&path=%2F`,
      ),
      apiFetch<ProjectCommitListPayload>(
        `/api/projects/${encodeURIComponent(projectID)}/commits?org_id=${encodeURIComponent(orgID)}&limit=100`,
      ),
      apiFetch<ProjectBranchesPayload>(`/api/projects/${encodeURIComponent(projectID)}/repo/branches?org_id=${encodeURIComponent(orgID)}`),
    ]).then(([activityResult, treeResult, commitsResult, branchesResult]) => {
      if (cancelled) {
        return;
      }

      if (activityResult.status === "fulfilled") {
        const mappedActivity = mapRecentActivityRows(activityResult.value, projectID);
        setRecentActivity(mappedActivity);
      } else {
        setRecentActivity([]);
      }

      if (treeResult.status === "fulfilled") {
        const mappedFiles = mapExplorerFiles(treeResult.value);
        setExplorerFiles(mappedFiles);
      } else {
        setExplorerFiles([]);
      }

      if (commitsResult.status === "fulfilled") {
        const payload = commitsResult.value;
        const total = typeof payload.total === "number" && Number.isFinite(payload.total) ? Math.max(0, Math.floor(payload.total)) : 0;
        setCommitCount(total);
        const uniqueAuthors = new Set(
          (payload.items || [])
            .map((entry) => (entry.author_name ?? "").trim())
            .filter((name) => name !== ""),
        );
        setContributorCount(uniqueAuthors.size);
      } else {
        setCommitCount(null);
        setContributorCount(null);
      }

      if (branchesResult.status === "fulfilled") {
        setBranchCount(Array.isArray(branchesResult.value.branches) ? branchesResult.value.branches.length : 0);
      } else {
        setBranchCount(null);
      }
    });

    return () => {
      cancelled = true;
    };
  }, [projectID, refreshKey]);

  const projectName = project?.name || "Project";
  const projectDescription = project?.description || "No description provided.";
  const projectRepo = project?.repo || `local/${projectID || "project"}`;
  const projectStats = project?.stats || {
    openIssues: 0,
    branches: 0,
    commits: 0,
    contributors: 0,
  };
  const resolvedStats = {
    openIssues: issues.length > 0 ? issues.length : projectStats.openIssues,
    branches: branchCount ?? projectStats.branches,
    commits: commitCount ?? projectStats.commits,
    contributors: contributorCount ?? projectStats.contributors,
  };
  const projectLastSync = project?.lastSync || "Unknown";
  const showRepoLink = projectRepo.startsWith("github.com/") || projectRepo.startsWith("git@github.com:");

  return (
    <div className="min-w-0 space-y-4 md:space-y-6">
      <section className="rounded-lg border border-stone-800 bg-stone-900 p-4 md:p-6" data-testid="project-detail-shell">
        <div className="mb-4 flex flex-col items-start justify-between gap-4 sm:flex-row">
          <div className="flex items-start gap-3 md:gap-4">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-amber-600 to-lime-600 text-white shadow-lg shadow-amber-600/20 md:h-12 md:w-12">
              <>⇄</>
            </div>
            <div>
              <h1 className="text-xl font-bold text-stone-100 md:text-2xl">{projectName}</h1>
              <p className="mt-1 text-xs text-stone-400 md:text-sm">{projectDescription}</p>
            </div>
          </div>
          <div className="flex items-center gap-2 self-end sm:self-auto">
            <button type="button" className="rounded-md p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200" aria-label="Star project">
              ☆
            </button>
            <button type="button" className="rounded-md p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200" aria-label="Project settings">
              ⚙
            </button>
          </div>
        </div>

        <div className="flex flex-col gap-2 text-xs md:text-sm sm:flex-row sm:items-center sm:gap-4">
          {showRepoLink ? (
            <a
              href={`https://${projectRepo.replace(/^git@github\.com:/i, "github.com/")}`}
              target="_blank"
              rel="noreferrer"
              className="break-all font-mono text-stone-400 transition-colors hover:text-amber-400"
            >
              {projectRepo}
            </a>
          ) : (
            <span className="break-all font-mono text-stone-400">{projectRepo}</span>
          )}
          <span className="hidden text-stone-600 sm:inline">•</span>
          <p className="flex items-center gap-1.5 text-stone-400">
            <span className="h-2 w-2 animate-pulse rounded-full bg-lime-500" />
            <span>Synced {projectLastSync}</span>
          </p>
        </div>

        <div className="mt-4 grid grid-cols-2 gap-3 md:mt-6 md:grid-cols-4 md:gap-4">
          <div className="rounded-lg border border-stone-800 bg-stone-950 p-3 md:p-4">
            <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-rose-400 md:text-xs">Issues</p>
            <p className="text-xl font-bold text-stone-100 md:text-2xl">{resolvedStats.openIssues}</p>
          </div>
          <div className="rounded-lg border border-stone-800 bg-stone-950 p-3 md:p-4">
            <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-amber-400 md:text-xs">Branches</p>
            <p className="text-xl font-bold text-stone-100 md:text-2xl">{resolvedStats.branches}</p>
          </div>
          <div className="rounded-lg border border-stone-800 bg-stone-950 p-3 md:p-4">
            <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-stone-400 md:text-xs">Commits</p>
            <p className="text-xl font-bold text-stone-100 md:text-2xl">{resolvedStats.commits}</p>
          </div>
          <div className="rounded-lg border border-stone-800 bg-stone-950 p-3 md:p-4">
            <p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-stone-400 md:text-xs">Contributors</p>
            <p className="text-xl font-bold text-stone-100 md:text-2xl">{resolvedStats.contributors}</p>
          </div>
        </div>
      </section>

      <div className="grid grid-cols-1 gap-4 md:gap-6 lg:grid-cols-3">
        <section className="space-y-4 md:space-y-6 lg:col-span-2">
          <section className="rounded-lg border border-stone-800 bg-stone-900">
            <div className="flex items-center justify-between border-b border-stone-800 px-4 py-3 md:px-6 md:py-4">
              <h2 className="text-sm font-semibold text-stone-100 md:text-base">Open Issues</h2>
              <button type="button" className="text-xs font-medium text-amber-400 hover:text-amber-300">
                View All
              </button>
            </div>

            <div className="divide-y divide-stone-800">
              {loading ? (
                <div className="px-4 py-4 text-sm text-stone-400 md:px-6">Loading issues...</div>
              ) : null}

              {!loading && loadError ? (
                <div className="space-y-3 px-4 py-4 md:px-6">
                  <p className="text-sm text-rose-400">{loadError}</p>
                  <button
                    type="button"
                    onClick={() => setRefreshKey((current) => current + 1)}
                    className="rounded border border-rose-500/40 bg-rose-500/10 px-3 py-1.5 text-xs font-semibold text-rose-300 transition-colors hover:bg-rose-500/20"
                  >
                    Retry
                  </button>
                </div>
              ) : null}

              {!loading && !loadError && issues.length === 0 ? (
                <div className="px-4 py-4 text-sm text-stone-400 md:px-6">No open issues.</div>
              ) : null}

              {!loading && !loadError
                ? issues.map((issue) => (
                    <NavLink
                      key={issue.id}
                      to={`/issue/${encodeURIComponent(issue.id)}`}
                      className="group block px-4 py-3 transition-colors hover:bg-stone-800/50 md:px-6 md:py-4"
                    >
                      <div className="flex items-start gap-2 md:gap-3">
                        <span className={`mt-2 h-1.5 w-1.5 shrink-0 rounded-full ${issuePriorityDot(issue.priority)}`} />
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
                            <div className="min-w-0 flex-1">
                              <h3 className="mb-1 text-sm font-medium text-stone-200 group-hover:text-stone-100 md:text-base">{issue.title}</h3>
                              <div className="flex flex-wrap items-center gap-2">
                                <span className="font-mono text-xs text-stone-500">{issue.id}</span>
                                <span className={`rounded-full px-2 py-0.5 text-[10px] md:text-xs ${issueStatusClass(issue.status)}`}>
                                  {issue.status.replace("-", " ")}
                                </span>
                                {issue.assignee ? <span className="text-xs text-stone-500">{issue.assignee}</span> : null}
                              </div>
                            </div>
                            <span className="whitespace-nowrap text-xs text-stone-600">{issue.created}</span>
                          </div>
                        </div>
                      </div>
                    </NavLink>
                  ))
                : null}
            </div>
          </section>
        </section>

        <aside className="space-y-4 md:space-y-6 lg:col-span-1" data-testid="project-detail-right-rail">
          <section className="rounded-lg border border-stone-800 bg-stone-900">
            <div className="border-b border-stone-800 px-4 py-3 md:px-6 md:py-4">
              <h2 className="text-sm font-semibold text-stone-100 md:text-base">Recent Activity</h2>
            </div>
            <div className="p-4 md:p-6">
              <div className="space-y-4">
                {recentActivity.map((activity, index) => (
                  <div key={`${activity.user}-${activity.target || "event"}-${index}`} className="flex gap-3">
                    <div className="relative">
                      <div className="flex h-6 w-6 items-center justify-center rounded-full bg-stone-700 text-[10px] font-mono text-stone-300">
                        {activity.user === "You" ? "U" : "A"}
                      </div>
                      {index < recentActivity.length - 1 ? (
                        <div className="absolute left-3 top-6 h-8 w-px bg-stone-800" aria-hidden="true" />
                      ) : null}
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="text-xs text-stone-300 md:text-sm">
                        <span className="font-medium text-stone-200">{activity.user}</span> {activity.action}{" "}
                        {activity.target ? <span className="break-all font-mono text-amber-400">{activity.target}</span> : null}
                      </p>
                      <p className="mt-0.5 text-xs text-stone-600">{activity.time}</p>
                    </div>
                  </div>
                ))}
                {recentActivity.length === 0 ? <p className="text-xs text-stone-500 md:text-sm">No recent activity yet.</p> : null}
              </div>
            </div>
          </section>

          <section className="h-96" data-testid="project-detail-file-explorer">
            <div className="flex h-full flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-900">
              <div className="shrink-0 border-b border-stone-800 px-4 py-3">
                <div className="mb-3 flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-stone-200">Files</h3>
                  <span className="text-xs text-stone-500">main</span>
                </div>
                <input
                  type="text"
                  className="w-full rounded-md border border-stone-800 bg-stone-950 px-3 py-1.5 text-xs text-stone-300 placeholder:text-stone-600 focus:outline-none"
                  placeholder="Search files..."
                  readOnly
                  value=""
                />
              </div>
              <div className="flex-1 space-y-1 overflow-y-auto p-2">
                {explorerFiles.map((file) => {
                  if (file.kind === "markdown") {
                    return (
                      <NavLink
                        key={file.path}
                        to={explorerLink(file.path)}
                        className="flex items-center justify-between rounded-md px-3 py-1.5 text-sm text-stone-300 transition-colors hover:bg-stone-800"
                      >
                        <span>{file.name}</span>
                        <span className="text-[10px] text-amber-400">Review</span>
                      </NavLink>
                    );
                  }

                  return (
                    <div key={file.path} className="flex items-center rounded-md px-3 py-1.5 text-sm text-stone-400">
                      {file.name}
                    </div>
                  );
                })}
                {explorerFiles.length === 0 ? <p className="px-3 py-1.5 text-xs text-stone-500">No files indexed yet.</p> : null}
              </div>
            </div>
          </section>
        </aside>
      </div>
    </div>
  );
}
