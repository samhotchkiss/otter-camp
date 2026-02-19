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
import { insertMarkdownImageLinkAtSelection } from "./markdownAsset";

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
  readOnly?: boolean;
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
  readOnly = false,
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
  const [imageComposerOpen, setImageComposerOpen] = useState(false);
  const [imagePathDraft, setImagePathDraft] = useState("");
  const [imageAltDraft, setImageAltDraft] = useState("");
  const [pendingSelection, setPendingSelection] = useState<TextSelection | null>(null);
  const [activeSectionId, setActiveSectionId] = useState<string>("document");

  const sourceRef = useRef<HTMLTextAreaElement | null>(null);

  const sections = useMemo(() => parseMarkdownSections(markdown), [markdown]);
  const inlineComments = useMemo(() => parseCriticMarkupComments(markdown), [markdown]);
  const reviewComments = useMemo(() => buildReviewComments(markdown, reviewerName), [markdown, reviewerName]);
  const markdownLines = useMemo(() => markdown.split("\n"), [markdown]);
  const resolvedCommentCount = useMemo(
    () => reviewComments.filter((comment) => comment.resolved).length,
    [reviewComments],
  );
  const unresolvedCommentCount = reviewComments.length - resolvedCommentCount;
  const commentLineLocations = useMemo(
    () =>
      inlineComments.map((comment) => ({
        comment,
        line: markdown.slice(0, comment.start).split("\n").length,
      })),
    [inlineComments, markdown],
  );
  const sectionCommentCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    sections.forEach((section) => {
      counts[section.id] = 0;
    });
    sections.forEach((section, index) => {
      const nextStartLine = sections[index + 1]?.startLine ?? Number.MAX_SAFE_INTEGER;
      const sectionCount = commentLineLocations.filter(
        ({ line }) => line >= section.startLine && line < nextStartLine,
      ).length;
      counts[section.id] = sectionCount;
    });
    return counts;
  }, [sections, commentLineLocations]);

  useEffect(() => {
    setMarkdown(initialMarkdown);
  }, [initialMarkdown]);

  useEffect(() => {
    const fallbackSectionID = sections[0]?.id ?? "document";
    setActiveSectionId((current) =>
      sections.some((section) => section.id === current) ? current : fallbackSectionID,
    );
  }, [sections]);

  useEffect(() => {
    if (documentView !== "source" || composerOpen || imageComposerOpen) {
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
  }, [documentView, sourceSelection, markdown.length, composerOpen, imageComposerOpen]);

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
    setImageComposerOpen(false);
  };

  const openImageComposerFromSelection = () => {
    const selection = captureSourceSelection();
    setPendingSelection(selection);
    setImageComposerOpen(true);
    setComposerOpen(false);
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
    if (readOnly) {
      return;
    }
    if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === "m") {
      event.preventDefault();
      openComposerFromSelection();
    }
  };

  const handleInsertImageLink = () => {
    if (!pendingSelection) {
      return;
    }
    const assetPath = imagePathDraft.trim();
    if (assetPath === "") {
      return;
    }

    const insertion = insertMarkdownImageLinkAtSelection({
      markdown,
      start: pendingSelection.start,
      end: pendingSelection.end,
      assetPath,
      altText: imageAltDraft,
    });

    setMarkdown(insertion.markdown);
    const collapsed = { start: insertion.cursor, end: insertion.cursor };
    setSourceSelection(collapsed);
    setPendingSelection(collapsed);
    setImagePathDraft("");
    setImageAltDraft("");
    setImageComposerOpen(false);
    setDocumentView("source");
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

  const handleSectionSelect = (sectionId: string) => {
    setActiveSectionId(sectionId);
    setDocumentView("rendered");
  };

  return (
    <section
      className="min-w-0 space-y-6 rounded-3xl border border-slate-200 bg-white/70 p-6 shadow-lg backdrop-blur dark:border-slate-800 dark:bg-slate-900/40"
      data-testid="content-review-shell"
    >
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
          {!readOnly && (reviewState === "draft" || reviewState === "needs_changes") && (
            <button
              type="button"
              onClick={handleMarkReadyForReview}
              className="rounded-full border border-indigo-200 bg-indigo-600 px-4 py-2 text-sm font-semibold text-white transition hover:bg-indigo-700 dark:border-indigo-400"
            >
              Mark Ready for Review
            </button>
          )}

          {!readOnly && reviewState === "ready_for_review" && (
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

          {!readOnly && reviewState === "approved" && (
            <span className="rounded-full border border-emerald-300 bg-emerald-50 px-4 py-2 text-sm font-semibold text-emerald-700 dark:border-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200">
              Approved
            </span>
          )}
          {readOnly && (
            <span
              className="rounded-full border border-slate-300 bg-slate-100 px-4 py-2 text-sm font-semibold text-slate-700 dark:border-slate-700 dark:bg-slate-900/50 dark:text-slate-200"
              data-testid="content-review-read-only"
            >
              Read-only snapshot
            </span>
          )}
        </div>
      </header>

      <div
        className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4"
        data-testid="review-stats-grid"
      >
        <div className="rounded-2xl border border-slate-200 bg-white/80 p-3 dark:border-slate-800 dark:bg-slate-900/40">
          <p className="text-[11px] font-semibold uppercase tracking-[0.2em] text-slate-500 dark:text-slate-300">Comments</p>
          <p className="mt-1 text-xl font-semibold text-slate-900 dark:text-white">{reviewComments.length}</p>
        </div>
        <div className="rounded-2xl border border-amber-200 bg-amber-50/60 p-3 dark:border-amber-700 dark:bg-amber-900/30">
          <p className="text-[11px] font-semibold uppercase tracking-[0.2em] text-amber-700 dark:text-amber-200">Unresolved</p>
          <p className="mt-1 text-xl font-semibold text-amber-800 dark:text-amber-100">{unresolvedCommentCount}</p>
        </div>
        <div className="rounded-2xl border border-emerald-200 bg-emerald-50/60 p-3 dark:border-emerald-700 dark:bg-emerald-900/30">
          <p className="text-[11px] font-semibold uppercase tracking-[0.2em] text-emerald-700 dark:text-emerald-200">Resolved</p>
          <p className="mt-1 text-xl font-semibold text-emerald-800 dark:text-emerald-100">{resolvedCommentCount}</p>
        </div>
        <div className="rounded-2xl border border-slate-200 bg-white/80 p-3 dark:border-slate-800 dark:bg-slate-900/40">
          <p className="text-[11px] font-semibold uppercase tracking-[0.2em] text-slate-500 dark:text-slate-300">Lines</p>
          <p className="mt-1 text-xl font-semibold text-slate-900 dark:text-white">{markdownLines.length}</p>
        </div>
      </div>

      <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-4" data-testid="review-line-lane">
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
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Line-by-line Review</p>
                {!readOnly && (
                  <>
                    <button
                      type="button"
                      onClick={openComposerFromSelection}
                      className="rounded-full border border-indigo-200 bg-indigo-50 px-3 py-1.5 text-xs font-semibold text-indigo-700 transition hover:border-indigo-300 hover:bg-indigo-100 dark:border-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-200"
                    >
                      Add Inline Comment
                    </button>
                    <button
                      type="button"
                      onClick={openImageComposerFromSelection}
                      className="rounded-full border border-slate-300 bg-white px-3 py-1.5 text-xs font-semibold text-slate-700 transition hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-900/40 dark:text-slate-200 dark:hover:bg-slate-800"
                    >
                      Add Image Link
                    </button>
                  </>
                )}
              </div>
              <div className="grid min-w-0 grid-cols-[44px_minmax(0,1fr)] gap-3 overflow-x-auto">
                <div className="rounded-xl border border-slate-200 bg-slate-50/70 py-2 text-right text-[11px] text-slate-500 dark:border-slate-700 dark:bg-slate-900/40 dark:text-slate-300">
                  {markdownLines.map((_, index) => (
                    <p key={`line-${index + 1}`} className="px-2 py-[0.18rem]">
                      {index + 1}
                    </p>
                  ))}
                </div>
                <textarea
                  ref={sourceRef}
                  value={markdown}
                  onChange={(event) => {
                    if (readOnly) {
                      return;
                    }
                    setMarkdown(event.target.value);
                  }}
                  onSelect={captureSourceSelection}
                  onKeyUp={captureSourceSelection}
                  onClick={captureSourceSelection}
                  onBlur={captureSourceSelection}
                  onKeyDown={handleSourceKeyDown}
                  readOnly={readOnly}
                  className="min-h-[280px] w-full resize-y rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                  data-testid="source-textarea"
                  spellCheck={false}
                />
              </div>

              {!readOnly && composerOpen && (
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

              {!readOnly && imageComposerOpen && (
                <div className="rounded-xl border border-emerald-200 bg-emerald-50/70 p-3 dark:border-emerald-700 dark:bg-emerald-900/20">
                  <label className="text-xs font-semibold uppercase tracking-[0.3em] text-emerald-700 dark:text-emerald-200">
                    Insert Markdown Image
                  </label>
                  <input
                    value={imagePathDraft}
                    onChange={(event) => setImagePathDraft(event.target.value)}
                    className="mt-2 w-full rounded-xl border border-emerald-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-emerald-700 dark:bg-slate-950 dark:text-slate-100"
                    placeholder="/assets/cover.png"
                    data-testid="image-path-input"
                  />
                  <input
                    value={imageAltDraft}
                    onChange={(event) => setImageAltDraft(event.target.value)}
                    className="mt-2 w-full rounded-xl border border-emerald-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-emerald-700 dark:bg-slate-950 dark:text-slate-100"
                    placeholder="Alt text (optional)"
                    data-testid="image-alt-input"
                  />
                  <div className="mt-3 flex flex-wrap gap-2">
                    <button
                      type="button"
                      onClick={handleInsertImageLink}
                      className="rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-60"
                      disabled={imagePathDraft.trim().length === 0}
                    >
                      Insert Image Link
                    </button>
                    <button
                      type="button"
                      onClick={() => {
                        setImageComposerOpen(false);
                        setImagePathDraft("");
                        setImageAltDraft("");
                      }}
                      className="rounded-lg border border-slate-300 px-3 py-1.5 text-xs font-semibold text-slate-700 transition hover:bg-slate-100 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              )}

              {!readOnly && (
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  Shortcut: Cmd/Ctrl+Shift+M inserts a CriticMarkup comment at the current caret/selection.
                </p>
              )}
            </div>
          ) : (
            <div className="rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-900/40">
              <MarkdownPreview
                markdown={markdown}
                activeSectionId={activeSectionId}
                commentCounts={sectionCommentCounts}
                onSectionSelect={handleSectionSelect}
              />
            </div>
          )}
        </div>

        <aside
          className="min-w-0 space-y-3 rounded-2xl border border-slate-200 bg-white/80 p-4 dark:border-slate-800 dark:bg-slate-900/40"
          data-testid="review-comment-sidebar"
        >
          <h3 className="text-sm font-semibold text-slate-900 dark:text-white">Comment Sidebar</h3>
          <p className="text-xs text-slate-500 dark:text-slate-300">
            {reviewComments.length} total · {unresolvedCommentCount} unresolved
          </p>
          <p className="text-[11px] uppercase tracking-wide text-slate-500 dark:text-slate-400" data-testid="active-review-section">
            {activeSectionId}
          </p>
          <ul className="space-y-2">
            {sections.map((section) => {
              const count = sectionCommentCounts[section.id] ?? 0;
              return (
                <li key={`section-${section.id}`}>
                  <button
                    type="button"
                    onClick={() => handleSectionSelect(section.id)}
                    className={`w-full rounded-xl border px-3 py-2 text-left text-xs transition ${
                      activeSectionId === section.id
                        ? "border-indigo-300 bg-indigo-50 text-indigo-900 dark:border-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-200"
                        : "border-slate-200 bg-white text-slate-700 hover:border-indigo-200 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200"
                    }`}
                    aria-label={`Open section ${section.title}`}
                  >
                    <p className="font-semibold">{section.title}</p>
                    <p className="mt-1 text-[11px] text-slate-500 dark:text-slate-400">
                      Line {section.startLine}
                      {count > 0 ? ` · ${count} comment${count === 1 ? "" : "s"}` : ""}
                    </p>
                  </button>
                </li>
              );
            })}
          </ul>
          {reviewComments.length === 0 ? (
            <p className="rounded-xl border border-dashed border-slate-300 bg-slate-50 px-3 py-4 text-xs text-slate-500 dark:border-slate-700 dark:bg-slate-900/30 dark:text-slate-300">
              No inline comments yet.
            </p>
          ) : (
            <ul className="space-y-2">
              {reviewComments.map((comment) => (
                <li
                  key={comment.id}
                  className="rounded-xl border border-slate-200 bg-white px-3 py-2 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200"
                >
                  <p className="font-semibold text-slate-900 dark:text-white">{comment.author}</p>
                  <p className="mt-1 text-[11px] uppercase tracking-wide text-slate-500 dark:text-slate-400">
                    Comment {comment.id}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </aside>
      </div>
    </section>
  );
}
