import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useWS } from "./WebSocketContext";
import type { Agent } from "../components/messaging/types";
import { API_URL } from "../lib/api";

const ORG_STORAGE_KEY = "otter-camp-org-id";
const STORAGE_KEY = "otter-camp-global-chat:v1";
const SYSTEM_SESSION_AUTHOR = "__otter_session__";

type ConversationType = "dm" | "project" | "issue";

type BaseConversation = {
  key: string;
  type: ConversationType;
  title: string;
  contextLabel: string;
  subtitle?: string;
  unreadCount: number;
  updatedAt: string;
};

export type GlobalDMConversation = BaseConversation & {
  type: "dm";
  threadId: string;
  agent: Agent;
};

export type GlobalProjectConversation = BaseConversation & {
  type: "project";
  projectId: string;
};

export type GlobalIssueConversation = BaseConversation & {
  type: "issue";
  issueId: string;
};

export type GlobalChatConversation =
  | GlobalDMConversation
  | GlobalProjectConversation
  | GlobalIssueConversation;

export type OpenConversationInput =
  | {
      type: "dm";
      agent: Agent;
      threadId?: string;
      title?: string;
      contextLabel?: string;
      subtitle?: string;
    }
  | {
      type: "project";
      projectId: string;
      title: string;
      contextLabel?: string;
      subtitle?: string;
    }
  | {
      type: "issue";
      issueId: string;
      title: string;
      contextLabel?: string;
      subtitle?: string;
    };

type OpenConversationOptions = {
  focus?: boolean;
  openDock?: boolean;
};

type GlobalChatContextValue = {
  isOpen: boolean;
  conversations: GlobalChatConversation[];
  selectedKey: string | null;
  selectedConversation: GlobalChatConversation | null;
  totalUnread: number;
  setDockOpen: (open: boolean) => void;
  toggleDock: () => void;
  selectConversation: (key: string) => void;
  markConversationRead: (key: string) => void;
  removeConversation: (key: string) => void;
  openConversation: (
    input: OpenConversationInput,
    options?: OpenConversationOptions,
  ) => void;
  upsertConversation: (input: OpenConversationInput) => void;
};

type StoredState = {
  isOpen: boolean;
  selectedKey: string | null;
  conversations: GlobalChatConversation[];
};

type IncomingEvent = {
  key: string;
  incoming: boolean;
  conversation: GlobalChatConversation;
};

type IncomingEventResolution = {
  agentNamesByID?: Map<string, string>;
  projectNamesByID?: Map<string, string>;
};

const GlobalChatContext = createContext<GlobalChatContextValue | undefined>(
  undefined,
);

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object";
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function extractProjectEventPayload(
  payload: Record<string, unknown>,
): Record<string, unknown> | null {
  const projectID = asString(payload.project_id) || asString(payload.projectId);
  if (projectID) {
    return payload;
  }

  if (isRecord(payload.message)) {
    return extractProjectEventPayload(payload.message);
  }
  if (isRecord(payload.data)) {
    return extractProjectEventPayload(payload.data);
  }
  return null;
}

function extractIssueEventPayload(
  payload: Record<string, unknown>,
): Record<string, unknown> | null {
  const issueID = asString(payload.issue_id) || asString(payload.issueId);
  if (issueID) {
    return payload;
  }

  if (isRecord(payload.comment)) {
    const nestedIssueID =
      asString(payload.comment.issue_id) || asString(payload.comment.issueId);
    if (nestedIssueID) {
      return {
        ...payload,
        issue_id: nestedIssueID,
      };
    }
  }
  if (isRecord(payload.data)) {
    return extractIssueEventPayload(payload.data);
  }
  return null;
}

function buildThreadId(agentId: string): string {
  return `dm_${agentId}`;
}

function buildDMKey(threadId: string): string {
  return `dm:${threadId}`;
}

function buildProjectKey(projectId: string): string {
  return `project:${projectId}`;
}

function buildIssueKey(issueId: string): string {
  return `issue:${issueId}`;
}

function getStoredOrgID(): string {
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return (window.localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function getStoredAuthToken(): string {
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return (window.localStorage.getItem("otter_camp_token") ?? "").trim();
  } catch {
    return "";
  }
}

function parseDMThreadAgentID(threadId: string): string {
  const trimmed = threadId.trim();
  if (!trimmed.startsWith("dm_")) {
    return "";
  }
  return trimmed.slice(3).trim();
}

function looksLikeProjectIdentifierTitle(title: string): boolean {
  return /^project\s+[a-f0-9-]{6,}$/i.test(title.trim());
}

function looksLikeAgentSlotName(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed) {
    return false;
  }
  if (trimmed.includes(" ")) {
    return false;
  }
  return /^[a-z0-9][a-z0-9._-]{1,127}$/.test(trimmed);
}

function normalizeAgentDirectory(payload: unknown): Map<string, string> {
  const records =
    payload && typeof payload === "object" && Array.isArray((payload as Record<string, unknown>).agents)
      ? ((payload as Record<string, unknown>).agents as unknown[])
      : Array.isArray(payload)
        ? payload
        : [];

  const result = new Map<string, string>();
  for (const raw of records) {
    if (!isRecord(raw)) {
      continue;
    }
    const id =
      asString(raw.id) ||
      asString(raw.agentId) ||
      asString(raw.slug);
    const name =
      asString(raw.name) ||
      asString(raw.displayName);
    if (!id || !name) {
      continue;
    }
    result.set(id, name);
  }
  return result;
}

function normalizeProjectDirectory(payload: unknown): Map<string, string> {
  const records =
    payload && typeof payload === "object" && Array.isArray((payload as Record<string, unknown>).projects)
      ? ((payload as Record<string, unknown>).projects as unknown[])
      : Array.isArray(payload)
        ? payload
        : [];

  const result = new Map<string, string>();
  for (const raw of records) {
    if (!isRecord(raw)) {
      continue;
    }
    const id = asString(raw.id);
    const name = asString(raw.name);
    if (!id || !name) {
      continue;
    }
    result.set(id, name);
  }
  return result;
}

function sortConversations(items: GlobalChatConversation[]): GlobalChatConversation[] {
  return [...items].sort((a, b) => {
    const aMs = Date.parse(a.updatedAt);
    const bMs = Date.parse(b.updatedAt);
    if (Number.isNaN(aMs) || Number.isNaN(bMs)) {
      return b.title.localeCompare(a.title);
    }
    if (aMs === bMs) {
      return a.title.localeCompare(b.title);
    }
    return bMs - aMs;
  });
}

function normalizeAgent(raw: unknown): Agent | null {
  if (!isRecord(raw)) {
    return null;
  }
  const id = asString(raw.id);
  const name = asString(raw.name);
  const statusRaw = asString(raw.status).toLowerCase();
  const status =
    statusRaw === "online" || statusRaw === "busy" || statusRaw === "offline"
      ? statusRaw
      : "offline";
  if (!id || !name) {
    return null;
  }
  const role = asString(raw.role) || undefined;
  const avatarUrl = asString(raw.avatarUrl) || undefined;
  return {
    id,
    name,
    status,
    role,
    avatarUrl,
  };
}

function normalizeConversation(raw: unknown): GlobalChatConversation | null {
  if (!isRecord(raw)) {
    return null;
  }

  const type = asString(raw.type);
  const key = asString(raw.key);
  const title = asString(raw.title);
  const contextLabel = asString(raw.contextLabel);
  if (!key || !title || !contextLabel) {
    return null;
  }

  const unreadRaw = raw.unreadCount;
  const unreadCount =
    typeof unreadRaw === "number" && Number.isFinite(unreadRaw)
      ? Math.max(0, Math.floor(unreadRaw))
      : 0;
  const updatedAt = asString(raw.updatedAt) || new Date().toISOString();
  const subtitle = asString(raw.subtitle) || undefined;

  if (type === "dm") {
    const threadId = asString(raw.threadId);
    const agent = normalizeAgent(raw.agent);
    if (!threadId || !agent) {
      return null;
    }
    return {
      key,
      type: "dm",
      threadId,
      agent,
      title,
      contextLabel,
      subtitle,
      unreadCount,
      updatedAt,
    };
  }

  if (type === "project") {
    const projectId = asString(raw.projectId);
    if (!projectId) {
      return null;
    }
    return {
      key,
      type: "project",
      projectId,
      title,
      contextLabel,
      subtitle,
      unreadCount,
      updatedAt,
    };
  }

  if (type === "issue") {
    const issueId = asString(raw.issueId);
    if (!issueId) {
      return null;
    }
    return {
      key,
      type: "issue",
      issueId,
      title,
      contextLabel,
      subtitle,
      unreadCount,
      updatedAt,
    };
  }

  return null;
}

function loadInitialState(): StoredState {
  if (typeof window === "undefined") {
    return {
      isOpen: false,
      selectedKey: null,
      conversations: [],
    };
  }

  const raw = window.localStorage.getItem(STORAGE_KEY);
  if (!raw) {
    return {
      isOpen: false,
      selectedKey: null,
      conversations: [],
    };
  }

  try {
    const parsed = JSON.parse(raw) as {
      isOpen?: unknown;
      selectedKey?: unknown;
      conversations?: unknown;
    };

    const conversations = Array.isArray(parsed.conversations)
      ? parsed.conversations
          .map((entry) => normalizeConversation(entry))
          .filter(
            (entry): entry is GlobalChatConversation =>
              entry !== null,
          )
      : [];

    return {
      isOpen: parsed.isOpen === true,
      selectedKey: asString(parsed.selectedKey) || null,
      conversations: sortConversations(conversations),
    };
  } catch {
    return {
      isOpen: false,
      selectedKey: null,
      conversations: [],
    };
  }
}

function toConversation(input: OpenConversationInput): GlobalChatConversation {
  const updatedAt = new Date().toISOString();

  if (input.type === "dm") {
    const threadId = asString(input.threadId) || buildThreadId(input.agent.id);
    return {
      key: buildDMKey(threadId),
      type: "dm",
      threadId,
      agent: input.agent,
      title: asString(input.title) || input.agent.name,
      contextLabel: asString(input.contextLabel) || "Direct message",
      subtitle: asString(input.subtitle) || input.agent.role,
      unreadCount: 0,
      updatedAt,
    };
  }

  if (input.type === "project") {
    return {
      key: buildProjectKey(input.projectId),
      type: "project",
      projectId: input.projectId,
      title: asString(input.title) || "Project chat",
      contextLabel: asString(input.contextLabel) || "Project",
      subtitle: asString(input.subtitle) || "Project chat",
      unreadCount: 0,
      updatedAt,
    };
  }

  return {
    key: buildIssueKey(input.issueId),
    type: "issue",
    issueId: input.issueId,
    title: asString(input.title) || "Issue thread",
    contextLabel: asString(input.contextLabel) || "Issue",
    subtitle: asString(input.subtitle) || "Issue conversation",
    unreadCount: 0,
    updatedAt,
  };
}

function parseIncomingEvent(lastMessage: {
  type: string;
  data: unknown;
}, resolution?: IncomingEventResolution): IncomingEvent | null {
  if (!isRecord(lastMessage.data)) {
    return null;
  }

  const payload = lastMessage.data;

  if (lastMessage.type === "DMMessageReceived") {
    const threadId = asString(payload.threadId) || asString(payload.thread_id);
    if (!threadId) {
      return null;
    }

    const messagePayload = isRecord(payload.message) ? payload.message : payload;
    const senderType =
      asString(messagePayload.senderType) || asString(messagePayload.sender_type);
    const senderId = asString(messagePayload.senderId) || asString(messagePayload.sender_id);
    const senderName =
      asString(messagePayload.senderName) || asString(messagePayload.sender_name) || "Agent";

    const normalizedSenderType = senderType.toLowerCase();
    const incoming = normalizedSenderType === "agent" || normalizedSenderType === "assistant";
    const agentId =
      senderId ||
      parseDMThreadAgentID(threadId) ||
      `agent-${threadId}`;
    const resolvedAgentName =
      resolution?.agentNamesByID?.get(agentId) ||
      senderName;

    return {
      key: buildDMKey(threadId),
      incoming,
      conversation: {
        key: buildDMKey(threadId),
        type: "dm",
        threadId,
        agent: {
          id: agentId,
          name: resolvedAgentName,
          status: "online",
        },
        title: resolvedAgentName,
        contextLabel: "Direct message",
        subtitle: "Agent chat",
        unreadCount: 0,
        updatedAt: new Date().toISOString(),
      },
    };
  }

  if (lastMessage.type === "ProjectChatMessageCreated") {
    const projectPayload = extractProjectEventPayload(payload);
    if (!projectPayload) {
      return null;
    }
    const projectId =
      asString(projectPayload.project_id) || asString(projectPayload.projectId);
    const author = asString(projectPayload.author);
    if (!projectId) {
      return null;
    }
    const projectName = resolution?.projectNamesByID?.get(projectId) ?? "";

    const currentAuthor =
      asString(window.localStorage.getItem("otter-camp-user-name")) || "you";
    const incoming =
      author !== "" &&
      author.toLowerCase() !== currentAuthor.toLowerCase() &&
      author !== SYSTEM_SESSION_AUTHOR;

    return {
      key: buildProjectKey(projectId),
      incoming,
      conversation: {
        key: buildProjectKey(projectId),
        type: "project",
        projectId,
        title: projectName || "Project chat",
        contextLabel: projectName ? `Project • ${projectName}` : "Project",
        subtitle: "Project chat",
        unreadCount: 0,
        updatedAt: new Date().toISOString(),
      },
    };
  }

  if (lastMessage.type === "IssueCommentCreated") {
    const issuePayload = extractIssueEventPayload(payload);
    if (!issuePayload) {
      return null;
    }
    const issueId = asString(issuePayload.issue_id) || asString(issuePayload.issueId);
    if (!issueId) {
      return null;
    }

    return {
      key: buildIssueKey(issueId),
      incoming: true,
      conversation: {
        key: buildIssueKey(issueId),
        type: "issue",
        issueId,
        title: `Issue ${issueId.slice(0, 8)}`,
        contextLabel: "Issue",
        subtitle: "Issue thread",
        unreadCount: 0,
        updatedAt: new Date().toISOString(),
      },
    };
  }

  return null;
}

export function GlobalChatProvider({ children }: { children: ReactNode }) {
  const initialState = useMemo(() => loadInitialState(), []);
  const [isOpen, setIsOpen] = useState(initialState.isOpen);
  const [selectedKey, setSelectedKey] = useState<string | null>(
    initialState.selectedKey,
  );
  const [conversations, setConversations] = useState<GlobalChatConversation[]>(
    initialState.conversations,
  );
  const [agentNamesByID, setAgentNamesByID] = useState<Map<string, string>>(
    () => new Map(),
  );
  const [projectNamesByID, setProjectNamesByID] = useState<Map<string, string>>(
    () => new Map(),
  );

  const { lastMessage } = useWS();

  useEffect(() => {
    let cancelled = false;

    const loadConversationMetadata = async () => {
      const orgID = getStoredOrgID();
      if (!orgID) {
        return;
      }

      const token = getStoredAuthToken();
      const headers: Record<string, string> = {};
      if (token) {
        headers.Authorization = `Bearer ${token}`;
      }

      try {
        const projectsURL = new URL(`${API_URL}/api/projects`);
        projectsURL.searchParams.set("org_id", orgID);

        const projectsResponse = await fetch(projectsURL.toString(), {
          headers,
          cache: "no-store",
        });
        const projectsPayload = projectsResponse.ok
          ? await projectsResponse.json().catch(() => null)
          : null;
        const nextProjectNames = normalizeProjectDirectory(projectsPayload);
        if (!cancelled) {
          setProjectNamesByID(nextProjectNames);
        }
      } catch {
        // Ignore metadata fetch failures; chat still functions with existing labels.
      }

      try {
        const syncAgentsResponse = await fetch(`${API_URL}/api/sync/agents`, {
          headers,
          cache: "no-store",
        });
        const syncPayload = syncAgentsResponse.ok
          ? await syncAgentsResponse.json().catch(() => null)
          : null;
        let nextAgentNames = normalizeAgentDirectory(syncPayload);

        if (nextAgentNames.size === 0) {
          const agentsURL = new URL(`${API_URL}/api/agents`);
          agentsURL.searchParams.set("org_id", orgID);
          const agentsResponse = await fetch(agentsURL.toString(), {
            headers,
            cache: "no-store",
          });
          const agentsPayload = agentsResponse.ok
            ? await agentsResponse.json().catch(() => null)
            : null;
          nextAgentNames = normalizeAgentDirectory(agentsPayload);
        }

        if (!cancelled) {
          setAgentNamesByID(nextAgentNames);
        }
      } catch {
        // Ignore metadata fetch failures; chat still functions with existing labels.
      }
    };

    void loadConversationMetadata();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (agentNamesByID.size === 0 && projectNamesByID.size === 0) {
      return;
    }

    setConversations((prev) => {
      let changed = false;
      const next = prev.map((conversation) => {
        if (conversation.type === "project") {
          const projectName = projectNamesByID.get(conversation.projectId);
          if (projectName) {
            const nextContextLabel = `Project • ${projectName}`;
            if (
              conversation.title !== projectName ||
              conversation.contextLabel !== nextContextLabel
            ) {
              changed = true;
              return {
                ...conversation,
                title: projectName,
                contextLabel: nextContextLabel,
                subtitle: conversation.subtitle || "Project chat",
              };
            }
            return conversation;
          }
          if (looksLikeProjectIdentifierTitle(conversation.title)) {
            changed = true;
            return {
              ...conversation,
              title: "Project chat",
              contextLabel: "Project",
            };
          }
          return conversation;
        }

        if (conversation.type === "dm") {
          const threadAgentID = parseDMThreadAgentID(conversation.threadId);
          const agentID = conversation.agent.id || threadAgentID;
          const resolvedName = agentNamesByID.get(agentID);
          if (!resolvedName) {
            return conversation;
          }
          const shouldUpdateTitle =
            conversation.title !== resolvedName || looksLikeAgentSlotName(conversation.title);
          const shouldUpdateAgentName =
            conversation.agent.name !== resolvedName || looksLikeAgentSlotName(conversation.agent.name);
          if (!shouldUpdateTitle && !shouldUpdateAgentName) {
            return conversation;
          }
          changed = true;
          return {
            ...conversation,
            title: resolvedName,
            agent: {
              ...conversation.agent,
              id: agentID || conversation.agent.id,
              name: resolvedName,
            },
          };
        }

        return conversation;
      });

      return changed ? sortConversations(next) : prev;
    });
  }, [agentNamesByID, projectNamesByID]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        isOpen,
        selectedKey,
        conversations,
      }),
    );
  }, [conversations, isOpen, selectedKey]);

  useEffect(() => {
    if (conversations.length === 0) {
      if (selectedKey !== null) {
        setSelectedKey(null);
      }
      return;
    }

    if (!selectedKey) {
      setSelectedKey(conversations[0].key);
      return;
    }

    const exists = conversations.some((conversation) => conversation.key === selectedKey);
    if (!exists) {
      setSelectedKey(conversations[0].key);
    }
  }, [conversations, selectedKey]);

  const mergeConversation = useCallback(
    (
      next: GlobalChatConversation,
      options: { markRead: boolean },
    ) => {
      setConversations((prev) => {
        const index = prev.findIndex((entry) => entry.key === next.key);
        if (index < 0) {
          return sortConversations([
            {
              ...next,
              unreadCount: options.markRead ? 0 : next.unreadCount,
            },
            ...prev,
          ]);
        }

        const existing = prev[index];
        const merged: GlobalChatConversation = {
          ...existing,
          ...next,
          unreadCount: options.markRead ? 0 : existing.unreadCount,
          updatedAt: next.updatedAt,
        };
        return sortConversations([
          merged,
          ...prev.filter((entry) => entry.key !== next.key),
        ]);
      });
    },
    [],
  );

  const openConversation = useCallback(
    (input: OpenConversationInput, options?: OpenConversationOptions) => {
      const resolved = toConversation(input);
      const focus = options?.focus !== false;
      const shouldOpen = options?.openDock !== false;

      mergeConversation(resolved, { markRead: focus });

      if (focus) {
        setSelectedKey(resolved.key);
      }
      if (shouldOpen) {
        setIsOpen(true);
      }
    },
    [mergeConversation],
  );

  const upsertConversation = useCallback(
    (input: OpenConversationInput) => {
      openConversation(input, { focus: false, openDock: false });
    },
    [openConversation],
  );

  const markConversationRead = useCallback((key: string) => {
    setConversations((prev) =>
      prev.map((entry) =>
        entry.key === key
          ? {
              ...entry,
              unreadCount: 0,
            }
          : entry,
      ),
    );
  }, []);

  const removeConversation = useCallback((key: string) => {
    setConversations((prev) => prev.filter((entry) => entry.key !== key));
    setSelectedKey((current) => (current === key ? null : current));
  }, []);

  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    const incomingEvent = parseIncomingEvent(lastMessage, {
      agentNamesByID,
      projectNamesByID,
    });
    if (!incomingEvent) {
      return;
    }

    const { key, incoming, conversation } = incomingEvent;

    setConversations((prev) => {
      const index = prev.findIndex((entry) => entry.key === key);
      const shouldIncrementUnread = incoming && (!isOpen || selectedKey !== key);

      if (index < 0) {
        return sortConversations([
          {
            ...conversation,
            unreadCount: shouldIncrementUnread ? 1 : 0,
            updatedAt: new Date().toISOString(),
          },
          ...prev,
        ]);
      }

      const existing = prev[index];
      const mergedTitle = existing.title || conversation.title;
      const mergedContextLabel = existing.contextLabel || conversation.contextLabel;
      const mergedSubtitle = existing.subtitle || conversation.subtitle;
      const merged: GlobalChatConversation = {
        ...existing,
        ...conversation,
        title: mergedTitle,
        contextLabel: mergedContextLabel,
        subtitle: mergedSubtitle,
        unreadCount: shouldIncrementUnread
          ? existing.unreadCount + 1
          : existing.unreadCount,
        updatedAt: new Date().toISOString(),
      };

      return sortConversations([
        merged,
        ...prev.filter((entry) => entry.key !== key),
      ]);
    });
  }, [agentNamesByID, isOpen, lastMessage, projectNamesByID, selectedKey]);

  const selectedConversation = useMemo(
    () =>
      selectedKey
        ? conversations.find((conversation) => conversation.key === selectedKey) ?? null
        : null,
    [conversations, selectedKey],
  );

  const totalUnread = useMemo(
    () =>
      conversations.reduce((sum, conversation) => sum + conversation.unreadCount, 0),
    [conversations],
  );

  const value = useMemo<GlobalChatContextValue>(
    () => ({
      isOpen,
      conversations,
      selectedKey,
      selectedConversation,
      totalUnread,
      setDockOpen: setIsOpen,
      toggleDock: () => setIsOpen((open) => !open),
      selectConversation: setSelectedKey,
      markConversationRead,
      removeConversation,
      openConversation,
      upsertConversation,
    }),
    [
      conversations,
      isOpen,
      markConversationRead,
      removeConversation,
      openConversation,
      selectedConversation,
      selectedKey,
      totalUnread,
      upsertConversation,
    ],
  );

  return (
    <GlobalChatContext.Provider value={value}>
      {children}
    </GlobalChatContext.Provider>
  );
}

export function useGlobalChat(): GlobalChatContextValue {
  const context = useContext(GlobalChatContext);
  if (!context) {
    throw new Error("useGlobalChat must be used within a GlobalChatProvider");
  }
  return context;
}
