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
  project_id?: string;
  parent_issue_id?: string | null;
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

type DeliveryTone = "neutral" | "success" | "warning";

type DeliveryIndicator = {
  tone: DeliveryTone;
  text: string;
};

type IssueCommentCreateResponse = IssueComment & {
  delivery?: {
    attempted?: boolean;
    delivered?: boolean;
    error?: string;
  };
};

type IssueDetailResponse = {
  issue: IssueSummary;
  participants: IssueParticipant[];
  comments: IssueComment[];
};

type IssueRelationSummary = {
  id: string;
  issue_number: number;
  title: string;
};

type IssueListResponse = {
  items: IssueRelationSummary[];
  total: number;
};

type IssueReviewHistoryItem = {
  sha: string;
  subject: string;
  body?: string | null;
  authored_at: string;
  author_name: string;
  is_review_checkpoint: boolean;
  addressed_in_commit_sha?: string | null;
};

type IssueReviewHistoryResponse = {
  issue_id: string;
  document_path: string;
  last_review_commit_sha?: string | null;
  items: IssueReviewHistoryItem[];
  total: number;
};

type IssueReviewVersionResponse = {
  issue_id: string;
  document_path: string;
  sha: string;
  content: string;
  read_only: boolean;
};

type IssueReviewChangesFile = {
  path: string;
  change_type: "added" | "modified" | "removed" | "renamed";
  patch?: string | null;
};

type IssueReviewChangesResponse = {
  issue_id: string;
  document_path: string;
  base_sha: string;
  head_sha: string;
  fallback_to_first_commit: boolean;
  files: IssueReviewChangesFile[];
  total: number;
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
  const issueID =
    (typeof record.issue_id === "string" && record.issue_id) ||
    (typeof record.issueId === "string" && record.issueId) ||
    "";
  if (issueID !== activeIssueID) {
    const nested =
      parseIssueCommentEvent(record.data, activeIssueID) ??
      parseIssueCommentEvent(record.payload, activeIssueID);
    if (nested) {
      return nested;
    }
    return null;
  }
  const commentRecord =
    record.comment && typeof record.comment === "object"
      ? (record.comment as Record<string, unknown>)
      : (record as Record<string, unknown>);
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

function parseIssueEventIssueID(payload: unknown): string {
  if (!payload || typeof payload !== "object") {
    return "";
  }
  const record = payload as Record<string, unknown>;
  if (typeof record.issue_id === "string") {
    return record.issue_id;
  }
  if (typeof record.issueId === "string") {
    return record.issueId;
  }
  return "";
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

type ApprovalAction = {
  label: string;
  toState: IssueApprovalState;
  style: "neutral" | "warn" | "success";
  endpoint: "transition" | "approve";
};

function nextApprovalActions(state: IssueApprovalState): ApprovalAction[] {
  switch (state) {
    case "draft":
      return [{
        label: "Mark Ready for Review",
        toState: "ready_for_review",
        style: "neutral",
        endpoint: "transition",
      }];
    case "ready_for_review":
      return [
        { label: "Request Changes", toState: "needs_changes", style: "warn", endpoint: "transition" },
        { label: "Approve", toState: "approved", style: "success", endpoint: "approve" },
      ];
    case "needs_changes":
      return [{
        label: "Mark Ready for Review",
        toState: "ready_for_review",
        style: "neutral",
        endpoint: "transition",
      }];
    case "approved":
    default:
      return [];
  }
}

function approvalActionButtonClass(style: ApprovalAction["style"]): string {
  switch (style) {
    case "warn":
      return "border-amber-300 bg-amber-50 text-amber-800 hover:bg-amber-100";
    case "success":
      return "border-emerald-300 bg-emerald-50 text-emerald-800 hover:bg-emerald-100";
    case "neutral":
    default:
      return "border-[var(--border)] bg-[var(--surface)] text-[var(--text)] hover:bg-[var(--surface-alt)]";
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
  const [deliveryIndicator, setDeliveryIndicator] = useState<DeliveryIndicator | null>(null);
  const [participants, setParticipants] = useState<IssueParticipant[]>([]);
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [visibleCount, setVisibleCount] = useState(COMMENTS_PAGE_SIZE);
  const [draft, setDraft] = useState("");
  const [commentAuthorID, setCommentAuthorID] = useState("");
  const [selectedAgentID, setSelectedAgentID] = useState("");
  const [submittingComment, setSubmittingComment] = useState(false);
  const [updatingParticipant, setUpdatingParticipant] = useState(false);
  const [updatingApprovalState, setUpdatingApprovalState] = useState<IssueApprovalState | null>(null);
  const [approvalError, setApprovalError] = useState<string | null>(null);
  const [showApprovalConfetti, setShowApprovalConfetti] = useState(false);
  const [parentIssue, setParentIssue] = useState<IssueRelationSummary | null>(null);
  const [parentIssueMissing, setParentIssueMissing] = useState(false);
  const [childIssues, setChildIssues] = useState<IssueRelationSummary[]>([]);
  const [reviewHistory, setReviewHistory] = useState<IssueReviewHistoryItem[]>([]);
  const [reviewHistoryLoading, setReviewHistoryLoading] = useState(false);
  const [reviewHistoryError, setReviewHistoryError] = useState<string | null>(null);
  const [selectedReviewSHA, setSelectedReviewSHA] = useState<string | null>(null);
  const [reviewVersionBySHA, setReviewVersionBySHA] = useState<Record<string, string>>({});
  const [reviewVersionLoadingSHA, setReviewVersionLoadingSHA] = useState<string | null>(null);
  const [reviewVersionError, setReviewVersionError] = useState<string | null>(null);
  const [reviewChanges, setReviewChanges] = useState<IssueReviewChangesResponse | null>(null);
  const [reviewChangesLoading, setReviewChangesLoading] = useState(false);
  const [reviewChangesError, setReviewChangesError] = useState<string | null>(null);
  const [reviewRealtimeRefreshNonce, setReviewRealtimeRefreshNonce] = useState(0);

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
        setReviewHistory([]);
        setReviewHistoryError(null);
        setSelectedReviewSHA(null);
        setReviewVersionBySHA({});
        setReviewVersionError(null);
        setReviewChanges(null);
        setReviewChangesError(null);
        setParentIssue(null);
        setParentIssueMissing(false);
        setChildIssues([]);
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
  }, [issueID, reviewRealtimeRefreshNonce]);

  useEffect(() => {
    const orgID = getOrgID();
    const currentIssueID = issue?.id ?? "";
    const projectID = issue?.project_id?.trim() ?? "";
    if (!orgID || !currentIssueID || !projectID) {
      setParentIssue(null);
      setParentIssueMissing(false);
      setChildIssues([]);
      return;
    }

    let cancelled = false;
    const parentIssueID = issue?.parent_issue_id?.trim() ?? "";
    setParentIssue(null);
    setParentIssueMissing(false);
    setChildIssues([]);

    const childURL = new URL(`${API_URL}/api/issues`);
    childURL.searchParams.set("org_id", orgID);
    childURL.searchParams.set("project_id", projectID);
    childURL.searchParams.set("parent_issue_id", currentIssueID);
    childURL.searchParams.set("limit", "200");

    const childrenRequest = fetch(childURL.toString(), { headers: { "Content-Type": "application/json" } })
      .then(async (response) => {
        if (!response.ok) {
          return [] as IssueRelationSummary[];
        }
        const payload = await response.json() as IssueListResponse;
        if (!Array.isArray(payload.items)) {
          return [] as IssueRelationSummary[];
        }
        return payload.items.filter((entry) =>
          typeof entry?.id === "string" &&
          typeof entry?.issue_number === "number" &&
          typeof entry?.title === "string"
        );
      });

    const parentRequest = parentIssueID === ""
      ? Promise.resolve<null | IssueRelationSummary>(null)
      : fetch(
        `${API_URL}/api/issues/${encodeURIComponent(parentIssueID)}?org_id=${encodeURIComponent(orgID)}`,
        { headers: { "Content-Type": "application/json" } },
      )
        .then(async (response) => {
          if (!response.ok) {
            return null;
          }
          const payload = await response.json() as IssueDetailResponse;
          if (!payload.issue || typeof payload.issue.id !== "string" || typeof payload.issue.issue_number !== "number" || typeof payload.issue.title !== "string") {
            return null;
          }
          return {
            id: payload.issue.id,
            issue_number: payload.issue.issue_number,
            title: payload.issue.title,
          } satisfies IssueRelationSummary;
        });

    void Promise.all([parentRequest, childrenRequest])
      .then(([parentRecord, childRecords]) => {
        if (cancelled) {
          return;
        }
        setParentIssue(parentRecord);
        setParentIssueMissing(parentIssueID !== "" && parentRecord == null);
        setChildIssues(childRecords);
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setParentIssue(null);
        setParentIssueMissing(parentIssueID !== "");
        setChildIssues([]);
      });

    return () => {
      cancelled = true;
    };
  }, [issue?.id, issue?.parent_issue_id, issue?.project_id]);

  useEffect(() => {
    const orgID = getOrgID();
    if (!orgID || !issue?.document_path) {
      setReviewHistory([]);
      setReviewHistoryError(null);
      return;
    }

    let cancelled = false;
    setReviewHistoryLoading(true);
    setReviewHistoryError(null);

    const url = new URL(`${API_URL}/api/issues/${issueID}/review/history`);
    url.searchParams.set("org_id", orgID);

    void fetch(url.toString(), { headers: { "Content-Type": "application/json" } })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load review history");
        }
        return response.json() as Promise<IssueReviewHistoryResponse>;
      })
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setReviewHistory(Array.isArray(payload.items) ? payload.items : []);
      })
      .catch((fetchError: unknown) => {
        if (cancelled) {
          return;
        }
        setReviewHistoryError(fetchError instanceof Error ? fetchError.message : "Failed to load review history");
      })
      .finally(() => {
        if (!cancelled) {
          setReviewHistoryLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [issue?.document_path, issueID, reviewRealtimeRefreshNonce]);

  useEffect(() => {
    const orgID = getOrgID();
    if (!orgID || !issue?.document_path) {
      setReviewChanges(null);
      setReviewChangesError(null);
      return;
    }

    let cancelled = false;
    setReviewChangesLoading(true);
    setReviewChangesError(null);

    const url = new URL(`${API_URL}/api/issues/${issueID}/review/changes`);
    url.searchParams.set("org_id", orgID);

    void fetch(url.toString(), { headers: { "Content-Type": "application/json" } })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load changes since last review");
        }
        return response.json() as Promise<IssueReviewChangesResponse>;
      })
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setReviewChanges(payload);
      })
      .catch((fetchError: unknown) => {
        if (cancelled) {
          return;
        }
        setReviewChangesError(
          fetchError instanceof Error ? fetchError.message : "Failed to load changes since last review",
        );
      })
      .finally(() => {
        if (!cancelled) {
          setReviewChangesLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [issue?.document_path, issueID, reviewRealtimeRefreshNonce]);

  useEffect(() => {
    if (!lastMessage) {
      return;
    }
    if (lastMessage.type === "IssueCommentCreated") {
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
      return;
    }

    if (lastMessage.type === "IssueReviewSaved" || lastMessage.type === "IssueReviewAddressed") {
      const eventIssueID = parseIssueEventIssueID(lastMessage.data);
      if (eventIssueID === issueID) {
        setReviewRealtimeRefreshNonce((value) => value + 1);
      }
    }
  }, [issueID, lastMessage]);

  async function handleOpenReviewVersion(sha: string): Promise<void> {
    const orgID = getOrgID();
    if (!orgID) {
      return;
    }
    if (reviewVersionBySHA[sha]) {
      setSelectedReviewSHA(sha);
      setReviewVersionError(null);
      return;
    }

    setReviewVersionLoadingSHA(sha);
    setReviewVersionError(null);
    try {
      const response = await fetch(
        `${API_URL}/api/issues/${issueID}/review/history/${encodeURIComponent(sha)}?org_id=${encodeURIComponent(orgID)}`,
        { headers: { "Content-Type": "application/json" } },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to load review snapshot");
      }
      const payload = await response.json() as IssueReviewVersionResponse;
      setReviewVersionBySHA((current) => ({ ...current, [sha]: payload.content }));
      setSelectedReviewSHA(sha);
    } catch (err) {
      setReviewVersionError(err instanceof Error ? err.message : "Failed to load review snapshot");
    } finally {
      setReviewVersionLoadingSHA(null);
    }
  }

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
            sender_type: "user",
          }),
        },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to post comment");
      }
      const payload = await response.json() as IssueCommentCreateResponse;
      const persistedComment: IssueComment = {
        id: payload.id,
        author_agent_id: payload.author_agent_id,
        body: payload.body,
        created_at: payload.created_at,
        updated_at: payload.updated_at,
      };
      setComments((prev) =>
        sortComments(
          prev.map((existing) =>
            existing.id === optimisticID ? persistedComment : existing
          ),
        )
      );
      if (payload.delivery?.delivered) {
        setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
      } else if (typeof payload.delivery?.error === "string" && payload.delivery.error.trim()) {
        setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
      } else {
        setDeliveryIndicator({ tone: "neutral", text: "Saved" });
      }
      await autoAddMentionedParticipants(body);
    } catch {
      setComments((prev) =>
        sortComments(
          prev.map((existing) =>
            existing.id === optimisticID ? { ...existing, failed: true } : existing
          ),
        )
      );
      setDeliveryIndicator({ tone: "warning", text: "Send failed; not saved" });
    } finally {
      setSubmittingComment(false);
    }
  }

  useEffect(() => {
    if (!showApprovalConfetti) {
      return;
    }
    const timeoutID = window.setTimeout(() => setShowApprovalConfetti(false), 1600);
    return () => window.clearTimeout(timeoutID);
  }, [showApprovalConfetti]);

  async function handleTransitionApprovalState(action: ApprovalAction): Promise<void> {
    const orgID = getOrgID();
    if (!orgID || !issue) {
      return;
    }

    setApprovalError(null);
    setUpdatingApprovalState(action.toState);
    try {
      const endpoint = action.endpoint === "approve"
        ? `${API_URL}/api/issues/${issue.id}/approve?org_id=${encodeURIComponent(orgID)}`
        : `${API_URL}/api/issues/${issue.id}/approval-state?org_id=${encodeURIComponent(orgID)}`;
      const requestInit: RequestInit = {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      };
      if (action.endpoint === "transition") {
        requestInit.body = JSON.stringify({ approval_state: action.toState });
      }

      const response = await fetch(
        endpoint,
        requestInit,
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to update review state");
      }
      const updated = await response.json() as IssueSummary;
      setIssue((current) => {
        if (!current) {
          return current;
        }
        return {
          ...current,
          ...updated,
          document_content: current.document_content,
        };
      });
      if (action.endpoint === "approve") {
        setShowApprovalConfetti(true);
      }
    } catch (err) {
      setApprovalError(err instanceof Error ? err.message : "Failed to update review state");
    } finally {
      setUpdatingApprovalState(null);
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

  const normalizedApprovalState = useMemo(
    () => normalizeApprovalState(issue?.approval_state),
    [issue?.approval_state],
  );

  const approvalActions = useMemo(() => {
    if (!issue || issue.origin !== "local") {
      return [] as ApprovalAction[];
    }
    return nextApprovalActions(normalizedApprovalState);
  }, [issue, normalizedApprovalState]);

  const selectedReviewContent = useMemo(() => {
    if (!selectedReviewSHA) {
      return null;
    }
    return reviewVersionBySHA[selectedReviewSHA] ?? null;
  }, [reviewVersionBySHA, selectedReviewSHA]);

  const isViewingHistoricalVersion = selectedReviewSHA !== null;

  const displayedDocumentContent = useMemo(() => {
    if (isViewingHistoricalVersion) {
      return selectedReviewContent;
    }
    return issue?.document_content ?? null;
  }, [isViewingHistoricalVersion, issue?.document_content, selectedReviewContent]);

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
                className={`rounded-full border px-2 py-0.5 text-[11px] font-semibold ${approvalStateBadgeClass(normalizedApprovalState)}`}
                data-testid="issue-thread-approval"
              >
                {approvalStateLabel(normalizedApprovalState)}
              </span>
            </div>
            {(issue.parent_issue_id || childIssues.length > 0) && (
              <div className="mt-2 space-y-1 text-xs text-[var(--text-muted)]">
                {issue.parent_issue_id && parentIssue && (
                  <p>{`Parent: #${parentIssue.issue_number} ${parentIssue.title}`}</p>
                )}
                {issue.parent_issue_id && !parentIssue && parentIssueMissing && (
                  <p>Parent issue unavailable</p>
                )}
                {childIssues.length > 0 && (
                  <div>
                    <p>{`Sub-issues: ${childIssues.length}`}</p>
                    <ul className="ml-4 list-disc">
                      {childIssues.map((child) => (
                        <li key={child.id}>{`#${child.issue_number} ${child.title}`}</li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            )}
            {approvalActions.length > 0 && (
              <div className="mt-3 flex flex-wrap gap-2">
                {approvalActions.map((action) => (
                  <button
                    key={action.toState}
                    type="button"
                    className={`rounded-lg border px-3 py-1.5 text-xs font-semibold transition disabled:opacity-50 ${approvalActionButtonClass(action.style)}`}
                    disabled={updatingApprovalState !== null}
                    onClick={() => void handleTransitionApprovalState(action)}
                  >
                    {updatingApprovalState === action.toState ? "Updating..." : action.label}
                  </button>
                ))}
              </div>
            )}
            {showApprovalConfetti && (
              <div
                className="mt-3 flex flex-wrap gap-1"
                data-testid="approval-confetti"
                aria-label="Approval celebration"
              >
                {["#f59e0b", "#ef4444", "#10b981", "#3b82f6", "#8b5cf6", "#eab308"].map((color, index) => (
                  <span
                    key={`${color}-${index}`}
                    className="inline-block h-2 w-2 rounded-full"
                    style={{ backgroundColor: color }}
                  />
                ))}
              </div>
            )}
            {approvalError && (
              <p className="mt-2 text-xs text-red-700">{approvalError}</p>
            )}
          </header>

          {issue.document_path && (
            <section className="space-y-3">
              <h3 className="text-sm font-semibold text-[var(--text)]">Linked document</h3>
              <p className="text-xs text-[var(--text-muted)]">{issue.document_path}</p>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-3">
                <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                  Changes Since Last Review
                </p>
                {reviewChangesLoading && (
                  <p className="mt-2 text-xs text-[var(--text-muted)]">Loading review diff…</p>
                )}
                {!reviewChangesLoading && reviewChangesError && (
                  <p className="mt-2 text-xs text-red-700">{reviewChangesError}</p>
                )}
                {!reviewChangesLoading && !reviewChangesError && reviewChanges && (
                  <div className="mt-2 space-y-2">
                    <p className="text-xs text-[var(--text-muted)]">
                      {reviewChanges.fallback_to_first_commit
                        ? "No review checkpoint set; comparing current version to first commit."
                        : "Comparing latest commit to the last review checkpoint."}
                    </p>
                    <p className="text-xs text-[var(--text-muted)]">
                      Base: {reviewChanges.base_sha.slice(0, 7)} → Head: {reviewChanges.head_sha.slice(0, 7)}
                    </p>
                    {reviewChanges.files.length > 0 ? (
                      <ul className="space-y-1 text-xs text-[var(--text)]">
                        {reviewChanges.files.map((file) => (
                          <li key={`${file.path}-${file.change_type}`} className="rounded border border-[var(--border)] bg-[var(--surface)] px-2 py-1">
                            <span className="font-semibold">{file.change_type}</span> {file.path}
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <p className="text-xs text-[var(--text-muted)]">No file changes since last review.</p>
                    )}
                  </div>
                )}
              </div>

              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-3">
                <div className="flex items-center justify-between gap-2">
                  <p className="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                    Review Version History
                  </p>
                  {isViewingHistoricalVersion && (
                    <button
                      type="button"
                      className="rounded border border-[var(--border)] bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text)] hover:bg-[var(--surface-alt)]"
                      onClick={() => setSelectedReviewSHA(null)}
                    >
                      Latest Version
                    </button>
                  )}
                </div>
                {reviewHistoryLoading && (
                  <p className="mt-2 text-xs text-[var(--text-muted)]">Loading history…</p>
                )}
                {!reviewHistoryLoading && reviewHistoryError && (
                  <p className="mt-2 text-xs text-red-700">{reviewHistoryError}</p>
                )}
                {!reviewHistoryLoading && !reviewHistoryError && reviewHistory.length > 0 && (
                  <ul className="mt-2 space-y-2" data-testid="issue-review-history-list">
                    {reviewHistory.map((item) => (
                      <li key={item.sha} className="rounded border border-[var(--border)] bg-[var(--surface)] px-2 py-2">
                        <div className="flex items-start justify-between gap-2">
                          <div>
                            <p className="text-xs font-semibold text-[var(--text)]">
                              {item.subject}
                            </p>
                            <p className="text-[11px] text-[var(--text-muted)]">
                              {item.sha.slice(0, 7)} · {item.author_name} · {formatTimestamp(item.authored_at)}
                            </p>
                            {item.is_review_checkpoint && (
                              <span className="mt-1 inline-flex rounded-full border border-indigo-300 bg-indigo-50 px-2 py-0.5 text-[10px] font-semibold text-indigo-700">
                                Review checkpoint
                              </span>
                            )}
                            {item.addressed_in_commit_sha && (
                              <p className="mt-1 text-[11px] text-emerald-700">
                                Addressed in commit {item.addressed_in_commit_sha.slice(0, 7)}
                              </p>
                            )}
                          </div>
                          <button
                            type="button"
                            className="rounded border border-[var(--border)] px-2 py-1 text-xs text-[var(--text)] hover:bg-[var(--surface-alt)]"
                            onClick={() => void handleOpenReviewVersion(item.sha)}
                            data-testid={`issue-review-open-${item.sha}`}
                          >
                            {reviewVersionLoadingSHA === item.sha ? "Loading..." : "Open"}
                          </button>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
                {!reviewHistoryLoading && !reviewHistoryError && reviewHistory.length === 0 && (
                  <p className="mt-2 text-xs text-[var(--text-muted)]">No review history yet.</p>
                )}
                {reviewVersionError && (
                  <p className="mt-2 text-xs text-red-700">{reviewVersionError}</p>
                )}
              </div>

              {typeof displayedDocumentContent === "string" ? (
                <DocumentWorkspace
                  path={issue.document_path}
                  content={displayedDocumentContent}
                  reviewerName={agentNameByID.get(commentAuthorID) ?? "Reviewer"}
                  readOnly={isViewingHistoricalVersion}
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
            {deliveryIndicator && (
              <div className="flex">
                <p
                  className={`inline-flex rounded-full border px-2.5 py-0.5 text-[11px] ${
                    deliveryIndicator.tone === "success"
                      ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-300"
                      : deliveryIndicator.tone === "warning"
                        ? "border-amber-500/40 bg-amber-500/10 text-amber-300"
                        : "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]"
                  }`}
                >
                  {deliveryIndicator.text}
                </p>
              </div>
            )}
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
