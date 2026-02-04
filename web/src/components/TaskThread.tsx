import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { useWS } from "../contexts/WebSocketContext";

/**
 * Message sender type - distinguishes between human users and AI agents.
 */
export type MessageSenderType = "user" | "agent";

/**
 * Represents a file attachment on a message.
 */
export type Attachment = {
  id: string;
  filename: string;
  size_bytes: number;
  mime_type: string;
  url: string;
  thumbnail_url?: string;
};

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
  attachments?: Attachment[];
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
 * Format file size for display.
 */
function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Check if a MIME type is an image.
 */
function isImageMimeType(mimeType: string): boolean {
  return mimeType.startsWith("image/");
}

/**
 * Get icon for file type.
 */
function getFileIcon(mimeType: string): string {
  if (mimeType.startsWith("image/")) return "🖼️";
  if (mimeType.startsWith("video/")) return "🎬";
  if (mimeType.startsWith("audio/")) return "🎵";
  if (mimeType.includes("pdf")) return "📄";
  if (mimeType.includes("word") || mimeType.includes("document")) return "📝";
  if (mimeType.includes("sheet") || mimeType.includes("excel")) return "📊";
  if (mimeType.includes("zip") || mimeType.includes("archive")) return "📦";
  if (mimeType.includes("json") || mimeType.includes("javascript") || mimeType.includes("text")) return "📋";
  return "📎";
}

/**
 * Attachment preview component.
 */
function AttachmentPreview({ attachment }: { attachment: Attachment }) {
  const isImage = isImageMimeType(attachment.mime_type);
  
  if (isImage) {
    return (
      <a
        href={attachment.url}
        target="_blank"
        rel="noopener noreferrer"
        className="group relative block overflow-hidden rounded-lg border border-slate-700 bg-slate-800 transition hover:border-slate-600"
      >
        <img
          src={attachment.thumbnail_url || attachment.url}
          alt={attachment.filename}
          loading="lazy"
          decoding="async"
          className="h-32 w-full object-cover transition group-hover:opacity-90"
        />
        <div className="absolute inset-0 flex items-center justify-center bg-black/0 transition group-hover:bg-black/20">
          <span className="scale-0 text-2xl transition group-hover:scale-100">🔍</span>
        </div>
        <div className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-black/70 to-transparent px-2 py-1">
          <p className="truncate text-xs text-white">{attachment.filename}</p>
        </div>
      </a>
    );
  }

  return (
    <a
      href={attachment.url}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center gap-3 rounded-lg border border-slate-700 bg-slate-800 p-3 transition hover:border-slate-600 hover:bg-slate-750"
    >
      <span className="text-2xl">{getFileIcon(attachment.mime_type)}</span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-slate-200">{attachment.filename}</p>
        <p className="text-xs text-slate-500">{formatFileSize(attachment.size_bytes)}</p>
      </div>
      <svg
        xmlns="http://www.w3.org/2000/svg"
        viewBox="0 0 20 20"
        fill="currentColor"
        className="h-5 w-5 text-slate-500"
      >
        <path d="M10.75 2.75a.75.75 0 0 0-1.5 0v8.614L6.295 8.235a.75.75 0 1 0-1.09 1.03l4.25 4.5a.75.75 0 0 0 1.09 0l4.25-4.5a.75.75 0 0 0-1.09-1.03l-2.955 3.129V2.75Z" />
        <path d="M3.5 12.75a.75.75 0 0 0-1.5 0v2.5A2.75 2.75 0 0 0 4.75 18h10.5A2.75 2.75 0 0 0 18 15.25v-2.5a.75.75 0 0 0-1.5 0v2.5c0 .69-.56 1.25-1.25 1.25H4.75c-.69 0-1.25-.56-1.25-1.25v-2.5Z" />
      </svg>
    </a>
  );
}

/**
 * Attachments grid for displaying multiple attachments.
 */
function AttachmentsGrid({ attachments }: { attachments: Attachment[] }) {
  if (!attachments || attachments.length === 0) return null;

  const images = attachments.filter((a) => isImageMimeType(a.mime_type));
  const files = attachments.filter((a) => !isImageMimeType(a.mime_type));

  return (
    <div className="mt-2 space-y-2">
      {images.length > 0 && (
        <div className={`grid gap-2 ${images.length === 1 ? "grid-cols-1" : "grid-cols-2"}`}>
          {images.map((attachment) => (
            <AttachmentPreview key={attachment.id} attachment={attachment} />
          ))}
        </div>
      )}
      {files.length > 0 && (
        <div className="space-y-2">
          {files.map((attachment) => (
            <AttachmentPreview key={attachment.id} attachment={attachment} />
          ))}
        </div>
      )}
    </div>
  );
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
        loading="lazy"
        decoding="async"
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

  const hasAttachments = message.attachments && message.attachments.length > 0;

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
          {message.content && (
            <p className="whitespace-pre-wrap text-sm leading-relaxed">
              {message.content}
            </p>
          )}
          {hasAttachments && (
            <AttachmentsGrid attachments={message.attachments!} />
          )}
        </div>
        <span className="mt-1 text-[10px] text-slate-500">
          {formatTimestamp(message.createdAt)}
        </span>
      </div>
    </div>
  );
}

/**
 * Pending attachment (uploaded but not yet sent with a message).
 */
type PendingAttachment = Attachment & { isUploading?: boolean };

/**
 * TaskThread - A real-time messaging UI for task discussions.
 * 
 * Features:
 * - Message list with user/agent differentiation
 * - Real-time updates via WebSocket
 * - Auto-scroll to bottom on new messages
 * - Input box with send functionality
 * - File attachments with drag-and-drop support
 */
export default function TaskThread({
  taskId,
  initialMessages = [],
  currentUserId = "current-user",
  currentUserName = "You",
  onSendMessage,
  apiEndpoint = "/api/messages",
  orgId = "",
}: TaskThreadProps & { orgId?: string }) {
  const [messages, setMessages] = useState<Message[]>(initialMessages);
  const [inputValue, setInputValue] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [isLoading, setIsLoading] = useState(!initialMessages.length);
  const [error, setError] = useState<string | null>(null);
  const [pendingAttachments, setPendingAttachments] = useState<PendingAttachment[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
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

  // Upload a file attachment
  const uploadFile = useCallback(async (file: File): Promise<Attachment | null> => {
    if (!orgId) {
      setError("Organization ID required for uploads");
      return null;
    }

    const formData = new FormData();
    formData.append("file", file);
    formData.append("org_id", orgId);

    try {
      const response = await fetch("/api/messages/attachments", {
        method: "POST",
        body: formData,
      });

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(errorData.error || "Failed to upload file");
      }

      const data = await response.json();
      return data.attachment;
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed");
      return null;
    }
  }, [orgId]);

  // Handle file selection
  const handleFileSelect = useCallback(async (files: FileList | null) => {
    if (!files || files.length === 0) return;

    const fileArray = Array.from(files);
    
    // Add placeholders for uploading files
    const placeholders: PendingAttachment[] = fileArray.map((file, idx) => ({
      id: `uploading-${Date.now()}-${idx}`,
      filename: file.name,
      size_bytes: file.size,
      mime_type: file.type || "application/octet-stream",
      url: "",
      isUploading: true,
    }));
    
    setPendingAttachments((prev) => [...prev, ...placeholders]);

    // Upload files in parallel
    const uploadPromises = fileArray.map(async (file, idx) => {
      const attachment = await uploadFile(file);
      if (attachment) {
        setPendingAttachments((prev) =>
          prev.map((p) =>
            p.id === placeholders[idx].id
              ? { ...attachment, isUploading: false }
              : p
          )
        );
      } else {
        // Remove failed upload
        setPendingAttachments((prev) =>
          prev.filter((p) => p.id !== placeholders[idx].id)
        );
      }
    });

    await Promise.all(uploadPromises);
  }, [uploadFile]);

  // Handle drag events
  const handleDragEnter = useCallback((e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(true);
  }, []);

  const handleDragLeave = useCallback((e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
  }, []);

  const handleDragOver = useCallback((e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback((e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragging(false);
    handleFileSelect(e.dataTransfer.files);
  }, [handleFileSelect]);

  // Handle file input change
  const handleFileInputChange = useCallback((e: ChangeEvent<HTMLInputElement>) => {
    handleFileSelect(e.target.files);
    // Reset input so same file can be selected again
    e.target.value = "";
  }, [handleFileSelect]);

  // Remove a pending attachment
  const removePendingAttachment = useCallback((id: string) => {
    setPendingAttachments((prev) => prev.filter((a) => a.id !== id));
  }, []);

  // Send a new message
  const handleSend = useCallback(async () => {
    const content = inputValue.trim();
    const hasAttachments = pendingAttachments.length > 0 && pendingAttachments.every((a) => !a.isUploading);
    
    if ((!content && !hasAttachments) || isSending) {
      return;
    }

    setIsSending(true);
    setError(null);

    // Get ready attachments (filter out uploading ones)
    const attachmentsToSend = pendingAttachments
      .filter((a) => !a.isUploading)
      .map(({ isUploading, ...attachment }) => attachment);

    // Optimistic update
    const optimisticMessage: Message = {
      id: `temp-${Date.now()}`,
      taskId,
      senderId: currentUserId,
      senderName: currentUserName,
      senderType: "user",
      content,
      attachments: attachmentsToSend.length > 0 ? attachmentsToSend : undefined,
      createdAt: new Date().toISOString(),
    };

    setMessages((prev) => [...prev, optimisticMessage]);
    setInputValue("");
    setPendingAttachments([]);

    try {
      if (onSendMessage) {
        await onSendMessage(content);
      } else {
        // Default: POST to API endpoint
        const response = await fetch(apiEndpoint, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ 
            taskId, 
            content,
            attachments: attachmentsToSend.length > 0 ? attachmentsToSend : undefined,
          }),
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
      setPendingAttachments(attachmentsToSend); // Restore attachments
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
    pendingAttachments,
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

  const canSend = inputValue.trim() || (pendingAttachments.length > 0 && pendingAttachments.every((a) => !a.isUploading));

  return (
    <div 
      className={`relative flex h-[500px] flex-col overflow-hidden rounded-2xl border bg-slate-900/95 shadow-xl transition-colors ${
        isDragging ? "border-sky-500 bg-sky-950/20" : "border-slate-800"
      }`}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* Drag overlay */}
      {isDragging && (
        <div className="absolute inset-0 z-50 flex items-center justify-center bg-slate-900/90">
          <div className="flex flex-col items-center gap-3 text-sky-400">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
              strokeWidth={1.5}
              stroke="currentColor"
              className="h-12 w-12"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M12 16.5V9.75m0 0 3 3m-3-3-3 3M6.75 19.5a4.5 4.5 0 0 1-1.41-8.775 5.25 5.25 0 0 1 10.233-2.33 3 3 0 0 1 3.758 3.848A3.752 3.752 0 0 1 18 19.5H6.75Z"
              />
            </svg>
            <p className="text-lg font-medium">Drop files to attach</p>
          </div>
        </div>
      )}

      {/* Hidden file input */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleFileInputChange}
      />

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

      {/* Pending attachments preview */}
      {pendingAttachments.length > 0 && (
        <div className="border-t border-slate-800 bg-slate-850 px-5 py-3">
          <div className="flex flex-wrap gap-2">
            {pendingAttachments.map((attachment) => (
              <div
                key={attachment.id}
                className="group relative flex items-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 py-2"
              >
                {attachment.isUploading ? (
                  <div className="h-4 w-4 animate-spin rounded-full border-2 border-slate-600 border-t-sky-500" />
                ) : (
                  <span className="text-sm">{getFileIcon(attachment.mime_type)}</span>
                )}
                <span className="max-w-[120px] truncate text-xs text-slate-300">
                  {attachment.filename}
                </span>
                {!attachment.isUploading && (
                  <button
                    type="button"
                    onClick={() => removePendingAttachment(attachment.id)}
                    className="ml-1 rounded p-0.5 text-slate-500 hover:bg-slate-700 hover:text-slate-300"
                  >
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      viewBox="0 0 16 16"
                      fill="currentColor"
                      className="h-3 w-3"
                    >
                      <path d="M5.28 4.22a.75.75 0 0 0-1.06 1.06L6.94 8l-2.72 2.72a.75.75 0 1 0 1.06 1.06L8 9.06l2.72 2.72a.75.75 0 1 0 1.06-1.06L9.06 8l2.72-2.72a.75.75 0 0 0-1.06-1.06L8 6.94 5.28 4.22Z" />
                    </svg>
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Input area */}
      <form
        onSubmit={handleSubmit}
        className="flex items-end gap-3 border-t border-slate-800 px-5 py-4"
      >
        {/* Attach button */}
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          disabled={isSending}
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl border border-slate-700 bg-slate-800 text-slate-400 transition hover:border-slate-600 hover:text-slate-300 focus:outline-none focus:ring-2 focus:ring-sky-500 disabled:cursor-not-allowed disabled:opacity-50"
          title="Attach files"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            fill="none"
            viewBox="0 0 24 24"
            strokeWidth={1.5}
            stroke="currentColor"
            className="h-5 w-5"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="m18.375 12.739-7.693 7.693a4.5 4.5 0 0 1-6.364-6.364l10.94-10.94A3 3 0 1 1 19.5 7.372L8.552 18.32m.009-.01-.01.01m5.699-9.941-7.81 7.81a1.5 1.5 0 0 0 2.112 2.13"
            />
          </svg>
        </button>
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
          disabled={!canSend || isSending}
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
          Press <span className="font-medium">Cmd/Ctrl + Enter</span> to send • Drag files to attach
        </p>
      </div>
    </div>
  );
}
