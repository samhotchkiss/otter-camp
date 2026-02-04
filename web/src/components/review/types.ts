export type ReviewUser = {
  id: string;
  name: string;
  avatarUrl?: string;
  handle?: string;
};

export type ReviewReaction = {
  id: string;
  emoji: string;
  count: number;
};

export type ReviewComment = {
  id: string;
  author: ReviewUser;
  body: string;
  createdAt: string;
  reactions?: ReviewReaction[];
};

export type ReviewCommentThread = {
  id: string;
  lineNumber: number;
  side: "left" | "right" | "unified";
  isResolved?: boolean;
  comments: ReviewComment[];
};

export type DiffLineType = "context" | "add" | "del";

export type DiffLine = {
  id: string;
  type: DiffLineType;
  oldNumber?: number;
  newNumber?: number;
  content: string;
  isDimmed?: boolean;
};

export type DiffHunk = {
  id: string;
  header: string;
  lines: DiffLine[];
};

export type DiffFileStatus = "modified" | "added" | "deleted" | "renamed";

export type DiffFile = {
  id: string;
  path: string;
  previousPath?: string;
  status: DiffFileStatus;
  additions: number;
  deletions: number;
  hunks: DiffHunk[];
  commentThreads?: ReviewCommentThread[];
};

export type ReviewSummary = {
  approvals: number;
  changesRequested: number;
  comments: number;
};

export type ReviewAction = "approve" | "request_changes" | "comment";
