import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useGlobalChat, type GlobalChatConversation } from "../../contexts/GlobalChatContext";
import { API_URL } from "../../lib/api";
import GlobalChatSurface from "./GlobalChatSurface";
import { getInitials } from "../messaging/utils";

const CHAT_SESSION_RESET_PREFIX = "chat_session_reset:";

function conversationTypeLabel(type: "dm" | "project" | "issue"): string {
  if (type === "project") {
    return "Project";
  }
  if (type === "issue") {
    return "Task";
  }
  return "DM";
}

function isGenericDMLabel(value: string): boolean {
  const lower = value.trim().toLowerCase();
  return lower === "" || lower === "you" || lower === "user" || lower === "agent" || lower === "assistant";
}

export default function GlobalChatDock() {
  const {
    isOpen,
    totalUnread,
    agentNamesByID,
    resolveAgentName,
    conversations,
    selectedConversation,
    selectedKey,
    setDockOpen,
    toggleDock,
    selectConversation,
    markConversationRead,
    archiveConversation,
  } = useGlobalChat();
  const navigate = useNavigate();
  const location = useLocation();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [resettingProjectSession, setResettingProjectSession] = useState(false);
  const [resetProjectError, setResetProjectError] = useState<string | null>(null);
  const [archiveError, setArchiveError] = useState<string | null>(null);
  const [archivingChatID, setArchivingChatID] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const visibleConversations = useMemo(() => {
    const byRecent = (items: GlobalChatConversation[]): GlobalChatConversation[] =>
      [...items].sort((a, b) => {
        const aMs = Date.parse(a.updatedAt);
        const bMs = Date.parse(b.updatedAt);
        const aScore = Number.isFinite(aMs) ? aMs : 0;
        const bScore = Number.isFinite(bMs) ? bMs : 0;
        if (aScore === bScore) {
          return 0;
        }
        return bScore - aScore;
      });

    if (conversations.length > 0) {
      return byRecent(conversations);
    }
    if (selectedConversation) {
      return byRecent([selectedConversation]);
    }
    return [];
  }, [conversations, selectedConversation]);

  const routeProjectID = useMemo(() => {
    const taskRouteMatch = /^\/projects\/([^/]+)\/tasks\/[^/]+$/.exec(location.pathname);
    if (taskRouteMatch?.[1]) {
      return decodeURIComponent(taskRouteMatch[1]);
    }
    const legacyIssueRouteMatch = /^\/projects\/([^/]+)\/issues\/[^/]+$/.exec(location.pathname);
    if (legacyIssueRouteMatch?.[1]) {
      return decodeURIComponent(legacyIssueRouteMatch[1]);
    }
    const projectRouteMatch = /^\/projects\/([^/]+)$/.exec(location.pathname);
    if (projectRouteMatch?.[1]) {
      return decodeURIComponent(projectRouteMatch[1]);
    }
    return "";
  }, [location.pathname]);

  const selectedJumpTarget = useMemo(() => {
    if (!selectedConversation) {
      return null;
    }
    if (selectedConversation.type === "project") {
      return {
        label: "Open project",
        href: `/projects/${encodeURIComponent(selectedConversation.projectId)}`,
      };
    }
    if (selectedConversation.type === "issue") {
      const projectID = selectedConversation.projectId?.trim() || routeProjectID;
      if (!projectID) {
        return null;
      }
      return {
        label: "Open task",
        href: `/projects/${encodeURIComponent(projectID)}/tasks/${encodeURIComponent(selectedConversation.issueId)}`,
      };
    }
    return null;
  }, [routeProjectID, selectedConversation]);

  useEffect(() => {
    if (isOpen && selectedKey) {
      markConversationRead(selectedKey);
    }
  }, [isOpen, markConversationRead, selectedKey]);

  useEffect(() => {
    const match = /^\/chats\/([^/]+)$/.exec(location.pathname);
    if (!match?.[1]) {
      return;
    }
    const chatID = decodeURIComponent(match[1]);
    const chatFromURL = conversations.find((conversation) => conversation.chatId === chatID);
    if (!chatFromURL) {
      return;
    }
    if (selectedKey !== chatFromURL.key) {
      selectConversation(chatFromURL.key);
    }
    setDockOpen(true);
  }, [conversations, location.pathname, selectConversation, selectedKey, setDockOpen]);

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

  useEffect(() => {
    setArchiveError(null);
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

  const resolveConversationTitle = useCallback(
    (conversation: GlobalChatConversation): string => {
      if (conversation.type !== "dm") {
        return conversation.title || "Untitled chat";
      }

      const candidates = [
        conversation.agent.name,
        conversation.agent.id,
        conversation.threadId,
        conversation.title,
      ];

      let fallbackLabel = "";
      for (const candidate of candidates) {
        const trimmed = candidate.trim();
        if (!trimmed) {
          continue;
        }
        const resolved = resolveAgentName(trimmed).trim();
        if (!resolved) {
          continue;
        }
        if (isGenericDMLabel(resolved)) {
          if (!fallbackLabel) {
            fallbackLabel = resolved;
          }
          continue;
        }
        if (resolved) {
          return resolved;
        }
      }

      return fallbackLabel || conversation.title || conversation.agent.name || "Untitled chat";
    },
    [resolveAgentName],
  );

  const formatConversationTimestamp = useCallback((value: string): string => {
    const parsed = Date.parse(value);
    if (!Number.isFinite(parsed)) {
      return "";
    }
    const date = new Date(parsed);
    const now = new Date();
    const sameDay = date.toDateString() === now.toDateString();
    if (sameDay) {
      return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
    }
    return date.toLocaleDateString([], { month: "short", day: "numeric" });
  }, []);

  const handleArchiveConversation = useCallback(async (chatID: string) => {
    const trimmedChatID = chatID.trim();
    if (!trimmedChatID) {
      return;
    }
    setArchiveError(null);
    setArchivingChatID(trimmedChatID);
    const success = await archiveConversation(trimmedChatID);
    if (!success) {
      setArchiveError("Failed to archive chat");
    } else if (location.pathname.startsWith("/chats/")) {
      navigate("/chats");
    }
    setArchivingChatID(null);
  }, [archiveConversation, location.pathname, navigate]);

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
        const threadAgentID = selectedConversation.threadId.startsWith("dm_")
          ? selectedConversation.threadId.slice(3).trim()
          : "";
        const resetAgentID = threadAgentID || selectedConversation.agent.id.trim();
        if (!resetAgentID) {
          throw new Error("Failed to resolve DM agent for reset");
        }

        const resetResponse = await fetch(
          `${API_URL}/api/admin/agents/${encodeURIComponent(resetAgentID)}/reset`,
          {
            method: "POST",
            headers,
          },
        );
        if (!resetResponse.ok) {
          const payload = await resetResponse.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to clear chat session");
        }

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
        const taskResponse = await fetch(
          `${API_URL}/api/project-tasks/${selectedConversation.issueId}?org_id=${encodeURIComponent(orgID)}`,
          {
            method: "GET",
            headers,
            cache: "no-store",
          },
        );
        if (!taskResponse.ok) {
          const payload = await taskResponse.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to resolve task participant for reset");
        }
        const taskPayload = (await taskResponse.json()) as {
          participants?: Array<{
            agent_id: string;
            role: "owner" | "collaborator";
            removed_at?: string | null;
          }>;
        };
        const activeParticipants = Array.isArray(taskPayload.participants)
          ? taskPayload.participants.filter((entry) => !entry.removed_at)
          : [];
        const ownerAgentID =
          activeParticipants.find((entry) => entry.role === "owner")?.agent_id ??
          activeParticipants[0]?.agent_id ??
          "";
        if (!ownerAgentID) {
          throw new Error("No task participant available to anchor reset marker");
        }

        const response = await fetch(
          `${API_URL}/api/project-tasks/${selectedConversation.issueId}/comments?org_id=${encodeURIComponent(orgID)}`,
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
      : "fixed bottom-4 right-4 z-50 w-[calc(100vw-2rem)] max-w-[960px]"
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
        {archiveError ? (
          <div className="border-b border-[var(--red)]/40 bg-[var(--red)]/15 px-4 py-2">
            <p className="text-xs text-[var(--red)]">{archiveError}</p>
          </div>
        ) : null}

        <div className={`grid grid-cols-[260px_minmax(0,1fr)] ${isFullscreen ? "h-[calc(100%-44px)]" : "h-[min(72vh,620px)] max-h-[calc(100vh-2rem)]"}`}>
          <aside className="min-h-0 border-r border-[var(--border)] bg-[var(--surface-alt)]/50">
            <div className="flex items-center justify-between border-b border-[var(--border)] px-3 py-2 text-[11px]">
              <span className="font-semibold text-[var(--text)]">Active chats</span>
              <button
                type="button"
                onClick={() => navigate("/chats/archived")}
                className="rounded border border-[var(--border)] px-1.5 py-0.5 text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
              >
                Archived
              </button>
            </div>
            <div className="h-full overflow-y-auto p-2">
              {visibleConversations.length === 0 ? (
                <div className="rounded-xl border border-dashed border-[var(--border)] p-4 text-xs text-[var(--text-muted)]">
                  Start a chat from Agents, Projects, or a Task thread.
                </div>
              ) : (
                visibleConversations.map((conversation) => {
                  const active = selectedKey === conversation.key;
                  const displayTitle = resolveConversationTitle(conversation);
                  return (
                    <div
                      key={conversation.key}
                      role="button"
                      tabIndex={0}
                      onClick={() => {
                        selectConversation(conversation.key);
                        markConversationRead(conversation.key);
                        if (conversation.chatId) {
                          navigate(`/chats/${encodeURIComponent(conversation.chatId)}`);
                        }
                      }}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          selectConversation(conversation.key);
                          markConversationRead(conversation.key);
                          if (conversation.chatId) {
                            navigate(`/chats/${encodeURIComponent(conversation.chatId)}`);
                          }
                        }
                      }}
                      className={`mb-1 w-full rounded-xl border px-3 py-2 text-left transition ${
                        active
                          ? "border-[var(--accent)] bg-[var(--surface)]"
                          : conversation.unreadCount > 0
                            ? "border-[var(--accent)]/60 bg-[var(--surface)]/80 hover:border-[var(--accent)]"
                            : "border-transparent hover:border-[var(--border)] hover:bg-[var(--surface)]/80"
                      }`}
                    >
                      <div className="mb-1 flex items-center justify-between gap-2">
                        <div className="flex min-w-0 items-center gap-2">
                          <div
                            data-testid={`chat-initials-${conversation.key}`}
                            className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-[var(--border)] bg-[var(--surface-alt)] text-[10px] font-semibold text-[var(--text-muted)]"
                          >
                            {getInitials(displayTitle || "Untitled chat")}
                          </div>
                          <span className="truncate text-sm font-semibold text-[var(--text)]">
                            {displayTitle || "Untitled chat"}
                          </span>
                        </div>
                        <div className="flex items-center gap-1">
                          <span className="text-[10px] text-[var(--text-muted)]">
                            {formatConversationTimestamp(conversation.updatedAt)}
                          </span>
                          {conversation.unreadCount > 0 ? (
                            <span className="inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-[var(--red)] px-1 text-[10px] font-semibold text-white">
                              {conversation.unreadCount > 9 ? "9+" : conversation.unreadCount}
                            </span>
                          ) : null}
                        </div>
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
                      {conversation.chatId ? (
                        <div className="mt-2 flex justify-end">
                          <button
                            type="button"
                            onClick={(event) => {
                              event.preventDefault();
                              event.stopPropagation();
                              void handleArchiveConversation(conversation.chatId ?? "");
                            }}
                            disabled={archivingChatID === conversation.chatId}
                            className="rounded border border-[var(--border)] px-2 py-0.5 text-[10px] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {archivingChatID === conversation.chatId ? "Archiving..." : "Archive"}
                          </button>
                        </div>
                      ) : null}
                    </div>
                  );
                })
              )}
            </div>
          </aside>

          <div className="flex h-full min-h-0 min-w-0 flex-col">
            {selectedConversation ? (
              <>
                {(() => {
                  const selectedTitle = resolveConversationTitle(selectedConversation);
                  return (
                    <div className="flex items-center justify-between border-b border-[var(--border)] px-4 py-2">
                      <div className="min-w-0 flex-1">
                        <h3 className="truncate text-sm font-semibold text-[var(--text)]">
                          {selectedTitle || "Untitled chat"}
                        </h3>
                        <p className="truncate text-xs text-[var(--text-muted)]">
                          {selectedConversation.contextLabel}
                        </p>
                      </div>
                      <div className="ml-3 flex shrink-0 items-center gap-2">
                        <button
                          type="button"
                          onClick={() => {
                            setIsFullscreen(false);
                            setDockOpen(false);
                          }}
                          className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
                          aria-label="Minimize global chat"
                        >
                          Minimize
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            if (selectedJumpTarget) {
                              navigate(selectedJumpTarget.href);
                            }
                          }}
                          disabled={!selectedJumpTarget}
                          className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {selectedJumpTarget?.label || "Open context"}
                        </button>
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
                    </div>
                  );
                })()}
                <div className="min-h-0 flex-1">
                  <GlobalChatSurface
                    conversation={selectedConversation}
                    refreshVersion={refreshVersion}
                    agentNamesByID={agentNamesByID}
                    resolveAgentName={resolveAgentName}
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
