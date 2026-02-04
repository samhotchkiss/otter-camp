import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { useWS } from "../contexts/WebSocketContext";

/**
 * Agent status for the status indicator.
 */
export type AgentStatus = "online" | "busy" | "offline";

/**
 * Agent information for the DM header.
 */
export type Agent = {
  id: string;
  name: string;
  avatarUrl?: string;
  status: AgentStatus;
  role?: string;
};

/**
 * Message sender type - distinguishes between human users and AI agents.
 */
export type MessageSenderType = "user" | "agent";

/**
 * Represents a single message in a DM thread.
 */
export type DMMessage = {
  id: string;
  threadId: string;
  senderId: string;
  senderName: string;
  senderType: MessageSenderType;
  senderAvatarUrl?: string;
  content: string;
  createdAt: string;
};

/**
 * Pagination info returned from API.
 */
export type PaginationInfo = {
  hasMore: boolean;
  nextCursor?: string;
  totalCount?: number;
};

/**
 * Props for the AgentDM component.
 */
export type AgentDMProps = {
  agent: Agent;
  currentUserId?: string;
  currentUserName?: string;
  onSendMessage?: (content: string) => Promise<void>;
  apiEndpoint?: string;
  pageSize?: number;
};

/**
 * Format a timestamp for display.
 */
function formatTimestamp(isoString: string): string {
  const date = new Date(isoString);
  const now = new Date();
  const isToday = date.toDateString() === now.toDateString();
  
  const timeStr = date.toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
  
  if (isToday) {
    return timeStr;
  }
  
  const dateStr = date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
  
  return `${dateStr} ${timeStr}`;
}

/**
 * Get initials from a name for avatar fallback.
 */
function getInitials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/**
 * Status indicator dot component.
 */
function StatusIndicator({ status }: { status: AgentStatus }) {
  const statusStyles: Record<AgentStatus, string> = {
    online: "bg-emerald-500 shadow-emerald-500/50",
    busy: "bg-amber-500 shadow-amber-500/50",
    offline: "bg-slate-500",
  };

  return (
    <span
      className={`absolute bottom-0 right-0 h-3 w-3 rounded-full border-2 border-slate-900 shadow-lg ${statusStyles[status]}`}
      title={status.charAt(0).toUpperCase() + status.slice(1)}
    />
  );
}

/**
 * Agent avatar component with status indicator.
 */
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
          className={`${sizeStyles[size]} rounded-full object-cover ring-2 ring-emerald-500/30`}
        />
      ) : (
        <div
          className={`${sizeStyles[size]} flex items-center justify-center rounded-full bg-emerald-500/20 font-semibold text-emerald-300 ring-2 ring-emerald-500/30`}
        >
          {getInitials(agent.name)}
        </div>
      )}
      {showStatus && <StatusIndicator status={agent.status} />}
    </div>
  );
}

/**
 * Message avatar component for chat bubbles.
 */
function MessageAvatar({
  name,
  avatarUrl,
  senderType,
}: {
  name: string;
  avatarUrl?: string;
  senderType: MessageSenderType;
}) {
  const bgColor =
    senderType === "agent"
      ? "bg-emerald-500/20 text-emerald-300"
      : "bg-sky-500/20 text-sky-300";

  if (avatarUrl) {
    return (
      <img
        src={avatarUrl}
        alt={name}
        className="h-8 w-8 rounded-full object-cover ring-2 ring-slate-700"
      />
    );
  }

  return (
    <div
      className={`flex h-8 w-8 items-center justify-center rounded-full text-xs font-semibold ${bgColor}`}
    >
      {senderType === "agent" ? "🤖" : getInitials(name)}
    </div>
  );
}

/**
 * Single message bubble component.
 */
function MessageBubble({
  message,
  isOwnMessage,
}: {
  message: DMMessage;
  isOwnMessage: boolean;
}) {
  const bubbleStyle = isOwnMessage
    ? "bg-sky-600 text-white"
    : message.senderType === "agent"
    ? "bg-emerald-900/50 text-emerald-100 border border-emerald-700/50"
    : "bg-slate-800 text-slate-200";

  return (
    <div
      className={`flex gap-3 ${isOwnMessage ? "flex-row-reverse" : "flex-row"}`}
    >
      <MessageAvatar
        name={message.senderName}
        avatarUrl={message.senderAvatarUrl}
        senderType={message.senderType}
      />
      <div
        className={`flex max-w-[75%] flex-col ${isOwnMessage ? "items-end" : "items-start"}`}
      >
        <div className="mb-1 flex items-center gap-2">
          <span className="text-xs font-medium text-slate-400">
            {message.senderName}
          </span>
          {message.senderType === "agent" && (
            <span className="rounded-full bg-emerald-500/20 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-emerald-400">
              Agent
            </span>
          )}
        </div>
        <div className={`rounded-2xl px-4 py-2.5 ${bubbleStyle}`}>
          <p className="whitespace-pre-wrap text-sm leading-relaxed">
            {message.content}
          </p>
        </div>
        <span className="mt-1 text-[10px] text-slate-500">
          {formatTimestamp(message.createdAt)}
        </span>
      </div>
    </div>
  );
}

/**
 * Load more button for pagination.
 */
function LoadMoreButton({
  onClick,
  isLoading,
}: {
  onClick: () => void;
  isLoading: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={isLoading}
      className="mx-auto flex items-center gap-2 rounded-full bg-slate-800 px-4 py-1.5 text-xs text-slate-400 transition hover:bg-slate-700 hover:text-slate-300 disabled:opacity-50"
    >
      {isLoading ? (
        <>
          <div className="h-3 w-3 animate-spin rounded-full border-2 border-slate-600 border-t-sky-500" />
          Loading...
        </>
      ) : (
        <>
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 20 20"
            fill="currentColor"
            className="h-4 w-4"
          >
            <path
              fillRule="evenodd"
              d="M9.47 6.47a.75.75 0 0 1 1.06 0l4.25 4.25a.75.75 0 1 1-1.06 1.06L10 8.06l-3.72 3.72a.75.75 0 0 1-1.06-1.06l4.25-4.25Z"
              clipRule="evenodd"
            />
          </svg>
          Load earlier messages
        </>
      )}
    </button>
  );
}

/**
 * AgentDM - Direct message interface between user and agent.
 *
 * Features:
 * - Agent avatar, name, and status indicator in header
 * - Message history with pagination (load earlier)
 * - Real-time updates via WebSocket
 * - Auto-scroll to bottom on new messages
 * - Input box with send functionality
 */
export default function AgentDM({
  agent,
  currentUserId = "current-user",
  currentUserName = "You",
  onSendMessage,
  apiEndpoint = "/api/messages",
  pageSize = 20,
}: AgentDMProps) {
  const threadId = `dm_${agent.id}`;
  
  const [messages, setMessages] = useState<DMMessage[]>([]);
  const [inputValue, setInputValue] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [isLoadingMore, setIsLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pagination, setPagination] = useState<PaginationInfo>({
    hasMore: false,
  });

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const { lastMessage, sendMessage: wsSend } = useWS();

  // Scroll to bottom when messages change
  const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
    messagesEndRef.current?.scrollIntoView({ behavior });
  }, []);

  // Scroll to bottom on initial load and new messages
  useEffect(() => {
    if (!isLoading) {
      scrollToBottom();
    }
  }, [messages.length, isLoading, scrollToBottom]);

  // Fetch messages with optional cursor for pagination
  const fetchMessages = useCallback(
    async (cursor?: string) => {
      try {
        const params = new URLSearchParams({
          thread_id: threadId,
          limit: String(pageSize),
        });
        if (cursor) {
          params.set("cursor", cursor);
        }

        const response = await fetch(`${apiEndpoint}?${params}`);
        if (!response.ok) {
          throw new Error("Failed to fetch messages");
        }

        const data = await response.json();
        return {
          messages: (data.messages || []) as DMMessage[],
          pagination: {
            hasMore: data.hasMore ?? false,
            nextCursor: data.nextCursor,
            totalCount: data.totalCount,
          } as PaginationInfo,
        };
      } catch (err) {
        throw err instanceof Error ? err : new Error("Failed to load messages");
      }
    },
    [apiEndpoint, pageSize, threadId]
  );

  // Initial message fetch
  useEffect(() => {
    const loadInitialMessages = async () => {
      setIsLoading(true);
      try {
        const result = await fetchMessages();
        setMessages(result.messages);
        setPagination(result.pagination);
      } catch (err) {
        setError(
          err instanceof Error ? err.message : "Failed to load messages"
        );
      } finally {
        setIsLoading(false);
      }
    };

    loadInitialMessages();
  }, [fetchMessages]);

  // Load more (earlier) messages
  const handleLoadMore = useCallback(async () => {
    if (!pagination.hasMore || isLoadingMore) {
      return;
    }

    setIsLoadingMore(true);
    const container = messagesContainerRef.current;
    const scrollHeightBefore = container?.scrollHeight || 0;

    try {
      const result = await fetchMessages(pagination.nextCursor);
      setMessages((prev) => [...result.messages, ...prev]);
      setPagination(result.pagination);

      // Preserve scroll position after prepending messages
      requestAnimationFrame(() => {
        if (container) {
          const scrollHeightAfter = container.scrollHeight;
          container.scrollTop = scrollHeightAfter - scrollHeightBefore;
        }
      });
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load more messages"
      );
    } finally {
      setIsLoadingMore(false);
    }
  }, [fetchMessages, isLoadingMore, pagination.hasMore, pagination.nextCursor]);

  // Handle WebSocket messages for real-time updates
  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    // Handle new DM message events
    if (lastMessage.type === "DMMessageReceived") {
      const data = lastMessage.data as {
        threadId?: string;
        message?: DMMessage;
      };
      if (data.threadId === threadId && data.message) {
        setMessages((prev) => {
          // Avoid duplicates
          if (prev.some((m) => m.id === data.message!.id)) {
            return prev;
          }
          return [...prev, data.message!];
        });
      }
    }
  }, [lastMessage, threadId]);

  // Send a new message
  const handleSend = useCallback(async () => {
    const content = inputValue.trim();
    if (!content || isSending) {
      return;
    }

    setIsSending(true);
    setError(null);

    // Optimistic update
    const optimisticMessage: DMMessage = {
      id: `temp-${Date.now()}`,
      threadId,
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
        // Default: POST to API endpoint
        const response = await fetch(apiEndpoint, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ thread_id: threadId, content }),
        });

        if (!response.ok) {
          throw new Error("Failed to send message");
        }

        const data = await response.json();

        // Replace optimistic message with real one
        setMessages((prev) =>
          prev.map((m) =>
            m.id === optimisticMessage.id
              ? { ...m, ...data.message, id: data.message?.id || m.id }
              : m
          )
        );
      }

      // Notify via WebSocket for other clients
      wsSend({
        type: "DMMessageReceived",
        data: { threadId, message: optimisticMessage },
      });
    } catch (err) {
      // Remove optimistic message on error
      setMessages((prev) => prev.filter((m) => m.id !== optimisticMessage.id));
      setError(err instanceof Error ? err.message : "Failed to send message");
      setInputValue(content); // Restore input
    } finally {
      setIsSending(false);
      inputRef.current?.focus();
    }
  }, [
    apiEndpoint,
    currentUserId,
    currentUserName,
    inputValue,
    isSending,
    onSendMessage,
    threadId,
    wsSend,
  ]);

  // Handle form submission
  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    handleSend();
  };

  // Handle keyboard shortcuts (Cmd/Ctrl + Enter to send)
  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      handleSend();
    }
  };

  // Auto-resize textarea
  const handleInput = () => {
    const textarea = inputRef.current;
    if (textarea) {
      textarea.style.height = "auto";
      textarea.style.height = `${Math.min(textarea.scrollHeight, 150)}px`;
    }
  };

  if (isLoading) {
    return (
      <div className="flex h-96 items-center justify-center rounded-2xl border border-slate-800 bg-slate-900/95">
        <div className="flex items-center gap-3 text-slate-400">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-emerald-500" />
          <span>Loading conversation...</span>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-[500px] flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/95 shadow-xl">
      {/* Header with agent info */}
      <div className="flex items-center gap-4 border-b border-slate-800 px-5 py-3">
        <AgentAvatar agent={agent} size="md" />
        <div className="flex-1">
          <h3 className="font-semibold text-slate-200">{agent.name}</h3>
          <div className="flex items-center gap-2">
            {agent.role && (
              <span className="text-xs text-slate-500">{agent.role}</span>
            )}
            <span
              className={`text-xs capitalize ${
                agent.status === "online"
                  ? "text-emerald-400"
                  : agent.status === "busy"
                  ? "text-amber-400"
                  : "text-slate-500"
              }`}
            >
              {agent.status}
            </span>
          </div>
        </div>
        <span className="rounded-full bg-slate-800 px-2 py-0.5 text-xs text-slate-400">
          {pagination.totalCount ?? messages.length}{" "}
          {(pagination.totalCount ?? messages.length) === 1
            ? "message"
            : "messages"}
        </span>
      </div>

      {/* Messages area */}
      <div
        ref={messagesContainerRef}
        className="flex-1 overflow-y-auto px-5 py-4"
      >
        {/* Load more button */}
        {pagination.hasMore && (
          <div className="mb-4 flex justify-center">
            <LoadMoreButton
              onClick={handleLoadMore}
              isLoading={isLoadingMore}
            />
          </div>
        )}

        {messages.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-slate-500">
            <AgentAvatar agent={agent} size="lg" showStatus={false} />
            <p className="mt-3 text-sm">
              Start a conversation with {agent.name}
            </p>
          </div>
        ) : (
          <div className="space-y-4">
            {messages.map((message) => (
              <MessageBubble
                key={message.id}
                message={message}
                isOwnMessage={message.senderId === currentUserId}
              />
            ))}
            <div ref={messagesEndRef} />
          </div>
        )}
      </div>

      {/* Error banner */}
      {error && (
        <div className="border-t border-red-900/50 bg-red-950/50 px-5 py-2">
          <p className="text-sm text-red-400">{error}</p>
        </div>
      )}

      {/* Input area */}
      <form
        onSubmit={handleSubmit}
        className="flex items-end gap-3 border-t border-slate-800 px-5 py-4"
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
          className="flex-1 resize-none rounded-xl border border-slate-700 bg-slate-800 px-4 py-2.5 text-sm text-slate-200 placeholder:text-slate-500 focus:border-emerald-500 focus:outline-none focus:ring-1 focus:ring-emerald-500 disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={!inputValue.trim() || isSending}
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-600 text-white transition hover:bg-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 focus:ring-offset-slate-900 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {isSending ? (
            <div className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
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

      {/* Keyboard hint */}
      <div className="border-t border-slate-800/50 bg-slate-950/50 px-5 py-1.5">
        <p className="text-[10px] text-slate-600">
          Press <span className="font-medium">Cmd/Ctrl + Enter</span> to send
        </p>
      </div>
    </div>
  );
}
