import { useMemo, useState } from "react";
import ReviewActions from "./ReviewActions";
import ReviewDiffSplit from "./ReviewDiffSplit";
import ReviewDiffUnified from "./ReviewDiffUnified";
import ReviewFileTree from "./ReviewFileTree";
import type { DiffFile, ReviewAction, ReviewSummary } from "./types";

type ReviewView = "split" | "unified";

type CodeReviewProps = {
  files: DiffFile[];
  selectedFileId?: string;
  onSelectFile?: (fileId: string) => void;
  view?: ReviewView;
  onChangeView?: (view: ReviewView) => void;
  summary?: ReviewSummary;
  onAction?: (action: ReviewAction) => void;
};

export default function CodeReview({
  files,
  selectedFileId,
  onSelectFile,
  view,
  onChangeView,
  summary,
  onAction,
}: CodeReviewProps) {
  const [internalSelected, setInternalSelected] = useState<string | undefined>(
    selectedFileId ?? files[0]?.id
  );
  const [internalView, setInternalView] = useState<ReviewView>(view ?? "split");

  const resolvedSelected = selectedFileId ?? internalSelected ?? files[0]?.id;
  const resolvedView = view ?? internalView;

  const activeFile = useMemo(
    () => files.find((file) => file.id === resolvedSelected) ?? files[0],
    [files, resolvedSelected]
  );

  const handleSelectFile = (fileId: string) => {
    setInternalSelected(fileId);
    onSelectFile?.(fileId);
  };

  const handleViewChange = (nextView: ReviewView) => {
    setInternalView(nextView);
    onChangeView?.(nextView);
  };

  return (
    <div className="grid gap-6 lg:grid-cols-[280px_minmax(0,1fr)]">
      <div className="space-y-6">
        <ReviewFileTree files={files} selectedFileId={resolvedSelected} onSelectFile={handleSelectFile} />
        <ReviewActions summary={summary} onAction={onAction} />
      </div>
      <div className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold text-otter-text">Code Review</h2>
            <p className="text-sm text-otter-muted">Focused review for the selected file.</p>
          </div>
          <div className="flex items-center rounded-md border border-otter-border bg-otter-surface p-1 text-xs font-semibold">
            {(["split", "unified"] as ReviewView[]).map((option) => (
              <button
                key={option}
                type="button"
                onClick={() => handleViewChange(option)}
                className={`rounded-md px-3 py-1 transition ${
                  resolvedView === option
                    ? "bg-otter-accent text-otter-surface"
                    : "text-otter-muted hover:text-otter-text"
                }`}
              >
                {option === "split" ? "Split" : "Unified"}
              </button>
            ))}
          </div>
        </div>
        {activeFile ? (
          resolvedView === "split" ? (
            <ReviewDiffSplit file={activeFile} />
          ) : (
            <ReviewDiffUnified file={activeFile} />
          )
        ) : (
          <div className="rounded-lg border border-dashed border-otter-border bg-otter-surface px-6 py-12 text-center text-sm text-otter-muted">
            No file selected yet.
          </div>
        )}
      </div>
    </div>
  );
}
