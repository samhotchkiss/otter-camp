export type CriticMarkupComment = {
  id: string;
  token: string;
  raw: string;
  message: string;
  author?: string;
  start: number;
  end: number;
};

export type CriticMarkupTokenized = {
  markdown: string;
  commentsByToken: Record<string, CriticMarkupComment>;
};

export type CriticMarkupInsertion = {
  markdown: string;
  start: number;
  end: number;
  author?: string | null;
  message: string;
};

export type CriticMarkupInsertionResult = {
  markdown: string;
  marker: string;
  insertionStart: number;
  insertionEnd: number;
  cursor: number;
};

const CRITIC_COMMENT_REGEX = /\{>>([\s\S]*?)<<\}/g;
const CRITIC_TOKEN_PREFIX = "@@CRITIC_COMMENT_";
const CRITIC_TOKEN_SUFFIX = "@@";
const AUTHOR_PREFIX_REGEX = /^([A-Za-z0-9_.-]{1,16})\s*:\s*([\s\S]+)$/;

function parseCommentContent(content: string): { author?: string; message: string } {
  const trimmed = content.trim();
  if (trimmed === "") {
    return { message: "" };
  }

  const match = AUTHOR_PREFIX_REGEX.exec(trimmed);
  if (!match) {
    return { message: trimmed };
  }

  return {
    author: match[1],
    message: match[2].trim(),
  };
}

export function parseCriticMarkupComments(markdown: string): CriticMarkupComment[] {
  const comments: CriticMarkupComment[] = [];
  let index = 0;

  markdown.replace(CRITIC_COMMENT_REGEX, (raw: string, content: string, offset: number) => {
    const parsed = parseCommentContent(content);
    const token = `${CRITIC_TOKEN_PREFIX}${index}${CRITIC_TOKEN_SUFFIX}`;
    comments.push({
      id: `critic-comment-${index}`,
      token,
      raw,
      message: parsed.message,
      author: parsed.author,
      start: offset,
      end: offset + raw.length,
    });
    index += 1;
    return raw;
  });

  return comments;
}

export function serializeCriticMarkupComment(comment: {
  author?: string | null;
  message: string;
  raw?: string;
}): string {
  if (typeof comment.raw === "string" && comment.raw.startsWith("{>>") && comment.raw.endsWith("<<}")) {
    return comment.raw;
  }

  const author = (comment.author ?? "").trim();
  const message = comment.message.trim();
  if (author !== "") {
    return `{>>${author}: ${message}<<}`;
  }
  return `{>>${message}<<}`;
}

export function tokenizeCriticMarkup(markdown: string): CriticMarkupTokenized {
  const commentsByToken: Record<string, CriticMarkupComment> = {};
  let index = 0;

  const tokenized = markdown.replace(CRITIC_COMMENT_REGEX, (raw: string, content: string, offset: number) => {
    const parsed = parseCommentContent(content);
    const token = `${CRITIC_TOKEN_PREFIX}${index}${CRITIC_TOKEN_SUFFIX}`;
    commentsByToken[token] = {
      id: `critic-comment-${index}`,
      token,
      raw,
      message: parsed.message,
      author: parsed.author,
      start: offset,
      end: offset + raw.length,
    };
    index += 1;
    return token;
  });

  return {
    markdown: tokenized,
    commentsByToken,
  };
}

export function isCriticToken(value: string): boolean {
  return value.startsWith(CRITIC_TOKEN_PREFIX) && value.endsWith(CRITIC_TOKEN_SUFFIX);
}

export function restoreCriticMarkupTokens(
  value: string,
  commentsByToken: Record<string, CriticMarkupComment>
): string {
  if (value.trim() === "") {
    return value;
  }

  const pattern = new RegExp(`(${CRITIC_TOKEN_PREFIX}\\d+${CRITIC_TOKEN_SUFFIX})`, "g");
  return value.replace(pattern, (token) => commentsByToken[token]?.raw ?? token);
}

function normalizeOffset(value: number, markdownLength: number): number {
  if (Number.isNaN(value) || !Number.isFinite(value)) {
    return 0;
  }
  if (value < 0) {
    return 0;
  }
  if (value > markdownLength) {
    return markdownLength;
  }
  return Math.floor(value);
}

function clampOutsideCommentBoundary(offset: number, comments: CriticMarkupComment[]): number {
  for (const comment of comments) {
    if (offset > comment.start && offset < comment.end) {
      return comment.end;
    }
  }
  return offset;
}

export function insertCriticMarkupCommentAtSelection(
  input: CriticMarkupInsertion
): CriticMarkupInsertionResult {
  const markdown = input.markdown ?? "";
  const length = markdown.length;
  let start = normalizeOffset(input.start, length);
  let end = normalizeOffset(input.end, length);
  if (start > end) {
    [start, end] = [end, start];
  }

  const comments = parseCriticMarkupComments(markdown);
  start = clampOutsideCommentBoundary(start, comments);
  end = clampOutsideCommentBoundary(end, comments);
  if (start > end) {
    start = end;
  }

  const marker = serializeCriticMarkupComment({
    author: input.author,
    message: input.message,
  });

  const insertionStart = end;
  const nextMarkdown = markdown.slice(0, insertionStart) + marker + markdown.slice(insertionStart);
  const cursor = insertionStart + marker.length;

  return {
    markdown: nextMarkdown,
    marker,
    insertionStart,
    insertionEnd: cursor,
    cursor,
  };
}
