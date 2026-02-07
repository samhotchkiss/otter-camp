import { useCallback, useEffect, useRef } from "react";
import type { Agent, AgentStatus, DMMessage, MessageSenderType } from "./types";
import { formatTimestamp, getInitials } from "./utils";

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
      ? "bg-[var(--accent)]/20 text-[var(--accent)]"
      : "bg-[var(--surface-alt)] text-[var(--text)]";

  if (avatarUrl) {
    return (
      <img
        src={avatarUrl}
        alt={name}
        loading="lazy"
        decoding="async"
        className="h-8 w-8 rounded-full object-cover ring-2 ring-[var(--border)]"
      />
    );
  }

  return (
    <div
      className={`flex h-8 w-8 items-center justify-center rounded-full text-xs font-semibold ${bgColor}`}
      aria-label={name}
    >
      {getInitials(name)}
    </div>
  );
}

function MessageBubble({
  message,
  isOwnMessage,
}: {
  message: DMMessage;
  isOwnMessage: boolean;
}) {
  const bubbleStyle = isOwnMessage
    ? "bg-[var(--accent)] text-[#1A1918]"
    : message.senderType === "agent"
      ? "border border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text)]"
      : "bg-[var(--surface-alt)] text-[var(--text)]";

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
          <span className="text-xs font-medium text-[var(--text-muted)]">
            {message.senderName}
          </span>
          {message.senderType === "agent" && (
            <span className="rounded-full bg-[var(--accent)]/20 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-[var(--accent)]">
              Agent
            </span>
          )}
        </div>
        <div className={`rounded-2xl px-4 py-2.5 ${bubbleStyle}`}>
          <p className="whitespace-pre-wrap text-sm leading-relaxed">
            {message.content}
          </p>
        </div>
        <span className="mt-1 text-[10px] text-[var(--text-muted)]">
          {formatTimestamp(message.createdAt)}
        </span>
      </div>
    </div>
  );
}

function AgentAvatarFallback({ agent }: { agent: Agent }) {
  const statusStyles: Record<AgentStatus, string> = {
    online: "bg-[var(--accent)] shadow-[var(--accent)]/50",
    busy: "bg-[var(--orange)] shadow-[var(--orange)]/50",
    offline: "bg-[var(--text-muted)]",
  };

  return (
    <div className="relative">
      {agent.avatarUrl ? (
        <img
          src={agent.avatarUrl}
          alt={agent.name}
          loading="lazy"
          decoding="async"
          className="h-12 w-12 rounded-full object-cover ring-2 ring-[var(--accent)]/30"
        />
      ) : (
        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-[var(--accent)]/20 text-base font-semibold text-[var(--accent)] ring-2 ring-[var(--accent)]/30">
          {getInitials(agent.name)}
        </div>
      )}
      <span
        className={`absolute bottom-0 right-0 h-3 w-3 rounded-full border-2 border-[var(--surface)] shadow-lg ${statusStyles[agent.status]}`}
        title={agent.status.charAt(0).toUpperCase() + agent.status.slice(1)}
      />
    </div>
  );
}

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
      className="mx-auto flex items-center gap-2 rounded-full bg-[var(--surface-alt)] px-4 py-1.5 text-xs text-[var(--text-muted)] transition hover:bg-[var(--border)] hover:text-[var(--text)] disabled:opacity-50"
    >
      {isLoading ? (
        <>
          <div className="h-3 w-3 animate-spin rounded-full border-2 border-[var(--border)] border-t-[var(--accent)]" />
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

export type MessageHistoryProps = {
  messages: DMMessage[];
  currentUserId: string;
  threadId?: string;
  agent?: Agent;
  hasMore?: boolean;
  isLoadingMore?: boolean;
  onLoadMore?: () => Promise<void> | void;
  className?: string;
};

export default function MessageHistory({
  messages,
  currentUserId,
  threadId,
  agent,
  hasMore = false,
  isLoadingMore = false,
  onLoadMore,
  className = "",
}: MessageHistoryProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);

  const prevMessageCountRef = useRef(messages.length);
  const pendingPrependRef = useRef(false);
  const scrollSnapshotRef = useRef({ scrollHeight: 0, scrollTop: 0 });

  const handleLoadMore = useCallback(async () => {
    if (!onLoadMore || isLoadingMore) return;

    const container = containerRef.current;
    if (container) {
      pendingPrependRef.current = true;
      scrollSnapshotRef.current = {
        scrollHeight: container.scrollHeight,
        scrollTop: container.scrollTop,
      };
    }

    try {
      await onLoadMore();
    } catch {
      pendingPrependRef.current = false;
    }
  }, [isLoadingMore, onLoadMore]);

  useEffect(() => {
    prevMessageCountRef.current = 0;
    pendingPrependRef.current = false;
    requestAnimationFrame(() => {
      endRef.current?.scrollIntoView({ behavior: "auto" });
    });
  }, [threadId]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    if (pendingPrependRef.current) {
      const { scrollHeight, scrollTop } = scrollSnapshotRef.current;
      requestAnimationFrame(() => {
        const nextScrollHeight = container.scrollHeight;
        const delta = nextScrollHeight - scrollHeight;
        container.scrollTop = scrollTop + delta;
        pendingPrependRef.current = false;
      });
      return;
    }

    const prevCount = prevMessageCountRef.current;
    prevMessageCountRef.current = messages.length;
    if (messages.length > prevCount) {
      endRef.current?.scrollIntoView({
        behavior: prevCount === 0 ? "auto" : "smooth",
      });
    }
  }, [messages]);

  return (
    <div
      ref={containerRef}
      className={`flex-1 overflow-y-auto px-5 py-4 ${className}`}
    >
      {hasMore && onLoadMore && (
        <div className="mb-4 flex justify-center">
          <LoadMoreButton onClick={handleLoadMore} isLoading={isLoadingMore} />
        </div>
      )}

      {messages.length === 0 ? (
        <div className="flex h-full flex-col items-center justify-center text-[var(--text-muted)]">
          {agent ? <AgentAvatarFallback agent={agent} /> : null}
          <p className="mt-3 text-sm">
            {agent ? `Start a conversation with ${agent.name}` : "No messages yet"}
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
          <div ref={endRef} />
        </div>
      )}
    </div>
  );
}
