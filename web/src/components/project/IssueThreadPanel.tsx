import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useWS } from "../../contexts/WebSocketContext";
import WebSocketIssueSubscriber from "../WebSocketIssueSubscriber";
import { DocumentWorkspace } from "../content-review";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const ORG_STORAGE_KEY = "otter-camp-org-id";
const COMMENTS_PAGE_SIZE = 20;
type IssueApprovalState = "draft" | "ready_for_review" | "needs_changes" | "approved";

type IssueSummary = {
  id: string;
  title: string;
  issue_number: number;
  state: "open" | "closed";
  origin: "local" | "github";
  approval_state?: IssueApprovalState | null;
  document_path?: string | null;
  document_content?: string | null;
};

type IssueParticipant = {
  id: string;
  agent_id: string;
  role: "owner" | "collaborator";
  removed_at?: string | null;
};

type IssueComment = {
  id: string;
  author_agent_id: string;
  body: string;
  created_at: string;
  updated_at: string;
  optimistic?: boolean;
  failed?: boolean;
};

type IssueDetailResponse = {
  issue: IssueSummary;
  participants: IssueParticipant[];
  comments: IssueComment[];
};

type AgentOption = {
  id: string;
  name: string;
};

type AgentsResponse = {
  agents: AgentOption[];
};

type IssueThreadPanelProps = {
  issueID: string;
};

function getOrgID(): string {
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function normalizeMentionToken(raw: string): string {
  return raw.toLowerCase().replace(/[^a-z0-9]/g, "");
}

function mentionedAgentIDs(body: string, agents: AgentOption[]): string[] {
  const matches = body.match(/@([a-zA-Z0-9_.-]+)/g) ?? [];
  if (matches.length === 0 || agents.length === 0) {
    return [];
  }
  const aliasesByAgentID = new Map<string, Set<string>>();
  for (const agent of agents) {
    const aliases = new Set<string>();
    aliases.add(normalizeMentionToken(agent.id));
    aliases.add(normalizeMentionToken(agent.name));
    aliasesByAgentID.set(agent.id, aliases);
  }

  const mentioned = new Set<string>();
  for (const match of matches) {
    const token = normalizeMentionToken(match.slice(1));
    if (!token) {
      continue;
    }
    for (const [agentID, aliases] of aliasesByAgentID.entries()) {
      if (aliases.has(token)) {
        mentioned.add(agentID);
      }
    }
  }
  return [...mentioned];
}

function parseIssueCommentEvent(
  payload: unknown,
  activeIssueID: string,
): IssueComment | null {
  if (!payload || typeof payload !== "object") {
    return null;
  }
  const record = payload as Record<string, unknown>;
  const issueID = typeof record.issue_id === "string" ? record.issue_id : "";
  if (issueID !== activeIssueID) {
    return null;
  }
  const commentRecord =
    record.comment && typeof record.comment === "object"
      ? (record.comment as Record<string, unknown>)
      : null;
  if (!commentRecord) {
    return null;
  }
  const id = typeof commentRecord.id === "string" ? commentRecord.id : "";
  const author = typeof commentRecord.author_agent_id === "string"
    ? commentRecord.author_agent_id
    : "";
  const body = typeof commentRecord.body === "string" ? commentRecord.body : "";
  const createdAt = typeof commentRecord.created_at === "string"
    ? commentRecord.created_at
    : "";
  const updatedAt = typeof commentRecord.updated_at === "string"
    ? commentRecord.updated_at
    : createdAt;
  if (!id || !author || !body || !createdAt) {
    return null;
  }
  return {
    id,
    author_agent_id: author,
    body,
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function formatTimestamp(iso: string): string {
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

function normalizeApprovalState(raw: string | null | undefined): IssueApprovalState {
  switch (raw) {
    case "ready_for_review":
    case "needs_changes":
    case "approved":
      return raw;
    default:
      return "draft";
  }
}

function approvalStateLabel(state: IssueApprovalState): string {
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

function approvalStateBadgeClass(state: IssueApprovalState): string {
  switch (state) {
    case "ready_for_review":
      return "border-blue-200 bg-blue-50 text-blue-700";
    case "needs_changes":
      return "border-amber-200 bg-amber-50 text-amber-700";
    case "approved":
      return "border-emerald-200 bg-emerald-50 text-emerald-700";
    default:
      return "border-slate-200 bg-slate-100 text-slate-700";
  }
}

function sortComments(comments: IssueComment[]): IssueComment[] {
  return [...comments].sort((a, b) => {
    const aMs = Date.parse(a.created_at);
    const bMs = Date.parse(b.created_at);
    if (Number.isNaN(aMs) || Number.isNaN(bMs)) {
      return a.id.localeCompare(b.id);
    }
    if (aMs === bMs) {
      return a.id.localeCompare(b.id);
    }
    return aMs - bMs;
  });
}

export default function IssueThreadPanel({ issueID }: IssueThreadPanelProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [issue, setIssue] = useState<IssueSummary | null>(null);
  const [comments, setComments] = useState<IssueComment[]>([]);
  const [participants, setParticipants] = useState<IssueParticipant[]>([]);
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [visibleCount, setVisibleCount] = useState(COMMENTS_PAGE_SIZE);
  const [draft, setDraft] = useState("");
  const [commentAuthorID, setCommentAuthorID] = useState("");
  const [selectedAgentID, setSelectedAgentID] = useState("");
  const [submittingComment, setSubmittingComment] = useState(false);
  const [updatingParticipant, setUpdatingParticipant] = useState(false);

  const { lastMessage } = useWS();

  const activeParticipants = useMemo(
    () => participants.filter((participant) => !participant.removed_at),
    [participants],
  );

  const availableAgents = useMemo(() => {
    const activeSet = new Set(activeParticipants.map((participant) => participant.agent_id));
    return agents.filter((agent) => !activeSet.has(agent.id));
  }, [activeParticipants, agents]);

  const visibleComments = useMemo(() => {
    const sorted = sortComments(comments);
    if (sorted.length <= visibleCount) {
      return sorted;
    }
    return sorted.slice(sorted.length - visibleCount);
  }, [comments, visibleCount]);

  useEffect(() => {
    let cancelled = false;
    const orgID = getOrgID();
    if (!orgID || !issueID) {
      setError("Missing organization or issue context");
      setLoading(false);
      return;
    }

    setLoading(true);
    setError(null);

    const issueURL = new URL(`${API_URL}/api/issues/${issueID}`);
    issueURL.searchParams.set("org_id", orgID);
    const agentsURL = new URL(`${API_URL}/api/agents`);
    agentsURL.searchParams.set("org_id", orgID);

    void Promise.all([
      fetch(issueURL.toString(), { headers: { "Content-Type": "application/json" } }),
      fetch(agentsURL.toString(), { headers: { "Content-Type": "application/json" } }),
    ])
      .then(async ([issueResponse, agentsResponse]) => {
        if (!issueResponse.ok) {
          const payload = await issueResponse.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load issue thread");
        }
        if (!agentsResponse.ok) {
          const payload = await agentsResponse.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load agents");
        }
        const issuePayload = await issueResponse.json() as IssueDetailResponse;
        const agentsPayload = await agentsResponse.json() as AgentsResponse;
        return { issuePayload, agentsPayload };
      })
      .then(({ issuePayload, agentsPayload }) => {
        if (cancelled) {
          return;
        }
        setIssue(issuePayload.issue);
        setParticipants(Array.isArray(issuePayload.participants) ? issuePayload.participants : []);
        setComments(Array.isArray(issuePayload.comments) ? sortComments(issuePayload.comments) : []);
        setVisibleCount(COMMENTS_PAGE_SIZE);

        const agentList = Array.isArray(agentsPayload.agents) ? agentsPayload.agents : [];
        setAgents(agentList);
        if (agentList.length > 0) {
          setCommentAuthorID((current) => current || agentList[0].id);
        }
      })
      .catch((loadError: unknown) => {
        if (cancelled) {
          return;
        }
        setError(loadError instanceof Error ? loadError.message : "Failed to load issue thread");
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [issueID]);

  useEffect(() => {
    if (!lastMessage || lastMessage.type !== "IssueCommentCreated") {
      return;
    }
    const incoming = parseIssueCommentEvent(lastMessage.data, issueID);
    if (!incoming) {
      return;
    }

    setComments((prev) => {
      if (prev.some((existing) => existing.id === incoming.id)) {
        return prev;
      }
      return sortComments([...prev, incoming]);
    });
  }, [issueID, lastMessage]);

  async function autoAddMentionedParticipants(commentBody: string): Promise<void> {
    const orgID = getOrgID()
    if (!orgID) {
      return
    }
    const mentionedIDs = mentionedAgentIDs(commentBody, agents)
    if (mentionedIDs.length === 0) {
      return
    }
    const activeParticipantSet = new Set(
      activeParticipants.map((participant) => participant.agent_id),
    )
    const toAdd = mentionedIDs.filter((agentID) => !activeParticipantSet.has(agentID))
    if (toAdd.length === 0) {
      return
    }

    const addedParticipants: IssueParticipant[] = []
    for (const agentID of toAdd) {
      const response = await fetch(
        `${API_URL}/api/issues/${issueID}/participants?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            agent_id: agentID,
            role: "collaborator",
          }),
        },
      )
      if (!response.ok) {
        continue
      }
      const participant = await response.json() as IssueParticipant
      addedParticipants.push(participant)
    }
    if (addedParticipants.length > 0) {
      setParticipants((prev) => [...prev, ...addedParticipants])
    }
  }

  async function handlePostComment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const orgID = getOrgID();
    const body = draft.trim();
    if (!orgID || !body || !commentAuthorID) {
      return;
    }

    setSubmittingComment(true);
    const optimisticID = `temp-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    const optimisticComment: IssueComment = {
      id: optimisticID,
      author_agent_id: commentAuthorID,
      body,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      optimistic: true,
    };
    setComments((prev) => sortComments([...prev, optimisticComment]));
    setDraft("");

    try {
      const response = await fetch(
        `${API_URL}/api/issues/${issueID}/comments?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            author_agent_id: commentAuthorID,
            body,
          }),
        },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to post comment");
      }
      const persistedComment = await response.json() as IssueComment;
      setComments((prev) =>
        sortComments(
          prev.map((existing) =>
            existing.id === optimisticID ? persistedComment : existing
          ),
        )
      );
      await autoAddMentionedParticipants(body);
    } catch {
      setComments((prev) =>
        sortComments(
          prev.map((existing) =>
            existing.id === optimisticID ? { ...existing, failed: true } : existing
          ),
        )
      );
    } finally {
      setSubmittingComment(false);
    }
  }

  async function handleAddParticipant(): Promise<void> {
    const orgID = getOrgID();
    if (!orgID || !selectedAgentID) {
      return;
    }
    setUpdatingParticipant(true);
    try {
      const response = await fetch(
        `${API_URL}/api/issues/${issueID}/participants?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            agent_id: selectedAgentID,
            role: "collaborator",
          }),
        },
      );
      if (!response.ok) {
        return;
      }
      const participant = await response.json() as IssueParticipant;
      setParticipants((prev) => [...prev, participant]);
      setSelectedAgentID("");
    } finally {
      setUpdatingParticipant(false);
    }
  }

  async function handleRemoveParticipant(participant: IssueParticipant): Promise<void> {
    const orgID = getOrgID();
    if (!orgID || participant.role === "owner") {
      return;
    }
    setUpdatingParticipant(true);
    try {
      const response = await fetch(
        `${API_URL}/api/issues/${issueID}/participants/${participant.agent_id}?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
        },
      );
      if (!response.ok) {
        return;
      }
      setParticipants((prev) =>
        prev.map((existing) =>
          existing.id === participant.id
            ? { ...existing, removed_at: new Date().toISOString() }
            : existing,
        ),
      );
    } finally {
      setUpdatingParticipant(false);
    }
  }

  const agentNameByID = useMemo(() => {
    const out = new Map<string, string>();
    for (const agent of agents) {
      out.set(agent.id, agent.name);
    }
    return out;
  }, [agents]);

  return (
    <section className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6">
      <WebSocketIssueSubscriber issueID={issueID} />
      {loading && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] p-4 text-sm text-[var(--text-muted)]">
          Loading issue thread...
        </div>
      )}

      {!loading && error && (
        <div className="rounded-xl border border-red-300 bg-red-50 p-4 text-sm text-red-700">
          {error}
        </div>
      )}

      {!loading && !error && issue && (
        <div className="space-y-5">
          <header className="border-b border-[var(--border)] pb-4">
            <h2 className="text-lg font-semibold text-[var(--text)]">
              #{issue.issue_number} {issue.title}
            </h2>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <p className="text-xs text-[var(--text-muted)]">
                {issue.origin === "github" ? "GitHub" : "Local"} · {issue.state === "open" ? "Open" : "Closed"}
              </p>
              <span
                className={`rounded-full border px-2 py-0.5 text-[11px] font-semibold ${approvalStateBadgeClass(normalizeApprovalState(issue.approval_state))}`}
                data-testid="issue-thread-approval"
              >
                {approvalStateLabel(normalizeApprovalState(issue.approval_state))}
              </span>
            </div>
          </header>

          {issue.document_path && (
            <section className="space-y-3">
              <h3 className="text-sm font-semibold text-[var(--text)]">Linked document</h3>
              <p className="text-xs text-[var(--text-muted)]">{issue.document_path}</p>
              {typeof issue.document_content === "string" ? (
                <DocumentWorkspace
                  path={issue.document_path}
                  content={issue.document_content}
                  reviewerName={agentNameByID.get(commentAuthorID) ?? "Reviewer"}
                />
              ) : (
                <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-3 text-xs text-[var(--text-muted)]">
                  Linked document content is unavailable.
                </div>
              )}
            </section>
          )}

          <div className="space-y-3">
            <h3 className="text-sm font-semibold text-[var(--text)]">Participants</h3>
            <div className="flex flex-wrap gap-2">
              {activeParticipants.length === 0 && (
                <span className="text-xs text-[var(--text-muted)]">No active participants</span>
              )}
              {activeParticipants.map((participant) => (
                <span
                  key={participant.id}
                  className="inline-flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-1 text-xs text-[var(--text)]"
                >
                  <span>
                    {agentNameByID.get(participant.agent_id) ?? participant.agent_id}
                    {participant.role === "owner" ? " (Owner)" : ""}
                  </span>
                  {participant.role !== "owner" && (
                    <button
                      type="button"
                      onClick={() => void handleRemoveParticipant(participant)}
                      className="rounded border border-[var(--border)] px-1 text-[10px] text-[var(--text-muted)] hover:bg-[var(--surface)]"
                      disabled={updatingParticipant}
                    >
                      Remove
                    </button>
                  )}
                </span>
              ))}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <select
                aria-label="Add agent to issue"
                className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)]"
                value={selectedAgentID}
                onChange={(event) => setSelectedAgentID(event.target.value)}
              >
                <option value="">Select agent…</option>
                {availableAgents.map((agent) => (
                  <option key={agent.id} value={agent.id}>
                    {agent.name}
                  </option>
                ))}
              </select>
              <button
                type="button"
                className="rounded-lg border border-[var(--border)] px-3 py-2 text-sm text-[var(--text)] hover:bg-[var(--surface-alt)] disabled:opacity-50"
                disabled={!selectedAgentID || updatingParticipant}
                onClick={() => void handleAddParticipant()}
              >
                Add Agent
              </button>
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold text-[var(--text)]">Thread</h3>
              {comments.length > visibleCount && (
                <button
                  type="button"
                  className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
                  onClick={() => setVisibleCount((count) => count + COMMENTS_PAGE_SIZE)}
                >
                  Load older comments
                </button>
              )}
            </div>

            <ul className="space-y-2">
              {visibleComments.length === 0 && (
                <li className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-4 text-sm text-[var(--text-muted)]">
                  No comments yet.
                </li>
              )}
              {visibleComments.map((comment) => (
                <li
                  key={comment.id}
                  className={`rounded-lg border px-3 py-2 ${
                    comment.failed
                      ? "border-red-300 bg-red-50"
                      : "border-[var(--border)] bg-[var(--surface-alt)]"
                  }`}
                >
                  <div className="flex items-center justify-between text-xs text-[var(--text-muted)]">
                    <span>{agentNameByID.get(comment.author_agent_id) ?? comment.author_agent_id}</span>
                    <span>{formatTimestamp(comment.created_at)}</span>
                  </div>
                  <p className="mt-1 whitespace-pre-wrap text-sm text-[var(--text)]">
                    {comment.body}
                  </p>
                  {comment.optimistic && !comment.failed && (
                    <p className="mt-1 text-[10px] font-semibold uppercase tracking-wide text-amber-600">
                      Sending…
                    </p>
                  )}
                  {comment.failed && (
                    <p className="mt-1 text-[10px] font-semibold uppercase tracking-wide text-red-700">
                      Failed to send
                    </p>
                  )}
                </li>
              ))}
            </ul>
          </div>

          <form className="space-y-2" onSubmit={handlePostComment}>
            <div className="flex flex-wrap items-center gap-2">
              <label className="text-xs font-semibold text-[var(--text-muted)]" htmlFor="issue-comment-author">
                Comment as
              </label>
              <select
                id="issue-comment-author"
                aria-label="Comment author agent"
                className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)]"
                value={commentAuthorID}
                onChange={(event) => setCommentAuthorID(event.target.value)}
              >
                <option value="">Select agent…</option>
                {agents.map((agent) => (
                  <option key={agent.id} value={agent.id}>
                    {agent.name}
                  </option>
                ))}
              </select>
            </div>
            <textarea
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              placeholder="Write a comment… use @AgentName to auto-add participants."
              className="h-24 w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)] outline-none ring-0 focus:border-amber-400"
            />
            <div className="flex justify-end">
              <button
                type="submit"
                className="rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-semibold text-white hover:bg-[#b79658] disabled:opacity-50"
                disabled={submittingComment || !draft.trim() || !commentAuthorID}
              >
                {submittingComment ? "Posting..." : "Post Comment"}
              </button>
            </div>
          </form>
        </div>
      )}
    </section>
  );
}
