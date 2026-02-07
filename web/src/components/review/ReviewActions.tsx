import type { ReviewAction, ReviewSummary } from "./types";

type ReviewActionsProps = {
  summary?: ReviewSummary;
  onAction?: (action: ReviewAction) => void;
};

const ACTIONS: {
  id: ReviewAction;
  label: string;
  tone: string;
  description: string;
  descriptionTone: string;
}[] = [
  {
    id: "approve",
    label: "Approve",
    tone: "bg-emerald-600 hover:bg-emerald-700 text-white",
    description: "Approve the changes as-is",
    descriptionTone: "text-white/80",
  },
  {
    id: "request_changes",
    label: "Request Changes",
    tone: "bg-rose-600 hover:bg-rose-700 text-white",
    description: "Request updates before merging",
    descriptionTone: "text-white/80",
  },
  {
    id: "comment",
    label: "Comment",
    tone: "bg-otter-surface-alt hover:bg-otter-surface text-otter-text",
    description: "Leave general feedback",
    descriptionTone: "text-otter-muted",
  },
];

export default function ReviewActions({ summary, onAction }: ReviewActionsProps) {
  return (
    <section className="rounded-lg border border-otter-border bg-otter-surface px-5 py-4 shadow-sm">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h3 className="text-sm font-semibold text-otter-text">Review summary</h3>
          <p className="text-xs text-otter-muted">Capture your final decision on this review.</p>
        </div>
        {summary ? (
          <div className="flex items-center gap-3 text-xs text-otter-muted">
            <span className="rounded-full bg-emerald-50 px-2 py-1 text-emerald-700">
              {summary.approvals} approvals
            </span>
            <span className="rounded-full bg-rose-50 px-2 py-1 text-rose-700">
              {summary.changesRequested} changes
            </span>
            <span className="rounded-full bg-amber-50 px-2 py-1 text-amber-700">
              {summary.comments} comments
            </span>
          </div>
        ) : null}
      </div>
      <div className="mt-4 grid gap-3 md:grid-cols-3">
        {ACTIONS.map((action) => (
          <button
            key={action.id}
            type="button"
            onClick={() => onAction?.(action.id)}
            className={`flex flex-col items-start gap-1 rounded-md px-4 py-3 text-left text-sm font-semibold transition ${action.tone}`}
          >
            <span>{action.label}</span>
            <span className={`text-xs font-normal ${action.descriptionTone}`}>{action.description}</span>
          </button>
        ))}
      </div>
    </section>
  );
}
