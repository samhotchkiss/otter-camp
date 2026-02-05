export type MessageSenderType = "user" | "agent";

export type Attachment = {
  id: string;
  filename: string;
  size_bytes: number;
  mime_type: string;
  url: string;
  thumbnail_url?: string;
};

export type TaskThreadMessage = {
  id: string;
  taskId?: string;
  senderId?: string;
  senderName?: string;
  senderType?: MessageSenderType;
  senderAvatarUrl?: string;
  content: string;
  attachments?: Attachment[];
  createdAt: string;
};

