import { useEffect, useState } from "react";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const ORG_STORAGE_KEY = "otter-camp-org-id";

type IssueFilterState = "all" | "open" | "closed";
type IssueFilterKind = "all" | "issue" | "pull_request";
type IssueFilterOrigin = "all" | "local" | "github";

type ProjectIssueItem = {
  id: string;
  issue_number: number;
  title: string;
  state: "open" | "closed";
  origin: "local" | "github";
  kind: "issue" | "pull_request";
  owner_agent_id?: string | null;
  last_activity_at: string;
  github_number?: number | null;
  github_url?: string | null;
  github_state?: string | null;
  github_repository_full_name?: string | null;
};

type ProjectIssuesResponse = {
  items: ProjectIssueItem[];
  total: number;
};

type ProjectIssuesListProps = {
  projectId: string;
  selectedIssueID?: string | null;
  onSelectIssue?: (issueID: string) => void;
};

function getOrgID(): string {
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function formatLastActivity(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return "Unknown";
  }
  return date.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function normalizeIssueKindLabel(kind: string): string {
  if (kind === "pull_request") {
    return "PR";
  }
  return "Issue";
}

function normalizeOriginLabel(origin: string): string {
  return origin === "github" ? "GitHub" : "Local";
}

export default function ProjectIssuesList({
  projectId,
  selectedIssueID,
  onSelectIssue,
}: ProjectIssuesListProps) {
  const [items, setItems] = useState<ProjectIssueItem[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [stateFilter, setStateFilter] = useState<IssueFilterState>("open");
  const [kindFilter, setKindFilter] = useState<IssueFilterKind>("all");
  const [originFilter, setOriginFilter] = useState<IssueFilterOrigin>("all");
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const orgID = getOrgID();
    if (!projectId || !orgID) {
      setItems([]);
      setError("Missing project or organization context");
      setIsLoading(false);
      return;
    }

    const url = new URL(`${API_URL}/api/issues`);
    url.searchParams.set("org_id", orgID);
    url.searchParams.set("project_id", projectId);
    url.searchParams.set("limit", "200");
    if (stateFilter !== "all") {
      url.searchParams.set("state", stateFilter);
    }
    if (kindFilter !== "all") {
      url.searchParams.set("kind", kindFilter);
    }
    if (originFilter !== "all") {
      url.searchParams.set("origin", originFilter);
    }

    setIsLoading(true);
    setError(null);

    void fetch(url.toString(), {
      headers: { "Content-Type": "application/json" },
    })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load issues");
        }
        return response.json() as Promise<ProjectIssuesResponse>;
      })
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setItems(Array.isArray(payload.items) ? payload.items : []);
      })
      .catch((fetchError: unknown) => {
        if (cancelled) {
          return;
        }
        setError(
          fetchError instanceof Error ? fetchError.message : "Failed to load issues",
        );
      })
      .finally(() => {
        if (!cancelled) {
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [kindFilter, originFilter, projectId, refreshKey, stateFilter]);

  return (
    <section className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-xs font-semibold text-[var(--text-muted)]" htmlFor="issues-state-filter">
            State
          </label>
          <select
            id="issues-state-filter"
            aria-label="Issue state filter"
            className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)]"
            value={stateFilter}
            onChange={(event) => setStateFilter(event.target.value as IssueFilterState)}
          >
            <option value="all">All</option>
            <option value="open">Open</option>
            <option value="closed">Closed</option>
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs font-semibold text-[var(--text-muted)]" htmlFor="issues-kind-filter">
            Type
          </label>
          <select
            id="issues-kind-filter"
            aria-label="Issue type filter"
            className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)]"
            value={kindFilter}
            onChange={(event) => setKindFilter(event.target.value as IssueFilterKind)}
          >
            <option value="all">All</option>
            <option value="issue">Issues</option>
            <option value="pull_request">PRs</option>
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs font-semibold text-[var(--text-muted)]" htmlFor="issues-origin-filter">
            Origin
          </label>
          <select
            id="issues-origin-filter"
            aria-label="Issue origin filter"
            className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)]"
            value={originFilter}
            onChange={(event) => setOriginFilter(event.target.value as IssueFilterOrigin)}
          >
            <option value="all">All</option>
            <option value="local">Local</option>
            <option value="github">GitHub</option>
          </select>
        </div>
      </div>

      {isLoading && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-6 text-sm text-[var(--text-muted)]">
          Loading issues...
        </div>
      )}

      {!isLoading && error && (
        <div className="rounded-xl border border-red-300 bg-red-50 px-4 py-6 text-sm text-red-700">
          <p>{error}</p>
          <button
            type="button"
            className="mt-3 rounded-lg border border-red-300 bg-white px-3 py-1.5 text-xs font-semibold text-red-700 hover:bg-red-100"
            onClick={() => setRefreshKey((current) => current + 1)}
          >
            Retry
          </button>
        </div>
      )}

      {!isLoading && !error && items.length === 0 && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-6 text-sm text-[var(--text-muted)]">
          No issues found for the selected filters.
        </div>
      )}

      {!isLoading && !error && items.length > 0 && (
        <ul className="space-y-3">
          {items.map((issue) => {
            const selected = selectedIssueID === issue.id;
            return (
              <li key={issue.id}>
                <button
                  type="button"
                  onClick={() => onSelectIssue?.(issue.id)}
                  className={`w-full rounded-xl border px-4 py-3 text-left transition ${
                    selected
                      ? "border-amber-500 bg-amber-50"
                      : "border-[var(--border)] hover:border-amber-300 hover:bg-[var(--surface-alt)]"
                  }`}
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-semibold text-[var(--text)]">
                      #{issue.issue_number} {issue.title}
                    </span>
                    <span className="rounded-full bg-[var(--surface-alt)] px-2 py-0.5 text-[11px] font-semibold text-[var(--text-muted)]">
                      {normalizeIssueKindLabel(issue.kind)}
                    </span>
                    <span className="rounded-full bg-[var(--surface-alt)] px-2 py-0.5 text-[11px] font-semibold text-[var(--text-muted)]">
                      {normalizeOriginLabel(issue.origin)}
                    </span>
                    <span className="rounded-full bg-[var(--surface-alt)] px-2 py-0.5 text-[11px] font-semibold text-[var(--text-muted)]">
                      {issue.state === "open" ? "Open" : "Closed"}
                    </span>
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-4 text-xs text-[var(--text-muted)]">
                    <span>Owner: {issue.owner_agent_id ?? "Unassigned"}</span>
                    <span>Last activity: {formatLastActivity(issue.last_activity_at)}</span>
                    {issue.github_number ? (
                      <span>
                        GitHub #{issue.github_number}
                        {issue.github_url && (
                          <>
                            {" "}
                            <a
                              className="font-semibold text-blue-600 hover:underline"
                              href={issue.github_url}
                              target="_blank"
                              rel="noreferrer"
                              onClick={(event) => event.stopPropagation()}
                            >
                              Open
                            </a>
                          </>
                        )}
                      </span>
                    ) : (
                      <span>GitHub metadata unavailable</span>
                    )}
                  </div>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
