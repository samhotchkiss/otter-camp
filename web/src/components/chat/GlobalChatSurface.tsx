import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { useWS } from "../../contexts/WebSocketContext";
import type {
  DMMessage,
  MessageAttachment,
  MessageQuestionnaire,
  MessageQuestionnaireQuestion,
} from "../messaging/types";
import MessageHistory from "../messaging/MessageHistory";
import type {
  GlobalChatConversation,
} from "../../contexts/GlobalChatContext";
import { API_URL } from "../../lib/api";
import { getChatContextCue } from "./chatContextCue";

const ORG_STORAGE_KEY = "otter-camp-org-id";
const USER_NAME_STORAGE_KEY = "otter-camp-user-name";
const PROJECT_CHAT_SESSION_RESET_AUTHOR = "__otter_session__";
const PROJECT_CHAT_SESSION_RESET_PREFIX = "project_chat_session_reset:";
const CHAT_SESSION_RESET_PREFIX = "chat_session_reset:";
const POST_SEND_REFRESH_DELAYS_MS = [1200, 3500, 7000, 12000, 20000, 30000, 45000, 60000, 90000, 120000];
const STALLED_TURN_TIMEOUT_MS = 120_000;

type DeliveryTone = "neutral" | "success" | "warning";

type DeliveryIndicator = {
  tone: DeliveryTone;
  text: string;
};

type IssueDetailResponse = {
  comments?: Array<{
    id: string;
    author_agent_id: string;
    body: string;
    created_at: string;
    updated_at: string;
  }>;
  participants?: Array<{
    agent_id: string;
    role: "owner" | "collaborator";
    removed_at?: string | null;
  }>;
  questionnaires?: unknown[];
};

type AgentsResponse = {
  agents?: Array<{ id: string; name: string }>;
};

type QuestionnairePayload = {
  id: string;
  context_type: "issue" | "project_chat" | "template";
  context_id: string;
  author: string;
  title?: string;
  questions: MessageQuestionnaireQuestion[];
  responses?: Record<string, unknown>;
  responded_by?: string;
  responded_at?: string;
  created_at: string;
};

type ChatAttachment = MessageAttachment;

type ChatMessage = DMMessage & {
  attachments?: ChatAttachment[];
  optimistic?: boolean;
  failed?: boolean;
};

type ChatEmission = {
  id: string;
  sourceID: string;
  summary: string;
  timestamp: string;
  scopeProjectID?: string;
  scopeIssueID?: string;
};

type GlobalChatSurfaceProps = {
  conversation: GlobalChatConversation;
  onConversationTouched?: () => void;
  refreshVersion?: number;
  agentNamesByID?: Map<string, string>;
  resolveAgentName?: (raw: string) => string;
  showContextHeader?: boolean;
};

function getStoredOrgID(): string {
  try {
    return (window.localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function getCurrentUserName(): string {
  try {
    const fromStorage = (window.localStorage.getItem(USER_NAME_STORAGE_KEY) ?? "").trim();
    return fromStorage || "You";
  } catch {
    return "You";
  }
}

function getAuthToken(): string {
  try {
    return (window.localStorage.getItem("otter_camp_token") ?? "").trim();
  } catch {
    return "";
  }
}

function normalizeTimestamp(value: unknown): string {
  if (typeof value !== "string" || value.trim() === "") {
    return new Date().toISOString();
  }
  return value;
}

function normalizeMessageAttachments(raw: unknown): ChatAttachment[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: ChatAttachment[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") {
      continue;
    }
    const record = entry as Record<string, unknown>;
    const id = typeof record.id === "string" ? record.id.trim() : "";
    const filename = typeof record.filename === "string" ? record.filename.trim() : "";
    const mimeType = typeof record.mime_type === "string" ? record.mime_type.trim() : "";
    const url = typeof record.url === "string" ? record.url.trim() : "";
    const sizeBytes = typeof record.size_bytes === "number" ? record.size_bytes : Number(record.size_bytes ?? 0);
    if (!id || !filename || !mimeType || !url || !Number.isFinite(sizeBytes)) {
      continue;
    }
    const thumbnailURL = typeof record.thumbnail_url === "string" ? record.thumbnail_url : undefined;
    out.push({
      id,
      filename,
      size_bytes: sizeBytes,
      mime_type: mimeType,
      url,
      thumbnail_url: thumbnailURL,
    });
  }
  return out;
}

function extractSessionResetID(content: string): string | null {
  const trimmed = content.trim();
  if (trimmed.startsWith(PROJECT_CHAT_SESSION_RESET_PREFIX)) {
    return trimmed.slice(PROJECT_CHAT_SESSION_RESET_PREFIX.length).trim() || "session";
  }
  if (trimmed.startsWith(CHAT_SESSION_RESET_PREFIX)) {
    return trimmed.slice(CHAT_SESSION_RESET_PREFIX.length).trim() || "session";
  }
  return null;
}

function normalizeThreadMessage(
  raw: unknown,
  fallbackThreadID: string,
  currentUserName: string,
  currentUserID: string,
): ChatMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;

  const id = typeof record.id === "string" && record.id.trim() !== ""
    ? record.id
    : `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const senderTypeRaw =
    (typeof record.senderType === "string" && record.senderType.trim().toLowerCase()) ||
    (typeof record.sender_type === "string" && record.sender_type.trim().toLowerCase()) ||
    "user";
  const senderType = senderTypeRaw === "agent" ? "agent" : "user";
  const senderName =
    (typeof record.senderName === "string" && record.senderName.trim()) ||
    (typeof record.sender_name === "string" && record.sender_name.trim()) ||
    (senderType === "agent" ? "Agent" : currentUserName);
  const rawSenderID =
    (typeof record.senderId === "string" && record.senderId.trim()) ||
    (typeof record.sender_id === "string" && record.sender_id.trim()) ||
    "";
  const senderID = rawSenderID || (senderType === "agent" ? `agent:${senderName}` : currentUserID);
  const content =
    (typeof record.content === "string" && record.content) || "";
  const attachments = normalizeMessageAttachments(record.attachments);
  const resetSessionID = extractSessionResetID(content);
  if (resetSessionID !== null) {
    return {
      id,
      threadId:
        (typeof record.threadId === "string" && record.threadId) ||
        (typeof record.thread_id === "string" && record.thread_id) ||
        fallbackThreadID,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.createdAt ?? record.created_at),
      isSessionReset: true,
      sessionID: resetSessionID,
    };
  }

  return {
    id,
    threadId:
      (typeof record.threadId === "string" && record.threadId) ||
      (typeof record.thread_id === "string" && record.thread_id) ||
      fallbackThreadID,
    senderId: senderID,
    senderName,
    senderType,
    senderAvatarUrl:
      (typeof record.senderAvatarUrl === "string" && record.senderAvatarUrl) ||
      (typeof record.sender_avatar_url === "string" && record.sender_avatar_url) ||
      undefined,
    content,
    attachments,
    createdAt: normalizeTimestamp(record.createdAt ?? record.created_at),
  };
}

function normalizeProjectMessage(
  raw: unknown,
  expectedProjectID: string,
  threadID: string,
  currentUserName: string,
  currentUserID: string,
): ChatMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id : "";
  const projectID = typeof record.project_id === "string" ? record.project_id : "";
  const author = typeof record.author === "string" ? record.author.trim() : "";
  const body = typeof record.body === "string" ? record.body : "";
  const attachments = normalizeMessageAttachments(record.attachments);
  if (!id || !projectID || !author || (body === "" && attachments.length === 0)) {
    return null;
  }
  if (projectID !== expectedProjectID) {
    return null;
  }
  const resetSessionID = extractSessionResetID(body);
  if ((author === PROJECT_CHAT_SESSION_RESET_AUTHOR || author === "Session") && resetSessionID !== null) {
    return {
      id,
      threadId: threadID,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.created_at),
      isSessionReset: true,
      sessionID: resetSessionID,
    };
  }

  const isUser = author.toLowerCase() === currentUserName.toLowerCase();
  return {
    id,
    threadId: threadID,
    senderId: isUser ? currentUserID : `agent:${author}`,
    senderName: isUser ? currentUserName : author,
    senderType: isUser ? "user" : "agent",
    content: body,
    attachments,
    createdAt: normalizeTimestamp(record.created_at),
  };
}

function normalizeIssueComment(
  raw: unknown,
  threadID: string,
  agentNameByID: Map<string, string>,
  authorAgentID: string,
): ChatMessage | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id : "";
  const authorID = typeof record.author_agent_id === "string" ? record.author_agent_id : "";
  const body = typeof record.body === "string" ? record.body : "";
  const attachments = normalizeMessageAttachments(record.attachments);
  if (!id || !authorID || (body === "" && attachments.length === 0)) {
    return null;
  }
  const resetSessionID = extractSessionResetID(body);
  if (resetSessionID !== null) {
    return {
      id,
      threadId: threadID,
      senderId: "session-reset",
      senderName: "Session",
      senderType: "agent",
      content: "",
      createdAt: normalizeTimestamp(record.created_at),
      isSessionReset: true,
      sessionID: resetSessionID,
    };
  }

  const senderName = agentNameByID.get(authorID) ?? authorID;
  const isUser = authorAgentID !== "" && authorID === authorAgentID;

  return {
    id,
    threadId: threadID,
    senderId: authorID,
    senderName,
    senderType: isUser ? "user" : "agent",
    content: body,
    attachments,
    createdAt: normalizeTimestamp(record.created_at),
  };
}

function normalizeQuestionType(raw: unknown): MessageQuestionnaireQuestion["type"] | null {
  if (typeof raw !== "string") {
    return null;
  }
  const normalized = raw.trim().toLowerCase();
  switch (normalized) {
    case "text":
    case "textarea":
    case "boolean":
    case "select":
    case "multiselect":
    case "number":
    case "date":
      return normalized;
    default:
      return null;
  }
}

function normalizeQuestionnaireQuestions(raw: unknown): MessageQuestionnaireQuestion[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const questions: MessageQuestionnaireQuestion[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") {
      continue;
    }
    const record = entry as Record<string, unknown>;
    const id = typeof record.id === "string" ? record.id.trim() : "";
    const text = typeof record.text === "string" ? record.text.trim() : "";
    const type = normalizeQuestionType(record.type);
    if (!id || !text || !type) {
      continue;
    }
    const options = Array.isArray(record.options)
      ? record.options
          .filter((value): value is string => typeof value === "string")
          .map((value) => value.trim())
          .filter((value) => value.length > 0)
      : undefined;
    const placeholder = typeof record.placeholder === "string" ? record.placeholder : undefined;
    questions.push({
      id,
      text,
      type,
      required: Boolean(record.required),
      options: options && options.length > 0 ? [...new Set(options)] : undefined,
      placeholder,
      default: record.default,
    });
  }
  return questions;
}

function normalizeQuestionnairePayload(
  raw: unknown,
  expectedContextType: QuestionnairePayload["context_type"],
  expectedContextID: string,
): QuestionnairePayload | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id.trim() : "";
  const contextType =
    record.context_type === "issue" ||
    record.context_type === "project_chat" ||
    record.context_type === "template"
      ? record.context_type
      : null;
  const contextID = typeof record.context_id === "string" ? record.context_id.trim() : "";
  const author = typeof record.author === "string" ? record.author.trim() : "";
  const createdAt = typeof record.created_at === "string" ? record.created_at : "";
  const questions = normalizeQuestionnaireQuestions(record.questions);
  if (!id || !contextType || !contextID || !author || !createdAt || questions.length === 0) {
    return null;
  }
  if (contextType !== expectedContextType || contextID !== expectedContextID) {
    return null;
  }
  const title = typeof record.title === "string" && record.title.trim() !== ""
    ? record.title
    : undefined;
  const responses = record.responses && typeof record.responses === "object" && !Array.isArray(record.responses)
    ? record.responses as Record<string, unknown>
    : undefined;
  const respondedBy = typeof record.responded_by === "string" && record.responded_by.trim() !== ""
    ? record.responded_by
    : undefined;
  const respondedAt = typeof record.responded_at === "string" && record.responded_at.trim() !== ""
    ? record.responded_at
    : undefined;
  return {
    id,
    context_type: contextType,
    context_id: contextID,
    author,
    title,
    questions,
    responses,
    responded_by: respondedBy,
    responded_at: respondedAt,
    created_at: createdAt,
  };
}

function normalizeQuestionnaireMessage(
  questionnaire: QuestionnairePayload,
  threadID: string,
  currentUserName: string,
  currentUserID: string,
): ChatMessage {
  const isUser = questionnaire.author.toLowerCase() === currentUserName.toLowerCase();
  return {
    id: `questionnaire:${questionnaire.id}`,
    threadId: threadID,
    senderId: isUser ? currentUserID : `questionnaire:${questionnaire.id}`,
    senderName: questionnaire.author,
    senderType: isUser ? "user" : "agent",
    content: questionnaire.title?.trim() ? `Questionnaire: ${questionnaire.title}` : "Questionnaire",
    questionnaire: questionnaire as MessageQuestionnaire,
    createdAt: normalizeTimestamp(questionnaire.created_at),
  };
}

function deliveryIndicatorClass(indicator: DeliveryIndicator): string {
  if (indicator.tone === "success") {
    return "border-[var(--green)]/40 bg-[var(--green)]/15 text-[var(--green)]";
  }
  if (indicator.tone === "warning") {
    return "border-[var(--orange)]/40 bg-[var(--orange)]/15 text-[var(--orange)]";
  }
  return "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]";
}

function normalizeDeliveryErrorText(raw: unknown): string {
  if (typeof raw !== "string" || raw.trim() === "") {
    return "";
  }
  const message = raw.trim();
  const lower = message.toLowerCase();
  if (lower.includes("bridge offline")) {
    return `${message} (bridge process needs to be running)`;
  }
  if (lower.includes("delivery unavailable")) {
    return `${message} (message saved; retry after bridge reconnects)`;
  }
  return message;
}

function getTrimmedString(raw: unknown): string {
  if (typeof raw !== "string") {
    return "";
  }
  return raw.trim();
}

function normalizeChatEmission(raw: unknown): ChatEmission | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const nested = record.emission && typeof record.emission === "object"
    ? (record.emission as Record<string, unknown>)
    : null;
  const target = nested ?? record;

  const id = getTrimmedString(target.id);
  const sourceID = getTrimmedString(target.source_id);
  const summary = getTrimmedString(target.summary);
  const timestampRaw = getTrimmedString(target.timestamp);
  const timestamp = timestampRaw ? new Date(timestampRaw).toISOString() : "";

  if (!id || !sourceID || !summary || !timestamp || Number.isNaN(Date.parse(timestamp))) {
    return null;
  }

  const scopeRecord = target.scope && typeof target.scope === "object"
    ? (target.scope as Record<string, unknown>)
    : null;
  const scopeProjectID = getTrimmedString(scopeRecord?.project_id);
  const scopeIssueID = getTrimmedString(scopeRecord?.issue_id);

  return {
    id,
    sourceID,
    summary,
    timestamp,
    ...(scopeProjectID ? { scopeProjectID } : {}),
    ...(scopeIssueID ? { scopeIssueID } : {}),
  };
}

function chatEmissionMatchesConversation(
  emission: ChatEmission,
  conversationType: GlobalChatConversation["type"],
  dmThreadID: string,
  projectID: string,
  issueID: string,
): boolean {
  if (conversationType === "dm") {
    return emission.sourceID === `dm:${dmThreadID}`;
  }
  if (conversationType === "project") {
    return emission.sourceID === `project:${projectID}` || emission.scopeProjectID === projectID;
  }
  return emission.sourceID === `issue:${issueID}` || emission.scopeIssueID === issueID;
}

function upsertEmissionTimelineMessage(
  prev: ChatMessage[],
  emission: ChatEmission,
  emissionMessageID: string,
  threadID: string,
  senderName: string,
): ChatMessage[] {
  const nextMessage: ChatMessage = {
    id: emissionMessageID,
    threadId: threadID,
    senderId: `emission:${emission.sourceID}`,
    senderName,
    senderType: "emission",
    content: emission.summary,
    createdAt: emission.timestamp,
    emissionWarning: false,
  };
  const byID = prev.findIndex((entry) => entry.id === emissionMessageID && entry.senderType === "emission");
  if (byID >= 0) {
    const next = [...prev];
    next[byID] = nextMessage;
    return next;
  }
  return [...prev, nextMessage];
}

function upsertStalledEmissionTimelineMessage(
  prev: ChatMessage[],
  emissionMessageID: string,
  threadID: string,
  senderName: string,
): ChatMessage[] {
  const warningMessage: ChatMessage = {
    id: emissionMessageID,
    threadId: threadID,
    senderId: `emission:stalled:${threadID}`,
    senderName,
    senderType: "emission",
    content: "Agent may be unresponsive",
    createdAt: new Date().toISOString(),
    emissionWarning: true,
  };
  const byID = prev.findIndex((entry) => entry.id === emissionMessageID && entry.senderType === "emission");
  if (byID >= 0) {
    const next = [...prev];
    next[byID] = warningMessage;
    return next;
  }
  return [...prev, warningMessage];
}

function upsertReplyReplacingEmission(
  prev: ChatMessage[],
  incoming: ChatMessage,
  emissionMessageID: string,
  sortResult = false,
): ChatMessage[] {
  const emissionIndex = prev.findIndex((entry) => entry.id === emissionMessageID && entry.senderType === "emission");
  if (emissionIndex < 0) {
    return upsertIncomingMessage(prev, incoming, sortResult);
  }
  const next = prev.filter((entry, index) => index === emissionIndex || entry.id !== incoming.id);
  next[emissionIndex] = {
    ...incoming,
    optimistic: false,
    failed: false,
  };
  return next;
}

function formatAttachmentSize(sizeBytes: number): string {
  if (!Number.isFinite(sizeBytes) || sizeBytes <= 0) {
    return "0 B";
  }
  if (sizeBytes < 1024) {
    return `${sizeBytes} B`;
  }
  if (sizeBytes < 1024 * 1024) {
    return `${(sizeBytes / 1024).toFixed(1)} KB`;
  }
  return `${(sizeBytes / (1024 * 1024)).toFixed(1)} MB`;
}

function buildAttachmentLinksMarkdown(attachments: ChatAttachment[]): string {
  if (attachments.length === 0) {
    return "";
  }
  return attachments
    .map((attachment) => `📎 [${attachment.filename}](${attachment.url})`)
    .join("\n");
}

function normalizeMessageTextForMatch(value: string): string {
  return value.trim().replace(/\s+/g, " ");
}

function attachmentSignature(message: ChatMessage): string {
  const attachments = Array.isArray(message.attachments) ? message.attachments : [];
  if (attachments.length === 0) {
    return "";
  }
  return attachments
    .map((attachment) => attachment.id || attachment.url || attachment.filename || "")
    .filter((token) => token.trim() !== "")
    .sort()
    .join("|");
}

function likelyOptimisticEchoMatch(existing: ChatMessage, incoming: ChatMessage): boolean {
  if (!existing.optimistic || existing.failed) {
    return false;
  }
  if (existing.threadId !== incoming.threadId || existing.senderType !== incoming.senderType) {
    return false;
  }
  if (existing.senderType !== "user") {
    return false;
  }
  if (normalizeMessageTextForMatch(existing.content) !== normalizeMessageTextForMatch(incoming.content)) {
    return false;
  }
  if (attachmentSignature(existing) !== attachmentSignature(incoming)) {
    return false;
  }
  const existingMs = Date.parse(existing.createdAt);
  const incomingMs = Date.parse(incoming.createdAt);
  if (Number.isNaN(existingMs) || Number.isNaN(incomingMs)) {
    return true;
  }
  return Math.abs(existingMs - incomingMs) <= 2 * 60 * 1000;
}

function likelyDuplicateAgentReply(existing: ChatMessage, incoming: ChatMessage): boolean {
  if (existing.senderType !== "agent" || incoming.senderType !== "agent") {
    return false;
  }
  if (existing.threadId !== incoming.threadId) {
    return false;
  }
  if (existing.isSessionReset || incoming.isSessionReset) {
    return false;
  }
  if (normalizeMessageTextForMatch(existing.content) !== normalizeMessageTextForMatch(incoming.content)) {
    return false;
  }
  if (attachmentSignature(existing) !== attachmentSignature(incoming)) {
    return false;
  }
  const existingMs = Date.parse(existing.createdAt);
  const incomingMs = Date.parse(incoming.createdAt);
  if (Number.isNaN(existingMs) || Number.isNaN(incomingMs)) {
    return true;
  }
  return Math.abs(existingMs - incomingMs) <= 30 * 1000;
}

function upsertIncomingMessage(
  prev: ChatMessage[],
  incoming: ChatMessage,
  sortResult = false,
): ChatMessage[] {
  const byID = prev.findIndex((entry) => entry.id === incoming.id);
  if (byID >= 0) {
    const next = [...prev];
    next[byID] = {
      ...next[byID],
      ...incoming,
      optimistic: false,
      failed: false,
    };
    if (sortResult) {
      return next.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
    }
    return next;
  }

  const optimisticMatchIndex = prev.findIndex((entry) => likelyOptimisticEchoMatch(entry, incoming));
  if (optimisticMatchIndex >= 0) {
    const next = [...prev];
    next[optimisticMatchIndex] = {
      ...incoming,
      optimistic: false,
      failed: false,
    };
    if (sortResult) {
      return next.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
    }
    return next;
  }

  const duplicateAgentIndex = prev.findIndex((entry) => likelyDuplicateAgentReply(entry, incoming));
  if (duplicateAgentIndex >= 0) {
    const next = [...prev];
    const existing = next[duplicateAgentIndex];
    next[duplicateAgentIndex] = {
      ...existing,
      ...incoming,
      id: existing.id,
      optimistic: false,
      failed: false,
    };
    if (sortResult) {
      return next.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
    }
    return next;
  }

  const next = [...prev, incoming];
  if (sortResult) {
    return next.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
  }
  return next;
}

function reconcileOptimisticPersistedMessage(
  prev: ChatMessage[],
  optimisticID: string,
  persisted: ChatMessage | null,
): ChatMessage[] {
  const replaced = prev.map((entry) =>
    entry.id === optimisticID
      ? {
          ...(persisted ?? entry),
          optimistic: false,
          failed: false,
        }
      : entry,
  );
  const deduped: ChatMessage[] = [];
  const seen = new Set<string>();
  for (const entry of replaced) {
    if (seen.has(entry.id)) {
      continue;
    }
    seen.add(entry.id);
    deduped.push(entry);
  }
  return deduped;
}

export default function GlobalChatSurface({
  conversation,
  onConversationTouched,
  refreshVersion = 0,
  agentNamesByID,
  resolveAgentName,
  showContextHeader = true,
}: GlobalChatSurfaceProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [deliveryIndicator, setDeliveryIndicator] = useState<DeliveryIndicator | null>(null);
  const [issueAuthorID, setIssueAuthorID] = useState("");
  const [issueAgentNameByID, setIssueAgentNameByID] = useState<Map<string, string>>(new Map());
  const [queuedAttachments, setQueuedAttachments] = useState<ChatAttachment[]>([]);
  const [uploadingAttachments, setUploadingAttachments] = useState(false);
  const [isDragActive, setIsDragActive] = useState(false);
  const [emissionAutoScrollSignal, setEmissionAutoScrollSignal] = useState(0);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const onConversationTouchedRef = useRef(onConversationTouched);
  const postSendRefreshTimersRef = useRef<number[]>([]);
  const stalledTurnTimerRef = useRef<number | null>(null);
  const awaitingAgentReplyRef = useRef(false);
  const { lastMessage } = useWS();
  const conversationType = conversation.type;
  const conversationKey = conversation.key;
  const conversationTitle = conversation.title;
  const conversationContextLabel = conversation.contextLabel;
  const dmThreadID = conversationType === "dm" ? conversation.threadId : "";
  const projectID = conversationType === "project" ? conversation.projectId : "";
  const issueID = conversationType === "issue" ? conversation.issueId : "";

  useEffect(() => {
    onConversationTouchedRef.current = onConversationTouched;
  }, [onConversationTouched]);

  const orgID = useMemo(() => getStoredOrgID(), [conversationKey]);
  const token = useMemo(() => getAuthToken(), [conversationKey]);
  const currentUserName = useMemo(() => getCurrentUserName(), [conversationKey]);
  const currentUserID = useMemo(() => `user:${currentUserName.toLowerCase()}`, [currentUserName]);

  const threadID = useMemo(() => {
    if (conversationType === "dm") {
      return dmThreadID;
    }
    return conversationKey;
  }, [conversationKey, conversationType, dmThreadID]);
  const emissionMessageID = useMemo(() => `emission-${conversationKey}`, [conversationKey]);

  const requestHeaders = useMemo(() => {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
    return headers;
  }, [token]);

  const touchConversation = useCallback(() => {
    onConversationTouchedRef.current?.();
  }, []);

  const clearPostSendRefreshTimers = useCallback(() => {
    for (const timerID of postSendRefreshTimersRef.current) {
      window.clearTimeout(timerID);
    }
    postSendRefreshTimersRef.current = [];
  }, []);

  const clearStalledTurnTimer = useCallback(() => {
    if (stalledTurnTimerRef.current !== null) {
      window.clearTimeout(stalledTurnTimerRef.current);
      stalledTurnTimerRef.current = null;
    }
  }, []);

  const clearStalledTurnWarning = useCallback(() => {
    awaitingAgentReplyRef.current = false;
    clearStalledTurnTimer();
  }, [clearStalledTurnTimer]);

  const startStalledTurnWarningWindow = useCallback(() => {
    awaitingAgentReplyRef.current = true;
    clearStalledTurnTimer();
    stalledTurnTimerRef.current = window.setTimeout(() => {
      if (!awaitingAgentReplyRef.current) {
        return;
      }
      setMessages((prev) =>
        upsertStalledEmissionTimelineMessage(prev, emissionMessageID, threadID, conversationTitle),
      );
      setEmissionAutoScrollSignal((prev) => prev + 1);
    }, STALLED_TURN_TIMEOUT_MS);
  }, [clearStalledTurnTimer, conversationTitle, emissionMessageID, threadID]);

  const loadConversation = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    if (!orgID) {
      setMessages([]);
      if (!silent) {
        setLoading(false);
        setError("Missing organization context");
      }
      return;
    }

    if (!silent) {
      setLoading(true);
      setError(null);
      setDeliveryIndicator(null);
    }

    try {
      if (conversationType === "dm") {
        const params = new URLSearchParams({
          org_id: orgID,
          thread_id: dmThreadID,
          limit: "100",
        });
        const response = await fetch(`${API_URL}/api/messages?${params.toString()}`, {
          headers: requestHeaders,
          cache: "no-store",
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to fetch messages");
        }
        const payload = await response.json();
        const normalized = Array.isArray(payload.messages)
          ? payload.messages
              .map((entry: unknown) =>
                normalizeThreadMessage(entry, dmThreadID, currentUserName, currentUserID),
              )
              .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
          : [];
        setMessages(normalized);
        touchConversation();
        return;
      }

      if (conversationType === "project") {
        const url = new URL(`${API_URL}/api/projects/${projectID}/chat`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("limit", "100");

        const response = await fetch(url.toString(), {
          headers: requestHeaders,
          cache: "no-store",
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to fetch project chat");
        }

        const payload = await response.json();
        const normalizedMessages = Array.isArray(payload.messages)
          ? payload.messages
              .map((entry: unknown) =>
                normalizeProjectMessage(
                  entry,
                  projectID,
                  conversationKey,
                  currentUserName,
                  currentUserID,
                ),
              )
              .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
          : [];
        const normalizedQuestionnaires = Array.isArray(payload.questionnaires)
          ? payload.questionnaires
              .map((entry: unknown) => normalizeQuestionnairePayload(entry, "project_chat", projectID))
              .filter((entry: QuestionnairePayload | null): entry is QuestionnairePayload => entry !== null)
              .map((entry: QuestionnairePayload) =>
                normalizeQuestionnaireMessage(
                  entry,
                  conversationKey,
                  currentUserName,
                  currentUserID,
                ),
              )
          : [];
        const normalized = [...normalizedMessages, ...normalizedQuestionnaires];

        normalized.sort((a: ChatMessage, b: ChatMessage) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
        setMessages(normalized);
        touchConversation();
        return;
      }

      const issueURL = new URL(`${API_URL}/api/issues/${issueID}`);
      issueURL.searchParams.set("org_id", orgID);
      const agentsURL = new URL(`${API_URL}/api/agents`);
      agentsURL.searchParams.set("org_id", orgID);

      const [issueResponse, agentsResponse] = await Promise.all([
        fetch(issueURL.toString(), { headers: requestHeaders, cache: "no-store" }),
        fetch(agentsURL.toString(), { headers: requestHeaders, cache: "no-store" }),
      ]);

      if (!issueResponse.ok) {
        const payload = await issueResponse.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to fetch issue chat");
      }

      const issuePayload = (await issueResponse.json()) as IssueDetailResponse;
      const agentsPayload = agentsResponse.ok
        ? ((await agentsResponse.json()) as AgentsResponse)
        : { agents: [] };

      const agentMap = new Map<string, string>();
      for (const agent of agentsPayload.agents ?? []) {
        if (agent.id && agent.name) {
          agentMap.set(agent.id, agent.name);
        }
      }
      setIssueAgentNameByID(agentMap);

      const participants = Array.isArray(issuePayload.participants)
        ? issuePayload.participants.filter((entry) => !entry.removed_at)
        : [];
      const owner = participants.find((entry) => entry.role === "owner")?.agent_id;
      const defaultAuthor = owner || participants[0]?.agent_id || "";
      setIssueAuthorID((current) => current || defaultAuthor);

      const normalizedComments = Array.isArray(issuePayload.comments)
        ? issuePayload.comments
            .map((entry) => normalizeIssueComment(entry, conversationKey, agentMap, defaultAuthor))
            .filter((entry: ChatMessage | null): entry is ChatMessage => entry !== null)
        : [];
      const normalizedQuestionnaires = Array.isArray(issuePayload.questionnaires)
        ? issuePayload.questionnaires
            .map((entry) => normalizeQuestionnairePayload(entry, "issue", issueID))
            .filter((entry: QuestionnairePayload | null): entry is QuestionnairePayload => entry !== null)
            .map((entry: QuestionnairePayload) =>
              normalizeQuestionnaireMessage(
                entry,
                conversationKey,
                currentUserName,
                currentUserID,
              ),
            )
        : [];
      const normalized = [...normalizedComments, ...normalizedQuestionnaires];
      normalized.sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
      setMessages(normalized);
      touchConversation();
    } catch (loadError) {
      if (!silent) {
        setError(loadError instanceof Error ? loadError.message : "Failed to load chat");
        setMessages([]);
      }
    } finally {
      if (!silent) {
        setLoading(false);
      }
    }
  }, [
    conversationKey,
    conversationType,
    currentUserID,
    currentUserName,
    dmThreadID,
    issueID,
    orgID,
    projectID,
    requestHeaders,
    touchConversation,
  ]);

  useEffect(() => {
    void loadConversation();
  }, [loadConversation, refreshVersion]);

  useEffect(() => {
    clearPostSendRefreshTimers();
    setQueuedAttachments([]);
    setUploadingAttachments(false);
    setIsDragActive(false);
    clearStalledTurnWarning();
    return () => {
      clearPostSendRefreshTimers();
      clearStalledTurnTimer();
    };
  }, [
    clearPostSendRefreshTimers,
    clearStalledTurnWarning,
    clearStalledTurnTimer,
    conversationKey,
  ]);

  useEffect(() => {
    if (!lastMessage) {
      return;
    }

    if (lastMessage.type === "EmissionReceived") {
      const emission = normalizeChatEmission(lastMessage.data);
      if (!emission) {
        return;
      }
      if (!chatEmissionMatchesConversation(emission, conversationType, dmThreadID, projectID, issueID)) {
        return;
      }
      setMessages((prev) =>
        upsertEmissionTimelineMessage(prev, emission, emissionMessageID, threadID, conversationTitle),
      );
      setEmissionAutoScrollSignal((prev) => prev + 1);
      clearStalledTurnWarning();
      return;
    }

    if (conversationType === "dm" && lastMessage.type === "DMMessageReceived") {
      if (!lastMessage.data || typeof lastMessage.data !== "object") {
        return;
      }
      const payload = lastMessage.data as Record<string, unknown>;
      const eventThreadID =
        (typeof payload.threadId === "string" && payload.threadId) ||
        (typeof payload.thread_id === "string" && payload.thread_id) ||
        "";
      if (eventThreadID !== dmThreadID) {
        return;
      }

      const normalized = normalizeThreadMessage(
        payload.message ?? payload,
        dmThreadID,
        currentUserName,
        currentUserID,
      );
      if (!normalized) {
        return;
      }

      if (normalized.senderType === "agent") {
        clearPostSendRefreshTimers();
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
        clearStalledTurnWarning();
      }

      setMessages((prev) =>
        normalized.senderType === "agent"
          ? upsertReplyReplacingEmission(prev, normalized, emissionMessageID)
          : upsertIncomingMessage(prev, normalized),
      );
      return;
    }

    if (conversationType === "project" && lastMessage.type === "ProjectChatMessageCreated") {
      const normalized = normalizeProjectMessage(
        lastMessage.data,
        projectID,
        conversationKey,
        currentUserName,
        currentUserID,
      );
      if (!normalized) {
        return;
      }

      if (normalized.senderType === "agent") {
        clearPostSendRefreshTimers();
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
        clearStalledTurnWarning();
      }
      if (normalized.isSessionReset) {
        setDeliveryIndicator({ tone: "neutral", text: "Started new session" });
      }

      setMessages((prev) =>
        normalized.senderType === "agent"
          ? upsertReplyReplacingEmission(prev, normalized, emissionMessageID, true)
          : upsertIncomingMessage(prev, normalized, true),
      );
      return;
    }

    if (conversationType === "issue" && lastMessage.type === "IssueCommentCreated") {
      if (!lastMessage.data || typeof lastMessage.data !== "object") {
        return;
      }
      const payload = lastMessage.data as Record<string, unknown>;
      const comment =
        payload.comment && typeof payload.comment === "object"
          ? (payload.comment as Record<string, unknown>)
          : null;
      const eventIssueID =
        (typeof payload.issue_id === "string" && payload.issue_id) ||
        (typeof payload.issueId === "string" && payload.issueId) ||
        (comment && typeof comment.issue_id === "string" && comment.issue_id) ||
        (comment && typeof comment.issueId === "string" && comment.issueId) ||
        "";
      if (eventIssueID !== issueID) {
        return;
      }
      if (!comment) {
        return;
      }

      const normalized = normalizeIssueComment(
        comment,
        conversationKey,
        issueAgentNameByID,
        issueAuthorID,
      );
      if (!normalized) {
        return;
      }

      if (normalized.senderType === "agent") {
        clearPostSendRefreshTimers();
        setDeliveryIndicator({ tone: "success", text: "Agent replied" });
        clearStalledTurnWarning();
      }

      setMessages((prev) =>
        normalized.senderType === "agent"
          ? upsertReplyReplacingEmission(prev, normalized, emissionMessageID, true)
          : upsertIncomingMessage(prev, normalized, true),
      );
    }
  }, [
    conversationKey,
    conversationTitle,
    conversationType,
    currentUserID,
    currentUserName,
    dmThreadID,
    emissionMessageID,
    issueID,
    projectID,
    threadID,
    issueAgentNameByID,
    issueAuthorID,
    lastMessage,
    clearPostSendRefreshTimers,
    clearStalledTurnWarning,
  ]);

  const uploadSelectedFiles = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    if (!orgID) {
      setError("Missing organization context");
      return;
    }

    setError(null);
    setUploadingAttachments(true);
    setDeliveryIndicator({ tone: "neutral", text: "Uploading attachments..." });

    let uploadedCount = 0;
    for (const file of files) {
      try {
        const formData = new FormData();
        formData.append("org_id", orgID);
        formData.append("file", file);

        const headers: Record<string, string> = {};
        if (token) {
          headers.Authorization = `Bearer ${token}`;
        }

        const response = await fetch(`${API_URL}/api/messages/attachments`, {
          method: "POST",
          headers,
          body: formData,
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? `Failed to upload ${file.name}`);
        }

        const payload = await response.json();
        const normalized = normalizeMessageAttachments([payload?.attachment])[0];
        if (!normalized) {
          throw new Error(`Invalid upload response for ${file.name}`);
        }
        uploadedCount += 1;
        setQueuedAttachments((prev) => {
          if (prev.some((attachment) => attachment.id === normalized.id)) {
            return prev;
          }
          return [...prev, normalized];
        });
      } catch (uploadError) {
        setError(uploadError instanceof Error ? uploadError.message : `Failed to upload ${file.name}`);
      }
    }

    if (uploadedCount > 0) {
      const suffix = uploadedCount === 1 ? "" : "s";
      setDeliveryIndicator({
        tone: "neutral",
        text: `${uploadedCount} attachment${suffix} ready`,
      });
    }
    setUploadingAttachments(false);
  }, [orgID, token]);

  const queueFilesFromInput = useCallback((input: FileList | File[] | null | undefined) => {
    const files = Array.from(input ?? []);
    if (files.length === 0) {
      return;
    }
    void uploadSelectedFiles(files);
  }, [uploadSelectedFiles]);

  const onAttachmentInputChange = useCallback((event: ChangeEvent<HTMLInputElement>) => {
    queueFilesFromInput(event.target.files);
    event.target.value = "";
  }, [queueFilesFromInput]);

  const onComposerPaste = useCallback((event: ClipboardEvent<HTMLTextAreaElement>) => {
    const clipboard = event.clipboardData;
    if (!clipboard) {
      return;
    }
    const pastedFiles: File[] = [];
    for (const item of Array.from(clipboard.items)) {
      if (item.kind !== "file") {
        continue;
      }
      const file = item.getAsFile();
      if (file) {
        pastedFiles.push(file);
      }
    }
    if (pastedFiles.length > 0) {
      event.preventDefault();
      queueFilesFromInput(pastedFiles);
    }
  }, [queueFilesFromInput]);

  const onContainerDragOver = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setIsDragActive(true);
  }, []);

  const onContainerDragLeave = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setIsDragActive(false);
  }, []);

  const onContainerDrop = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setIsDragActive(false);
    queueFilesFromInput(event.dataTransfer.files);
  }, [queueFilesFromInput]);

  const onOpenFilePicker = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const onRemoveQueuedAttachment = useCallback((attachmentID: string) => {
    setQueuedAttachments((prev) => prev.filter((attachment) => attachment.id !== attachmentID));
  }, []);

  const sendMessage = useCallback(async (
    bodyOverride?: string,
    retryMessageID?: string,
    attachmentOverride?: ChatAttachment[],
  ) => {
    const body = (bodyOverride ?? draft).trim();
    const attachmentsForSend = attachmentOverride ?? queuedAttachments;
    if ((body === "" && attachmentsForSend.length === 0) || sending || !orgID) {
      return;
    }

    const isRetry = typeof retryMessageID === "string" && retryMessageID.trim() !== "";

    setError(null);
    setSending(true);
    setDeliveryIndicator({ tone: "neutral", text: "Sending..." });
    clearPostSendRefreshTimers();
    startStalledTurnWarningWindow();

    const optimisticID = isRetry
      ? retryMessageID!.trim()
      : `temp-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const optimisticMessage: ChatMessage = {
      id: optimisticID,
      threadId: threadID,
      senderId: conversationType === "issue" ? issueAuthorID || currentUserID : currentUserID,
      senderName: conversationType === "issue"
        ? (issueAgentNameByID.get(issueAuthorID) ?? currentUserName)
        : currentUserName,
      senderType: "user",
      content: body,
      attachments: attachmentsForSend,
      createdAt: new Date().toISOString(),
      optimistic: true,
    };

    setMessages((prev) => {
      if (isRetry) {
        return prev.map((entry) =>
          entry.id === optimisticID
            ? {
                ...entry,
                optimistic: true,
                failed: false,
              }
            : entry,
        );
      }
      return [...prev, optimisticMessage];
    });
    if (!isRetry) {
      setDraft("");
    }
    touchConversation();

    try {
      if (conversationType === "dm") {
        const response = await fetch(`${API_URL}/api/messages`, {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            org_id: orgID,
            thread_id: dmThreadID,
            content: body,
            attachments: attachmentsForSend,
            sender_type: "user",
            sender_name: currentUserName,
            sender_id: currentUserID,
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to send message");
        }

        const payload = await response.json();
        const persisted = normalizeThreadMessage(
          payload.message,
          dmThreadID,
          currentUserName,
          currentUserID,
        );
        setMessages((prev) => reconcileOptimisticPersistedMessage(prev, optimisticID, persisted));

        if (payload?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
          for (const delay of POST_SEND_REFRESH_DELAYS_MS) {
            const timerID = window.setTimeout(() => {
              void loadConversation({ silent: true });
            }, delay);
            postSendRefreshTimersRef.current.push(timerID);
          }
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
        if (!isRetry) {
          setQueuedAttachments([]);
        }
      } else if (conversationType === "project") {
        const url = new URL(`${API_URL}/api/projects/${projectID}/chat/messages`);
        url.searchParams.set("org_id", orgID);

        const response = await fetch(url.toString(), {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            author: currentUserName,
            body,
            attachment_ids: attachmentsForSend.map((attachment) => attachment.id),
            sender_type: "user",
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to send project message");
        }

        const payload = await response.json();
        const persisted = normalizeProjectMessage(
          payload.message,
          projectID,
          conversationKey,
          currentUserName,
          currentUserID,
        );
        setMessages((prev) => reconcileOptimisticPersistedMessage(prev, optimisticID, persisted));

        if (payload?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
          for (const delay of POST_SEND_REFRESH_DELAYS_MS) {
            const timerID = window.setTimeout(() => {
              void loadConversation({ silent: true });
            }, delay);
            postSendRefreshTimersRef.current.push(timerID);
          }
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
        if (!isRetry) {
          setQueuedAttachments([]);
        }
      } else {
        if (!issueAuthorID) {
          throw new Error("No issue author configured for this thread");
        }

        const attachmentLinks = buildAttachmentLinksMarkdown(attachmentsForSend);
        const issueBody = attachmentLinks === ""
          ? body
          : body === ""
            ? attachmentLinks
            : `${body}\n\n${attachmentLinks}`;

        const url = new URL(`${API_URL}/api/issues/${issueID}/comments`);
        url.searchParams.set("org_id", orgID);

        const response = await fetch(url.toString(), {
          method: "POST",
          headers: requestHeaders,
          body: JSON.stringify({
            author_agent_id: issueAuthorID,
            body: issueBody,
            sender_type: "user",
          }),
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to send issue message");
        }

        const payload = await response.json();
        const persisted = normalizeIssueComment(
          payload,
          conversationKey,
          issueAgentNameByID,
          issueAuthorID,
        );
        setMessages((prev) => reconcileOptimisticPersistedMessage(prev, optimisticID, persisted));

        if (payload?.delivery?.delivered === true) {
          setDeliveryIndicator({ tone: "success", text: "Delivered to bridge" });
          for (const delay of POST_SEND_REFRESH_DELAYS_MS) {
            const timerID = window.setTimeout(() => {
              void loadConversation({ silent: true });
            }, delay);
            postSendRefreshTimersRef.current.push(timerID);
          }
        } else if (typeof payload?.delivery?.error === "string" && payload.delivery.error.trim() !== "") {
          setError(normalizeDeliveryErrorText(payload.delivery.error));
          setDeliveryIndicator({ tone: "warning", text: "Saved; delivery pending" });
        } else {
          setDeliveryIndicator({ tone: "neutral", text: "Saved" });
        }
        if (!isRetry) {
          setQueuedAttachments([]);
        }
      }
    } catch (sendError) {
      clearStalledTurnWarning();
      setMessages((prev) =>
        prev.map((entry) =>
          entry.id === optimisticID
            ? {
                ...entry,
                optimistic: false,
                failed: true,
              }
            : entry,
        ),
      );
      setError(sendError instanceof Error ? sendError.message : "Failed to send message");
      setDeliveryIndicator({ tone: "warning", text: "Send failed; retry to resend" });
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  }, [
    conversationKey,
    conversationType,
    currentUserID,
    currentUserName,
    draft,
    dmThreadID,
    issueAgentNameByID,
    issueAuthorID,
    issueID,
    orgID,
    queuedAttachments,
    projectID,
    requestHeaders,
    sending,
    threadID,
    touchConversation,
    loadConversation,
    clearPostSendRefreshTimers,
    clearStalledTurnWarning,
    startStalledTurnWarningWindow,
  ]);

  const handleRetryMessage = useCallback(
    (message: DMMessage) => {
      const retryChatMessage = message as ChatMessage;
      void sendMessage(message.content, message.id, retryChatMessage.attachments ?? []);
    },
    [sendMessage],
  );

  const submitQuestionnaireResponse = useCallback(async (
    questionnaireID: string,
    responses: Record<string, unknown>,
  ) => {
    if (!orgID) {
      throw new Error("Missing organization context");
    }
    const trimmedID = questionnaireID.trim();
    if (!trimmedID) {
      throw new Error("Questionnaire id is required");
    }
    if (conversationType !== "project" && conversationType !== "issue") {
      throw new Error("Questionnaires are only available in project or issue conversations");
    }

    setError(null);
    setDeliveryIndicator({ tone: "neutral", text: "Submitting questionnaire..." });

    const url = new URL(`${API_URL}/api/questionnaires/${trimmedID}/response`);
    url.searchParams.set("org_id", orgID);

    const response = await fetch(url.toString(), {
      method: "POST",
      headers: requestHeaders,
      body: JSON.stringify({
        responded_by: currentUserName,
        responses,
      }),
    });
    if (!response.ok) {
      const payload = await response.json().catch(() => null);
      throw new Error(payload?.error ?? "Failed to submit questionnaire");
    }

    const payload = await response.json();
    const normalizedPayload = normalizeQuestionnairePayload(
      payload,
      conversationType === "project" ? "project_chat" : "issue",
      conversationType === "project" ? projectID : issueID,
    );
    if (!normalizedPayload) {
      throw new Error("Invalid questionnaire response payload");
    }
    const normalizedMessage = normalizeQuestionnaireMessage(
      normalizedPayload,
      conversationKey,
      currentUserName,
      currentUserID,
    );

    setMessages((prev) =>
      prev.map((entry) =>
        entry.questionnaire?.id === trimmedID
          ? normalizedMessage
          : entry,
      ),
    );
    setDeliveryIndicator({ tone: "success", text: "Questionnaire submitted" });
  }, [
    conversationKey,
    conversationType,
    currentUserID,
    currentUserName,
    issueID,
    orgID,
    projectID,
    requestHeaders,
  ]);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void sendMessage();
  };

  const onDraftKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void sendMessage();
    }
  };

  const onDraftInput = () => {
    const textarea = inputRef.current;
    if (!textarea) {
      return;
    }
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 150)}px`;
  };

  const pseudoAgent = useMemo(
    () => ({
      id: conversationKey,
      name: conversationTitle,
      status: "online" as const,
      role: conversationContextLabel,
    }),
    [conversationContextLabel, conversationKey, conversationTitle],
  );
  const surfaceContextCue = useMemo(() => {
    return getChatContextCue(conversationType);
  }, [conversationType]);

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="flex items-center gap-3 text-[var(--text-muted)]">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-[var(--border)] border-t-[var(--accent)]" />
          <span>Loading conversation...</span>
        </div>
      </div>
    );
  }

  return (
    <div
      className={`oc-chat-surface flex h-full min-h-0 flex-col overflow-hidden ${isDragActive ? "ring-2 ring-inset ring-[var(--accent)]" : ""}`}
      onDragOver={onContainerDragOver}
      onDragLeave={onContainerDragLeave}
      onDrop={onContainerDrop}
    >
      {showContextHeader ? (
        <div className="border-b border-[var(--border)]/70 bg-[var(--surface-alt)]/40 px-4 py-2">
          <p
            data-testid="global-chat-surface-context-cue"
            className="oc-chip inline-flex rounded-full border border-[var(--border)] px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-[var(--text-muted)]"
          >
            {surfaceContextCue}
          </p>
          <p className="truncate text-xs text-[var(--text-muted)]">{conversationContextLabel}</p>
        </div>
      ) : null}

      <MessageHistory
        messages={messages}
        currentUserId={conversationType === "issue" ? issueAuthorID || currentUserID : currentUserID}
        threadId={threadID}
        autoScrollSignal={emissionAutoScrollSignal}
        agent={pseudoAgent}
        agentNamesByID={agentNamesByID}
        resolveAgentName={resolveAgentName}
        onRetryMessage={handleRetryMessage}
        onSubmitQuestionnaire={submitQuestionnaireResponse}
      />

      {error ? (
        <div className="border-t border-[var(--red)]/40 bg-[var(--red)]/15 px-4 py-2">
          <p className="text-sm text-[var(--red)]">{error}</p>
        </div>
      ) : null}

      {deliveryIndicator ? (
        <div className="border-t border-[var(--border)]/70 px-4 py-1.5">
          <p className={`inline-flex rounded-full border px-2.5 py-0.5 text-[11px] ${deliveryIndicatorClass(deliveryIndicator)}`}>
            {deliveryIndicator.text}
          </p>
        </div>
      ) : null}

      {queuedAttachments.length > 0 ? (
        <div className="border-t border-[var(--border)]/70 px-4 py-2">
          <div className="flex flex-wrap gap-2">
            {queuedAttachments.map((attachment) => (
              <div
                key={attachment.id}
                className="inline-flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-1 text-xs text-[var(--text-muted)]"
              >
                <span className="max-w-[220px] truncate">{attachment.filename}</span>
                <span>{formatAttachmentSize(attachment.size_bytes)}</span>
                <button
                  type="button"
                  onClick={() => onRemoveQueuedAttachment(attachment.id)}
                  className="rounded-full px-1 text-[var(--text-muted)] transition hover:bg-[var(--border)] hover:text-[var(--text)]"
                  aria-label={`Remove ${attachment.filename}`}
                  disabled={sending}
                >
                  ×
                </button>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      <form onSubmit={onSubmit} className="oc-chat-composer flex items-end gap-3 px-4 py-3">
        <input
          ref={fileInputRef}
          type="file"
          multiple
          onChange={onAttachmentInputChange}
          className="hidden"
          aria-label="Attach files"
        />
        <button
          type="button"
          onClick={onOpenFilePicker}
          disabled={sending || uploadingAttachments || (conversationType === "issue" && issueAuthorID === "")}
          className="inline-flex h-10 w-10 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Attach files"
        >
          📎
        </button>
        <textarea
          ref={inputRef}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onInput={onDraftInput}
          onKeyDown={onDraftKeyDown}
          onPaste={onComposerPaste}
          aria-label="Message composer"
          placeholder={`Message ${conversationTitle}...`}
          rows={1}
          disabled={sending || uploadingAttachments || (conversationType === "issue" && issueAuthorID === "")}
          className="oc-chat-input flex-1 resize-none rounded-xl border px-4 py-2.5 text-sm text-[var(--text)] placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={
            sending ||
            uploadingAttachments ||
            (draft.trim() === "" && queuedAttachments.length === 0) ||
            (conversationType === "issue" && issueAuthorID === "")
          }
          className="oc-chat-send inline-flex h-10 w-10 items-center justify-center rounded-xl transition disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Send message"
        >
          {sending ? (
            <div className="h-4 w-4 animate-spin rounded-full border-2 border-[#1A1918]/30 border-t-[#1A1918]" />
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

      <div className="border-t border-[var(--border)]/70 bg-[var(--surface-alt)]/40 px-4 py-1.5">
        <p className="text-[10px] text-[var(--text-muted)]">
          Press <span className="font-medium">Enter</span> to send, <span className="font-medium">Shift + Enter</span> for a new line
        </p>
      </div>
    </div>
  );
}
