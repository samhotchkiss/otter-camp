import { useCallback, useEffect, useRef } from "react";
import type {
  Agent,
  AgentStatus,
  DMMessage,
  MessageAttachment,
  MessageSenderType,
} from "./types";
import { formatTimestamp, getInitials } from "./utils";
import MessageMarkdown from "./MessageMarkdown";
import Questionnaire from "../Questionnaire";
import QuestionnaireResponse from "../QuestionnaireResponse";

const SCROLL_BOTTOM_THRESHOLD_PX = 200;

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

function MessageAttachments({ attachments }: { attachments: MessageAttachment[] }) {
  if (attachments.length === 0) {
    return null;
  }

  return (
    <div className="mt-2 space-y-2">
      {attachments.map((attachment) => {
        const isImage = attachment.mime_type.startsWith("image/");
        if (isImage) {
          return (
            <a
              key={attachment.id}
              href={attachment.url}
              target="_blank"
              rel="noreferrer"
              className="block overflow-hidden rounded-xl border border-[var(--border)] bg-black/20"
            >
              <img
                src={attachment.thumbnail_url || attachment.url}
                alt={attachment.filename}
                loading="lazy"
                className="max-h-64 w-full object-cover"
              />
              <div className="border-t border-[var(--border)]/70 px-3 py-2 text-xs text-[var(--text-muted)]">
                {attachment.filename}
              </div>
            </a>
          );
        }

        return (
          <div
            key={attachment.id}
            className="flex items-center justify-between gap-3 rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-xs"
          >
            <div className="min-w-0">
              <p className="truncate font-medium text-[var(--text)]">{attachment.filename}</p>
              <p className="text-[var(--text-muted)]">{formatAttachmentSize(attachment.size_bytes)}</p>
            </div>
            <a
              href={attachment.url}
              target="_blank"
              rel="noreferrer"
              className="rounded border border-[var(--border)] px-2 py-1 text-[10px] font-medium text-[var(--text)] transition hover:border-[var(--accent)] hover:text-[var(--accent)]"
            >
              Download
            </a>
          </div>
        );
      })}
    </div>
  );
}

function appendCandidate(candidates: string[], seen: Set<string>, value: string) {
  const trimmed = value.trim();
  if (!trimmed || seen.has(trimmed)) {
    return;
  }
  seen.add(trimmed);
  candidates.push(trimmed);
}

function buildAgentLookupCandidates(raw: string): string[] {
  const candidates: string[] = [];
  const seen = new Set<string>();
  const trimmed = raw.trim();
  if (!trimmed) {
    return candidates;
  }

  appendCandidate(candidates, seen, trimmed);
  appendCandidate(candidates, seen, trimmed.toLowerCase());

  if (trimmed.startsWith("agent:")) {
    const withoutPrefix = trimmed.slice("agent:".length).trim();
    appendCandidate(candidates, seen, withoutPrefix);
    appendCandidate(candidates, seen, withoutPrefix.toLowerCase());
  }

  if (trimmed.startsWith("dm_")) {
    const withoutDM = trimmed.slice("dm_".length).trim();
    appendCandidate(candidates, seen, withoutDM);
    appendCandidate(candidates, seen, withoutDM.toLowerCase());
  }

  const separatorIndex = trimmed.lastIndexOf(":");
  if (separatorIndex > -1 && separatorIndex + 1 < trimmed.length) {
    const tail = trimmed.slice(separatorIndex + 1).trim();
    appendCandidate(candidates, seen, tail);
    appendCandidate(candidates, seen, tail.toLowerCase());
  }

  return candidates;
}

function lookupAgentName(agentNamesByID: Map<string, string> | undefined, raw: string): string | null {
  if (!agentNamesByID || agentNamesByID.size === 0) {
    return null;
  }
  for (const candidate of buildAgentLookupCandidates(raw)) {
    const resolved = (agentNamesByID.get(candidate) ?? "").trim();
    if (resolved) {
      return resolved;
    }
  }
  return null;
}

function resolveMessageSenderName(
  message: DMMessage,
  agentNamesByID?: Map<string, string>,
  resolveAgentName?: (raw: string) => string,
): string {
  if (message.senderType !== "agent") {
    return message.senderName;
  }

  const candidates = [message.senderId, message.senderName, message.threadId];
  for (const candidate of candidates) {
    const resolved = lookupAgentName(agentNamesByID, candidate);
    if (resolved) {
      return resolved;
    }
  }

  if (resolveAgentName) {
    for (const candidate of candidates) {
      const resolved = resolveAgentName(candidate).trim();
      if (resolved && resolved !== candidate.trim()) {
        return resolved;
      }
    }
  }

  return message.senderName;
}

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
      ? "bg-[#ff9800] text-[#1c1200]"
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
  displaySenderName,
  isOwnMessage,
  onRetryMessage,
  onSubmitQuestionnaire,
}: {
  message: DMMessage;
  displaySenderName: string;
  isOwnMessage: boolean;
  onRetryMessage?: (message: DMMessage) => void;
  onSubmitQuestionnaire?: (
    questionnaireID: string,
    responses: Record<string, unknown>,
  ) => Promise<void> | void;
}) {
  if (message.isSessionReset) {
    return (
      <div
        className="my-2 rounded-lg border border-[#C9A86C]/45 bg-[#C9A86C]/12 px-3 py-2 text-center"
        data-testid="project-chat-session-divider"
      >
        <p className="text-xs font-semibold uppercase tracking-wide text-[#C9A86C]">
          New chat session started
        </p>
        <p className="mt-1 text-[11px] text-[var(--text-muted)]">
          {formatTimestamp(message.createdAt)}
        </p>
      </div>
    );
  }

  const bubbleStyle = isOwnMessage
    ? "oc-message-bubble-user bg-[var(--accent)] text-[#1A1918]"
    : message.senderType === "agent"
      ? "oc-message-bubble-agent border border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text)]"
      : "bg-[var(--surface-alt)] text-[var(--text)]";

  return (
    <div
      className={`flex min-w-0 gap-3 ${isOwnMessage ? "flex-row-reverse" : "flex-row"}`}
    >
      <MessageAvatar
        name={displaySenderName}
        avatarUrl={message.senderAvatarUrl}
        senderType={message.senderType}
      />
      <div
        className={`flex min-w-0 max-w-[75%] flex-col ${isOwnMessage ? "items-end" : "items-start"}`}
      >
        <div className="mb-1 flex min-w-0 items-center gap-2">
          <span className="truncate text-xs font-medium text-[var(--text-muted)]">
            {displaySenderName}
          </span>
          {message.senderType === "agent" && (
            <span className="oc-message-agent-chip rounded-full bg-[var(--accent)]/20 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-[var(--accent)]">
              Agent
            </span>
          )}
          <span className="text-[10px] text-[var(--text-muted)]">
            {formatTimestamp(message.createdAt)}
          </span>
        </div>
        <div className={`min-w-0 max-w-full overflow-hidden rounded-2xl px-4 py-2.5 ${bubbleStyle}`}>
          {message.questionnaire ? (
            message.questionnaire.responses ? (
              <QuestionnaireResponse questionnaire={message.questionnaire} />
            ) : onSubmitQuestionnaire ? (
              <Questionnaire
                questionnaire={message.questionnaire}
                onSubmit={(responses) => onSubmitQuestionnaire(message.questionnaire!.id, responses)}
              />
            ) : (
              <QuestionnaireResponse questionnaire={message.questionnaire} />
            )
          ) : (
            <>
              <MessageMarkdown
                markdown={message.content}
                className="text-sm leading-relaxed"
              />
              {message.attachments && message.attachments.length > 0 ? (
                <MessageAttachments attachments={message.attachments} />
              ) : null}
            </>
          )}
        </div>
        {message.optimistic ? (
          <p className="mt-1 text-[10px] text-[var(--orange)]">Sending...</p>
        ) : null}
        {message.failed ? (
          <div className="mt-1 flex items-center gap-2">
            <p className="text-[10px] text-[var(--red)]">Send failed</p>
            {onRetryMessage ? (
              <button
                type="button"
                onClick={() => onRetryMessage(message)}
                className="rounded border border-[var(--red)]/50 px-1.5 py-0.5 text-[10px] font-medium text-[var(--red)] transition hover:bg-[var(--red)]/10"
              >
                Retry
              </button>
            ) : null}
          </div>
        ) : null}
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
  onRetryMessage?: (message: DMMessage) => void;
  onSubmitQuestionnaire?: (
    questionnaireID: string,
    responses: Record<string, unknown>,
  ) => Promise<void> | void;
  agentNamesByID?: Map<string, string>;
  resolveAgentName?: (raw: string) => string;
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
  onRetryMessage,
  onSubmitQuestionnaire,
  agentNamesByID,
  resolveAgentName,
  className = "",
}: MessageHistoryProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);

  const prevMessageCountRef = useRef(messages.length);
  const pendingPrependRef = useRef(false);
  const scrollSnapshotRef = useRef({ scrollHeight: 0, scrollTop: 0 });
  const pinnedToBottomRef = useRef(true);

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
    pinnedToBottomRef.current = true;
    requestAnimationFrame(() => {
      endRef.current?.scrollIntoView({ behavior: "auto" });
    });
  }, [threadId]);

  const handleScroll = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;

    const distanceFromBottom =
      container.scrollHeight - container.scrollTop - container.clientHeight;
    pinnedToBottomRef.current =
      distanceFromBottom <= SCROLL_BOTTOM_THRESHOLD_PX;
  }, []);

  useEffect(() => {
    const container = containerRef.current;
    if (!container || typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver(() => {
      if (!pinnedToBottomRef.current) {
        return;
      }
      endRef.current?.scrollIntoView({ behavior: "auto" });
    });
    observer.observe(container);
    return () => {
      observer.disconnect();
    };
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
      if (prevCount === 0 || pinnedToBottomRef.current) {
        // Double-RAF to ensure DOM has fully reflowed with new content
        requestAnimationFrame(() => {
          requestAnimationFrame(() => {
            endRef.current?.scrollIntoView({
              behavior: prevCount === 0 ? "auto" : "smooth",
            });
          });
        });
      }
    }
  }, [messages]);

  return (
    <div
      ref={containerRef}
      onScroll={handleScroll}
      className={`oc-chat-history flex-1 overflow-x-hidden overflow-y-auto px-5 py-4 ${className}`}
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
              displaySenderName={resolveMessageSenderName(message, agentNamesByID, resolveAgentName)}
              isOwnMessage={message.senderId === currentUserId}
              onRetryMessage={onRetryMessage}
              onSubmitQuestionnaire={onSubmitQuestionnaire}
            />
          ))}
          <div ref={endRef} />
        </div>
      )}
    </div>
  );
}
