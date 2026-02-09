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
import { API_URL } from "../../lib/api";
import MessageHistory from "./MessageHistory";
import AgentStatusIndicator from "./AgentStatusIndicator";
import type { Agent, DMMessage, PaginationInfo } from "./types";
import { getInitials } from "./utils";

export type DMConversationViewProps = {
  agent: Agent;
  threadId?: string;
  currentUserId?: string;
  currentUserName?: string;
  onSendMessage?: (content: string) => Promise<void>;
  apiEndpoint?: string;
  pageSize?: number;
  className?: string;
};

type DeliveryIndicatorTone = "neutral" | "success" | "warning";

type DeliveryIndicator = {
  tone: DeliveryIndicatorTone;
  text: string;
};

function getStoredAuthContext() {
  if (typeof window === "undefined") {
    return { token: "", orgId: "" };
  }

  const token = (window.localStorage.getItem("otter_camp_token") ?? "").trim();
  const orgId = (window.localStorage.getItem("otter-camp-org-id") ?? "").trim();
  return { token, orgId };
}

function buildRequestHeaders({
  token,
  includeJSONContentType = false,
}: {
  token: string;
  includeJSONContentType?: boolean;
}): HeadersInit {
  const headers: Record<string, string> = {};

  if (includeJSONContentType) {
    headers["Content-Type"] = "application/json";
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  return headers;
}

async function toResponseError(
  response: Response,
  fallbackMessage: string,
): Promise<Error> {
  const contentType = response.headers.get("Content-Type") ?? "";
  if (contentType.includes("application/json")) {
    const payload = (await response.json().catch(() => null)) as
      | { error?: string; message?: string }
      | null;
    const message = payload?.error ?? payload?.message;
    if (message) {
      return new Error(message);
    }
  }

  return new Error(fallbackMessage);
}

function getNonEmptyString(value: unknown): string | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

function normalizeSenderType(value: unknown): "user" | "agent" | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const normalized = value.trim().toLowerCase();
  if (normalized === "user" || normalized === "agent") {
    return normalized;
  }
  return undefined;
}

function normalizeMessage(
  raw: unknown,
  defaults: {
    threadId: string;
    currentUserId: string;
    currentUserName: string;
    agentId: string;
    agentName: string;
  },
): DMMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }

  const message = raw as Record<string, unknown>;
  const senderId =
    getNonEmptyString(message.senderId) ??
    getNonEmptyString(message.sender_id) ??
    "";

  const senderType =
    normalizeSenderType(message.senderType) ??
    normalizeSenderType(message.sender_type) ??
    (senderId === defaults.agentId ? "agent" : "user");
  const senderName =
    getNonEmptyString(message.senderName) ??
    getNonEmptyString(message.sender_name) ??
    (senderType === "agent" ? defaults.agentName : "User");

  return {
    id:
      getNonEmptyString(message.id) ??
      `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    threadId:
      getNonEmptyString(message.threadId) ??
      getNonEmptyString(message.thread_id) ??
      defaults.threadId,
    senderId:
      senderId ||
      (senderType === "agent" ? defaults.agentId : defaults.currentUserId),
    senderName,
    senderType,
    senderAvatarUrl:
      getNonEmptyString(message.senderAvatarUrl) ??
      getNonEmptyString(message.sender_avatar_url),
    content:
      getNonEmptyString(message.content) ??
      (typeof message.content === "string" ? message.content : ""),
    createdAt:
      getNonEmptyString(message.createdAt) ??
      getNonEmptyString(message.created_at) ??
      new Date().toISOString(),
  };
}

function AgentAvatar({
  agent,
  size = "md",
  showStatus = true,
}: {
  agent: Agent;
  size?: "sm" | "md" | "lg";
  showStatus?: boolean;
}) {
  const sizeStyles = {
    sm: "h-8 w-8 text-xs",
    md: "h-10 w-10 text-sm",
    lg: "h-12 w-12 text-base",
  };

  return (
    <div className="relative">
      {agent.avatarUrl ? (
        <img
          src={agent.avatarUrl}
          alt={agent.name}
          loading="lazy"
          decoding="async"
          className={`${sizeStyles[size]} rounded-full object-cover ring-2 ring-[var(--accent)]/30`}
        />
      ) : (
        <div
          className={`${sizeStyles[size]} flex items-center justify-center rounded-full bg-[var(--accent)]/20 font-semibold text-[var(--accent)] ring-2 ring-[var(--accent)]/30`}
        >
          {getInitials(agent.name)}
        </div>
      )}

      {showStatus ? (
        <AgentStatusIndicator
          status={agent.status}
          size="sm"
          className="absolute bottom-0 right-0 border-2 border-[var(--surface)]"
        />
      ) : null}
    </div>
  );
}

export default function DMConversationView({
  agent,
  threadId,
  currentUserId = "current-user",
  currentUserName = "You",
  onSendMessage,
  apiEndpoint = `${API_URL}/api/messages`,
  pageSize = 20,
  className = "",
}: DMConversationViewProps) {
  const computedThreadId = useMemo(() => {
    if (threadId) return threadId;
    return `dm_${agent.id}`;
  }, [agent.id, threadId]);

  const [messages, setMessages] = useState<DMMessage[]>([]);
  const [inputValue, setInputValue] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deliveryIndicator, setDeliveryIndicator] = useState<DeliveryIndicator | null>(null);
  const [pagination, setPagination] = useState<PaginationInfo>({
    hasMore: false,
  });

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const { lastMessage, sendMessage: wsSend } = useWS();
  const messageDefaults = useMemo(
    () => ({
      threadId: computedThreadId,
      currentUserId,
      currentUserName,
      agentId: agent.id,
      agentName: agent.name,
    }),
    [agent.id, agent.name, computedThreadId, currentUserId, currentUserName],
  );

  const fetchMessages = useCallback(
    async (cursor?: string) => {
      const { token, orgId } = getStoredAuthContext();
      const params = new URLSearchParams({
        thread_id: computedThreadId,
        limit: String(pageSize),
      });
      if (cursor) {
        params.set("cursor", cursor);
      }
      if (orgId) {
        params.set("org_id", orgId);
      }

      const response = await fetch(`${apiEndpoint}?${params}`, {
        headers: buildRequestHeaders({ token }),
      });
      if (!response.ok) {
        throw await toResponseError(response, "Failed to fetch messages");
      }

      const data = await response.json();
      const rawMessages: unknown[] = Array.isArray(data.messages) ? data.messages : [];
      const normalizedMessages = rawMessages
        .map((message: unknown) => normalizeMessage(message, messageDefaults))
        .filter((message): message is DMMessage => message !== null);
      return {
        messages: normalizedMessages,
        pagination: {
          hasMore: data.hasMore ?? false,
          nextCursor: data.nextCursor,
          totalCount: data.totalCount,
        } as PaginationInfo,
      };
    },
    [apiEndpoint, computedThreadId, messageDefaults, pageSize],
  );

  useEffect(() => {
    let isActive = true;

    const loadInitialMessages = async () => {
      setIsLoading(true);
      setError(null);
      setDeliveryIndicator(null);
      setMessages([]);
      setPagination({ hasMore: false });

      try {
        const result = await fetchMessages();
        if (!isActive) return;
        setMessages(result.messages);
        setPagination(result.pagination);
      } catch (err) {
        if (!isActive) return;
        setError(err instanceof Error ? err.message : "Failed to load messages");
      } finally {
        if (isActive) {
          setIsLoading(false);
        }
      }
    };

    loadInitialMessages();

    return () => {
      isActive = false;
    };
  }, [fetchMessages]);

  const handleLoadMore = useCallback(async () => {
    if (!pagination.hasMore || isLoadingMore) {
      return;
    }

    setIsLoadingMore(true);
    try {
      const result = await fetchMessages(pagination.nextCursor);
      setMessages((prev) => [...result.messages, ...prev]);
      setPagination(result.pagination);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load more messages");
    } finally {
      setIsLoadingMore(false);
    }
  }, [fetchMessages, isLoadingMore, pagination.hasMore, pagination.nextCursor]);

  useEffect(() => {
    if (!lastMessage) return;
    if (lastMessage.type !== "DMMessageReceived") return;

    const data = lastMessage.data as {
      threadId?: string;
      thread_id?: string;
      message?: unknown;
    };

    const eventThreadID = data.threadId ?? data.thread_id;
    if (eventThreadID !== computedThreadId || !data.message) return;
    const nextMessage = normalizeMessage(data.message, messageDefaults);
    if (!nextMessage) return;

    if (nextMessage.senderType === "agent") {
      setDeliveryIndicator({ tone: "success", text: "Agent replied" });
    }

    setMessages((prev) => {
      if (prev.some((m) => m.id === nextMessage.id)) {
        return prev;
      }
      return [...prev, nextMessage];
    });
  }, [computedThreadId, lastMessage, messageDefaults]);

  const handleSend = useCallback(async () => {
    const content = inputValue.trim();
    if (!content || isSending) return;

    setIsSending(true);
    setError(null);
    setDeliveryIndicator({ tone: "neutral", text: "Sending..." });

    const optimisticMessage: DMMessage = {
      id: `temp-${Date.now()}`,
      threadId: computedThreadId,
      senderId: currentUserId,
      senderName: currentUserName,
      senderType: "user",
      content,
      createdAt: new Date().toISOString(),
    };

    setMessages((prev) => [...prev, optimisticMessage]);
    setInputValue("");

    try {
      if (onSendMessage) {
        await onSendMessage(content);
      } else {
        const { token, orgId } = getStoredAuthContext();
        const response = await fetch(apiEndpoint, {
          method: "POST",
          headers: buildRequestHeaders({ token, includeJSONContentType: true }),
          body: JSON.stringify({
            thread_id: computedThreadId,
            content,
            sender_id: currentUserId,
            sender_type: "user",
            sender_name: currentUserName,
            ...(orgId ? { org_id: orgId } : {}),
          }),
        });

        if (!response.ok) {
          throw await toResponseError(response, "Failed to send message");
        }

        const data = await response.json();
        const serverMessage = normalizeMessage(data.message, messageDefaults);
        setMessages((prev) =>
          prev.map((m) =>
            m.id === optimisticMessage.id
              ? serverMessage ?? m
              : m,
          ),
        );

        if (typeof data?.delivery?.error === "string" && data.delivery.error.trim()) {
          setError(data.delivery.error.trim());
        }

        if (data?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
        } else if (typeof data?.delivery?.error === "string" && data.delivery.error.trim()) {
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
      }

      wsSend({
        type: "DMMessageReceived",
        data: { threadId: computedThreadId, message: optimisticMessage },
      });
    } catch (err) {
      setMessages((prev) => prev.filter((m) => m.id !== optimisticMessage.id));
      setError(err instanceof Error ? err.message : "Failed to send message");
      setDeliveryIndicator({ tone: "warning", text: "Send failed; not saved" });
      setInputValue(content);
    } finally {
      setIsSending(false);
      inputRef.current?.focus();
    }
  }, [
    apiEndpoint,
    computedThreadId,
    currentUserId,
    currentUserName,
    messageDefaults,
    inputValue,
    isSending,
    onSendMessage,
    wsSend,
  ]);

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    handleSend();
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      handleSend();
    }
  };

  const handleInput = () => {
    const textarea = inputRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 150)}px`;
  };

  const statusTextClassName = useMemo(() => {
    if (agent.status === "online") return "text-[var(--accent)]";
    if (agent.status === "busy") return "text-[var(--orange)]";
    return "text-[var(--text-muted)]";
  }, [agent.status]);

  const deliveryIndicatorClassName = useMemo(() => {
    if (!deliveryIndicator) {
      return "";
    }
    if (deliveryIndicator.tone === "success") {
      return "border-[var(--green)]/40 bg-[var(--green)]/15 text-[var(--green)]";
    }
    if (deliveryIndicator.tone === "warning") {
      return "border-[var(--orange)]/40 bg-[var(--orange)]/15 text-[var(--orange)]";
    }
    return "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]";
  }, [deliveryIndicator]);

  if (isLoading) {
    return (
      <div className="flex h-96 items-center justify-center rounded-2xl border border-[var(--border)] bg-[var(--surface)]">
        <div className="flex items-center gap-3 text-[var(--text-muted)]">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-[var(--border)] border-t-[var(--accent)]" />
          <span>Loading conversation...</span>
        </div>
      </div>
    );
  }

  const messageCount = pagination.totalCount ?? messages.length;

  return (
    <div
      className={`flex h-[500px] flex-col overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-xl ${className}`}
    >
      <div className="flex items-center gap-4 border-b border-[var(--border)] px-5 py-3">
        <AgentAvatar agent={agent} size="md" />
        <div className="flex-1">
          <h3 className="font-semibold text-[var(--text)]">{agent.name}</h3>
          <div className="flex items-center gap-2">
            {agent.role ? (
              <span className="text-xs text-[var(--text-muted)]">{agent.role}</span>
            ) : null}
            <span className={`text-xs capitalize ${statusTextClassName}`}>
              {agent.status}
            </span>
          </div>
        </div>
        <span className="rounded-full bg-[var(--surface-alt)] px-2 py-0.5 text-xs text-[var(--text-muted)]">
          {messageCount} {messageCount === 1 ? "message" : "messages"}
        </span>
      </div>

      <MessageHistory
        messages={messages}
        currentUserId={currentUserId}
        threadId={computedThreadId}
        agent={agent}
        hasMore={pagination.hasMore}
        isLoadingMore={isLoadingMore}
        onLoadMore={handleLoadMore}
      />

      {error ? (
        <div className="border-t border-[var(--red)]/40 bg-[var(--red)]/15 px-5 py-2">
          <p className="text-sm text-[var(--red)]">{error}</p>
        </div>
      ) : null}

      {deliveryIndicator ? (
        <div className="border-t border-[var(--border)]/70 px-5 py-1.5">
          <p className={`inline-flex rounded-full border px-2.5 py-0.5 text-[11px] ${deliveryIndicatorClassName}`}>
            {deliveryIndicator.text}
          </p>
        </div>
      ) : null}

      <form
        onSubmit={handleSubmit}
        className="flex items-end gap-3 border-t border-[var(--border)] px-5 py-4"
      >
        <textarea
          ref={inputRef}
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          onKeyDown={handleKeyDown}
          onInput={handleInput}
          placeholder={`Message ${agent.name}...`}
          rows={1}
          disabled={isSending}
          className="flex-1 resize-none rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-2.5 text-sm text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={!inputValue.trim() || isSending}
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-[var(--accent)] text-[#1A1918] transition hover:bg-[var(--accent-hover)] focus:outline-none focus:ring-2 focus:ring-[var(--accent)] focus:ring-offset-2 focus:ring-offset-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Send message"
        >
          {isSending ? (
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

      <div className="border-t border-[var(--border)]/70 bg-[var(--surface-alt)]/40 px-5 py-1.5">
        <p className="text-[10px] text-[var(--text-muted)]">
          Press <span className="font-medium">Enter</span> to send,{" "}
          <span className="font-medium">Shift + Enter</span> for a new line
        </p>
      </div>
    </div>
  );
}
