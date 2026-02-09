import { useEffect, useState } from "react";
import { API_URL } from "../../lib/api";
import ReviewDiffUnified from "../review/ReviewDiffUnified";
import type { DiffFile, DiffFileStatus, DiffHunk, DiffLine } from "../review/types";

const ORG_STORAGE_KEY = "otter-camp-org-id";
const EMPTY_BODY_FALLBACK = "No detailed description provided in commit body.";

type ProjectCommitListItem = {
  id: string;
  project_id: string;
  repository_full_name: string;
  branch_name: string;
  sha: string;
  parent_sha?: string | null;
  author_name: string;
  author_email?: string | null;
  authored_at: string;
  subject: string;
  body?: string | null;
  message: string;
  created_at: string;
  updated_at: string;
};

type ProjectCommitListResponse = {
  items: ProjectCommitListItem[];
  has_more: boolean;
  next_offset?: number | null;
  limit: number;
  offset: number;
  total: number;
};

type ProjectCommitDiffFile = {
  path: string;
  change_type: "added" | "modified" | "removed" | "renamed";
  patch?: string | null;
};

type ProjectCommitDiffResponse = {
  sha: string;
  files: ProjectCommitDiffFile[];
  total: number;
};

type ProjectCommitBrowserProps = {
  projectId: string;
};

function getOrgID(): string {
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function shortSHA(sha: string): string {
  return sha.slice(0, 7);
}

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return "Unknown time";
  }
  return date.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function normalizeDiffStatus(changeType: string): DiffFileStatus {
  switch (changeType) {
    case "added":
      return "added";
    case "removed":
      return "deleted";
    case "renamed":
      return "renamed";
    default:
      return "modified";
  }
}

function parsePatchToHunks(patch: string): DiffHunk[] {
  const lines = patch.replace(/\r\n/g, "\n").split("\n");
  const hunks: DiffHunk[] = [];
  let current: DiffHunk | null = null;
  let oldLineNumber = 1;
  let newLineNumber = 1;

  const pushCurrent = () => {
    if (current) {
      hunks.push(current);
      current = null;
    }
  };

  const ensureCurrent = () => {
    if (!current) {
      current = {
        id: `hunk-${hunks.length}`,
        header: "@@",
        lines: [],
      };
    }
  };

  for (let index = 0; index < lines.length; index += 1) {
    const rawLine = lines[index];
    if (rawLine === "" && index === lines.length - 1) {
      continue;
    }

    if (rawLine.startsWith("@@")) {
      pushCurrent();
      const hunkHeader = rawLine.trim();
      const match = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(hunkHeader);
      if (match) {
        oldLineNumber = Number.parseInt(match[1], 10);
        newLineNumber = Number.parseInt(match[2], 10);
      }
      current = {
        id: `hunk-${hunks.length}`,
        header: hunkHeader,
        lines: [],
      };
      continue;
    }

    if (rawLine.startsWith("--- ") || rawLine.startsWith("+++ ")) {
      continue;
    }

    ensureCurrent();

    let type: DiffLine["type"] = "context";
    let content = rawLine;
    let oldNumber: number | undefined = oldLineNumber;
    let newNumber: number | undefined = newLineNumber;

    if (rawLine.startsWith("+")) {
      type = "add";
      content = rawLine.slice(1);
      oldNumber = undefined;
      newNumber = newLineNumber;
      newLineNumber += 1;
    } else if (rawLine.startsWith("-")) {
      type = "del";
      content = rawLine.slice(1);
      oldNumber = oldLineNumber;
      newNumber = undefined;
      oldLineNumber += 1;
    } else {
      if (rawLine.startsWith(" ")) {
        content = rawLine.slice(1);
      }
      oldNumber = oldLineNumber;
      newNumber = newLineNumber;
      oldLineNumber += 1;
      newLineNumber += 1;
    }

    if (!current) {
      continue;
    }

    current.lines.push({
      id: `${current.id}-line-${current.lines.length}`,
      type,
      oldNumber,
      newNumber,
      content,
    });
  }

  pushCurrent();
  return hunks;
}

function buildReviewDiffFile(file: ProjectCommitDiffFile, index: number): DiffFile | null {
  const patch = typeof file.patch === "string" ? file.patch.trim() : "";
  if (patch === "") {
    return null;
  }

  const hunks = parsePatchToHunks(patch);
  let additions = 0;
  let deletions = 0;
  for (const hunk of hunks) {
    for (const line of hunk.lines) {
      if (line.type === "add") {
        additions += 1;
      } else if (line.type === "del") {
        deletions += 1;
      }
    }
  }

  return {
    id: `commit-file-${index}-${file.path}`,
    path: file.path,
    status: normalizeDiffStatus(file.change_type),
    additions,
    deletions,
    hunks,
  };
}

export default function ProjectCommitBrowser({ projectId }: ProjectCommitBrowserProps) {
  const [commits, setCommits] = useState<ProjectCommitListItem[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [expandedSHA, setExpandedSHA] = useState<string | null>(null);

  const [detailBySHA, setDetailBySHA] = useState<Record<string, ProjectCommitListItem>>({});
  const [detailLoadingBySHA, setDetailLoadingBySHA] = useState<Record<string, boolean>>({});
  const [detailErrorBySHA, setDetailErrorBySHA] = useState<Record<string, string | null>>({});

  const [showDiffBySHA, setShowDiffBySHA] = useState<Record<string, boolean>>({});
  const [diffBySHA, setDiffBySHA] = useState<Record<string, ProjectCommitDiffResponse>>({});
  const [diffLoadingBySHA, setDiffLoadingBySHA] = useState<Record<string, boolean>>({});
  const [diffErrorBySHA, setDiffErrorBySHA] = useState<Record<string, string | null>>({});

  const orgID = getOrgID();

  useEffect(() => {
    let cancelled = false;

    async function loadCommits() {
      if (!projectId || !orgID) {
        setCommits([]);
        setError("Missing project or organization context");
        setIsLoading(false);
        return;
      }

      setIsLoading(true);
      setError(null);

      try {
        const url = new URL(`${API_URL}/api/projects/${projectId}/commits`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("limit", "50");
        const response = await fetch(url.toString(), {
          headers: { "Content-Type": "application/json" },
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load commits");
        }
        const payload = (await response.json()) as ProjectCommitListResponse;
        if (!cancelled) {
          setCommits(Array.isArray(payload.items) ? payload.items : []);
        }
      } catch (fetchError) {
        if (!cancelled) {
          setError(fetchError instanceof Error ? fetchError.message : "Failed to load commits");
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    }

    void loadCommits();
    return () => {
      cancelled = true;
    };
  }, [orgID, projectId, refreshKey]);

  async function fetchCommitDetail(sha: string): Promise<void> {
    if (!projectId || !orgID || detailLoadingBySHA[sha]) {
      return;
    }
    setDetailLoadingBySHA((current) => ({ ...current, [sha]: true }));
    setDetailErrorBySHA((current) => ({ ...current, [sha]: null }));

    try {
      const url = new URL(`${API_URL}/api/projects/${projectId}/commits/${sha}`);
      url.searchParams.set("org_id", orgID);
      const response = await fetch(url.toString(), {
        headers: { "Content-Type": "application/json" },
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to load commit detail");
      }
      const payload = (await response.json()) as ProjectCommitListItem;
      setDetailBySHA((current) => ({ ...current, [sha]: payload }));
    } catch (fetchError) {
      setDetailErrorBySHA((current) => ({
        ...current,
        [sha]: fetchError instanceof Error ? fetchError.message : "Failed to load commit detail",
      }));
    } finally {
      setDetailLoadingBySHA((current) => ({ ...current, [sha]: false }));
    }
  }

  async function fetchCommitDiff(sha: string): Promise<void> {
    if (!projectId || !orgID || diffLoadingBySHA[sha]) {
      return;
    }
    setDiffLoadingBySHA((current) => ({ ...current, [sha]: true }));
    setDiffErrorBySHA((current) => ({ ...current, [sha]: null }));

    try {
      const url = new URL(`${API_URL}/api/projects/${projectId}/commits/${sha}/diff`);
      url.searchParams.set("org_id", orgID);
      const response = await fetch(url.toString(), {
        headers: { "Content-Type": "application/json" },
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to load commit diff");
      }
      const payload = (await response.json()) as ProjectCommitDiffResponse;
      setDiffBySHA((current) => ({ ...current, [sha]: payload }));
    } catch (fetchError) {
      setDiffErrorBySHA((current) => ({
        ...current,
        [sha]: fetchError instanceof Error ? fetchError.message : "Failed to load commit diff",
      }));
    } finally {
      setDiffLoadingBySHA((current) => ({ ...current, [sha]: false }));
    }
  }

  function handleToggleExpand(sha: string): void {
    setExpandedSHA((current) => {
      const next = current === sha ? null : sha;
      if (next === sha && !detailBySHA[sha] && !detailLoadingBySHA[sha]) {
        void fetchCommitDetail(sha);
      }
      return next;
    });
  }

  function handleToggleDiff(sha: string): void {
    setShowDiffBySHA((current) => {
      const next = !current[sha];
      if (next && !diffBySHA[sha] && !diffLoadingBySHA[sha]) {
        void fetchCommitDiff(sha);
      }
      return { ...current, [sha]: next };
    });
  }

  return (
    <section className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-[var(--text)]">Code Browser</h2>
          <p className="text-sm text-[var(--text-muted)]">
            Commit-first review flow: list, description, then optional diff.
          </p>
        </div>
        <button
          type="button"
          className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-semibold text-[var(--text)] hover:bg-[var(--surface-alt)]"
          onClick={() => setRefreshKey((current) => current + 1)}
        >
          Refresh
        </button>
      </div>

      {isLoading && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-6 text-sm text-[var(--text-muted)]">
          Loading commits...
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

      {!isLoading && !error && commits.length === 0 && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-6 text-sm text-[var(--text-muted)]">
          No commits available for this project yet.
        </div>
      )}

      {!isLoading && !error && commits.length > 0 && (
        <ul className="space-y-3">
          {commits.map((commit) => {
            const expanded = expandedSHA === commit.sha;
            const commitDetail = detailBySHA[commit.sha] ?? commit;
            const detailLoading = Boolean(detailLoadingBySHA[commit.sha]);
            const detailError = detailErrorBySHA[commit.sha];
            const bodyText = (commitDetail.body ?? "").trim();
            const showDiff = Boolean(showDiffBySHA[commit.sha]);
            const diffLoading = Boolean(diffLoadingBySHA[commit.sha]);
            const diffError = diffErrorBySHA[commit.sha];
            const diffPayload = diffBySHA[commit.sha];

            return (
              <li key={commit.sha} className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)]">
                <button
                  type="button"
                  onClick={() => handleToggleExpand(commit.sha)}
                  data-testid={`commit-expand-${commit.sha}`}
                  className="w-full rounded-xl px-4 py-3 text-left hover:bg-[var(--surface)]"
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs font-semibold text-[var(--text-muted)]">
                      {shortSHA(commit.sha)}
                    </span>
                    <span className="rounded-full bg-[var(--surface)] px-2 py-0.5 text-[11px] font-semibold text-[var(--text-muted)]">
                      {commit.branch_name}
                    </span>
                    <span className="text-sm font-semibold text-[var(--text)]">{commit.subject}</span>
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-3 text-xs text-[var(--text-muted)]">
                    <span>Author: {commit.author_name}</span>
                    <span>Authored: {formatTimestamp(commit.authored_at)}</span>
                  </div>
                </button>

                {expanded && (
                  <div className="border-t border-[var(--border)] px-4 py-4">
                    {detailLoading && (
                      <p className="text-sm text-[var(--text-muted)]">Loading commit description...</p>
                    )}

                    {!detailLoading && detailError && (
                      <div className="rounded-lg border border-red-300 bg-red-50 px-3 py-3 text-sm text-red-700">
                        <p>{detailError}</p>
                        <button
                          type="button"
                          className="mt-2 rounded-md border border-red-300 bg-white px-2 py-1 text-xs font-semibold text-red-700 hover:bg-red-100"
                          onClick={() => void fetchCommitDetail(commit.sha)}
                        >
                          Retry description
                        </button>
                      </div>
                    )}

                    {!detailLoading && !detailError && (
                      <div>
                        <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                          Description
                        </h3>
                        <p className="mt-2 whitespace-pre-wrap rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-3 text-sm leading-relaxed text-[var(--text)]">
                          {bodyText !== "" ? bodyText : EMPTY_BODY_FALLBACK}
                        </p>
                      </div>
                    )}

                    <div className="mt-4">
                      <button
                        type="button"
                        data-testid={`commit-diff-toggle-${commit.sha}`}
                        className="rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-semibold text-[var(--text)] hover:bg-[var(--surface-alt)]"
                        onClick={() => handleToggleDiff(commit.sha)}
                      >
                        {showDiff ? "Hide diff" : "Show diff"}
                      </button>
                    </div>

                    {showDiff && (
                      <div className="mt-4 space-y-4" data-testid={`commit-diff-panel-${commit.sha}`}>
                        {diffLoading && (
                          <p className="text-sm text-[var(--text-muted)]">Loading diff...</p>
                        )}

                        {!diffLoading && diffError && (
                          <div className="rounded-lg border border-red-300 bg-red-50 px-3 py-3 text-sm text-red-700">
                            <p>{diffError}</p>
                            <button
                              type="button"
                              className="mt-2 rounded-md border border-red-300 bg-white px-2 py-1 text-xs font-semibold text-red-700 hover:bg-red-100"
                              onClick={() => void fetchCommitDiff(commit.sha)}
                            >
                              Retry diff
                            </button>
                          </div>
                        )}

                        {!diffLoading && !diffError && diffPayload && diffPayload.files.length === 0 && (
                          <div className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-3 text-sm text-[var(--text-muted)]">
                            Diff payload returned no file changes.
                          </div>
                        )}

                        {!diffLoading &&
                          !diffError &&
                          diffPayload &&
                          diffPayload.files.map((file, index) => {
                            const reviewFile = buildReviewDiffFile(file, index);
                            if (reviewFile) {
                              return <ReviewDiffUnified key={`${commit.sha}-${file.path}`} file={reviewFile} />;
                            }
                            return (
                              <article
                                key={`${commit.sha}-${file.path}`}
                                className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-3"
                              >
                                <div className="flex flex-wrap items-center gap-2">
                                  <h4 className="text-sm font-semibold text-[var(--text)]">{file.path}</h4>
                                  <span className="rounded-full bg-[var(--surface-alt)] px-2 py-0.5 text-[11px] font-semibold text-[var(--text-muted)]">
                                    {file.change_type}
                                  </span>
                                </div>
                                <p className="mt-2 text-xs text-[var(--text-muted)]">
                                  No patch text available for this file.
                                </p>
                              </article>
                            );
                          })}
                      </div>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

export { EMPTY_BODY_FALLBACK };
