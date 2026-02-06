import { useMemo, useState } from "react";
import MarkdownPreview from "./MarkdownPreview";
import { parseMarkdownSections } from "./markdownUtils";
import {
  canTransitionReviewState,
  reviewStateLabel,
  transitionReviewState,
  type ReviewWorkflowState,
} from "./reviewStateMachine";

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

type ViewMode = "split" | "edit" | "preview";

const VIEW_MODES: { id: ViewMode; label: string; description: string }[] = [
  { id: "split", label: "Split", description: "Side-by-side edit and preview." },
  { id: "edit", label: "Edit", description: "Focus on writing." },
  { id: "preview", label: "Preview", description: "Focus on review." },
];

function formatTimestamp(date: string) {
  return new Date(date).toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

export default function ContentReview({
  initialMarkdown,
  reviewerName = "Reviewer",
  onApprove,
  onRequestChanges,
  onCommentAdd,
}: ContentReviewProps) {
  const [markdown, setMarkdown] = useState(initialMarkdown);
  const [viewMode, setViewMode] = useState<ViewMode>("split");
  const [reviewState, setReviewState] = useState<ReviewWorkflowState>("draft");
  const [activeSectionId, setActiveSectionId] = useState<string | null>(null);
  const [commentDraft, setCommentDraft] = useState("");
  const [comments, setComments] = useState<ReviewComment[]>([]);

  const sections = useMemo(() => parseMarkdownSections(markdown), [markdown]);
  const activeSection = useMemo(
    () => sections.find((section) => section.id === activeSectionId) ?? sections[0],
    [activeSectionId, sections]
  );

  const commentCounts = useMemo(() => {
    return comments.reduce<Record<string, number>>((acc, comment) => {
      acc[comment.sectionId] = (acc[comment.sectionId] ?? 0) + 1;
      return acc;
    }, {});
  }, [comments]);

  const filteredComments = useMemo(() => {
    if (!activeSection) return comments;
    return comments.filter((comment) => comment.sectionId === activeSection.id);
  }, [comments, activeSection]);

  const totalComments = comments.length;

  const handleSectionSelect = (sectionId: string) => {
    setActiveSectionId(sectionId);
  };

  const handleAddComment = () => {
    if (!activeSection) return;
    const trimmed = commentDraft.trim();
    if (!trimmed) return;

    const nextComment: ReviewComment = {
      id: `${activeSection.id}-${Date.now()}`,
      sectionId: activeSection.id,
      author: reviewerName,
      message: trimmed,
      createdAt: new Date().toISOString(),
    };

    setComments((prev) => [...prev, nextComment]);
    setCommentDraft("");
    onCommentAdd?.(nextComment);
  };

  const handleApprove = () => {
    if (!canTransitionReviewState(reviewState, "approved")) return;
    onApprove?.({ markdown, comments });
    setReviewState(transitionReviewState(reviewState, "approved"));
  };

  const handleRequestChanges = () => {
    if (!canTransitionReviewState(reviewState, "needs_changes")) return;
    onRequestChanges?.({ markdown, comments });
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
          <p className="text-xs font-semibold uppercase tracking-[0.35em] text-indigo-500">
            Content Review
          </p>
          <h2 className="text-2xl font-semibold text-slate-900 dark:text-white">
            Markdown Review Session
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-300" data-testid="review-state-label">
            State: {reviewStateLabel(reviewState)} ·{" "}
            {totalComments} comment{totalComments === 1 ? "" : "s"} across {sections.length} section
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
        {VIEW_MODES.map((mode) => (
          <button
            key={mode.id}
            type="button"
            onClick={() => setViewMode(mode.id)}
            className={`flex flex-1 items-center justify-center rounded-xl px-3 py-2 text-sm font-medium transition ${
              viewMode === mode.id
                ? "bg-indigo-600 text-white shadow"
                : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
            }`}
          >
            {mode.label}
          </button>
        ))}
      </div>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="space-y-4">
          {(viewMode === "edit" || viewMode === "split") && (
            <div className="rounded-2xl border border-slate-200 bg-white/70 p-4 dark:border-slate-800 dark:bg-slate-900/40">
              <label className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                Draft
              </label>
              <textarea
                value={markdown}
                onChange={(event) => setMarkdown(event.target.value)}
                className="mt-3 min-h-[240px] w-full resize-y rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
            </div>
          )}

          {(viewMode === "preview" || viewMode === "split") && (
            <MarkdownPreview
              markdown={markdown}
              activeSectionId={activeSection?.id}
              commentCounts={commentCounts}
              onSectionSelect={handleSectionSelect}
            />
          )}
        </div>

        <aside className="space-y-4">
          <div className="rounded-2xl border border-slate-200 bg-white/70 p-4 shadow-sm dark:border-slate-800 dark:bg-slate-900/40">
            <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Sections</p>
            <ul className="mt-4 space-y-2">
              {sections.map((section) => {
                const count = commentCounts[section.id] ?? 0;
                const isActive = section.id === activeSection?.id;
                return (
                  <li key={section.id}>
                    <button
                      type="button"
                      onClick={() => handleSectionSelect(section.id)}
                      className={`flex w-full items-center justify-between rounded-xl px-3 py-2 text-left text-sm transition ${
                        isActive
                          ? "bg-indigo-600 text-white"
                          : "text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800"
                      }`}
                    >
                      <span className="truncate">
                        {"".padStart((section.level - 1) * 2, " ")}
                        {section.title}
                      </span>
                      <span
                        className={`ml-2 rounded-full px-2 py-0.5 text-xs font-semibold ${
                          isActive
                            ? "bg-white/20 text-white"
                            : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-200"
                        }`}
                      >
                        {count}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>

          <div className="rounded-2xl border border-slate-200 bg-white/70 p-4 shadow-sm dark:border-slate-800 dark:bg-slate-900/40">
            <div className="flex items-center justify-between">
              <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">Annotations</p>
              <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-200">
                {activeSection?.title ?? "Overview"}
              </span>
            </div>

            <div className="mt-4 space-y-3">
              {filteredComments.length === 0 ? (
                <p className="text-sm text-slate-500 dark:text-slate-400">
                  No comments yet for this section.
                </p>
              ) : (
                filteredComments.map((comment) => (
                  <div
                    key={comment.id}
                    className="rounded-xl border border-slate-200 bg-white px-3 py-3 text-sm text-slate-700 shadow-sm dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200"
                  >
                    <div className="flex items-center justify-between text-xs text-slate-400">
                      <span className="font-semibold text-slate-600 dark:text-slate-200">
                        {comment.author}
                      </span>
                      <span>{formatTimestamp(comment.createdAt)}</span>
                    </div>
                    <p className="mt-2">{comment.message}</p>
                  </div>
                ))
              )}
            </div>

            <div className="mt-4">
              <label className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                Add comment
              </label>
              <textarea
                value={commentDraft}
                onChange={(event) => setCommentDraft(event.target.value)}
                placeholder={
                  activeSection
                    ? `Note for ${activeSection.title}`
                    : "Select a section to comment"
                }
                className="mt-2 min-h-[96px] w-full resize-y rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-200 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                disabled={!activeSection}
              />
              <button
                type="button"
                onClick={handleAddComment}
                className="mt-3 w-full rounded-xl bg-indigo-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60"
                disabled={!activeSection || commentDraft.trim().length === 0}
              >
                Add Annotation
              </button>
            </div>
          </div>
        </aside>
      </div>
    </section>
  );
}
