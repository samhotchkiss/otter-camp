import { useEffect, useMemo, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";

import { API_URL } from "../lib/api";
import DocumentWorkspace from "../components/content-review/DocumentWorkspace";

const ORG_STORAGE_KEY = "otter-camp-org-id";

type IssueDetailPayload = {
  issue?: {
    id?: string;
    project_id?: string;
    document_path?: string | null;
    document_content?: string | null;
    approval_state?: string;
  };
  participants?: Array<{ agent_id?: string; removed_at?: string | null }>;
  comments?: Array<{ id?: string }>;
};

function decodeDocumentPath(rawPath?: string): string {
  const candidate = (rawPath || "").trim();
  if (!candidate) {
    return "untitled.md";
  }
  try {
    return decodeURIComponent(candidate);
  } catch {
    return candidate;
  }
}

function defaultDocumentContent(path: string): string {
  return [
    `# Review: ${path}`,
    "",
    "This route is an adapter for Figma alias paths.",
    "Detailed content-review redesign is tracked in later specs.",
  ].join("\n");
}

function getOrgID(): string {
  try {
    return (window.localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function trimLeadingSlash(path: string): string {
  return path.replace(/^\/+/, "");
}

export default function ContentReviewPage() {
  const { documentId } = useParams<{ documentId?: string }>();
  const [searchParams] = useSearchParams();
  const routePath = useMemo(() => decodeDocumentPath(documentId), [documentId]);
  const linkedIssueID = (searchParams.get("issue_id") ?? "").trim();
  const linkedProjectIDFromQuery = (searchParams.get("project_id") ?? "").trim();
  const [path, setPath] = useState(routePath);
  const [content, setContent] = useState(defaultDocumentContent(routePath));
  const [linkedProjectID, setLinkedProjectID] = useState(linkedProjectIDFromQuery);
  const [linkedApprovalState, setLinkedApprovalState] = useState<string>("");
  const [linkedCommentCount, setLinkedCommentCount] = useState(0);
  const [commentAuthorID, setCommentAuthorID] = useState<string>("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionStatus, setActionStatus] = useState<string | null>(null);
  const [loadingContext, setLoadingContext] = useState(Boolean(linkedIssueID));
  const orgID = getOrgID();

  useEffect(() => {
    if (!linkedIssueID) {
      setPath(routePath);
      setContent(defaultDocumentContent(routePath));
      setLinkedProjectID(linkedProjectIDFromQuery);
      setLinkedApprovalState("");
      setLinkedCommentCount(0);
      setCommentAuthorID("");
      setLoadError(null);
      setLoadingContext(false);
      return;
    }

    if (!orgID) {
      setPath(routePath);
      setContent(defaultDocumentContent(routePath));
      setLoadError("Set an organization to load linked review context.");
      setLoadingContext(false);
      return;
    }

    let cancelled = false;
    setLoadingContext(true);
    setLoadError(null);
    setActionError(null);
    setActionStatus(null);

    const url = new URL(`${API_URL}/api/issues/${encodeURIComponent(linkedIssueID)}`);
    url.searchParams.set("org_id", orgID);

    void fetch(url.toString(), { headers: { "Content-Type": "application/json" } })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load linked review context");
        }
        return response.json() as Promise<IssueDetailPayload>;
      })
      .then((payload) => {
        if (cancelled) {
          return;
        }

        const issue = payload.issue ?? {};
        const issuePathRaw = typeof issue.document_path === "string" ? issue.document_path.trim() : "";
        const issuePath = issuePathRaw ? trimLeadingSlash(issuePathRaw) : routePath;
        const issueContent = typeof issue.document_content === "string" ? issue.document_content : "";
        const issueProjectID = typeof issue.project_id === "string" ? issue.project_id.trim() : "";
        const issueApprovalState = typeof issue.approval_state === "string" ? issue.approval_state.trim() : "";
        const comments = Array.isArray(payload.comments) ? payload.comments : [];
        const participants = Array.isArray(payload.participants) ? payload.participants : [];
        const activeParticipant = participants.find((participant) =>
          !participant?.removed_at && typeof participant?.agent_id === "string" && participant.agent_id.trim(),
        );

        setPath(issuePath);
        setContent(issueContent || defaultDocumentContent(issuePath));
        setLinkedProjectID(issueProjectID || linkedProjectIDFromQuery);
        setLinkedApprovalState(issueApprovalState);
        setLinkedCommentCount(comments.length);
        setCommentAuthorID(activeParticipant?.agent_id?.trim() ?? "");
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        setPath(routePath);
        setContent(defaultDocumentContent(routePath));
        setLoadError(error instanceof Error ? error.message : "Failed to load linked review context");
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingContext(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [linkedIssueID, linkedProjectIDFromQuery, orgID, routePath]);

  async function transitionApprovalState(nextState: "approved" | "needs_changes"): Promise<void> {
    if (!linkedIssueID || !orgID) {
      return;
    }

    setActionError(null);
    setActionStatus(null);

    const endpoint = nextState === "approved"
      ? `${API_URL}/api/issues/${encodeURIComponent(linkedIssueID)}/approve?org_id=${encodeURIComponent(orgID)}`
      : `${API_URL}/api/issues/${encodeURIComponent(linkedIssueID)}/approval-state?org_id=${encodeURIComponent(orgID)}`;
    const requestInit: RequestInit = {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    };
    if (nextState === "needs_changes") {
      requestInit.body = JSON.stringify({ approval_state: "needs_changes" });
    }

    try {
      const response = await fetch(endpoint, requestInit);
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to update review state");
      }
      setLinkedApprovalState(nextState);
      setActionStatus(nextState === "approved" ? "Issue approved." : "Changes requested.");
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Failed to update review state");
    }
  }

  async function persistComment(comment: { message: string }): Promise<void> {
    if (!linkedIssueID || !orgID || !commentAuthorID) {
      return;
    }

    try {
      const response = await fetch(
        `${API_URL}/api/issues/${encodeURIComponent(linkedIssueID)}/comments?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            author_agent_id: commentAuthorID,
            body: comment.message,
            sender_type: "user",
          }),
        },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to save comment");
      }
      setLinkedCommentCount((count) => count + 1);
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Failed to save comment");
    }
  }

  return (
    <section
      className="min-w-0 space-y-5"
      data-testid="content-review-page-shell"
      aria-labelledby="content-review-page-title"
    >
      <header
        className="flex flex-col gap-2 rounded-2xl border border-slate-200 bg-white/80 p-4 shadow-sm sm:flex-row sm:items-start sm:justify-between dark:border-slate-800 dark:bg-slate-900/50"
        data-testid="content-review-route-header"
      >
        <div className="min-w-0">
          <p className="text-xs font-semibold uppercase tracking-[0.3em] text-indigo-500">
            Review Route Adapter
          </p>
          <h1 id="content-review-page-title" className="page-title">Content Review</h1>
          <p className="page-subtitle break-all" data-testid="content-review-route-path">
            {path}
          </p>
          {linkedIssueID && (
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-300" data-testid="content-review-linked-issue">
              Linked issue: {linkedIssueID}
              {linkedProjectID ? ` · Project: ${linkedProjectID}` : ""}
              {linkedApprovalState ? ` · State: ${linkedApprovalState}` : ""}
              {` · Comments: ${linkedCommentCount}`}
            </p>
          )}
          {loadingContext && (
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-300">Loading linked review context...</p>
          )}
          {loadError && (
            <p className="mt-1 text-xs text-red-600 dark:text-red-300" role="alert">
              {loadError}
            </p>
          )}
          {actionStatus && (
            <p className="mt-1 text-xs text-emerald-700 dark:text-emerald-300" role="status">
              {actionStatus}
            </p>
          )}
          {actionError && (
            <p className="mt-1 text-xs text-red-600 dark:text-red-300" role="alert">
              {actionError}
            </p>
          )}
        </div>
      </header>
      <DocumentWorkspace
        path={path}
        content={content}
        reviewerName="Otter reviewer"
        onContentChange={setContent}
        onApprove={({ markdown }) => {
          setContent(markdown);
          void transitionApprovalState("approved");
        }}
        onRequestChanges={({ markdown }) => {
          setContent(markdown);
          void transitionApprovalState("needs_changes");
        }}
        onCommentAdd={(comment) => {
          void persistComment(comment);
        }}
      />
    </section>
  );
}
