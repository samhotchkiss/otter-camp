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
          className={`${sizeStyles[size]} rounded-full object-cover ring-2 ring-emerald-500/30`}
        />
      ) : (
        <div
          className={`${sizeStyles[size]} flex items-center justify-center rounded-full bg-emerald-500/20 font-semibold text-emerald-300 ring-2 ring-emerald-500/30`}
        >
          {getInitials(agent.name)}
        </div>
      )}

      {showStatus ? (
        <AgentStatusIndicator
          status={agent.status}
          size="sm"
          className="absolute bottom-0 right-0 border-2 border-slate-900"
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
  apiEndpoint = "/api/messages",
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
  const [pagination, setPagination] = useState<PaginationInfo>({
    hasMore: false,
  });

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const { lastMessage, sendMessage: wsSend } = useWS();

  const fetchMessages = useCallback(
    async (cursor?: string) => {
      const params = new URLSearchParams({
        thread_id: computedThreadId,
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
    },
    [apiEndpoint, computedThreadId, pageSize],
  );

  useEffect(() => {
    let isActive = true;

    const loadInitialMessages = async () => {
      setIsLoading(true);
      setError(null);
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
      message?: DMMessage;
    };

    if (data.threadId !== computedThreadId || !data.message) return;

    setMessages((prev) => {
      if (prev.some((m) => m.id === data.message!.id)) {
        return prev;
      }
      return [...prev, data.message!];
    });
  }, [computedThreadId, lastMessage]);

  const handleSend = useCallback(async () => {
    const content = inputValue.trim();
    if (!content || isSending) return;

    setIsSending(true);
    setError(null);

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
        const response = await fetch(apiEndpoint, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ thread_id: computedThreadId, content }),
        });

        if (!response.ok) {
          throw new Error("Failed to send message");
        }

        const data = await response.json();
        setMessages((prev) =>
          prev.map((m) =>
            m.id === optimisticMessage.id
              ? { ...m, ...data.message, id: data.message?.id || m.id }
              : m,
          ),
        );
      }

      wsSend({
        type: "DMMessageReceived",
        data: { threadId: computedThreadId, message: optimisticMessage },
      });
    } catch (err) {
      setMessages((prev) => prev.filter((m) => m.id !== optimisticMessage.id));
      setError(err instanceof Error ? err.message : "Failed to send message");
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
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
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
    if (agent.status === "online") return "text-emerald-400";
    if (agent.status === "busy") return "text-amber-400";
    return "text-slate-500";
  }, [agent.status]);

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

  const messageCount = pagination.totalCount ?? messages.length;

  return (
    <div
      className={`flex h-[500px] flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/95 shadow-xl ${className}`}
    >
      <div className="flex items-center gap-4 border-b border-slate-800 px-5 py-3">
        <AgentAvatar agent={agent} size="md" />
        <div className="flex-1">
          <h3 className="font-semibold text-slate-200">{agent.name}</h3>
          <div className="flex items-center gap-2">
            {agent.role ? (
              <span className="text-xs text-slate-500">{agent.role}</span>
            ) : null}
            <span className={`text-xs capitalize ${statusTextClassName}`}>
              {agent.status}
            </span>
          </div>
        </div>
        <span className="rounded-full bg-slate-800 px-2 py-0.5 text-xs text-slate-400">
          {messageCount} {messageCount === 1 ? "message" : "messages"}
        </span>
      </div>

      <MessageHistory
        messages={messages}
        currentUserId={currentUserId}
        agent={agent}
        hasMore={pagination.hasMore}
        isLoadingMore={isLoadingMore}
        onLoadMore={handleLoadMore}
      />

      {error ? (
        <div className="border-t border-red-900/50 bg-red-950/50 px-5 py-2">
          <p className="text-sm text-red-400">{error}</p>
        </div>
      ) : null}

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
          aria-label="Send message"
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

      <div className="border-t border-slate-800/50 bg-slate-950/50 px-5 py-1.5">
        <p className="text-[10px] text-slate-600">
          Press <span className="font-medium">Cmd/Ctrl + Enter</span> to send
        </p>
      </div>
    </div>
  );
}

