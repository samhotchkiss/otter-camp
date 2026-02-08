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
}): IncomingEvent | null {
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
      (threadId.startsWith("dm_") ? threadId.slice(3) : `agent-${threadId}`);

    return {
      key: buildDMKey(threadId),
      incoming,
      conversation: {
        key: buildDMKey(threadId),
        type: "dm",
        threadId,
        agent: {
          id: agentId,
          name: senderName,
          status: "online",
        },
        title: senderName,
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
        title: `Project ${projectId.slice(0, 8)}`,
        contextLabel: "Project",
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

  const { lastMessage } = useWS();

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

  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    const incomingEvent = parseIncomingEvent(lastMessage);
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
  }, [isOpen, lastMessage, selectedKey]);

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
      openConversation,
      upsertConversation,
    }),
    [
      conversations,
      isOpen,
      markConversationRead,
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
