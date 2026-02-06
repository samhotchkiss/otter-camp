import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import MarkdownPreview from "./MarkdownPreview";
import { parseMarkdownSections } from "./markdownUtils";
import {
  canTransitionReviewState,
  reviewStateLabel,
  transitionReviewState,
  type ReviewWorkflowState,
} from "./reviewStateMachine";
import { insertCriticMarkupCommentAtSelection, parseCriticMarkupComments } from "./criticMarkup";

export type ReviewComment = {
  id: string;
  sectionId: string;
  author: string;
  message: string;
  createdAt: string;
  resolved?: boolean;
};

export type ContentReviewActionPayload = {
  markdown: string;
  comments: ReviewComment[];
};

export type ContentReviewProps = {
  initialMarkdown: string;
  reviewerName?: string;
  onApprove?: (payload: ContentReviewActionPayload) => void;
  onRequestChanges?: (payload: ContentReviewActionPayload) => void;
  onCommentAdd?: (comment: ReviewComment) => void;
};

type DocumentViewMode = "source" | "rendered";

type TextSelection = {
  start: number;
  end: number;
};

const DOCUMENT_VIEWS: { id: DocumentViewMode; label: string; description: string }[] = [
  { id: "source", label: "Source", description: "Raw markdown and markers" },
  { id: "rendered", label: "Rendered", description: "Readable markdown with inline comments" },
];

function clampSelection(selection: TextSelection, contentLength: number): TextSelection {
  const start = Math.max(0, Math.min(selection.start, contentLength));
  const end = Math.max(0, Math.min(selection.end, contentLength));
  if (start <= end) {
    return { start, end };
  }
  return { start: end, end: start };
}

function buildReviewComments(markdown: string, reviewerName: string): ReviewComment[] {
  const parsed = parseCriticMarkupComments(markdown);
  return parsed.map((comment, index) => ({
    id: comment.id,
    sectionId: "document",
    author: comment.author ?? reviewerName,
    message: comment.message,
    createdAt: new Date(0 + index).toISOString(),
  }));
}

export default function ContentReview({
  initialMarkdown,
  reviewerName = "Reviewer",
  onApprove,
  onRequestChanges,
  onCommentAdd,
}: ContentReviewProps) {
  const [markdown, setMarkdown] = useState(initialMarkdown);
  const [documentView, setDocumentView] = useState<DocumentViewMode>("source");
  const [reviewState, setReviewState] = useState<ReviewWorkflowState>("draft");
  const [sourceSelection, setSourceSelection] = useState<TextSelection>({ start: 0, end: 0 });
  const [commentDraft, setCommentDraft] = useState("");
  const [composerOpen, setComposerOpen] = useState(false);
  const [pendingSelection, setPendingSelection] = useState<TextSelection | null>(null);

  const sourceRef = useRef<HTMLTextAreaElement | null>(null);

  const sections = useMemo(() => parseMarkdownSections(markdown), [markdown]);
  const inlineComments = useMemo(() => parseCriticMarkupComments(markdown), [markdown]);
  const reviewComments = useMemo(() => buildReviewComments(markdown, reviewerName), [markdown, reviewerName]);

  useEffect(() => {
    setMarkdown(initialMarkdown);
  }, [initialMarkdown]);

  useEffect(() => {
    if (documentView !== "source" || composerOpen) {
      return;
    }
    const source = sourceRef.current;
    if (!source) {
      return;
    }

    const nextSelection = clampSelection(sourceSelection, markdown.length);
    const rafID = window.requestAnimationFrame(() => {
      const currentSource = sourceRef.current;
      if (!currentSource) {
        return;
      }
      currentSource.focus();
      currentSource.setSelectionRange(nextSelection.start, nextSelection.end);
    });

    return () => window.cancelAnimationFrame(rafID);
  }, [documentView, sourceSelection, markdown.length, composerOpen]);

  const captureSourceSelection = () => {
    const source = sourceRef.current;
    if (!source) {
      return sourceSelection;
    }
    const nextSelection = clampSelection(
      {
        start: source.selectionStart,
        end: source.selectionEnd,
      },
      markdown.length
    );
    setSourceSelection(nextSelection);
    return nextSelection;
  };

  const switchView = (nextView: DocumentViewMode) => {
    if (nextView === documentView) {
      return;
    }
    if (documentView === "source") {
      captureSourceSelection();
    }
    setDocumentView(nextView);
  };

  const openComposerFromSelection = () => {
    const selection = captureSourceSelection();
    setPendingSelection(selection);
    setComposerOpen(true);
  };

  const handleInsertInlineComment = () => {
    if (!pendingSelection) {
      return;
    }

    const message = commentDraft.trim();
    if (message === "") {
      return;
    }

    const insertion = insertCriticMarkupCommentAtSelection({
      markdown,
      start: pendingSelection.start,
      end: pendingSelection.end,
      author: reviewerName,
      message,
    });

    const timestamp = new Date().toISOString();
    onCommentAdd?.({
      id: `inline-${timestamp}`,
      sectionId: "document",
      author: reviewerName,
      message,
      createdAt: timestamp,
    });

    setMarkdown(insertion.markdown);
    const collapsed = { start: insertion.cursor, end: insertion.cursor };
    setSourceSelection(collapsed);
    setPendingSelection(collapsed);
    setCommentDraft("");
    setComposerOpen(false);
    setDocumentView("source");
  };

  const handleSourceKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === "m") {
      event.preventDefault();
      openComposerFromSelection();
    }
  };

  const handleApprove = () => {
    if (!canTransitionReviewState(reviewState, "approved")) return;
    onApprove?.({ markdown, comments: reviewComments });
    setReviewState(transitionReviewState(reviewState, "approved"));
  };

  const handleRequestChanges = () => {
    if (!canTransitionReviewState(reviewState, "needs_changes")) return;
    onRequestChanges?.({ markdown, comments: reviewComments });
    setReviewState(transitionReviewState(reviewState, "needs_changes"));
  };

  const handleMarkReadyForReview = () => {
    if (!canTransitionReviewState(reviewState, "ready_for_review")) return;
    setReviewState(transitionReviewState(reviewState, "ready_for_review"));
  };

  return (
    <section className="space-y-6 rounded-3xl border border-slate-200 bg-white/70 p-6 shadow-lg backdrop-blur dark:border-slate-800 dark:bg-slate-900/40">
      <header className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.35em] text-indigo-500">Content Review</p>
          <h2 className="text-2xl font-semibold text-slate-900 dark:text-white">Markdown Review Session</h2>
          <p className="text-sm text-slate-600 dark:text-slate-300" data-testid="review-state-label">
            State: {reviewStateLabel(reviewState)} · {inlineComments.length} inline comment
            {inlineComments.length === 1 ? "" : "s"} across {sections.length} section
            {sections.length === 1 ? "" : "s"}.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {(reviewState === "draft" || reviewState === "needs_changes") && (
            <button
              type="button"
              onClick={handleMarkReadyForReview}
              className="rounded-full border border-indigo-200 bg-indigo-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-indigo-700 dark:border-indigo-400"
            >
              Mark Ready for Review
            </button>
          )}

          {reviewState === "ready_for_review" && (
            <>
              <button
                type="button"
                onClick={handleRequestChanges}
                className="rounded-full border border-amber-200 bg-amber-50 px-4 py-2 text-sm font-semibold text-amber-700 transition hover:border-amber-300 hover:bg-amber-100 dark:border-amber-700 dark:bg-amber-900/40 dark:text-amber-200"
              >
                Request Changes
              </button>
              <button
                type="button"
                onClick={handleApprove}
                className="rounded-full border border-emerald-200 bg-emerald-500 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-emerald-600 dark:border-emerald-400"
              >
                Approve Content
              </button>
            </>
          )}

          {reviewState === "approved" && (
            <span className="rounded-full border border-emerald-300 bg-emerald-50 px-4 py-2 text-sm font-semibold text-emerald-700 dark:border-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200">
              Approved
            </span>
          )}
        </div>
      </header>

      <div className="flex flex-wrap gap-2 rounded-2xl border border-slate-200 bg-white/70 p-2 dark:border-slate-800 dark:bg-slate-900/40">
        {DOCUMENT_VIEWS.map((mode) => (
          <button
            key={mode.id}
            type="button"
            onClick={() => switchView(mode.id)}
            className={`flex flex-1 items-center justify-center rounded-xl px-3 py-2 text-sm font-medium transition ${
              documentView === mode.id
                ? "bg-indigo-600 text-white shadow"
                : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
            }`}
            title={mode.description}
          >
            {mode.label}
          </button>
        ))}
      </div>

      {documentView === "source" ? (
        <div className="space-y-4 rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-900/40">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Source Markdown</p>
            <button
              type="button"
              onClick={openComposerFromSelection}
              className="rounded-full border border-indigo-200 bg-indigo-50 px-3 py-1.5 text-xs font-semibold text-indigo-700 transition hover:border-indigo-300 hover:bg-indigo-100 dark:border-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-200"
            >
              Add Inline Comment
            </button>
          </div>
          <textarea
            ref={sourceRef}
            value={markdown}
            onChange={(event) => setMarkdown(event.target.value)}
            onSelect={captureSourceSelection}
            onKeyUp={captureSourceSelection}
            onClick={captureSourceSelection}
            onBlur={captureSourceSelection}
            onKeyDown={handleSourceKeyDown}
            className="min-h-[280px] w-full resize-y rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            data-testid="source-textarea"
            spellCheck={false}
          />

          {composerOpen && (
            <div className="rounded-xl border border-amber-200 bg-amber-50/70 p-3 dark:border-amber-700 dark:bg-amber-900/20">
              <label className="text-xs font-semibold uppercase tracking-[0.3em] text-amber-700 dark:text-amber-200">
                Inline Comment
              </label>
              <textarea
                value={commentDraft}
                onChange={(event) => setCommentDraft(event.target.value)}
                className="mt-2 min-h-[90px] w-full resize-y rounded-xl border border-amber-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-amber-700 dark:bg-slate-950 dark:text-slate-100"
                placeholder="What should change here?"
                data-testid="inline-comment-input"
              />
              <div className="mt-3 flex flex-wrap gap-2">
                <button
                  type="button"
                  onClick={handleInsertInlineComment}
                  className="rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60"
                  disabled={commentDraft.trim().length === 0}
                >
                  Insert Inline Comment
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setComposerOpen(false);
                    setCommentDraft("");
                  }}
                  className="rounded-lg border border-slate-300 px-3 py-1.5 text-xs font-semibold text-slate-700 transition hover:bg-slate-100 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                >
                  Cancel
                </button>
              </div>
            </div>
          )}

          <p className="text-xs text-slate-500 dark:text-slate-400">
            Shortcut: Cmd/Ctrl+Shift+M inserts a CriticMarkup comment at the current caret/selection.
          </p>
        </div>
      ) : (
        <MarkdownPreview markdown={markdown} />
      )}
    </section>
  );
}
