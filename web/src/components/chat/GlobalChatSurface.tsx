import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { useWS } from "../../contexts/WebSocketContext";
import type { DMMessage, MessageAttachment } from "../messaging/types";
import MessageHistory from "../messaging/MessageHistory";
import type {
  GlobalChatConversation,
} from "../../contexts/GlobalChatContext";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const ORG_STORAGE_KEY = "otter-camp-org-id";
const USER_NAME_STORAGE_KEY = "otter-camp-user-name";
const PROJECT_CHAT_SESSION_RESET_AUTHOR = "__otter_session__";
const PROJECT_CHAT_SESSION_RESET_PREFIX = "project_chat_session_reset:";
const CHAT_SESSION_RESET_PREFIX = "chat_session_reset:";
const POST_SEND_REFRESH_DELAYS_MS = [1200, 3500, 7000, 12000];
const PERIODIC_REFRESH_CONNECTED_MS = 10000;
const PERIODIC_REFRESH_DEGRADED_MS = 3500;

type DeliveryTone = "neutral" | "success" | "warning";

type DeliveryIndicator = {
  tone: DeliveryTone;
  text: string;
};

type IssueDetailResponse = {
  comments?: Array<{
    id: string;
    author_agent_id: string;
    body: string;
    created_at: string;
    updated_at: string;
  }>;
  participants?: Array<{
    agent_id: string;
    role: "owner" | "collaborator";
    removed_at?: string | null;
  }>;
};

type AgentsResponse = {
  agents?: Array<{ id: string; name: string }>;
};

type ChatAttachment = MessageAttachment;

type ChatMessage = DMMessage & {
  attachments?: ChatAttachment[];
  optimistic?: boolean;
  failed?: boolean;
};

type GlobalChatSurfaceProps = {
  conversation: GlobalChatConversation;
  onConversationTouched?: () => void;
  refreshVersion?: number;
};

type DMRealtimeEvent = {
  threadID: string;
  message: unknown;
};

type IssueCommentRealtimeEvent = {
  issueID: string;
  comment: unknown;
};

function getStoredOrgID(): string {
  try {
    return (window.localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function getCurrentUserName(): string {
  try {
    const fromStorage = (window.localStorage.getItem(USER_NAME_STORAGE_KEY) ?? "").trim();
    return fromStorage || "You";
  } catch {
    return "You";
  }
}

function getAuthToken(): string {
  try {
    return (window.localStorage.getItem("otter_camp_token") ?? "").trim();
  } catch {
    return "";
  }
}

function normalizeTimestamp(value: unknown): string {
  if (typeof value !== "string" || value.trim() === "") {
    return new Date().toISOString();
  }
  return value;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : null;
}

function normalizeMessageAttachments(raw: unknown): ChatAttachment[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: ChatAttachment[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") {
      continue;
    }
    const record = entry as Record<string, unknown>;
    const id = typeof record.id === "string" ? record.id.trim() : "";
    const filename = typeof record.filename === "string" ? record.filename.trim() : "";
    const mimeType = typeof record.mime_type === "string" ? record.mime_type.trim() : "";
    const url = typeof record.url === "string" ? record.url.trim() : "";
    const sizeBytes = typeof record.size_bytes === "number" ? record.size_bytes : Number(record.size_bytes ?? 0);
    if (!id || !filename || !mimeType || !url || !Number.isFinite(sizeBytes)) {
      continue;
    }
    const thumbnailURL = typeof record.thumbnail_url === "string" ? record.thumbnail_url : undefined;
    out.push({
      id,
      filename,
      size_bytes: sizeBytes,
      mime_type: mimeType,
      url,
      thumbnail_url: thumbnailURL,
    });
  }
  return out;
}

function extractSessionResetID(content: string): string | null {
  const trimmed = content.trim();
  if (trimmed.startsWith(PROJECT_CHAT_SESSION_RESET_PREFIX)) {
    return trimmed.slice(PROJECT_CHAT_SESSION_RESET_PREFIX.length).trim() || "session";
  }
  if (trimmed.startsWith(CHAT_SESSION_RESET_PREFIX)) {
    return trimmed.slice(CHAT_SESSION_RESET_PREFIX.length).trim() || "session";
  }
  return null;
}

function normalizeThreadMessage(
  raw: unknown,
  fallbackThreadID: string,
  currentUserName: string,
  currentUserID: string,
): ChatMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;

  const id = typeof record.id === "string" && record.id.trim() !== ""
    ? record.id
    : `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const senderTypeRaw =
    (typeof record.senderType === "string" && record.senderType.trim().toLowerCase()) ||
    (typeof record.sender_type === "string" && record.sender_type.trim().toLowerCase()) ||
    "user";
  const senderType = senderTypeRaw === "agent" ? "agent" : "user";
  const senderName =
    (typeof record.senderName === "string" && record.senderName.trim()) ||
    (typeof record.sender_name === "string" && record.sender_name.trim()) ||
    (senderType === "agent" ? "Agent" : currentUserName);
  const rawSenderID =
    (typeof record.senderId === "string" && record.senderId.trim()) ||
    (typeof record.sender_id === "string" && record.sender_id.trim()) ||
    "";
  const senderID = rawSenderID || (senderType === "agent" ? `agent:${senderName}` : currentUserID);
  const content =
    (typeof record.content === "string" && record.content) || "";
  const attachments = normalizeMessageAttachments(record.attachments);
  const resetSessionID = extractSessionResetID(content);
  if (resetSessionID !== null) {
    return {
      id,
      threadId:
        (typeof record.threadId === "string" && record.threadId) ||
        (typeof record.thread_id === "string" && record.thread_id) ||
        fallbackThreadID,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.createdAt ?? record.created_at),
      isSessionReset: true,
      sessionID: resetSessionID,
    };
  }

  return {
    id,
    threadId:
      (typeof record.threadId === "string" && record.threadId) ||
      (typeof record.thread_id === "string" && record.thread_id) ||
      fallbackThreadID,
    senderId: senderID,
    senderName,
    senderType,
    senderAvatarUrl:
      (typeof record.senderAvatarUrl === "string" && record.senderAvatarUrl) ||
      (typeof record.sender_avatar_url === "string" && record.sender_avatar_url) ||
      undefined,
    content,
    attachments,
    createdAt: normalizeTimestamp(record.createdAt ?? record.created_at),
  };
}

function normalizeProjectMessage(
  raw: unknown,
  expectedProjectID: string,
  threadID: string,
  currentUserName: string,
  currentUserID: string,
): ChatMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id : "";
  const projectID = typeof record.project_id === "string" ? record.project_id : "";
  const author = typeof record.author === "string" ? record.author.trim() : "";
  const body = typeof record.body === "string" ? record.body : "";
  const attachments = normalizeMessageAttachments(record.attachments);
  if (!id || !projectID || !author || (body === "" && attachments.length === 0)) {
    return null;
  }
  if (projectID !== expectedProjectID) {
    return null;
  }
  const resetSessionID = extractSessionResetID(body);
  if ((author === PROJECT_CHAT_SESSION_RESET_AUTHOR || author === "Session") && resetSessionID !== null) {
    return {
      id,
      threadId: threadID,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.created_at),
      isSessionReset: true,
      sessionID: resetSessionID,
    };
  }

  const isUser = author.toLowerCase() === currentUserName.toLowerCase();
  return {
    id,
    threadId: threadID,
    senderId: isUser ? currentUserID : `agent:${author}`,
    senderName: isUser ? currentUserName : author,
    senderType: isUser ? "user" : "agent",
    content: body,
    attachments,
    createdAt: normalizeTimestamp(record.created_at),
  };
}

function normalizeIssueComment(
  raw: unknown,
  threadID: string,
  agentNameByID: Map<string, string>,
  authorAgentID: string,
): ChatMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id : "";
  const authorID = typeof record.author_agent_id === "string" ? record.author_agent_id : "";
  const body = typeof record.body === "string" ? record.body : "";
  const attachments = normalizeMessageAttachments(record.attachments);
  if (!id || !authorID || (body === "" && attachments.length === 0)) {
    return null;
  }
  const resetSessionID = extractSessionResetID(body);
  if (resetSessionID !== null) {
    return {
      id,
      threadId: threadID,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.created_at),
      isSessionReset: true,
      sessionID: resetSessionID,
    };
  }

  const senderName = agentNameByID.get(authorID) ?? authorID;
  const isUser = authorAgentID !== "" && authorID === authorAgentID;

  return {
    id,
    threadId: threadID,
    senderId: authorID,
    senderName,
    senderType: isUser ? "user" : "agent",
    content: body,
    attachments,
    createdAt: normalizeTimestamp(record.created_at),
  };
}

function sortChatMessagesByCreatedAt(messages: ChatMessage[]): ChatMessage[] {
  return [...messages].sort(
    (a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt),
  );
}

function areAttachmentsEqual(
  a: ChatAttachment[] | undefined,
  b: ChatAttachment[] | undefined,
): boolean {
  const left = a ?? [];
  const right = b ?? [];
  if (left.length !== right.length) {
    return false;
  }
  for (let i = 0; i < left.length; i += 1) {
    const leftItem = left[i];
    const rightItem = right[i];
    if (
      leftItem.id !== rightItem.id ||
      leftItem.filename !== rightItem.filename ||
      leftItem.mime_type !== rightItem.mime_type ||
      leftItem.size_bytes !== rightItem.size_bytes ||
      leftItem.url !== rightItem.url ||
      (leftItem.thumbnail_url ?? "") !== (rightItem.thumbnail_url ?? "")
    ) {
      return false;
    }
  }
  return true;
}

function areMessagesEquivalent(left: ChatMessage[], right: ChatMessage[]): boolean {
  if (left.length !== right.length) {
    return false;
  }
  for (let i = 0; i < left.length; i += 1) {
    const a = left[i];
    const b = right[i];
    if (
      a.id !== b.id ||
      a.threadId !== b.threadId ||
      a.senderId !== b.senderId ||
      a.senderName !== b.senderName ||
      a.senderType !== b.senderType ||
      (a.senderAvatarUrl ?? "") !== (b.senderAvatarUrl ?? "") ||
      a.content !== b.content ||
      a.createdAt !== b.createdAt ||
      Boolean(a.optimistic) !== Boolean(b.optimistic) ||
      Boolean(a.failed) !== Boolean(b.failed) ||
      Boolean(a.isSessionReset) !== Boolean(b.isSessionReset) ||
      (a.sessionID ?? "") !== (b.sessionID ?? "") ||
      !areAttachmentsEqual(a.attachments, b.attachments)
    ) {
      return false;
    }
  }
  return true;
}

function extractDMRealtimeEvent(raw: unknown): DMRealtimeEvent | null {
  const record = asRecord(raw);
  if (!record) {
    return null;
  }

  const threadID =
    (typeof record.threadId === "string" && record.threadId) ||
    (typeof record.thread_id === "string" && record.thread_id) ||
    "";
  if (threadID) {
    const message = asRecord(record.message) ?? record;
    return { threadID, message };
  }

  return (
    extractDMRealtimeEvent(record.data) ??
    extractDMRealtimeEvent(record.payload) ??
    extractDMRealtimeEvent(record.message)
  );
}

function extractProjectRealtimeMessage(raw: unknown): unknown {
  const record = asRecord(raw);
  if (!record) {
    return null;
  }
  const hasProjectID =
    (typeof record.project_id === "string" && record.project_id.trim() !== "") ||
    (typeof record.projectId === "string" && record.projectId.trim() !== "");
  if (hasProjectID) {
    return record;
  }

  return (
    extractProjectRealtimeMessage(record.message) ??
    extractProjectRealtimeMessage(record.data) ??
    extractProjectRealtimeMessage(record.payload)
  );
}

function extractIssueCommentRealtimeEvent(raw: unknown): IssueCommentRealtimeEvent | null {
  const record = asRecord(raw);
  if (!record) {
    return null;
  }

  const issueID =
    (typeof record.issue_id === "string" && record.issue_id) ||
    (typeof record.issueId === "string" && record.issueId) ||
    "";
  const comment = asRecord(record.comment);
  if (issueID && comment) {
    return { issueID, comment };
  }
  if (issueID && typeof record.id === "string" && typeof record.author_agent_id === "string") {
    return { issueID, comment: record };
  }

  if (comment) {
    const nestedIssueID =
      (typeof comment.issue_id === "string" && comment.issue_id) ||
      (typeof comment.issueId === "string" && comment.issueId) ||
      "";
    if (nestedIssueID) {
      return { issueID: nestedIssueID, comment };
    }
  }

  return (
    extractIssueCommentRealtimeEvent(record.data) ??
    extractIssueCommentRealtimeEvent(record.payload)
  );
}

function deliveryIndicatorClass(indicator: DeliveryIndicator): string {
  if (indicator.tone === "success") {
    return "border-[var(--green)]/40 bg-[var(--green)]/15 text-[var(--green)]";
  }
  if (indicator.tone === "warning") {
    return "border-[var(--orange)]/40 bg-[var(--orange)]/15 text-[var(--orange)]";
  }
  return "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]";
}

function normalizeDeliveryErrorText(raw: unknown): string {
  if (typeof raw !== "string" || raw.trim() === "") {
    return "";
  }
  const message = raw.trim();
  const lower = message.toLowerCase();
  if (lower.includes("bridge offline")) {
    return `${message} (bridge process needs to be running)`;
  }
  if (lower.includes("delivery unavailable")) {
    return `${message} (message saved; retry after bridge reconnects)`;
  }
  return message;
}

function formatAttachmentSize(sizeBytes: number): string {
  if (!Number.isFinite(sizeBytes) || sizeBytes <= 0) {
    return "0 B";
  }
  if (sizeBytes < 1024) {
    return `${sizeBytes} B`;
  }
  if (sizeBytes < 1024 * 1024) {
    return `${(sizeBytes / 1024).toFixed(1)} KB`;
  }
  return `${(sizeBytes / (1024 * 1024)).toFixed(1)} MB`;
}

function buildAttachmentLinksMarkdown(attachments: ChatAttachment[]): string {
  if (attachments.length === 0) {
    return "";
  }
  return attachments
    .map((attachment) => `📎 [${attachment.filename}](${attachment.url})`)
    .join("\n");
}

export default function GlobalChatSurface({
  conversation,
  onConversationTouched,
  refreshVersion = 0,
}: GlobalChatSurfaceProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [deliveryIndicator, setDeliveryIndicator] = useState<DeliveryIndicator | null>(null);
  const [issueAuthorID, setIssueAuthorID] = useState("");
  const [issueAgentNameByID, setIssueAgentNameByID] = useState<Map<string, string>>(new Map());
  const [queuedAttachments, setQueuedAttachments] = useState<ChatAttachment[]>([]);
  const [uploadingAttachments, setUploadingAttachments] = useState(false);
  const [isDragActive, setIsDragActive] = useState(false);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const onConversationTouchedRef = useRef(onConversationTouched);
  const postSendRefreshTimersRef = useRef<number[]>([]);
  const { connected, lastMessage } = useWS();
  const conversationType = conversation.type;
  const conversationKey = conversation.key;
  const conversationTitle = conversation.title;
  const conversationContextLabel = conversation.contextLabel;
  const dmThreadID = conversationType === "dm" ? conversation.threadId : "";
  const projectID = conversationType === "project" ? conversation.projectId : "";
  const issueID = conversationType === "issue" ? conversation.issueId : "";

  useEffect(() => {
    onConversationTouchedRef.current = onConversationTouched;
  }, [onConversationTouched]);

  const orgID = useMemo(() => getStoredOrgID(), [conversationKey]);
  const token = useMemo(() => getAuthToken(), [conversationKey]);
  const currentUserName = useMemo(() => getCurrentUserName(), [conversationKey]);
  const currentUserID = useMemo(() => `user:${currentUserName.toLowerCase()}`, [currentUserName]);

  const threadID = useMemo(() => {
    if (conversationType === "dm") {
      return dmThreadID;
    }
    return conversationKey;
  }, [conversationKey, conversationType, dmThreadID]);

  const requestHeaders = useMemo(() => {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
    return headers;
  }, [token]);

  const touchConversation = useCallback(() => {
    onConversationTouchedRef.current?.();
  }, []);

  const clearPostSendRefreshTimers = useCallback(() => {
    for (const timerID of postSendRefreshTimersRef.current) {
      window.clearTimeout(timerID);
    }
    postSendRefreshTimersRef.current = [];
  }, []);

  const setMessagesIfChanged = useCallback((next: ChatMessage[]) => {
    setMessages((prev) => (areMessagesEquivalent(prev, next) ? prev : next));
  }, []);

  const loadConversation = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    if (!orgID) {
      setMessages([]);
      if (!silent) {
        setLoading(false);
        setError("Missing organization context");
      }
      return;
    }

    if (!silent) {
      setLoading(true);
      setError(null);
      setDeliveryIndicator(null);
    }

    try {
      if (conversationType === "dm") {
        const params = new URLSearchParams({
          org_id: orgID,
          thread_id: dmThreadID,
          limit: "100",
        });
        const response = await fetch(`${API_URL}/api/messages?${params.toString()}`, {
          headers: requestHeaders,
          cache: "no-store",
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to fetch messages");
        }
        const payload = await response.json();
        const normalized = Array.isArray(payload.messages)
          ? payload.messages
              .map((entry: unknown) =>
                normalizeThreadMessage(entry, dmThreadID, currentUserName, currentUserID),
              )
              .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
          : [];
        setMessagesIfChanged(sortChatMessagesByCreatedAt(normalized));
        touchConversation();
        return;
      }

      if (conversationType === "project") {
        const url = new URL(`${API_URL}/api/projects/${projectID}/chat`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("limit", "100");

        const response = await fetch(url.toString(), {
          headers: requestHeaders,
          cache: "no-store",
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to fetch project chat");
        }

        const payload = await response.json();
        const normalized = Array.isArray(payload.messages)
          ? payload.messages
              .map((entry: unknown) =>
                normalizeProjectMessage(
                  entry,
                  projectID,
                  conversationKey,
                  currentUserName,
                  currentUserID,
                ),
              )
              .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
          : [];

        setMessagesIfChanged(sortChatMessagesByCreatedAt(normalized));
        touchConversation();
        return;
      }

      const issueURL = new URL(`${API_URL}/api/issues/${issueID}`);
      issueURL.searchParams.set("org_id", orgID);
      const agentsURL = new URL(`${API_URL}/api/agents`);
      agentsURL.searchParams.set("org_id", orgID);

      const [issueResponse, agentsResponse] = await Promise.all([
        fetch(issueURL.toString(), { headers: requestHeaders, cache: "no-store" }),
        fetch(agentsURL.toString(), { headers: requestHeaders, cache: "no-store" }),
      ]);

      if (!issueResponse.ok) {
        const payload = await issueResponse.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to fetch issue chat");
      }

      const issuePayload = (await issueResponse.json()) as IssueDetailResponse;
      const agentsPayload = agentsResponse.ok
        ? ((await agentsResponse.json()) as AgentsResponse)
        : { agents: [] };

      const agentMap = new Map<string, string>();
      for (const agent of agentsPayload.agents ?? []) {
        if (agent.id && agent.name) {
          agentMap.set(agent.id, agent.name);
        }
      }
      setIssueAgentNameByID(agentMap);

      const participants = Array.isArray(issuePayload.participants)
        ? issuePayload.participants.filter((entry) => !entry.removed_at)
        : [];
      const owner = participants.find((entry) => entry.role === "owner")?.agent_id;
      const defaultAuthor = owner || participants[0]?.agent_id || "";
      setIssueAuthorID((current) => current || defaultAuthor);

      const normalized = Array.isArray(issuePayload.comments)
        ? issuePayload.comments
            .map((entry) => normalizeIssueComment(entry, conversationKey, agentMap, defaultAuthor))
            .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
        : [];
      setMessagesIfChanged(sortChatMessagesByCreatedAt(normalized));
      touchConversation();
    } catch (loadError) {
      if (!silent) {
        setError(loadError instanceof Error ? loadError.message : "Failed to load chat");
        setMessages([]);
      }
    } finally {
      if (!silent) {
        setLoading(false);
      }
    }
  }, [
    conversationKey,
    conversationType,
    currentUserID,
    currentUserName,
    dmThreadID,
    issueID,
    orgID,
    projectID,
    requestHeaders,
    touchConversation,
    setMessagesIfChanged,
  ]);

  useEffect(() => {
    void loadConversation();
  }, [loadConversation, refreshVersion]);

  useEffect(() => {
    clearPostSendRefreshTimers();
    setQueuedAttachments([]);
    setUploadingAttachments(false);
    setIsDragActive(false);
    return () => {
      clearPostSendRefreshTimers();
    };
  }, [clearPostSendRefreshTimers, conversationKey]);

  useEffect(() => {
    const refresh = () => {
      if (typeof document !== "undefined" && document.visibilityState !== "visible") {
        return;
      }
      void loadConversation({ silent: true });
    };

    const intervalID = window.setInterval(
      refresh,
      connected ? PERIODIC_REFRESH_CONNECTED_MS : PERIODIC_REFRESH_DEGRADED_MS,
    );
    const onFocus = () => refresh();
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        refresh();
      }
    };

    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibilityChange);

    return () => {
      window.clearInterval(intervalID);
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [connected, conversationKey, loadConversation]);

  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    if (conversationType === "dm" && lastMessage.type === "DMMessageReceived") {
      const dmEvent = extractDMRealtimeEvent(lastMessage.data);
      if (!dmEvent) {
        return;
      }
      if (dmEvent.threadID !== dmThreadID) {
        return;
      }

      const normalized = normalizeThreadMessage(
        dmEvent.message,
        dmThreadID,
        currentUserName,
        currentUserID,
      );
      if (!normalized) {
        void loadConversation({ silent: true });
        return;
      }

      if (normalized.senderType === "agent") {
        clearPostSendRefreshTimers();
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
      }

      setMessages((prev) => {
        if (prev.some((entry) => entry.id === normalized.id)) {
          return prev;
        }
        return [...prev, normalized];
      });
      return;
    }

    if (conversationType === "project" && lastMessage.type === "ProjectChatMessageCreated") {
      const realtimeMessage = extractProjectRealtimeMessage(lastMessage.data);
      if (!realtimeMessage) {
        return;
      }
      const normalized = normalizeProjectMessage(
        realtimeMessage,
        projectID,
        conversationKey,
        currentUserName,
        currentUserID,
      );
      if (!normalized) {
        void loadConversation({ silent: true });
        return;
      }

      if (normalized.senderType === "agent") {
        clearPostSendRefreshTimers();
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
      }
      if (normalized.isSessionReset) {
        setDeliveryIndicator({ tone: "neutral", text: "Started new session" });
      }

      setMessages((prev) => {
        if (prev.some((entry) => entry.id === normalized.id)) {
          return prev;
        }
        return sortChatMessagesByCreatedAt([...prev, normalized]);
      });
      return;
    }

    if (conversationType === "issue" && lastMessage.type === "IssueCommentCreated") {
      const issueEvent = extractIssueCommentRealtimeEvent(lastMessage.data);
      if (!issueEvent) {
        return;
      }
      if (issueEvent.issueID !== issueID) {
        return;
      }

      const normalized = normalizeIssueComment(
        issueEvent.comment,
        conversationKey,
        issueAgentNameByID,
        issueAuthorID,
      );
      if (!normalized) {
        void loadConversation({ silent: true });
        return;
      }

      if (normalized.senderType === "agent") {
        clearPostSendRefreshTimers();
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
      }

      setMessages((prev) => {
        if (prev.some((entry) => entry.id === normalized.id)) {
          return prev;
        }
        return sortChatMessagesByCreatedAt([...prev, normalized]);
      });
    }
  }, [
    conversationKey,
    conversationType,
    currentUserID,
    currentUserName,
    dmThreadID,
    issueID,
    projectID,
    issueAgentNameByID,
    issueAuthorID,
    lastMessage,
    clearPostSendRefreshTimers,
    loadConversation,
  ]);

  const uploadSelectedFiles = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    if (!orgID) {
      setError("Missing organization context");
      return;
    }

    setError(null);
    setUploadingAttachments(true);
    setDeliveryIndicator({ tone: "neutral", text: "Uploading attachments..." });

    let uploadedCount = 0;
    for (const file of files) {
      try {
        const formData = new FormData();
        formData.append("org_id", orgID);
        formData.append("file", file);

        const headers: Record<string, string> = {};
        if (token) {
          headers.Authorization = `Bearer ${token}`;
        }

        const response = await fetch(`${API_URL}/api/messages/attachments`, {
          method: "POST",
          headers,
          body: formData,
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? `Failed to upload ${file.name}`);
        }

        const payload = await response.json();
        const normalized = normalizeMessageAttachments([payload?.attachment])[0];
        if (!normalized) {
          throw new Error(`Invalid upload response for ${file.name}`);
        }
        uploadedCount += 1;
        setQueuedAttachments((prev) => {
          if (prev.some((attachment) => attachment.id === normalized.id)) {
            return prev;
          }
          return [...prev, normalized];
        });
      } catch (uploadError) {
        setError(uploadError instanceof Error ? uploadError.message : `Failed to upload ${file.name}`);
      }
    }

    if (uploadedCount > 0) {
      const suffix = uploadedCount === 1 ? "" : "s";
      setDeliveryIndicator({
        tone: "neutral",
        text: `${uploadedCount} attachment${suffix} ready`,
      });
    }
    setUploadingAttachments(false);
  }, [orgID, token]);

  const queueFilesFromInput = useCallback((input: FileList | File[] | null | undefined) => {
    const files = Array.from(input ?? []);
    if (files.length === 0) {
      return;
    }
    void uploadSelectedFiles(files);
  }, [uploadSelectedFiles]);

  const onAttachmentInputChange = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    queueFilesFromInput(event.target.files);
    event.target.value = "";
  }, [queueFilesFromInput]);

  const onComposerPaste = useCallback((event: ClipboardEvent<HTMLTextAreaElement>) => {
    const clipboard = event.clipboardData;
    if (!clipboard) {
      return;
    }
    const pastedFiles: File[] = [];
    for (const item of Array.from(clipboard.items)) {
      if (item.kind !== "file") {
        continue;
      }
      const file = item.getAsFile();
      if (file) {
        pastedFiles.push(file);
      }
    }
    if (pastedFiles.length > 0) {
      event.preventDefault();
      queueFilesFromInput(pastedFiles);
    }
  }, [queueFilesFromInput]);

  const onContainerDragOver = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setIsDragActive(true);
  }, []);

  const onContainerDragLeave = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setIsDragActive(false);
  }, []);

  const onContainerDrop = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setIsDragActive(false);
    queueFilesFromInput(event.dataTransfer.files);
  }, [queueFilesFromInput]);

  const onOpenFilePicker = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const onRemoveQueuedAttachment = useCallback((attachmentID: string) => {
    setQueuedAttachments((prev) => prev.filter((attachment) => attachment.id !== attachmentID));
  }, []);

  const sendMessage = useCallback(async (
    bodyOverride?: string,
    retryMessageID?: string,
    attachmentOverride?: ChatAttachment[],
  ) => {
    const body = (bodyOverride ?? draft).trim();
    const attachmentsForSend = attachmentOverride ?? queuedAttachments;
    if ((body === "" && attachmentsForSend.length === 0) || sending || !orgID) {
      return;
    }

    const isRetry = typeof retryMessageID === "string" && retryMessageID.trim() !== "";

    setError(null);
    setSending(true);
    setDeliveryIndicator({ tone: "neutral", text: "Sending..." });
    clearPostSendRefreshTimers();

    const optimisticID = isRetry
      ? retryMessageID!.trim()
      : `temp-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const optimisticMessage: ChatMessage = {
      id: optimisticID,
      threadId: threadID,
      senderId: conversationType === "issue" ? issueAuthorID || currentUserID : currentUserID,
      senderName: conversationType === "issue"
        ? (issueAgentNameByID.get(issueAuthorID) ?? currentUserName)
        : currentUserName,
      senderType: "user",
      content: body,
      attachments: attachmentsForSend,
      createdAt: new Date().toISOString(),
      optimistic: true,
    };

    setMessages((prev) => {
      if (isRetry) {
        return prev.map((entry) =>
          entry.id === optimisticID
            ? {
                ...entry,
                optimistic: true,
                failed: false,
              }
            : entry,
        );
      }
      return [...prev, optimisticMessage];
    });
    if (!isRetry) {
      setDraft("");
    }
    touchConversation();

    try {
      if (conversationType === "dm") {
        const response = await fetch(`${API_URL}/api/messages`, {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            org_id: orgID,
            thread_id: dmThreadID,
            content: body,
            attachments: attachmentsForSend,
            sender_type: "user",
            sender_name: currentUserName,
            sender_id: currentUserID,
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to send message");
        }

        const payload = await response.json();
        const persisted = normalizeThreadMessage(
          payload.message,
          dmThreadID,
          currentUserName,
          currentUserID,
        );
        setMessages((prev) =>
          prev.map((entry) =>
            entry.id === optimisticID
              ? {
                  ...(persisted ?? entry),
                  optimistic: false,
                  failed: false,
                }
              : entry,
          ),
        );

        if (payload?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
          for (const delay of POST_SEND_REFRESH_DELAYS_MS) {
            const timerID = window.setTimeout(() => {
              void loadConversation({ silent: true });
            }, delay);
            postSendRefreshTimersRef.current.push(timerID);
          }
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
        if (!isRetry) {
          setQueuedAttachments([]);
        }
      } else if (conversationType === "project") {
        const url = new URL(`${API_URL}/api/projects/${projectID}/chat/messages`);
        url.searchParams.set("org_id", orgID);

        const response = await fetch(url.toString(), {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            author: currentUserName,
            body,
            attachment_ids: attachmentsForSend.map((attachment) => attachment.id),
            sender_type: "user",
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to send project message");
        }

        const payload = await response.json();
        const persisted = normalizeProjectMessage(
          payload.message,
          projectID,
          conversationKey,
          currentUserName,
          currentUserID,
        );
        setMessages((prev) =>
          prev.map((entry) =>
            entry.id === optimisticID
              ? {
                  ...(persisted ?? entry),
                  optimistic: false,
                  failed: false,
                }
              : entry,
          ),
        );

        if (payload?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
          for (const delay of POST_SEND_REFRESH_DELAYS_MS) {
            const timerID = window.setTimeout(() => {
              void loadConversation({ silent: true });
            }, delay);
            postSendRefreshTimersRef.current.push(timerID);
          }
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
        if (!isRetry) {
          setQueuedAttachments([]);
        }
      } else {
        if (!issueAuthorID) {
          throw new Error("No issue author configured for this thread");
        }

        const attachmentLinks = buildAttachmentLinksMarkdown(attachmentsForSend);
        const issueBody = attachmentLinks === ""
          ? body
          : body === ""
            ? attachmentLinks
            : `${body}\n\n${attachmentLinks}`;

        const url = new URL(`${API_URL}/api/issues/${issueID}/comments`);
        url.searchParams.set("org_id", orgID);

        const response = await fetch(url.toString(), {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            author_agent_id: issueAuthorID,
            body: issueBody,
            sender_type: "user",
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to send issue message");
        }

        const payload = await response.json();
        const persisted = normalizeIssueComment(
          payload,
          conversationKey,
          issueAgentNameByID,
          issueAuthorID,
        );
        setMessages((prev) =>
          prev.map((entry) =>
            entry.id === optimisticID
              ? {
                  ...(persisted ?? entry),
                  optimistic: false,
                  failed: false,
                }
              : entry,
          ),
        );

        if (payload?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
          for (const delay of POST_SEND_REFRESH_DELAYS_MS) {
            const timerID = window.setTimeout(() => {
              void loadConversation({ silent: true });
            }, delay);
            postSendRefreshTimersRef.current.push(timerID);
          }
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
        if (!isRetry) {
          setQueuedAttachments([]);
        }
      }
    } catch (sendError) {
      setMessages((prev) =>
        prev.map((entry) =>
          entry.id === optimisticID
            ? {
                ...entry,
                optimistic: false,
                failed: true,
              }
            : entry,
        ),
      );
      setError(sendError instanceof Error ? sendError.message : "Failed to send message");
      setDeliveryIndicator({ tone: "warning", text: "Send failed; retry to resend" });
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  }, [
    conversationKey,
    conversationType,
    currentUserID,
    currentUserName,
    draft,
    dmThreadID,
    issueAgentNameByID,
    issueAuthorID,
    issueID,
    orgID,
    queuedAttachments,
    projectID,
    requestHeaders,
    sending,
    threadID,
    touchConversation,
    loadConversation,
    clearPostSendRefreshTimers,
  ]);

  const handleRetryMessage = useCallback(
    (message: DMMessage) => {
      const retryChatMessage = message as ChatMessage;
      void sendMessage(message.content, message.id, retryChatMessage.attachments ?? []);
    },
    [sendMessage],
  );

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void sendMessage();
  };

  const onDraftKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void sendMessage();
    }
  };

  const onDraftInput = () => {
    const textarea = inputRef.current;
    if (!textarea) {
      return;
    }
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 150)}px`;
  };

  const pseudoAgent = useMemo(
    () => ({
      id: conversationKey,
      name: conversationTitle,
      status: "online" as const,
      role: conversationContextLabel,
    }),
    [conversationContextLabel, conversationKey, conversationTitle],
  );

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="flex items-center gap-3 text-[var(--text-muted)]">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-[var(--border)] border-t-[var(--accent)]" />
          <span>Loading conversation...</span>
        </div>
      </div>
    );
  }

  return (
    <div
      className={`flex h-full min-h-0 flex-col overflow-hidden ${isDragActive ? "ring-2 ring-inset ring-[var(--accent)]" : ""}`}
      onDragOver={onContainerDragOver}
      onDragLeave={onContainerDragLeave}
      onDrop={onContainerDrop}
    >
      <MessageHistory
        messages={messages}
        currentUserId={conversationType === "issue" ? issueAuthorID || currentUserID : currentUserID}
        threadId={threadID}
        agent={pseudoAgent}
        onRetryMessage={handleRetryMessage}
      />

      {error ? (
        <div className="border-t border-[var(--red)]/40 bg-[var(--red)]/15 px-4 py-2">
          <p className="text-sm text-[var(--red)]">{error}</p>
        </div>
      ) : null}

      {deliveryIndicator ? (
        <div className="border-t border-[var(--border)]/70 px-4 py-1.5">
          <p className={`inline-flex rounded-full border px-2.5 py-0.5 text-[11px] ${deliveryIndicatorClass(deliveryIndicator)}`}>
            {deliveryIndicator.text}
          </p>
        </div>
      ) : null}

      {queuedAttachments.length > 0 ? (
        <div className="border-t border-[var(--border)]/70 px-4 py-2">
          <div className="flex flex-wrap gap-2">
            {queuedAttachments.map((attachment) => (
              <div
                key={attachment.id}
                className="inline-flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-1 text-xs text-[var(--text-muted)]"
              >
                <span className="max-w-[220px] truncate">{attachment.filename}</span>
                <span>{formatAttachmentSize(attachment.size_bytes)}</span>
                <button
                  type="button"
                  onClick={() => onRemoveQueuedAttachment(attachment.id)}
                  className="rounded-full px-1 text-[var(--text-muted)] transition hover:bg-[var(--border)] hover:text-[var(--text)]"
                  aria-label={`Remove ${attachment.filename}`}
                  disabled={sending}
                >
                  ×
                </button>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      <form onSubmit={onSubmit} className="flex items-end gap-3 border-t border-[var(--border)] px-4 py-3">
        <input
          ref={fileInputRef}
          type="file"
          multiple
          onChange={onAttachmentInputChange}
          className="hidden"
          aria-label="Attach files"
        />
        <button
          type="button"
          onClick={onOpenFilePicker}
          disabled={sending || uploadingAttachments || (conversationType === "issue" && issueAuthorID === "")}
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Attach files"
        >
          📎
        </button>
        <textarea
          ref={inputRef}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onInput={onDraftInput}
          onKeyDown={onDraftKeyDown}
          onPaste={onComposerPaste}
          placeholder={`Message ${conversationTitle}...`}
          rows={1}
          disabled={sending || uploadingAttachments || (conversationType === "issue" && issueAuthorID === "")}
          className="flex-1 resize-none rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-2.5 text-sm text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={
            sending ||
            uploadingAttachments ||
            (draft.trim() === "" && queuedAttachments.length === 0) ||
            (conversationType === "issue" && issueAuthorID === "")
          }
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-[var(--accent)] text-[#1A1918] transition hover:bg-[var(--accent-hover)] disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Send message"
        >
          {sending ? (
            <div className="h-4 w-4 animate-spin rounded-full border-2 border-[#1A1918]/30 border-t-[#1A1918]" />
          ) : (
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 20 20"
              fill="currentColor"
              className="h-5 w-5"
            >
              <path d="M3.105 2.288a.75.75 0 0 0-.826.95l1.414 4.926A1.5 1.5 0 0 0 5.135 9.25h6.115a.75.75 0 0 1 0 1.5H5.135a1.5 1.5 0 0 0-1.442 1.086l-1.414 4.926a.75.75 0 0 0 .826.95 28.897 28.897 0 0 0 15.293-7.155.75.75 0 0 0 0-1.114A28.897 28.897 0 0 0 3.105 2.288Z" />
            </svg>
          )}
        </button>
      </form>

      <div className="border-t border-[var(--border)]/70 bg-[var(--surface-alt)]/40 px-4 py-1.5">
        <p className="text-[10px] text-[var(--text-muted)]">
          Press <span className="font-medium">Enter</span> to send, <span className="font-medium">Shift + Enter</span> for a new line
        </p>
      </div>
    </div>
  );
}
