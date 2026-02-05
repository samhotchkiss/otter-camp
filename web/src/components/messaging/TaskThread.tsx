import { useCallback, useEffect, useState } from "react";
import { useWS } from "../../contexts/WebSocketContext";
import LoadingSpinner from "../LoadingSpinner";
import TaskMessageInput from "./TaskMessageInput";
import TaskThreadView from "./TaskThreadView";
import type { TaskThreadMessage } from "./types";

export type TaskThreadProps = {
  taskId: string;
  initialMessages?: TaskThreadMessage[];
  currentUserId?: string;
  currentUserName?: string;
  currentUserAvatarUrl?: string;
  apiEndpoint?: string;
  onSendMessage?: (content: string) => Promise<TaskThreadMessage | void>;
};

function isTaskThreadMessage(value: unknown): value is TaskThreadMessage {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return (
    typeof record.id === "string" &&
    typeof record.content === "string" &&
    typeof record.createdAt === "string"
  );
}

function extractCommentAddedMessage(payload: unknown): TaskThreadMessage | null {
  if (!payload) return null;

  if (isTaskThreadMessage(payload)) {
    return payload;
  }

  if (typeof payload === "object") {
    const record = payload as Record<string, unknown>;
    const nested =
      record.message ?? record.comment ?? record.data ?? record.payload;
    if (isTaskThreadMessage(nested)) {
      return nested;
    }
  }

  return null;
}

function extractCommentAddedTaskId(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") return null;
  const record = payload as Record<string, unknown>;

  const direct = record.taskId ?? record.task_id;
  if (typeof direct === "string") return direct;

  const nested =
    record.message ?? record.comment ?? record.data ?? record.payload;
  if (nested && typeof nested === "object") {
    const nestedRecord = nested as Record<string, unknown>;
    const nestedTaskId = nestedRecord.taskId ?? nestedRecord.task_id;
    if (typeof nestedTaskId === "string") return nestedTaskId;
  }

  return null;
}

export default function TaskThread({
  taskId,
  initialMessages = [],
  currentUserId = "current-user",
  currentUserName = "You",
  currentUserAvatarUrl,
  apiEndpoint = "/api/messages",
  onSendMessage,
}: TaskThreadProps) {
  const { lastMessage } = useWS();
  const [messages, setMessages] = useState<TaskThreadMessage[]>(initialMessages);
  const [inputValue, setInputValue] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [isLoading, setIsLoading] = useState(initialMessages.length === 0);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (initialMessages.length > 0) return;

    let cancelled = false;
    const fetchMessages = async () => {
      try {
        const response = await fetch(`${apiEndpoint}?taskId=${taskId}`);
        if (!response.ok) {
          throw new Error("Failed to fetch messages");
        }
        const data = (await response.json()) as { messages?: TaskThreadMessage[] };
        if (!cancelled) {
          setMessages(data.messages ?? []);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to load messages");
        }
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    };

    fetchMessages();
    return () => {
      cancelled = true;
    };
  }, [apiEndpoint, initialMessages.length, taskId]);

  useEffect(() => {
    if (!lastMessage || lastMessage.type !== "CommentAdded") return;

    const commentTaskId = extractCommentAddedTaskId(lastMessage.data);
    const incoming = extractCommentAddedMessage(lastMessage.data);
    if (!incoming) return;

    const incomingTaskId = incoming.taskId ?? commentTaskId;
    if (!incomingTaskId) return;
    if (incomingTaskId !== taskId) return;

    setMessages((prev) => {
      if (prev.some((m) => m.id === incoming.id)) return prev;
      return [...prev, incoming];
    });
  }, [lastMessage, taskId]);

  const handleSend = useCallback(async () => {
    const content = inputValue.trim();
    if (!content || isSending) return;

    setIsSending(true);
    setError(null);

    const optimisticMessage: TaskThreadMessage = {
      id: `temp-${Date.now()}`,
      taskId,
      senderId: currentUserId,
      senderName: currentUserName,
      senderType: "user",
      senderAvatarUrl: currentUserAvatarUrl,
      content,
      createdAt: new Date().toISOString(),
    };

    setMessages((prev) => [...prev, optimisticMessage]);
    setInputValue("");

    try {
      if (onSendMessage) {
        const created = await onSendMessage(content);
        if (created) {
          setMessages((prev) =>
            prev.map((m) => (m.id === optimisticMessage.id ? created : m)),
          );
        }
      } else {
        const response = await fetch(apiEndpoint, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ taskId, content }),
        });
        if (!response.ok) {
          throw new Error("Failed to send message");
        }

        const data = (await response.json()) as { message?: TaskThreadMessage };
        if (data.message) {
          setMessages((prev) =>
            prev.map((m) => (m.id === optimisticMessage.id ? data.message! : m)),
          );
        }
      }
    } catch (err) {
      setMessages((prev) => prev.filter((m) => m.id !== optimisticMessage.id));
      setError(err instanceof Error ? err.message : "Failed to send message");
      setInputValue(content);
    } finally {
      setIsSending(false);
    }
  }, [
    apiEndpoint,
    currentUserAvatarUrl,
    currentUserId,
    currentUserName,
    inputValue,
    isSending,
    onSendMessage,
    taskId,
  ]);

  return (
    <div className="flex h-[520px] flex-col overflow-hidden rounded-2xl border border-slate-200 bg-white/70 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/40">
      <div className="flex items-center justify-between border-b border-slate-200 px-4 py-3 dark:border-slate-800">
        <div className="flex items-center gap-2">
          <span className="text-lg">💬</span>
          <h3 className="font-semibold text-slate-900 dark:text-slate-100">
            Thread
          </h3>
          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600 dark:bg-slate-800/60 dark:text-slate-300">
            {messages.length} {messages.length === 1 ? "message" : "messages"}
          </span>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-4">
        {isLoading ? (
          <LoadingSpinner message="Loading messages..." size="sm" />
        ) : (
          <TaskThreadView messages={messages} currentUserId={currentUserId} />
        )}
      </div>

      {error ? (
        <div className="border-t border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-900/15 dark:text-red-300">
          {error}
        </div>
      ) : null}

      <TaskMessageInput
        value={inputValue}
        onChange={setInputValue}
        onSend={handleSend}
        disabled={false}
        isSending={isSending}
      />
    </div>
  );
}
