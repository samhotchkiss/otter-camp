import { useCallback, useEffect, useMemo, useState } from "react";
import { useGlobalChat } from "../../contexts/GlobalChatContext";
import GlobalChatSurface from "./GlobalChatSurface";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const CHAT_SESSION_RESET_PREFIX = "chat_session_reset:";

function conversationTypeLabel(type: "dm" | "project" | "issue"): string {
  if (type === "project") {
    return "Project";
  }
  if (type === "issue") {
    return "Issue";
  }
  return "DM";
}

export default function GlobalChatDock() {
  const {
    isOpen,
    totalUnread,
    conversations,
    selectedConversation,
    selectedKey,
    setDockOpen,
    toggleDock,
    selectConversation,
    markConversationRead,
  } = useGlobalChat();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [resettingProjectSession, setResettingProjectSession] = useState(false);
  const [resetProjectError, setResetProjectError] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const visibleConversations = useMemo(() => {
    if (conversations.length > 0) {
      return conversations;
    }
    if (selectedConversation) {
      return [selectedConversation];
    }
    return [];
  }, [conversations, selectedConversation]);

  useEffect(() => {
    if (isOpen && selectedKey) {
      markConversationRead(selectedKey);
    }
  }, [isOpen, markConversationRead, selectedKey]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && isOpen) {
        if (isFullscreen) {
          setIsFullscreen(false);
        } else {
          setDockOpen(false);
        }
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [isOpen, isFullscreen, setDockOpen]);

  useEffect(() => {
    setResetProjectError(null);
  }, [selectedKey]);

  const unreadBadge = useMemo(() => {
    if (totalUnread <= 0) {
      return null;
    }
    return (
      <span className="inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-[var(--red)] px-1.5 text-[11px] font-semibold text-white">
        {totalUnread > 99 ? "99+" : totalUnread}
      </span>
    );
  }, [totalUnread]);

  const handleClearSession = useCallback(async () => {
    if (!selectedConversation) {
      return;
    }

    const orgID = (window.localStorage.getItem("otter-camp-org-id") ?? "").trim();
    if (!orgID) {
      setResetProjectError("Missing organization context");
      return;
    }

    setResettingProjectSession(true);
    setResetProjectError(null);
    try {
      const token = (window.localStorage.getItem("otter_camp_token") ?? "").trim();
      const headers = {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      };
      const resetMarker = `${CHAT_SESSION_RESET_PREFIX}${Date.now().toString(36)}`;

      if (selectedConversation.type === "project") {
        const response = await fetch(
          `${API_URL}/api/projects/${selectedConversation.projectId}/chat/reset?org_id=${encodeURIComponent(orgID)}`,
          {
            method: "POST",
            headers,
          },
        );
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to clear chat session");
        }
      } else if (selectedConversation.type === "dm") {
        const response = await fetch(`${API_URL}/api/messages`, {
          method: "POST",
          headers,
          body: JSON.stringify({
            org_id: orgID,
            thread_id: selectedConversation.threadId,
            content: resetMarker,
            sender_type: "agent",
            sender_name: "Session",
            sender_id: "session-reset",
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to clear chat session");
        }
      } else {
        const issueResponse = await fetch(
          `${API_URL}/api/issues/${selectedConversation.issueId}?org_id=${encodeURIComponent(orgID)}`,
          {
            method: "GET",
            headers,
            cache: "no-store",
          },
        );
        if (!issueResponse.ok) {
          const payload = await issueResponse.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to resolve issue participant for reset");
        }
        const issuePayload = (await issueResponse.json()) as {
          participants?: Array<{
            agent_id: string;
            role: "owner" | "collaborator";
            removed_at?: string | null;
          }>;
        };
        const activeParticipants = Array.isArray(issuePayload.participants)
          ? issuePayload.participants.filter((entry) => !entry.removed_at)
          : [];
        const ownerAgentID =
          activeParticipants.find((entry) => entry.role === "owner")?.agent_id ??
          activeParticipants[0]?.agent_id ??
          "";
        if (!ownerAgentID) {
          throw new Error("No issue participant available to anchor reset marker");
        }

        const response = await fetch(
          `${API_URL}/api/issues/${selectedConversation.issueId}/comments?org_id=${encodeURIComponent(orgID)}`,
          {
            method: "POST",
            headers,
            body: JSON.stringify({
              author_agent_id: ownerAgentID,
              body: resetMarker,
              sender_type: "agent",
            }),
          },
        );
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to clear chat session");
        }
      }

      setRefreshVersion((version) => version + 1);
    } catch (error) {
      setResetProjectError(error instanceof Error ? error.message : "Failed to clear chat session");
    } finally {
      setResettingProjectSession(false);
    }
  }, [selectedConversation]);

  if (!isOpen) {
    return (
      <div className="fixed bottom-4 right-4 z-50">
        <button
          type="button"
          onClick={() => setDockOpen(true)}
          className="inline-flex items-center gap-2 rounded-t-xl rounded-bl-xl border border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 text-sm font-medium text-[var(--text)] shadow-lg transition hover:border-[var(--accent)]"
          aria-label="Open global chat"
        >
          <span>Chats</span>
          {unreadBadge}
        </button>
      </div>
    );
  }

  return (
    <div className={isFullscreen
      ? "fixed inset-0 top-[var(--topbar-height,56px)] z-50"
      : "fixed bottom-4 right-4 z-50 w-[min(960px,calc(100vw-2rem))]"
    }>
      <section className={`overflow-hidden border border-[var(--border)] bg-[var(--surface)] shadow-2xl ${isFullscreen ? "h-full" : "rounded-2xl"}`}>
        <header className="flex items-center justify-between border-b border-[var(--border)] bg-[var(--surface-alt)] px-4 py-2.5">
          <div className="flex items-center gap-3">
            <h2 className="text-sm font-semibold text-[var(--text)]">Global Chat</h2>
            {unreadBadge}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => {
                void handleClearSession();
              }}
              disabled={resettingProjectSession || !selectedConversation}
              className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {resettingProjectSession ? "Clearing..." : "Clear session"}
            </button>
            <button
              type="button"
              onClick={() => setIsFullscreen(!isFullscreen)}
              className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
              aria-label={isFullscreen ? "Exit fullscreen chat" : "Fullscreen chat"}
            >
              {isFullscreen ? "⊡" : "⊞"}
            </button>
            <button
              type="button"
              onClick={() => { setIsFullscreen(false); toggleDock(); }}
              className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
              aria-label="Collapse global chat"
            >
              Close
            </button>
            <button
              type="button"
              onClick={() => { setIsFullscreen(false); setDockOpen(false); }}
              className="inline-flex h-6 w-6 items-center justify-center rounded-md border border-[var(--border)] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
              aria-label="Close global chat"
            >
              ×
            </button>
          </div>
        </header>
        {resetProjectError ? (
          <div className="border-b border-[var(--red)]/40 bg-[var(--red)]/15 px-4 py-2">
            <p className="text-xs text-[var(--red)]">{resetProjectError}</p>
          </div>
        ) : null}

        <div className={`grid grid-cols-[260px_1fr] ${isFullscreen ? "h-[calc(100%-44px)]" : "h-[min(72vh,620px)] max-h-[calc(100vh-2rem)]"}`}>
          <aside className="min-h-0 border-r border-[var(--border)] bg-[var(--surface-alt)]/50">
            <div className="h-full overflow-y-auto p-2">
              {visibleConversations.length === 0 ? (
                <div className="rounded-xl border border-dashed border-[var(--border)] p-4 text-xs text-[var(--text-muted)]">
                  Start a chat from Agents, Projects, or an Issue thread.
                </div>
              ) : (
                visibleConversations.map((conversation) => {
                  const active = selectedKey === conversation.key;
                  return (
                    <button
                      key={conversation.key}
                      type="button"
                      onClick={() => {
                        selectConversation(conversation.key);
                        markConversationRead(conversation.key);
                      }}
                      className={`mb-1 w-full rounded-xl border px-3 py-2 text-left transition ${
                        active
                          ? "border-[var(--accent)] bg-[var(--surface)]"
                          : "border-transparent hover:border-[var(--border)] hover:bg-[var(--surface)]/80"
                      }`}
                    >
                      <div className="mb-1 flex items-center justify-between gap-2">
                        <span className="truncate text-sm font-semibold text-[var(--text)]">
                          {conversation.title || "Untitled chat"}
                        </span>
                        {conversation.unreadCount > 0 ? (
                          <span className="inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-[var(--red)] px-1 text-[10px] font-semibold text-white">
                            {conversation.unreadCount > 9 ? "9+" : conversation.unreadCount}
                          </span>
                        ) : null}
                      </div>
                      <div className="flex items-center gap-2 text-[11px] text-[var(--text-muted)]">
                        <span className="rounded-full border border-[var(--border)] px-1.5 py-0.5 uppercase tracking-wide">
                          {conversationTypeLabel(conversation.type)}
                        </span>
                        <span className="truncate">{conversation.contextLabel}</span>
                      </div>
                      {conversation.subtitle ? (
                        <p className="mt-1 truncate text-[11px] text-[var(--text-muted)]">{conversation.subtitle}</p>
                      ) : null}
                    </button>
                  );
                })
              )}
            </div>
          </aside>

          <div className="flex h-full min-h-0 flex-col">
            {selectedConversation ? (
              <>
                <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-2">
                  <div>
                    <h3 className="text-sm font-semibold text-[var(--text)]">
                      {selectedConversation.title || "Untitled chat"}
                    </h3>
                    <p className="text-xs text-[var(--text-muted)]">{selectedConversation.contextLabel}</p>
                  </div>
                  <button
                    type="button"
                    onClick={() => {
                      void handleClearSession();
                    }}
                    disabled={resettingProjectSession}
                    className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {resettingProjectSession ? "Clearing..." : "Clear session"}
                  </button>
                </div>
                <div className="min-h-0 flex-1">
                  <GlobalChatSurface
                    conversation={selectedConversation}
                    refreshVersion={refreshVersion}
                    onConversationTouched={() => {
                      if (selectedConversation.unreadCount > 0) {
                        markConversationRead(selectedConversation.key);
                      }
                    }}
                  />
                </div>
              </>
            ) : (
              <div className="flex h-full items-center justify-center text-sm text-[var(--text-muted)]">
                Select a chat conversation.
              </div>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
