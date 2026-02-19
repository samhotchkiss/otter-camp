import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import IssueThreadPanel from "../components/project/IssueThreadPanel";
import { API_URL } from "../lib/api";

const ORG_STORAGE_KEY = "otter-camp-org-id";

type ApprovalAction = "needs_changes" | "approved";

type IssueHeaderPayload = {
  issue?: {
    title?: string;
    body?: string | null;
    issue_number?: number;
    project_id?: string;
    approval_state?: string;
  };
};

function getOrgID(): string {
  return (window.localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
}

function normalizeApprovalLabel(state: string | null): string {
  switch (state) {
    case "ready_for_review":
      return "Ready for Review";
    case "needs_changes":
      return "Needs Changes";
    case "approved":
      return "Approved";
    default:
      return "Draft";
  }
}

export default function IssueDetailPage() {
  const { id: projectId, issueId } = useParams<{ id?: string; issueId?: string }>();
  const resolvedIssueId = (issueId ?? "").trim();
  const resolvedProjectId = (projectId ?? "").trim();
  const [pendingAction, setPendingAction] = useState<ApprovalAction | null>(null);
  const [approvalStatus, setApprovalStatus] = useState<string | null>(null);
  const [approvalError, setApprovalError] = useState<string | null>(null);
  const [issueLoading, setIssueLoading] = useState(true);
  const [issueLoadError, setIssueLoadError] = useState<string | null>(null);
  const [issueTitle, setIssueTitle] = useState<string>("");
  const [issueSummary, setIssueSummary] = useState<string>(
    "Dedicated issue workflow surface with approval, discussion, and review context.",
  );
  const [issueNumber, setIssueNumber] = useState<number | null>(null);
  const [issueProjectScope, setIssueProjectScope] = useState<string>("");
  const [issueApprovalState, setIssueApprovalState] = useState<string | null>(null);
  const [issueRefreshKey, setIssueRefreshKey] = useState(0);

  const orgID = getOrgID();
  const orgMissing = orgID === "";

  useEffect(() => {
    if (!resolvedIssueId) {
      setIssueLoading(false);
      return;
    }

    let cancelled = false;
    setIssueLoading(true);
    setIssueLoadError(null);

    const url = new URL(`${API_URL}/api/issues/${encodeURIComponent(resolvedIssueId)}`);
    if (orgID) {
      url.searchParams.set("org_id", orgID);
    }

    void fetch(url.toString(), { headers: { "Content-Type": "application/json" } })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load issue details");
        }
        return response.json() as Promise<IssueHeaderPayload>;
      })
      .then((payload) => {
        if (cancelled) {
          return;
        }
        const issue = payload.issue;
        const title = typeof issue?.title === "string" ? issue.title.trim() : "";
        const body = typeof issue?.body === "string" ? issue.body.trim() : "";
        const number = typeof issue?.issue_number === "number" && Number.isFinite(issue.issue_number)
          ? issue.issue_number
          : null;
        const scope = typeof issue?.project_id === "string" ? issue.project_id.trim() : "";
        const approvalState = typeof issue?.approval_state === "string" ? issue.approval_state.trim() : "";

        setIssueTitle(title);
        setIssueSummary(body || "Dedicated issue workflow surface with approval, discussion, and review context.");
        setIssueNumber(number);
        setIssueProjectScope(scope);
        setIssueApprovalState(approvalState || null);
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        setIssueLoadError(error instanceof Error ? error.message : "Failed to load issue details");
      })
      .finally(() => {
        if (!cancelled) {
          setIssueLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [orgID, resolvedIssueId, issueRefreshKey]);

  if (!resolvedIssueId) {
    return (
      <div data-testid="issue-detail-shell" className="mx-auto w-full max-w-[1280px]">
        <div className="rounded-2xl border border-red-500/40 bg-red-500/10 p-6 text-sm text-red-300">
          Missing issue id.
        </div>
      </div>
    );
  }

  async function handleApprovalAction(action: ApprovalAction): Promise<void> {
    if (orgMissing) {
      setApprovalError("Set an organization to use approval actions.");
      return;
    }

    setPendingAction(action);
    setApprovalError(null);
    setApprovalStatus(null);
    try {
      const endpoint = action === "approved"
        ? `${API_URL}/api/issues/${encodeURIComponent(resolvedIssueId)}/approve?org_id=${encodeURIComponent(orgID)}`
        : `${API_URL}/api/issues/${encodeURIComponent(resolvedIssueId)}/approval-state?org_id=${encodeURIComponent(orgID)}`;
      const requestInit: RequestInit = {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      };
      if (action === "needs_changes") {
        requestInit.body = JSON.stringify({ approval_state: "needs_changes" });
      }
      const response = await fetch(endpoint, requestInit);
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to update issue approval state");
      }
      setIssueApprovalState(action);
      setApprovalStatus(action === "approved" ? "Issue approved." : "Changes requested.");
    } catch (error) {
      setApprovalError(error instanceof Error ? error.message : "Failed to update issue approval state");
    } finally {
      setPendingAction(null);
    }
  }

  return (
    <div data-testid="issue-detail-shell" className="mx-auto w-full max-w-[1280px] space-y-4">
      <nav className="flex items-center gap-2 text-sm text-[var(--text-muted)]">
        <Link to="/projects" className="hover:text-[var(--text)]">
          Projects
        </Link>
        {resolvedProjectId ? (
          <>
            <span>›</span>
            <Link to={`/projects/${encodeURIComponent(resolvedProjectId)}`} className="hover:text-[var(--text)]">
              Project
            </Link>
          </>
        ) : null}
        <span>›</span>
        <span className="font-medium text-[var(--text)]">Issue detail</span>
      </nav>

      <header className="rounded-3xl border border-[var(--border)] bg-[var(--surface)]/75 p-6 shadow-sm">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)]">{issueTitle || `Issue #${resolvedIssueId}`}</h1>
            <p className="mt-1 text-xs font-semibold uppercase tracking-[0.2em] text-[var(--text-muted)]">
              Issue #{issueNumber ?? resolvedIssueId}
            </p>
            <p className="mt-1 text-xs text-[var(--text-muted)]">
              {normalizeApprovalLabel(issueApprovalState)}
            </p>
            <p className="mt-1 text-sm text-[var(--text-muted)]">
              {issueSummary}
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className="rounded-lg border border-amber-300 bg-amber-50 px-3 py-1.5 text-xs font-semibold text-amber-800 hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-60"
              onClick={() => void handleApprovalAction("needs_changes")}
              disabled={pendingAction !== null || orgMissing}
            >
              {pendingAction === "needs_changes" ? "Updating..." : "Request Changes"}
            </button>
            <button
              type="button"
              className="rounded-lg border border-emerald-300 bg-emerald-50 px-3 py-1.5 text-xs font-semibold text-emerald-800 hover:bg-emerald-100 disabled:cursor-not-allowed disabled:opacity-60"
              onClick={() => void handleApprovalAction("approved")}
              disabled={pendingAction !== null || orgMissing}
            >
              {pendingAction === "approved" ? "Updating..." : "Approve"}
            </button>
            {resolvedProjectId ? (
              <Link
                to={`/projects/${encodeURIComponent(resolvedProjectId)}`}
                className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--text)] hover:bg-[var(--surface-alt)]"
              >
                Back to Project
              </Link>
            ) : null}
            <Link
              to="/projects"
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--text)] hover:bg-[var(--surface-alt)]"
            >
              All Projects
            </Link>
          </div>
        </div>
        {orgMissing && (
          <p className="mt-3 text-xs text-amber-700">Set an organization to enable approval actions.</p>
        )}
        {approvalStatus && (
          <p className="mt-3 text-xs text-emerald-700" role="status">
            {approvalStatus}
          </p>
        )}
        {approvalError && (
          <p className="mt-3 text-xs text-red-700" role="alert">
            {approvalError}
          </p>
        )}
        {issueLoading && (
          <p className="mt-3 text-xs text-[var(--text-muted)]">Loading issue context...</p>
        )}
        {!issueLoading && issueLoadError && (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <p className="text-xs text-red-700">{issueLoadError}</p>
            <button
              type="button"
              className="rounded border border-red-500/40 px-2 py-1 text-xs text-red-700 hover:bg-red-500/10"
              onClick={() => setIssueRefreshKey((current) => current + 1)}
            >
              Retry
            </button>
          </div>
        )}
      </header>

      <div className="grid gap-4 xl:grid-cols-[minmax(260px,320px)_1fr]">
        <aside className="rounded-2xl border border-[var(--border)] bg-[var(--surface)]/70 p-4">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-[var(--text-muted)]">Issue context</h2>
          <dl className="mt-3 space-y-2 text-sm">
            <div>
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">Issue ID</dt>
              <dd className="text-[var(--text)]">{resolvedIssueId}</dd>
            </div>
            <div>
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">Project scope</dt>
              <dd className="text-[var(--text)]">{issueProjectScope || resolvedProjectId || "Global alias route"}</dd>
            </div>
            <div>
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">Controls</dt>
              <dd className="text-[var(--text-muted)]">Approve and request-change actions are pinned in the issue header.</dd>
            </div>
          </dl>
        </aside>
        <IssueThreadPanel issueID={resolvedIssueId} projectID={resolvedProjectId || undefined} />
      </div>
    </div>
  );
}
