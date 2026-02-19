import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import {
  useGlobalChat,
  type GlobalChatConversation,
  type OpenConversationInput,
} from "../../contexts/GlobalChatContext";
import { API_URL } from "../../lib/api";
import GlobalChatSurface from "./GlobalChatSurface";
import { getChatContextCue } from "./chatContextCue";
import { getInitials } from "../messaging/utils";

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

function isGenericDMLabel(value: string): boolean {
  const lower = value.trim().toLowerCase();
  return lower === "" || lower === "you" || lower === "user" || lower === "agent" || lower === "assistant";
}

type GlobalChatDockProps = {
  embedded?: boolean;
  onToggleRail?: () => void;
};

type RouteScopedConversation =
  | {
      type: "project";
      projectId: string;
    }
  | {
      type: "issue";
      projectId: string;
      issueId: string;
    };

function buildProjectConversationKey(projectId: string): string {
  return `project:${projectId}`;
}

function buildIssueConversationKey(issueId: string): string {
  return `issue:${issueId}`;
}

function extractProjectNameFromContextLabel(label: string): string {
  const parts = label.split("•");
  if (parts.length < 2) {
    return "";
  }
  return parts.slice(1).join("•").trim();
}

function findFrankAgentFallback(agentNamesByID: Map<string, string>): { id: string; name: string } | null {
  const seen = new Set<string>();
  let best: { id: string; name: string; score: number } | null = null;

  for (const [rawAlias, rawName] of agentNamesByID.entries()) {
    const alias = rawAlias.trim();
    const name = rawName.trim();
    if (!alias || !name) {
      continue;
    }
    if (!name.toLowerCase().includes("frank")) {
      continue;
    }
    if (alias.startsWith("agent:")) {
      continue;
    }

    const normalizedID = alias.startsWith("dm_") ? alias.slice("dm_".length).trim() : alias;
    if (!normalizedID || seen.has(normalizedID)) {
      continue;
    }
    seen.add(normalizedID);

    const lower = normalizedID.toLowerCase();
    let score = 0;
    if (lower === "frank") {
      score += 6;
    }
    if (lower === "main") {
      score += 5;
    }
    if (lower.includes("frank")) {
      score += 4;
    }
    if (lower.startsWith("agent-")) {
      score += 2;
    }

    if (!best || score > best.score) {
      best = { id: normalizedID, name, score };
    }
  }

  if (!best) {
    return null;
  }
  return { id: best.id, name: best.name };
}

export default function GlobalChatDock({ embedded = false, onToggleRail }: GlobalChatDockProps) {
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
    openConversation,
  } = useGlobalChat();
  const navigate = useNavigate();
  const location = useLocation();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [resettingProjectSession, setResettingProjectSession] = useState(false);
  const [resetProjectError, setResetProjectError] = useState<string | null>(null);
  const [archiveError, setArchiveError] = useState<string | null>(null);
  const [archivingChatID, setArchivingChatID] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [routeScopeMode, setRouteScopeMode] = useState<"route" | "org">("route");
  const embeddedPanelPinnedOpen = embedded && typeof onToggleRail === "function";
  const dockOpen = embeddedPanelPinnedOpen ? true : isOpen;

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

  const routeScopedConversation = useMemo<RouteScopedConversation | null>(() => {
    const issueRouteMatch = /^\/projects\/([^/]+)\/issues\/([^/]+)(?:\/.*)?$/.exec(location.pathname);
    if (issueRouteMatch?.[1] && issueRouteMatch?.[2]) {
      return {
        type: "issue",
        projectId: decodeURIComponent(issueRouteMatch[1]),
        issueId: decodeURIComponent(issueRouteMatch[2]),
      };
    }
    const projectRouteMatch = /^\/projects\/([^/]+)\/?$/.exec(location.pathname);
    if (projectRouteMatch?.[1]) {
      return {
        type: "project",
        projectId: decodeURIComponent(projectRouteMatch[1]),
      };
    }
    return null;
  }, [location.pathname]);

  const routeProjectID = routeScopedConversation?.projectId ?? "";

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
        label: "Open issue",
        href: `/projects/${encodeURIComponent(projectID)}/issues/${encodeURIComponent(selectedConversation.issueId)}`,
      };
    }
    return null;
  }, [routeProjectID, selectedConversation]);
  const selectedContextCue = useMemo(() => {
    return getChatContextCue(selectedConversation?.type ?? null);
  }, [selectedConversation]);

  const routeScopedKey = useMemo(() => {
    if (!routeScopedConversation) {
      return "";
    }
    if (routeScopedConversation.type === "issue") {
      return buildIssueConversationKey(routeScopedConversation.issueId);
    }
    return buildProjectConversationKey(routeScopedConversation.projectId);
  }, [routeScopedConversation]);

  const routeProjectNameHint = useMemo(() => {
    if (!routeScopedConversation) {
      return "";
    }
    const fromProjectConversation = conversations.find(
      (conversation) =>
        conversation.type === "project" &&
        conversation.projectId === routeScopedConversation.projectId &&
        conversation.title.trim() !== "",
    );
    if (fromProjectConversation) {
      return fromProjectConversation.title.trim();
    }
    const fromIssueContext = conversations.find(
      (conversation) =>
        conversation.type === "issue" &&
        conversation.projectId === routeScopedConversation.projectId &&
        extractProjectNameFromContextLabel(conversation.contextLabel) !== "",
    );
    if (fromIssueContext) {
      return extractProjectNameFromContextLabel(fromIssueContext.contextLabel);
    }
    return "";
  }, [conversations, routeScopedConversation]);

  const routeScopedInput = useMemo<OpenConversationInput | null>(() => {
    if (!routeScopedConversation) {
      return null;
    }
    if (routeScopedConversation.type === "issue") {
      const issueTitle =
        conversations.find(
          (conversation) =>
            conversation.type === "issue" &&
            conversation.issueId === routeScopedConversation.issueId &&
            conversation.title.trim() !== "",
        )?.title ??
        routeScopedConversation.issueId;
      const contextLabel = routeProjectNameHint ? `Issue • ${routeProjectNameHint}` : "Issue";
      return {
        type: "issue",
        issueId: routeScopedConversation.issueId,
        projectId: routeScopedConversation.projectId,
        title: issueTitle,
        contextLabel,
        subtitle: "Issue conversation",
      };
    }
    const projectTitle = routeProjectNameHint || routeScopedConversation.projectId;
    return {
      type: "project",
      projectId: routeScopedConversation.projectId,
      title: projectTitle,
      contextLabel: routeProjectNameHint ? `Project • ${routeProjectNameHint}` : "Project",
      subtitle: "Project chat",
    };
  }, [conversations, routeProjectNameHint, routeScopedConversation]);

  useEffect(() => {
    if (dockOpen && selectedKey) {
      markConversationRead(selectedKey);
    }
  }, [dockOpen, markConversationRead, selectedKey]);

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
    setRouteScopeMode("route");
  }, [routeScopedKey]);

  useEffect(() => {
    if (!routeScopedConversation || routeScopeMode !== "route" || !routeScopedInput || !routeScopedKey) {
      return;
    }
    if (selectedKey === routeScopedKey) {
      return;
    }
    const existing = conversations.find((conversation) => conversation.key === routeScopedKey);
    if (existing) {
      selectConversation(existing.key);
      markConversationRead(existing.key);
      return;
    }
    openConversation(routeScopedInput, { focus: true, openDock: false });
  }, [
    conversations,
    markConversationRead,
    openConversation,
    routeScopedConversation,
    routeScopedInput,
    routeScopedKey,
    routeScopeMode,
    selectConversation,
    selectedKey,
  ]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && dockOpen) {
        if (isFullscreen) {
          setIsFullscreen(false);
        } else if (embeddedPanelPinnedOpen) {
          onToggleRail?.();
        } else {
          setDockOpen(false);
        }
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [dockOpen, embeddedPanelPinnedOpen, isFullscreen, onToggleRail, setDockOpen]);

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

  const routeScopedConversationLabel = routeScopedConversation?.type === "issue"
    ? "Issue chat"
    : "Project chat";

  const routeScopedSelectedConversation = useMemo(() => {
    if (!routeScopedConversation) {
      return null;
    }
    if (routeScopedConversation.type === "issue") {
      return conversations.find(
        (conversation) =>
          conversation.type === "issue" &&
          conversation.issueId === routeScopedConversation.issueId,
      ) ?? null;
    }
    return conversations.find(
      (conversation) =>
        conversation.type === "project" &&
        conversation.projectId === routeScopedConversation.projectId,
    ) ?? null;
  }, [conversations, routeScopedConversation]);

  const anyOrgDMConversation = useMemo(() => {
    const dmConversations = visibleConversations.filter((conversation) => conversation.type === "dm");
    return dmConversations[0] ?? null;
  }, [visibleConversations]);

  const frankOrgDMConversation = useMemo(() => {
    const dmConversations = visibleConversations.filter((conversation) => conversation.type === "dm");
    if (dmConversations.length === 0) {
      return null;
    }
    const frankMatch = dmConversations.find((conversation) => {
      const displayTitle = resolveConversationTitle(conversation).toLowerCase();
      const threadID = conversation.threadId.toLowerCase();
      const agentID = conversation.agent.id.toLowerCase();
      const agentName = conversation.agent.name.toLowerCase();
      return (
        displayTitle.includes("frank") ||
        agentName.includes("frank") ||
        agentID === "main" ||
        agentID.includes("frank") ||
        threadID.includes("frank")
      );
    });
    return frankMatch ?? null;
  }, [resolveConversationTitle, visibleConversations]);

  const frankFallbackAgent = useMemo(() => {
    return findFrankAgentFallback(agentNamesByID);
  }, [agentNamesByID]);

  const orgConversationTitle = useMemo(() => {
    if (frankOrgDMConversation) {
      return resolveConversationTitle(frankOrgDMConversation);
    }
    if (frankFallbackAgent) {
      return frankFallbackAgent.name;
    }
    if (anyOrgDMConversation) {
      return resolveConversationTitle(anyOrgDMConversation);
    }
    return "Frank";
  }, [anyOrgDMConversation, frankFallbackAgent, frankOrgDMConversation, resolveConversationTitle]);

  const hasOrgChatTarget = useMemo(() => {
    return Boolean(frankOrgDMConversation || frankFallbackAgent || anyOrgDMConversation);
  }, [anyOrgDMConversation, frankFallbackAgent, frankOrgDMConversation]);

  const routeScopedSwapLabel = useMemo(() => {
    if (!routeScopedConversation) {
      return "";
    }
    if (routeScopeMode === "route") {
      return `Org chat (${orgConversationTitle})`;
    }
    return `Back to ${routeScopedConversationLabel}`;
  }, [orgConversationTitle, routeScopeMode, routeScopedConversation, routeScopedConversationLabel]);

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

  const handleSelectConversation = useCallback((conversation: GlobalChatConversation) => {
    selectConversation(conversation.key);
    markConversationRead(conversation.key);
    if (routeScopedConversation) {
      if (
        (routeScopedConversation.type === "project" &&
          conversation.type === "project" &&
          conversation.projectId === routeScopedConversation.projectId) ||
        (routeScopedConversation.type === "issue" &&
          conversation.type === "issue" &&
          conversation.issueId === routeScopedConversation.issueId)
      ) {
        setRouteScopeMode("route");
      } else if (conversation.type === "dm") {
        setRouteScopeMode("org");
      }
    }
    if (!embedded && conversation.chatId) {
      navigate(`/chats/${encodeURIComponent(conversation.chatId)}`);
    }
  }, [embedded, markConversationRead, navigate, routeScopedConversation, selectConversation]);

  const handleSwapScope = useCallback(() => {
    if (!routeScopedConversation) {
      return;
    }
    if (routeScopeMode === "route") {
      if (frankOrgDMConversation) {
        setRouteScopeMode("org");
        selectConversation(frankOrgDMConversation.key);
        markConversationRead(frankOrgDMConversation.key);
        return;
      }
      if (frankFallbackAgent) {
        setRouteScopeMode("org");
        openConversation(
          {
            type: "dm",
            agent: {
              id: frankFallbackAgent.id,
              name: frankFallbackAgent.name,
              status: "online",
            },
            threadId: `dm_${frankFallbackAgent.id}`,
            title: frankFallbackAgent.name,
            contextLabel: "Organization chat",
            subtitle: "Direct message",
          },
          { focus: true, openDock: true },
        );
        return;
      }
      if (anyOrgDMConversation) {
        setRouteScopeMode("org");
        selectConversation(anyOrgDMConversation.key);
        markConversationRead(anyOrgDMConversation.key);
      }
      return;
    }

    setRouteScopeMode("route");
    if (routeScopedSelectedConversation) {
      selectConversation(routeScopedSelectedConversation.key);
      markConversationRead(routeScopedSelectedConversation.key);
      return;
    }
    if (routeScopedInput) {
      openConversation(routeScopedInput, { focus: true, openDock: true });
    }
  }, [
    anyOrgDMConversation,
    frankFallbackAgent,
    frankOrgDMConversation,
    markConversationRead,
    openConversation,
    routeScopeMode,
    routeScopedConversation,
    routeScopedInput,
    routeScopedSelectedConversation,
    selectConversation,
  ]);

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

  if (embedded) {
    if (!dockOpen) {
      return (
        <div className="flex h-full items-end justify-end p-3">
          <button
            type="button"
            onClick={() => setDockOpen(true)}
            className="inline-flex items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 text-sm font-medium text-[var(--text)] shadow-md transition hover:border-[var(--accent)]"
            aria-label="Open global chat"
          >
            <span>Chats</span>
            {unreadBadge}
          </button>
        </div>
      );
    }

    return (
      <section
        className={`oc-chat-shell flex h-full min-h-0 flex-col overflow-hidden border border-[var(--border)] bg-[var(--surface)] ${
          isFullscreen ? "fixed inset-0 top-[var(--topbar-height,56px)] z-50" : ""
        }`}
      >
        <header className="oc-chat-shell-header flex min-h-[52px] items-center justify-between gap-2 border-b border-[var(--border)] px-4 py-2.5">
          <div className="min-w-0 flex-1">
            <h2 className="sr-only">Global Chat</h2>
            <div className="flex min-w-0 items-center gap-2">
              <span
                aria-hidden="true"
                className="font-mono text-[15px] leading-none text-lime-400"
              >
                &gt;_
              </span>
              <span className="truncate whitespace-nowrap font-mono text-sm font-semibold text-[var(--text)]">Otter Shell</span>
              <span className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-[10px] font-mono uppercase tracking-wide text-[var(--text-muted)]">
                <span className="h-1.5 w-1.5 rounded-full bg-[var(--green)]" />
                ONLINE
              </span>
            </div>
          </div>
          <div className="ml-2 flex shrink-0 items-center gap-1.5">
            <button
              type="button"
              onClick={() => {
                setIsFullscreen(false);
                if (embeddedPanelPinnedOpen) {
                  onToggleRail?.();
                } else {
                  toggleDock();
                }
              }}
              className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-[11px] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
              aria-label="Collapse global chat"
            >
              Hide
            </button>
            <button
              type="button"
              onClick={() => {
                setIsFullscreen(false);
                if (embeddedPanelPinnedOpen) {
                  onToggleRail?.();
                } else {
                  setDockOpen(false);
                }
              }}
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

        <div className="min-h-0 flex-1">
          {selectedConversation ? (
            <div className="flex h-full min-h-0 flex-col">
              <div className="border-b border-[var(--border)]/90 bg-[var(--surface-alt)]/40 px-4 py-2.5">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 flex-1">
                    <h3 className="truncate text-base font-semibold text-[var(--text)]">
                      {resolveConversationTitle(selectedConversation)}
                    </h3>
                    <p className="truncate text-[11px] text-[var(--text-muted)]">{selectedConversation.contextLabel}</p>
                  </div>
                  <div className="mt-0.5 flex items-center gap-1.5">
                    {routeScopedConversation ? (
                      <button
                        type="button"
                        onClick={handleSwapScope}
                        disabled={routeScopeMode === "route" && !hasOrgChatTarget}
                        className="rounded-lg border border-[var(--border)] px-2 py-1 text-[10px] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        {routeScopedSwapLabel}
                      </button>
                    ) : null}
                    <button
                      type="button"
                      onClick={() => {
                        if (selectedJumpTarget) {
                          navigate(selectedJumpTarget.href);
                        }
                      }}
                      disabled={!selectedJumpTarget}
                      className="shrink-0 rounded-lg border border-[var(--border)] px-2 py-1 text-[10px] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      {selectedJumpTarget?.label || "Open"}
                    </button>
                  </div>
                </div>
                <div className="mt-2 flex justify-end">
                  <button
                    type="button"
                    onClick={() => {
                      void handleClearSession();
                    }}
                    disabled={resettingProjectSession}
                    className="rounded-lg border border-[var(--border)] px-2 py-1 text-[10px] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {resettingProjectSession ? "Clearing..." : "Clear session"}
                  </button>
                </div>
              </div>
              <div className="min-h-0 flex-1">
                <GlobalChatSurface
                  conversation={selectedConversation}
                  showContextHeader={false}
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
            </div>
          ) : (
            <div className="flex h-full flex-col justify-between p-4">
              <div className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)]/65 p-3">
                <p className="text-sm text-[var(--text)]">Welcome to Otter Camp. Systems are online. How can I assist you today?</p>
              </div>
              <p className="text-xs text-[var(--text-muted)]">Open a project, issue, or direct-message thread to continue.</p>
            </div>
          )}
        </div>
      </section>
    );
  }

  if (!dockOpen) {
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
            <span
              data-testid="global-chat-context-cue"
              className="oc-chip rounded-full border border-[var(--border)] px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-[var(--text-muted)]"
            >
              {selectedContextCue}
            </span>
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
                  Start a chat from Agents, Projects, or an Issue thread.
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
                        handleSelectConversation(conversation);
                      }}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          handleSelectConversation(conversation);
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
                        {routeScopedConversation ? (
                          <button
                            type="button"
                            onClick={handleSwapScope}
                            disabled={routeScopeMode === "route" && !hasOrgChatTarget}
                            className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {routeScopedSwapLabel}
                          </button>
                        ) : null}
                        <button
                          type="button"
                          onClick={() => {
                            setIsFullscreen(false);
                            setDockOpen(false);
                          }}
                          className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]"
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
                          className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {selectedJumpTarget?.label || "Open context"}
                        </button>
                        <button
                          type="button"
                          onClick={() => {
                            void handleClearSession();
                          }}
                          disabled={resettingProjectSession}
                          className="rounded-lg border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-60"
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
