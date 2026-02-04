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
 * Message sender type - distinguishes between human users and AI agents.
 */
export type MessageSenderType = "user" | "agent";

/**
 * Represents a single message in a task thread.
 */
export type Message = {
  id: string;
  taskId: string;
  senderId: string;
  senderName: string;
  senderType: MessageSenderType;
  senderAvatarUrl?: string;
  content: string;
  createdAt: string;
};

/**
 * Props for the TaskThread component.
 */
export type TaskThreadProps = {
  taskId: string;
  initialMessages?: Message[];
  currentUserId?: string;
  currentUserName?: string;
  onSendMessage?: (content: string) => Promise<void>;
  apiEndpoint?: string;
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
 * Avatar component for message senders.
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
  const bgColor = senderType === "agent" 
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
  message: Message;
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
      <div className={`flex max-w-[75%] flex-col ${isOwnMessage ? "items-end" : "items-start"}`}>
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
 * TaskThread - A real-time messaging UI for task discussions.
 * 
 * Features:
 * - Message list with user/agent differentiation
 * - Real-time updates via WebSocket
 * - Auto-scroll to bottom on new messages
 * - Input box with send functionality
 */
export default function TaskThread({
  taskId,
  initialMessages = [],
  currentUserId = "current-user",
  currentUserName = "You",
  onSendMessage,
  apiEndpoint = "/api/messages",
}: TaskThreadProps) {
  const [messages, setMessages] = useState<Message[]>(initialMessages);
  const [inputValue, setInputValue] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [isLoading, setIsLoading] = useState(!initialMessages.length);
  const [error, setError] = useState<string | null>(null);
  
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const { lastMessage, sendMessage: wsSend } = useWS();

  // Scroll to bottom when messages change
  const scrollToBottom = useCallback(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, []);

  useEffect(() => {
    scrollToBottom();
  }, [messages, scrollToBottom]);

  // Fetch initial messages
  useEffect(() => {
    if (initialMessages.length > 0) {
      return;
    }

    const fetchMessages = async () => {
      try {
        const response = await fetch(`${apiEndpoint}?taskId=${taskId}`);
        if (!response.ok) {
          throw new Error("Failed to fetch messages");
        }
        const data = await response.json();
        setMessages(data.messages || []);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load messages");
      } finally {
        setIsLoading(false);
      }
    };

    fetchMessages();
  }, [apiEndpoint, initialMessages.length, taskId]);

  // Handle WebSocket messages for real-time updates
  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    // Handle new message events
    if (lastMessage.type === "CommentAdded") {
      const data = lastMessage.data as { taskId?: string; message?: Message };
      if (data.taskId === taskId && data.message) {
        setMessages((prev) => {
          // Avoid duplicates
          if (prev.some((m) => m.id === data.message!.id)) {
            return prev;
          }
          return [...prev, data.message!];
        });
      }
    }
  }, [lastMessage, taskId]);

  // Send a new message
  const handleSend = useCallback(async () => {
    const content = inputValue.trim();
    if (!content || isSending) {
      return;
    }

    setIsSending(true);
    setError(null);

    // Optimistic update
    const optimisticMessage: Message = {
      id: `temp-${Date.now()}`,
      taskId,
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
          body: JSON.stringify({ taskId, content }),
        });

        if (!response.ok) {
          throw new Error("Failed to send message");
        }

        const data = await response.json();
        
        // Replace optimistic message with real one
        setMessages((prev) =>
          prev.map((m) =>
            m.id === optimisticMessage.id ? { ...m, ...data.message, id: data.message?.id || m.id } : m
          )
        );
      }

      // Notify via WebSocket for other clients
      wsSend({
        type: "CommentAdded",
        data: { taskId, message: optimisticMessage },
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
    taskId,
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
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-sky-500" />
          <span>Loading messages...</span>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-[500px] flex-col overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/95 shadow-xl">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-slate-800 px-5 py-3">
        <div className="flex items-center gap-2">
          <span className="text-lg">💬</span>
          <h3 className="font-semibold text-slate-200">Thread</h3>
          <span className="rounded-full bg-slate-800 px-2 py-0.5 text-xs text-slate-400">
            {messages.length} {messages.length === 1 ? "message" : "messages"}
          </span>
        </div>
      </div>

      {/* Messages area */}
      <div className="flex-1 overflow-y-auto px-5 py-4">
        {messages.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-slate-500">
            <span className="text-4xl">🦦</span>
            <p className="mt-3 text-sm">No messages yet. Start the conversation!</p>
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
          placeholder="Type a message..."
          rows={1}
          disabled={isSending}
          className="flex-1 resize-none rounded-xl border border-slate-700 bg-slate-800 px-4 py-2.5 text-sm text-slate-200 placeholder:text-slate-500 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={!inputValue.trim() || isSending}
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl bg-sky-600 text-white transition hover:bg-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2 focus:ring-offset-slate-900 disabled:cursor-not-allowed disabled:opacity-50"
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
