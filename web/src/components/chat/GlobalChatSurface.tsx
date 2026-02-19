import { useMemo, useState, type KeyboardEvent } from "react";
import type { GlobalChatConversation } from "../../contexts/GlobalChatContext";
import { getChatContextCue } from "./chatContextCue";

type ChatRole = "user" | "assistant";

type ChatMessage = {
  id: string;
  role: ChatRole;
  content: string;
  timestamp: Date;
};

type GlobalChatSurfaceProps = {
  conversation: GlobalChatConversation | null;
};

const INITIAL_MESSAGE: ChatMessage = {
  id: "initial",
  role: "assistant",
  content: "Welcome to Otter Camp. Systems are online. How can I assist you today?",
  timestamp: new Date("2026-02-08T12:00:00Z"),
};

function buildContextResponses(conversationType: GlobalChatConversation["type"] | null): string[] {
  if (conversationType === "project") {
    return [
      "Analyzing project structure...",
      "I found pending work and review opportunities in this project.",
      "I can draft a plan and dispatch the next work item.",
    ];
  }
  if (conversationType === "issue") {
    return [
      "Reviewing issue context...",
      "I can summarize blockers and propose a fix path.",
      "Would you like a commit-ready implementation plan?",
    ];
  }
  if (conversationType === "dm") {
    return [
      "Ready when you are.",
      "I can help with planning, coding, or review.",
      "Share the task and I will break it down.",
    ];
  }
  return [
    "Analyzing request...",
    "Deploying changes to staging...",
    "Running compliance checks...",
  ];
}

function buildConversationTitle(conversation: GlobalChatConversation | null): string {
  if (!conversation) {
    return "Otter Shell";
  }
  return conversation.title.trim() || "Otter Shell";
}

function buildPlaceholder(conversation: GlobalChatConversation | null): string {
  const title = buildConversationTitle(conversation);
  if (!conversation) {
    return "Type a command or ask Frank...";
  }
  if (conversation.type === "project") {
    return `Ask about ${title}...`;
  }
  if (conversation.type === "issue") {
    return `Discuss ${title}...`;
  }
  return `Message ${title}...`;
}

export default function GlobalChatSurface({ conversation }: GlobalChatSurfaceProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([INITIAL_MESSAGE]);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);

  const conversationType = conversation?.type ?? null;
  const conversationTitle = useMemo(() => buildConversationTitle(conversation), [conversation]);
  const contextCue = useMemo(() => getChatContextCue(conversationType), [conversationType]);
  const placeholder = useMemo(() => buildPlaceholder(conversation), [conversation]);

  const sendMessage = () => {
    const body = draft.trim();
    if (!body || sending) {
      return;
    }

    const sentAt = new Date("2026-02-08T12:01:00Z");
    const userMessage: ChatMessage = {
      id: `user-${sentAt.getTime()}`,
      role: "user",
      content: body,
      timestamp: sentAt,
    };

    setMessages((prev) => [...prev, userMessage]);
    setDraft("");
    setSending(true);

    const responses = buildContextResponses(conversationType);
    const response = responses[0] || "Acknowledged.";

    window.setTimeout(() => {
      setMessages((prev) => [
        ...prev,
        {
          id: `assistant-${Date.now()}`,
          role: "assistant",
          content: response,
          timestamp: new Date("2026-02-08T12:02:00Z"),
        },
      ]);
      setSending(false);
    }, 1000);
  };

  const onComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      sendMessage();
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col border-l border-stone-800 bg-stone-900">
      <div className="flex items-center justify-between border-b border-stone-800 px-4 py-3">
        <div className="min-w-0">
          <p
            className="text-[10px] font-semibold uppercase tracking-[0.14em] text-stone-500"
            data-testid="global-chat-surface-context-cue"
          >
            {contextCue}
          </p>
          <p className="truncate text-sm font-semibold text-stone-200">{conversationTitle}</p>
        </div>
        <div className="inline-flex items-center gap-2 text-[10px] font-mono uppercase tracking-[0.12em] text-lime-400">
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-lime-400" />
          Live
        </div>
      </div>

      <div className="flex-1 space-y-3 overflow-y-auto p-4">
        {messages.map((message) => {
          const isUser = message.role === "user";
          return (
            <div
              key={message.id}
              className={`flex gap-2 ${isUser ? "justify-end" : "justify-start"}`}
            >
              <div className={`max-w-[88%] rounded-lg border px-3 py-2 text-sm leading-relaxed ${
                isUser
                  ? "border-amber-500/30 bg-amber-500 text-stone-950"
                  : "border-stone-700 bg-stone-800 text-stone-200"
              }`}
              >
                <p>{message.content}</p>
                <p className={`mt-1 text-[10px] ${isUser ? "text-stone-900/80" : "text-stone-500"}`}>
                  {message.timestamp.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
                </p>
              </div>
            </div>
          );
        })}
      </div>

      <div className="border-t border-stone-800 bg-stone-900/90 p-4">
        <div className="relative">
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={onComposerKeyDown}
            placeholder={placeholder}
            className="h-[52px] w-full resize-none rounded-lg border border-stone-700 bg-stone-950 px-3 py-3 pr-12 text-sm text-stone-200 outline-none placeholder:text-stone-600 focus:border-amber-500/50 focus:ring-1 focus:ring-amber-500/40"
          />
          <button
            type="button"
            aria-label="Send message"
            onClick={sendMessage}
            disabled={draft.trim() === "" || sending}
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded-md p-1.5 text-stone-400 transition hover:bg-stone-800 hover:text-stone-200 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {sending ? "..." : ">"}
          </button>
        </div>
        <div className="mt-2 flex items-center justify-between">
          <div className="flex items-center gap-2 text-stone-500">
            <button type="button" className="rounded p-1 transition hover:bg-stone-800 hover:text-stone-300" aria-label="Attach file">
              +
            </button>
            <button type="button" className="rounded p-1 transition hover:bg-stone-800 hover:text-amber-300" aria-label="Enhance prompt">
              *
            </button>
          </div>
          <span className="text-[10px] font-mono uppercase tracking-[0.12em] text-stone-600">
            Enter to send
          </span>
        </div>
      </div>
    </div>
  );
}
