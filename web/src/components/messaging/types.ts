/**
 * Agent status for the status indicator.
 */
export type AgentStatus = "online" | "busy" | "offline";

/**
 * Agent information used by messaging components.
 */
export type Agent = {
  id: string;
  name: string;
  avatarUrl?: string;
  status: AgentStatus;
  role?: string;
};

/**
 * Message sender type - distinguishes between human users and AI agents.
 */
export type MessageSenderType = "user" | "agent" | "emission";

export type MessageAttachment = {
  id: string;
  filename: string;
  size_bytes: number;
  mime_type: string;
  url: string;
  thumbnail_url?: string;
};

export type QuestionnaireQuestionType =
  | "text"
  | "textarea"
  | "boolean"
  | "select"
  | "multiselect"
  | "number"
  | "date";

export type MessageQuestionnaireQuestion = {
  id: string;
  text: string;
  type: QuestionnaireQuestionType;
  required: boolean;
  options?: string[];
  placeholder?: string;
  default?: unknown;
};

export type MessageQuestionnaire = {
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

/**
 * Represents a single message in a DM thread.
 */
export type DMMessage = {
  id: string;
  threadId: string;
  senderId: string;
  senderName: string;
  senderType: MessageSenderType;
  senderAvatarUrl?: string;
  content: string;
  attachments?: MessageAttachment[];
  questionnaire?: MessageQuestionnaire;
  createdAt: string;
  optimistic?: boolean;
  failed?: boolean;
  emissionWarning?: boolean;
  isSessionReset?: boolean;
  sessionID?: string;
};

/**
 * Represents a single message in a task thread.
 */
export type TaskThreadMessage = {
  id: string;
  taskId?: string;
  senderId?: string;
  senderName?: string;
  senderType?: MessageSenderType;
  senderAvatarUrl?: string;
  content: string;
  createdAt: string;
};

/**
 * Pagination info returned from API.
 */
export type PaginationInfo = {
  hasMore: boolean;
  nextCursor?: string;
  totalCount?: number;
};
