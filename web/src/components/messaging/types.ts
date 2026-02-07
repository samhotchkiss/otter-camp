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
export type MessageSenderType = "user" | "agent";

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
  createdAt: string;
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
