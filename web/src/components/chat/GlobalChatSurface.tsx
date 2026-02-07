import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { useWS } from "../../contexts/WebSocketContext";
import type { DMMessage } from "../messaging/types";
import MessageHistory from "../messaging/MessageHistory";
import type {
  GlobalChatConversation,
  GlobalIssueConversation,
  GlobalProjectConversation,
} from "../../contexts/GlobalChatContext";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const ORG_STORAGE_KEY = "otter-camp-org-id";
const USER_NAME_STORAGE_KEY = "otter-camp-user-name";
const PROJECT_CHAT_SESSION_RESET_AUTHOR = "__otter_session__";
const PROJECT_CHAT_SESSION_RESET_PREFIX = "project_chat_session_reset:";

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

type ChatMessage = DMMessage & {
  optimistic?: boolean;
  failed?: boolean;
};

type GlobalChatSurfaceProps = {
  conversation: GlobalChatConversation;
  onConversationTouched?: () => void;
  refreshVersion?: number;
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
    createdAt: normalizeTimestamp(record.createdAt ?? record.created_at),
  };
}

function normalizeProjectMessage(
  raw: unknown,
  conversation: GlobalProjectConversation,
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
  if (!id || !projectID || !author || !body) {
    return null;
  }
  if (projectID !== conversation.projectId) {
    return null;
  }
  if (author === PROJECT_CHAT_SESSION_RESET_AUTHOR && body.startsWith(PROJECT_CHAT_SESSION_RESET_PREFIX)) {
    const sessionID = body.slice(PROJECT_CHAT_SESSION_RESET_PREFIX.length).trim();
    return {
      id,
      threadId: conversation.key,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.created_at),
      isSessionReset: true,
      sessionID: sessionID || undefined,
    };
  }

  const isUser = author.toLowerCase() === currentUserName.toLowerCase();
  return {
    id,
    threadId: conversation.key,
    senderId: isUser ? currentUserID : `agent:${author}`,
    senderName: isUser ? currentUserName : author,
    senderType: isUser ? "user" : "agent",
    content: body,
    createdAt: normalizeTimestamp(record.created_at),
  };
}

function normalizeIssueComment(
  raw: unknown,
  conversation: GlobalIssueConversation,
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
  if (!id || !authorID || !body) {
    return null;
  }

  const senderName = agentNameByID.get(authorID) ?? authorID;
  const isUser = authorAgentID !== "" && authorID === authorAgentID;

  return {
    id,
    threadId: conversation.key,
    senderId: authorID,
    senderName,
    senderType: isUser ? "user" : "agent",
    content: body,
    createdAt: normalizeTimestamp(record.created_at),
  };
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

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const { lastMessage } = useWS();

  const orgID = useMemo(() => getStoredOrgID(), [conversation.key]);
  const token = useMemo(() => getAuthToken(), [conversation.key]);
  const currentUserName = useMemo(() => getCurrentUserName(), [conversation.key]);
  const currentUserID = useMemo(() => `user:${currentUserName.toLowerCase()}`, [currentUserName]);

  const threadID = useMemo(() => {
    if (conversation.type === "dm") {
      return conversation.threadId;
    }
    return conversation.key;
  }, [conversation]);

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
    onConversationTouched?.();
  }, [onConversationTouched]);

  const loadConversation = useCallback(async () => {
    if (!orgID) {
      setMessages([]);
      setLoading(false);
      setError("Missing organization context");
      return;
    }

    setLoading(true);
    setError(null);
    setDeliveryIndicator(null);

    try {
      if (conversation.type === "dm") {
        const params = new URLSearchParams({
          org_id: orgID,
          thread_id: conversation.threadId,
          limit: "100",
        });
        const response = await fetch(`${API_URL}/api/messages?${params.toString()}`, {
          headers: requestHeaders,
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to fetch messages");
        }
        const payload = await response.json();
        const normalized = Array.isArray(payload.messages)
          ? payload.messages
              .map((entry: unknown) =>
                normalizeThreadMessage(entry, conversation.threadId, currentUserName, currentUserID),
              )
              .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
          : [];
        setMessages(normalized);
        touchConversation();
        return;
      }

      if (conversation.type === "project") {
        const url = new URL(`${API_URL}/api/projects/${conversation.projectId}/chat`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("limit", "100");

        const response = await fetch(url.toString(), {
          headers: requestHeaders,
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
                  conversation,
                  currentUserName,
                  currentUserID,
                ),
              )
              .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
          : [];

        normalized.sort((a: ChatMessage, b: ChatMessage) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
        setMessages(normalized);
        touchConversation();
        return;
      }

      const issueURL = new URL(`${API_URL}/api/issues/${conversation.issueId}`);
      issueURL.searchParams.set("org_id", orgID);
      const agentsURL = new URL(`${API_URL}/api/agents`);
      agentsURL.searchParams.set("org_id", orgID);

      const [issueResponse, agentsResponse] = await Promise.all([
        fetch(issueURL.toString(), { headers: requestHeaders }),
        fetch(agentsURL.toString(), { headers: requestHeaders }),
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
            .map((entry) => normalizeIssueComment(entry, conversation, agentMap, defaultAuthor))
            .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
        : [];
      normalized.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
      setMessages(normalized);
      touchConversation();
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Failed to load chat");
      setMessages([]);
    } finally {
      setLoading(false);
    }
  }, [
    conversation,
    currentUserID,
    currentUserName,
    orgID,
    requestHeaders,
    touchConversation,
  ]);

  useEffect(() => {
    void loadConversation();
  }, [loadConversation, refreshVersion]);

  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    if (conversation.type === "dm" && lastMessage.type === "DMMessageReceived") {
      if (!lastMessage.data || typeof lastMessage.data !== "object") {
        return;
      }
      const payload = lastMessage.data as Record<string, unknown>;
      const eventThreadID =
        (typeof payload.threadId === "string" && payload.threadId) ||
        (typeof payload.thread_id === "string" && payload.thread_id) ||
        "";
      if (eventThreadID !== conversation.threadId) {
        return;
      }

      const normalized = normalizeThreadMessage(
        payload.message ?? payload,
        conversation.threadId,
        currentUserName,
        currentUserID,
      );
      if (!normalized) {
        return;
      }

      if (normalized.senderType === "agent") {
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

    if (conversation.type === "project" && lastMessage.type === "ProjectChatMessageCreated") {
      const normalized = normalizeProjectMessage(
        lastMessage.data,
        conversation,
        currentUserName,
        currentUserID,
      );
      if (!normalized) {
        return;
      }

      if (normalized.senderType === "agent") {
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
      }
      if (normalized.isSessionReset) {
        setDeliveryIndicator({ tone: "neutral", text: "Started new session" });
      }

      setMessages((prev) => {
        if (prev.some((entry) => entry.id === normalized.id)) {
          return prev;
        }
        return [...prev, normalized].sort(
          (a: ChatMessage, b: ChatMessage) => Date.parse(a.createdAt) - Date.parse(b.createdAt),
        );
      });
      return;
    }

    if (conversation.type === "issue" && lastMessage.type === "IssueCommentCreated") {
      if (!lastMessage.data || typeof lastMessage.data !== "object") {
        return;
      }
      const payload = lastMessage.data as Record<string, unknown>;
      const issueID =
        (typeof payload.issue_id === "string" && payload.issue_id) ||
        (typeof payload.issueId === "string" && payload.issueId) ||
        "";
      if (issueID !== conversation.issueId) {
        return;
      }
      const comment =
        payload.comment && typeof payload.comment === "object"
          ? payload.comment
          : null;
      if (!comment) {
        return;
      }

      const normalized = normalizeIssueComment(
        comment,
        conversation,
        issueAgentNameByID,
        issueAuthorID,
      );
      if (!normalized) {
        return;
      }

      if (normalized.senderType === "agent") {
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
      }

      setMessages((prev) => {
        if (prev.some((entry) => entry.id === normalized.id)) {
          return prev;
        }
        return [...prev, normalized].sort(
          (a: ChatMessage, b: ChatMessage) => Date.parse(a.createdAt) - Date.parse(b.createdAt),
        );
      });
    }
  }, [
    conversation,
    currentUserID,
    currentUserName,
    issueAgentNameByID,
    issueAuthorID,
    lastMessage,
  ]);

  const sendMessage = useCallback(async (bodyOverride?: string, retryMessageID?: string) => {
    const body = (bodyOverride ?? draft).trim();
    if (!body || sending || !orgID) {
      return;
    }

    const isRetry = typeof retryMessageID === "string" && retryMessageID.trim() !== "";

    setError(null);
    setSending(true);
    setDeliveryIndicator({ tone: "neutral", text: "Sending..." });

    const optimisticID = isRetry
      ? retryMessageID!.trim()
      : `temp-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const optimisticMessage: ChatMessage = {
      id: optimisticID,
      threadId: threadID,
      senderId: conversation.type === "issue" ? issueAuthorID || currentUserID : currentUserID,
      senderName: conversation.type === "issue"
        ? (issueAgentNameByID.get(issueAuthorID) ?? currentUserName)
        : currentUserName,
      senderType: "user",
      content: body,
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
      if (conversation.type === "dm") {
        const response = await fetch(`${API_URL}/api/messages`, {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            org_id: orgID,
            thread_id: conversation.threadId,
            content: body,
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
          conversation.threadId,
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
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
      } else if (conversation.type === "project") {
        const url = new URL(`${API_URL}/api/projects/${conversation.projectId}/chat/messages`);
        url.searchParams.set("org_id", orgID);

        const response = await fetch(url.toString(), {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            author: currentUserName,
            body,
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
          conversation,
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
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
      } else {
        if (!issueAuthorID) {
          throw new Error("No issue author configured for this thread");
        }

        const url = new URL(`${API_URL}/api/issues/${conversation.issueId}/comments`);
        url.searchParams.set("org_id", orgID);

        const response = await fetch(url.toString(), {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            author_agent_id: issueAuthorID,
            body,
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
          conversation,
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
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
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
    conversation,
    currentUserID,
    currentUserName,
    draft,
    issueAgentNameByID,
    issueAuthorID,
    orgID,
    requestHeaders,
    sending,
    threadID,
    touchConversation,
  ]);

  const handleRetryMessage = useCallback(
    (message: DMMessage) => {
      void sendMessage(message.content, message.id);
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
      id: conversation.key,
      name: conversation.title,
      status: "online" as const,
      role: conversation.contextLabel,
    }),
    [conversation.contextLabel, conversation.key, conversation.title],
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
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <MessageHistory
        messages={messages}
        currentUserId={conversation.type === "issue" ? issueAuthorID || currentUserID : currentUserID}
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

      <form onSubmit={onSubmit} className="flex items-end gap-3 border-t border-[var(--border)] px-4 py-3">
        <textarea
          ref={inputRef}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onInput={onDraftInput}
          onKeyDown={onDraftKeyDown}
          placeholder={`Message ${conversation.title}...`}
          rows={1}
          disabled={sending || (conversation.type === "issue" && issueAuthorID === "")}
          className="flex-1 resize-none rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-2.5 text-sm text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={sending || draft.trim() === "" || (conversation.type === "issue" && issueAuthorID === "")}
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
