import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useWS } from "../../contexts/WebSocketContext";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const ORG_STORAGE_KEY = "otter-camp-org-id";
const USER_NAME_STORAGE_KEY = "otter-camp-user-name";
const PROJECT_CHAT_SESSION_RESET_AUTHOR = "__otter_session__";
const PROJECT_CHAT_SESSION_RESET_PREFIX = "project_chat_session_reset:";

type ProjectChatMessage = {
  id: string;
  project_id: string;
  author: string;
  body: string;
  created_at: string;
  updated_at: string;
  optimistic?: boolean;
  failed?: boolean;
  isSessionReset?: boolean;
  sessionID?: string;
};

type ProjectChatSearchItem = {
  message: ProjectChatMessage;
  relevance: number;
  snippet: string;
};

type ProjectChatPanelProps = {
  projectId: string;
  active: boolean;
  onUnreadChange?: (count: number) => void;
};

type DeliveryIndicatorTone = "neutral" | "success" | "warning";

type DeliveryIndicator = {
  tone: DeliveryIndicatorTone;
  text: string;
};

function getStoredOrgID(): string {
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function getCurrentAuthor(): string {
  try {
    const fromStorage = (localStorage.getItem(USER_NAME_STORAGE_KEY) ?? "").trim();
    if (fromStorage !== "") {
      return fromStorage;
    }
  } catch {
    // no-op
  }
  return "You";
}

function normalizeMessage(input: unknown): ProjectChatMessage | null {
  if (!input || typeof input !== "object") {
    return null;
  }
  const record = input as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id : "";
  const projectID = typeof record.project_id === "string" ? record.project_id : "";
  const author = typeof record.author === "string" ? record.author : "";
  const body = typeof record.body === "string" ? record.body : "";
  const createdAt =
    typeof record.created_at === "string"
      ? record.created_at
      : new Date().toISOString();
  const updatedAt =
    typeof record.updated_at === "string"
      ? record.updated_at
      : createdAt;
  const isSessionReset =
    author === PROJECT_CHAT_SESSION_RESET_AUTHOR &&
    typeof body === "string" &&
    body.startsWith(PROJECT_CHAT_SESSION_RESET_PREFIX);
  const sessionID = isSessionReset
    ? body.slice(PROJECT_CHAT_SESSION_RESET_PREFIX.length).trim()
    : undefined;

  if (!id || !projectID || !author || !body) {
    return null;
  }

  return {
    id,
    project_id: projectID,
    author,
    body,
    created_at: createdAt,
    updated_at: updatedAt,
    isSessionReset,
    sessionID,
  };
}

function extractRealtimeProjectPayload(input: unknown): unknown {
  if (!input || typeof input !== "object") {
    return null;
  }
  const record = input as Record<string, unknown>;
  const hasProjectID =
    (typeof record.project_id === "string" && record.project_id.trim() !== "") ||
    (typeof record.projectId === "string" && record.projectId.trim() !== "");
  if (hasProjectID) {
    return record;
  }

  return (
    extractRealtimeProjectPayload(record.message) ??
    extractRealtimeProjectPayload(record.data) ??
    extractRealtimeProjectPayload(record.payload)
  );
}

function formatMessageTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function sortByCreatedDesc(messages: ProjectChatMessage[]): ProjectChatMessage[] {
  return [...messages].sort((a, b) => {
    const aMs = Date.parse(a.created_at);
    const bMs = Date.parse(b.created_at);
    if (Number.isNaN(aMs) || Number.isNaN(bMs)) {
      return b.id.localeCompare(a.id);
    }
    if (aMs === bMs) {
      return b.id.localeCompare(a.id);
    }
    return bMs - aMs;
  });
}

export default function ProjectChatPanel({
  projectId,
  active,
  onUnreadChange,
}: ProjectChatPanelProps) {
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [sendError, setSendError] = useState<string | null>(null);
  const [messages, setMessages] = useState<ProjectChatMessage[]>([]);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [searchItems, setSearchItems] = useState<ProjectChatSearchItem[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [deliveryIndicator, setDeliveryIndicator] = useState<DeliveryIndicator | null>(null);
  const [resettingSession, setResettingSession] = useState(false);
  const [resetError, setResetError] = useState<string | null>(null);

  const { connected, lastMessage, sendMessage } = useWS();
  const searchTimeoutRef = useRef<number | null>(null);
  const awaitingResponseTimeoutRef = useRef<number | null>(null);

  const orgID = useMemo(() => getStoredOrgID(), [projectId]);
  const currentAuthor = useMemo(() => getCurrentAuthor(), [projectId]);

  const clearAwaitingResponseTimeout = () => {
    if (awaitingResponseTimeoutRef.current !== null) {
      window.clearTimeout(awaitingResponseTimeoutRef.current);
      awaitingResponseTimeoutRef.current = null;
    }
  };

  useEffect(() => {
    onUnreadChange?.(unreadCount);
  }, [onUnreadChange, unreadCount]);

  useEffect(() => {
    if (!active) {
      return;
    }
    setUnreadCount(0);
  }, [active]);

  useEffect(() => {
    return () => {
      clearAwaitingResponseTimeout();
    };
  }, []);

  useEffect(() => {
    let cancelled = false;

    async function loadMessages() {
      if (!projectId || !orgID) {
        setMessages([]);
        setLoading(false);
        return;
      }

      setLoading(true);
      setLoadError(null);

      try {
        const url = new URL(`${API_URL}/api/projects/${projectId}/chat`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("limit", "100");

        const response = await fetch(url.toString(), {
          headers: {
            "Content-Type": "application/json",
          },
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load project chat");
        }

        const payload = await response.json();
        const parsed = Array.isArray(payload?.messages)
          ? payload.messages
              .map((entry: unknown) => normalizeMessage(entry))
              .filter((entry: ProjectChatMessage | null): entry is ProjectChatMessage =>
                entry !== null
              )
          : [];

        if (!cancelled) {
          setMessages(sortByCreatedDesc(parsed));
        }
      } catch (error) {
        if (!cancelled) {
          setLoadError(error instanceof Error ? error.message : "Failed to load chat");
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }

    void loadMessages();

    return () => {
      cancelled = true;
    };
  }, [projectId, orgID]);

  useEffect(() => {
    if (!connected || !orgID || !projectId) {
      return;
    }

    sendMessage({
      type: "subscribe",
      org_id: orgID,
      channel: `project:${projectId}:chat`,
    });
  }, [connected, orgID, projectId, sendMessage]);

  useEffect(() => {
    if (!lastMessage || lastMessage.type !== "ProjectChatMessageCreated") {
      return;
    }

    const incoming = normalizeMessage(extractRealtimeProjectPayload(lastMessage.data));
    if (!incoming || incoming.project_id !== projectId) {
      return;
    }

    if (incoming.isSessionReset) {
      setDeliveryIndicator({ tone: "neutral", text: "Started new session" });
    }

    const incomingAuthor = incoming.author.trim().toLowerCase();
    const currentAuthorNormalized = currentAuthor.trim().toLowerCase();
    if (
      !incoming.isSessionReset &&
      incomingAuthor !== "" &&
      incomingAuthor !== currentAuthorNormalized
    ) {
      clearAwaitingResponseTimeout();
      setSendError(null);
      setDeliveryIndicator({ tone: "success", text: "Agent replied" });
    }

    setMessages((prev) => {
      const withoutOptimisticDuplicate = prev.filter((existing) => {
        if (!existing.optimistic) {
          return true;
        }
        return !(
          existing.body === incoming.body &&
          existing.author === incoming.author &&
          existing.project_id === incoming.project_id
        );
      });

      if (withoutOptimisticDuplicate.some((existing) => existing.id === incoming.id)) {
        return withoutOptimisticDuplicate;
      }

      return sortByCreatedDesc([incoming, ...withoutOptimisticDuplicate]);
    });

    if (!active) {
      setUnreadCount((count) => count + 1);
    }
  }, [active, currentAuthor, lastMessage, projectId]);

  const resetChatSession = async (): Promise<void> => {
    if (!projectId || !orgID || resettingSession) {
      return;
    }
    setResettingSession(true);
    setResetError(null);
    try {
      const response = await fetch(
        `${API_URL}/api/projects/${projectId}/chat/reset?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
        },
      );
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to reset chat session");
      }
      const payload = await response.json();
      const marker = normalizeMessage(payload?.message);
      if (marker) {
        setMessages((prev) => {
          if (prev.some((message) => message.id === marker.id)) {
            return prev;
          }
          return sortByCreatedDesc([marker, ...prev]);
        });
      }
      setDeliveryIndicator({ tone: "neutral", text: "Started new session" });
    } catch (error) {
      setResetError(error instanceof Error ? error.message : "Failed to reset chat session");
    } finally {
      setResettingSession(false);
    }
  };

  useEffect(() => {
    if (searchTimeoutRef.current !== null) {
      window.clearTimeout(searchTimeoutRef.current);
      searchTimeoutRef.current = null;
    }

    const trimmed = searchQuery.trim();
    if (trimmed === "") {
      setSearchItems([]);
      setSearchError(null);
      setSearching(false);
      return;
    }

    if (!orgID || !projectId) {
      return;
    }

    setSearching(true);
    setSearchError(null);

    searchTimeoutRef.current = window.setTimeout(async () => {
      try {
        const url = new URL(`${API_URL}/api/projects/${projectId}/chat/search`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("q", trimmed);
        url.searchParams.set("limit", "25");

        const response = await fetch(url.toString(), {
          headers: { "Content-Type": "application/json" },
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Search failed");
        }

        const payload = await response.json();
        const items = Array.isArray(payload?.items)
          ? payload.items
              .map((entry: unknown) => {
                if (!entry || typeof entry !== "object") {
                  return null;
                }
                const itemRecord = entry as Record<string, unknown>;
                const message = normalizeMessage(itemRecord.message);
                if (!message) {
                  return null;
                }
                return {
                  message,
                  relevance:
                    typeof itemRecord.relevance === "number"
                      ? itemRecord.relevance
                      : 0,
                  snippet:
                    typeof itemRecord.snippet === "string"
                      ? itemRecord.snippet
                      : message.body,
                } satisfies ProjectChatSearchItem;
              })
              .filter((entry: ProjectChatSearchItem | null): entry is ProjectChatSearchItem =>
                entry !== null
              )
          : [];

        setSearchItems(items);
      } catch (error) {
        setSearchError(error instanceof Error ? error.message : "Search failed");
      } finally {
        setSearching(false);
      }
    }, 250);

    return () => {
      if (searchTimeoutRef.current !== null) {
        window.clearTimeout(searchTimeoutRef.current);
        searchTimeoutRef.current = null;
      }
    };
  }, [orgID, projectId, searchQuery]);

  const displayMessages = useMemo(() => {
    return [...messages].sort((a, b) => {
      const aMs = Date.parse(a.created_at);
      const bMs = Date.parse(b.created_at);
      if (Number.isNaN(aMs) || Number.isNaN(bMs)) {
        return a.id.localeCompare(b.id);
      }
      return aMs - bMs;
    });
  }, [messages]);

  const sendChatMessage = async (
    body: string,
    retryingMessage?: ProjectChatMessage
  ): Promise<void> => {
    const trimmedBody = body.trim();
    if (trimmedBody === "" || !projectId || !orgID) {
      return;
    }

    const optimisticID = retryingMessage?.id ?? `temp-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    const optimisticMessage: ProjectChatMessage = {
      id: optimisticID,
      project_id: projectId,
      author: getCurrentAuthor(),
      body: trimmedBody,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      optimistic: true,
      failed: false,
    };

    if (!retryingMessage) {
      setDraft("");
    }

    setMessages((prev) => {
      const withoutRetrying = retryingMessage
        ? prev.filter((message) => message.id !== retryingMessage.id)
        : prev;
      return sortByCreatedDesc([optimisticMessage, ...withoutRetrying]);
    });

    setSending(true);
    setSendError(null);
    setDeliveryIndicator({ tone: "neutral", text: "Sending..." });
    try {
      const response = await fetch(
        `${API_URL}/api/projects/${projectId}/chat/messages?org_id=${encodeURIComponent(orgID)}`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            author: optimisticMessage.author,
            body: trimmedBody,
          }),
        }
      );

      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to send message");
      }

      const payload = await response.json();
      const savedMessage = normalizeMessage(payload?.message);
      if (!savedMessage) {
        throw new Error("Server returned invalid message");
      }

      setMessages((prev) => {
        const next = prev.filter((message) => message.id !== optimisticID);
        if (!next.some((message) => message.id === savedMessage.id)) {
          next.unshift(savedMessage);
        }
        return sortByCreatedDesc(next);
      });

      const delivery = payload?.delivery as
        | { attempted?: boolean; delivered?: boolean; error?: string }
        | undefined;
      const deliveryError =
        typeof delivery?.error === "string" ? delivery.error.trim() : "";
      if (deliveryError) {
        setSendError(deliveryError);
      } else {
        setSendError(null);
      }

      clearAwaitingResponseTimeout();
      if (delivery?.delivered === true) {
        setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
        awaitingResponseTimeoutRef.current = window.setTimeout(() => {
          setDeliveryIndicator({
            tone: "warning",
            text: "Delivered; waiting for agent response",
          });
        }, 20000);
      } else if (deliveryError) {
        setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
      } else {
        setDeliveryIndicator({ tone: "neutral", text: "Saved" });
      }
    } catch {
      setMessages((prev) =>
        prev.map((message) =>
          message.id === optimisticID
            ? {
                ...message,
                optimistic: false,
                failed: true,
              }
            : message
        )
      );
      setSendError("Send failed; message not saved");
      setDeliveryIndicator({ tone: "warning", text: "Send failed; not saved" });
    } finally {
      setSending(false);
    }
  };

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    await sendChatMessage(draft);
  };

  const onDraftKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void sendChatMessage(draft);
    }
  };

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

  return (
    <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
      <section className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <header className="mb-4 flex items-center justify-between gap-3">
          <h2 className="text-base font-semibold text-[var(--text)]">Project Chat</h2>
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => {
                void resetChatSession();
              }}
              disabled={resettingSession}
              className="rounded-md border border-[var(--border)] px-2.5 py-1 text-[11px] font-medium text-[var(--text)] hover:bg-[var(--surface-alt)] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {resettingSession ? "Resetting..." : "Reset chat session"}
            </button>
            <span className="text-xs text-[var(--text-muted)]">
              {messages.filter((message) => !message.isSessionReset).length} message
              {messages.filter((message) => !message.isSessionReset).length === 1 ? "" : "s"}
            </span>
          </div>
        </header>

        <div className="mb-4 max-h-[420px] space-y-3 overflow-y-auto rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] p-3">
          {loading ? (
            <p className="text-sm text-[var(--text-muted)]">Loading chat...</p>
          ) : loadError ? (
            <p className="text-sm text-red-500">{loadError}</p>
          ) : displayMessages.length === 0 ? (
            <p className="text-sm text-[var(--text-muted)]">No project chat messages yet.</p>
          ) : (
            displayMessages.map((message) => {
              if (message.isSessionReset) {
                return (
                  <div
                    key={message.id}
                    className="my-2 rounded-lg border border-[#C9A86C]/45 bg-[#C9A86C]/12 px-3 py-2 text-center"
                    data-testid="project-chat-session-divider"
                  >
                    <p className="text-xs font-semibold uppercase tracking-wide text-[#C9A86C]">
                      New chat session started
                    </p>
                    <p className="mt-1 text-[11px] text-[var(--text-muted)]">
                      {formatMessageTime(message.created_at)}
                    </p>
                  </div>
                );
              }
              return (
                <article
                  key={message.id}
                  className={`rounded-lg border p-3 text-sm ${
                    message.failed
                      ? "border-red-300 bg-red-50 dark:border-red-900/60 dark:bg-red-950/20"
                      : "border-[var(--border)] bg-[var(--surface)]"
                  }`}
                  data-testid="project-chat-message"
                >
                  <div className="mb-1 flex items-center justify-between gap-2 text-xs text-[var(--text-muted)]">
                    <span className="font-semibold text-[var(--text)]">{message.author}</span>
                    <span>{formatMessageTime(message.created_at)}</span>
                  </div>
                  <p className="whitespace-pre-wrap text-[var(--text)]">{message.body}</p>
                  {message.optimistic ? (
                    <p className="mt-2 text-xs text-amber-600 dark:text-amber-300">Sending...</p>
                  ) : null}
                  {message.failed ? (
                    <button
                      type="button"
                      onClick={() => {
                        void sendChatMessage(message.body, message);
                      }}
                      className="mt-2 text-xs font-semibold text-red-600 hover:underline dark:text-red-300"
                    >
                      Retry send
                    </button>
                  ) : null}
                </article>
              );
            })
          )}
        </div>

        {resetError ? (
          <div className="mb-3 rounded-lg border border-[var(--red)]/40 bg-[var(--red)]/15 px-3 py-2 text-sm text-[var(--red)]">
            {resetError}
          </div>
        ) : null}

        {sendError ? (
          <div className="mb-3 rounded-lg border border-[var(--red)]/40 bg-[var(--red)]/15 px-3 py-2 text-sm text-[var(--red)]">
            {sendError}
          </div>
        ) : null}

        {deliveryIndicator ? (
          <div className="mb-3">
            <p
              className={`inline-flex rounded-full border px-2.5 py-0.5 text-[11px] ${deliveryIndicatorClassName}`}
            >
              {deliveryIndicator.text}
            </p>
          </div>
        ) : null}

        <form onSubmit={onSubmit} className="space-y-3">
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={onDraftKeyDown}
            placeholder="Share an idea for this project..."
            className="min-h-[88px] w-full resize-y rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--text)]"
          />
          <div className="flex justify-end">
            <button
              type="submit"
              disabled={sending || draft.trim() === ""}
              className="rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-medium text-white transition hover:bg-[#B8975B] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {sending ? "Sending..." : "Send"}
            </button>
          </div>
          <p className="text-[11px] text-[var(--text-muted)]">
            Press <span className="font-medium">Enter</span> to send,{" "}
            <span className="font-medium">Shift + Enter</span> for a new line
          </p>
        </form>
      </section>

      <aside className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <header className="mb-3 flex items-center justify-between gap-3">
          <h3 className="text-sm font-semibold text-[var(--text)]">Search Chat</h3>
          {searchQuery.trim() !== "" ? (
            <button
              type="button"
              className="text-xs font-medium text-[var(--text-muted)] hover:text-[var(--text)]"
              onClick={() => {
                setSearchQuery("");
                setSearchItems([]);
                setSearchError(null);
              }}
            >
              Clear
            </button>
          ) : null}
        </header>

        <input
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.target.value)}
          placeholder="Search project chat"
          className="mb-3 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
        />

        <div className="max-h-[380px] space-y-2 overflow-y-auto pr-1">
          {searchQuery.trim() === "" ? (
            <p className="text-xs text-[var(--text-muted)]">
              Search comments, decisions, and brainstorm notes for this project.
            </p>
          ) : searching ? (
            <p className="text-xs text-[var(--text-muted)]">Searching...</p>
          ) : searchError ? (
            <p className="text-xs text-red-500">{searchError}</p>
          ) : searchItems.length === 0 ? (
            <p className="text-xs text-[var(--text-muted)]">No matches found.</p>
          ) : (
            searchItems.map((item) => (
              <article
                key={`search-${item.message.id}`}
                className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2"
              >
                <div className="mb-1 flex items-center justify-between gap-2 text-[11px] text-[var(--text-muted)]">
                  <span className="font-semibold text-[var(--text)]">{item.message.author}</span>
                  <span>{formatMessageTime(item.message.created_at)}</span>
                </div>
                <p className="text-xs text-[var(--text)]">{item.snippet || item.message.body}</p>
              </article>
            ))
          )}
        </div>
      </aside>
    </div>
  );
}
