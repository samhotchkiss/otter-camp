import { useEffect, useMemo, useState } from "react";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark, oneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import ContentReview, { type ContentReviewActionPayload, type ReviewComment } from "./ContentReview";
import { resolveEditorForPath } from "./editorModeResolver";

export type DocumentWorkspaceProps = {
  path: string;
  content: string;
  previousContent?: string;
  imageSrc?: string;
  reviewerName?: string;
  readOnly?: boolean;
  onContentChange?: (next: string) => void;
  onApprove?: (payload: ContentReviewActionPayload) => void;
  onRequestChanges?: (payload: ContentReviewActionPayload) => void;
  onCommentAdd?: (comment: ReviewComment) => void;
};

type DiffLine = {
  type: "context" | "add" | "del";
  content: string;
};

function detectLanguage(path: string): string {
  const lower = path.toLowerCase();
  if (lower.endsWith(".ts") || lower.endsWith(".tsx")) return "typescript";
  if (lower.endsWith(".js") || lower.endsWith(".jsx")) return "javascript";
  if (lower.endsWith(".py")) return "python";
  if (lower.endsWith(".go")) return "go";
  return "text";
}

function buildSimpleLineDiff(previous: string, current: string): DiffLine[] {
  const beforeLines = previous.split("\n");
  const afterLines = current.split("\n");
  const max = Math.max(beforeLines.length, afterLines.length);
  const lines: DiffLine[] = [];

  for (let index = 0; index < max; index += 1) {
    const before = beforeLines[index];
    const after = afterLines[index];
    if (before === after) {
      if (typeof after === "string") {
        lines.push({ type: "context", content: after });
      }
      continue;
    }
    if (typeof before === "string") {
      lines.push({ type: "del", content: before });
    }
    if (typeof after === "string") {
      lines.push({ type: "add", content: after });
    }
  }

  return lines;
}

export default function DocumentWorkspace({
  path,
  content,
  previousContent = "",
  imageSrc,
  reviewerName = "Reviewer",
  readOnly = false,
  onContentChange,
  onApprove,
  onRequestChanges,
  onCommentAdd,
}: DocumentWorkspaceProps) {
  const resolution = useMemo(() => resolveEditorForPath(path), [path]);
  const [draft, setDraft] = useState(content);
  const [imageFailed, setImageFailed] = useState(false);

  useEffect(() => {
    setDraft(content);
    setImageFailed(false);
  }, [path, content]);

  const prefersDark = useMemo(() => {
    if (typeof window === "undefined") return false;
    const query = window.matchMedia?.("(prefers-color-scheme: dark)");
    return query?.matches ?? false;
  }, []);

  const codeDiffLines = useMemo(() => {
    if (resolution.editorMode !== "code") {
      return [];
    }
    if (previousContent === draft) {
      return [];
    }
    return buildSimpleLineDiff(previousContent, draft);
  }, [resolution.editorMode, previousContent, draft]);

  if (resolution.editorMode === "markdown") {
    return (
      <div data-testid="editor-mode-markdown">
        <ContentReview
          key={path}
          initialMarkdown={draft}
          reviewerName={reviewerName}
          readOnly={readOnly}
          onApprove={onApprove}
          onRequestChanges={onRequestChanges}
          onCommentAdd={onCommentAdd}
        />
      </div>
    );
  }

  if (resolution.editorMode === "text") {
    return (
      <section
        className="min-w-0 space-y-3 rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-900/40"
        data-testid="editor-mode-text"
      >
        <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Text Editor</p>
        <textarea
          value={draft}
          onChange={(event) => {
            if (readOnly) {
              return;
            }
            setDraft(event.target.value);
            onContentChange?.(event.target.value);
          }}
          readOnly={readOnly}
          className="min-h-[260px] w-full resize-y rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
          data-testid="text-editor"
        />
      </section>
    );
  }

  if (resolution.editorMode === "code") {
    return (
      <section
        className="min-w-0 space-y-4 rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-900/40"
        data-testid="editor-mode-code"
      >
        <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Code Editor</p>
        <textarea
          value={draft}
          onChange={(event) => {
            if (readOnly) {
              return;
            }
            setDraft(event.target.value);
            onContentChange?.(event.target.value);
          }}
          readOnly={readOnly}
          className="min-h-[180px] w-full resize-y rounded-xl border border-slate-200 bg-slate-950 px-4 py-3 font-mono text-sm text-slate-100 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-slate-700"
          data-testid="code-editor-input"
          spellCheck={false}
        />
        <div className="overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-800" data-testid="code-syntax-preview">
          <SyntaxHighlighter
            language={detectLanguage(path)}
            style={prefersDark ? oneDark : oneLight}
            customStyle={{ margin: 0, borderRadius: 0, minHeight: "120px" }}
          >
            {draft}
          </SyntaxHighlighter>
        </div>
        {codeDiffLines.length > 0 && (
          <div className="overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-800" data-testid="code-diff-view">
            <div className="bg-slate-100 px-3 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-slate-500 dark:bg-slate-900 dark:text-slate-300">
              Diff vs previous
            </div>
            <div className="font-mono text-xs">
              {codeDiffLines.map((line, index) => (
                <div
                  key={`${line.type}-${index}-${line.content}`}
                  className={`px-3 py-1 ${
                    line.type === "add"
                      ? "bg-emerald-50 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-200"
                      : line.type === "del"
                        ? "bg-rose-50 text-rose-800 dark:bg-rose-900/30 dark:text-rose-200"
                        : "bg-white text-slate-600 dark:bg-slate-950 dark:text-slate-300"
                  }`}
                >
                  {line.type === "add" ? "+" : line.type === "del" ? "-" : " "}
                  {line.content}
                </div>
              ))}
            </div>
          </div>
        )}
      </section>
    );
  }

  return (
    <section
      className="min-w-0 space-y-3 rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-900/40"
      data-testid="editor-mode-image"
    >
      <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Image Preview</p>
      {imageSrc && !imageFailed ? (
        <img
          src={imageSrc}
          alt={path}
          className="max-h-[440px] w-full rounded-xl border border-slate-200 object-contain dark:border-slate-800"
          onError={() => setImageFailed(true)}
          data-testid="image-preview"
        />
      ) : (
        <div
          className="rounded-xl border border-dashed border-slate-300 bg-slate-50 px-4 py-10 text-center text-sm text-slate-500 dark:border-slate-700 dark:bg-slate-900/30 dark:text-slate-300"
          data-testid="image-fallback"
        >
          Image preview unavailable.
        </div>
      )}
    </section>
  );
}

export { buildSimpleLineDiff };
