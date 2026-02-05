import { useEffect, useMemo, useRef } from "react";
import MessageAvatar from "./MessageAvatar";
import MessageMarkdown from "./MessageMarkdown";
import type { TaskThreadMessage } from "./types";
import { formatMessageTimestamp } from "./utils";

export type TaskThreadViewProps = {
  messages: TaskThreadMessage[];
  currentUserId?: string;
};

function MessageBubble({
  message,
  isOwnMessage,
}: {
  message: TaskThreadMessage;
  isOwnMessage: boolean;
}) {
  const senderType = message.senderType ?? "user";
  const senderName =
    message.senderName?.trim() || (senderType === "agent" ? "Agent" : "User");

  const bubbleClassName = isOwnMessage
    ? "bg-sky-600 text-white"
    : senderType === "agent"
      ? "border border-emerald-700/40 bg-emerald-900/30 text-emerald-50"
      : "bg-slate-100 text-slate-900 dark:bg-slate-800 dark:text-slate-100";

  return (
    <div className={`flex gap-3 ${isOwnMessage ? "flex-row-reverse" : ""}`}>
      <MessageAvatar
        name={senderName}
        avatarUrl={message.senderAvatarUrl}
        senderType={senderType}
      />
      <div
        className={`flex max-w-[78%] flex-col ${isOwnMessage ? "items-end" : "items-start"}`}
      >
        <div className="mb-1 flex items-center gap-2">
          <span className="text-xs font-medium text-slate-500 dark:text-slate-400">
            {senderName}
          </span>
          {senderType === "agent" ? (
            <span className="rounded-full bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-600 dark:text-emerald-300">
              Agent
            </span>
          ) : null}
        </div>
        <div className={`rounded-2xl px-4 py-2.5 shadow-sm ${bubbleClassName}`}>
          <MessageMarkdown markdown={message.content} className="text-sm leading-relaxed" />
        </div>
        <span className="mt-1 text-[10px] text-slate-400 dark:text-slate-500">
          {formatMessageTimestamp(message.createdAt)}
        </span>
      </div>
    </div>
  );
}

export default function TaskThreadView({
  messages,
  currentUserId,
}: TaskThreadViewProps) {
  const messagesEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length]);

  const hasMessages = messages.length > 0;
  const normalizedMessages = useMemo(() => messages.filter(Boolean), [messages]);

  if (!hasMessages) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-slate-500 dark:text-slate-400">
        <span className="text-4xl">🦦</span>
        <p className="mt-3 text-sm">No messages yet. Start the conversation!</p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {normalizedMessages.map((message) => (
        <MessageBubble
          key={message.id}
          message={message}
          isOwnMessage={Boolean(currentUserId && message.senderId === currentUserId)}
        />
      ))}
      <div ref={messagesEndRef} />
    </div>
  );
}

