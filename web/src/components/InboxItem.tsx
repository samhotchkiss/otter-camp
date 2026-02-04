import type { ReactNode } from "react";

export type InboxItemType = "approval" | "review" | "decision" | "done";

export type InboxAction = {
  id: string;
  label: string;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  onClick?: () => void;
};

export type InboxItemData = {
  id: string;
  icon: ReactNode;
  project: string;
  type: InboxItemType;
  timestamp: string;
  description: string;
  agentName: string;
  urgent?: boolean;
  actions: InboxAction[];
};

const TYPE_LABELS: Record<InboxItemType, string> = {
  approval: "Approval",
  review: "Review",
  decision: "Decision",
  done: "Done",
};

const TYPE_BADGE_STYLES: Record<InboxItemType, string> = {
  approval: "bg-emerald-500/15 text-emerald-200 border-emerald-500/30",
  review: "bg-sky-500/15 text-sky-200 border-sky-500/30",
  decision: "bg-amber-500/15 text-amber-200 border-amber-500/30",
  done: "bg-otter-dark-accent/20 text-otter-dark-accent border-otter-dark-accent/40",
};

const ACTION_STYLES: Record<NonNullable<InboxAction["variant"]>, string> = {
  primary:
    "bg-otter-dark-accent text-otter-dark-bg hover:bg-otter-dark-accent-hover focus:ring-otter-dark-accent",
  secondary:
    "bg-otter-dark-surface-alt text-otter-dark-text hover:bg-otter-dark-border focus:ring-otter-dark-accent",
  ghost:
    "border border-otter-dark-border text-otter-dark-text hover:border-otter-dark-accent hover:text-otter-dark-accent focus:ring-otter-dark-accent",
  danger:
    "bg-otter-red/80 text-white hover:bg-otter-red focus:ring-otter-red",
};

export default function InboxItem({ item }: { item: InboxItemData }) {
  return (
    <article
      className={`flex flex-col gap-4 rounded-2xl border bg-otter-dark-surface p-5 shadow-sm transition hover:-translate-y-0.5 hover:shadow-lg dark:bg-otter-dark-surface ${
        item.urgent
          ? "border-otter-orange shadow-[0_0_0_1px_rgba(200,121,65,0.3)]"
          : "border-otter-dark-border"
      }`}
    >
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex min-w-0 flex-1 items-start gap-4">
          <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl border border-otter-dark-border bg-otter-dark-surface-alt text-2xl">
            {item.icon}
          </div>

          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-3">
              <h3 className="truncate text-lg font-semibold text-otter-dark-text">
                {item.project}
              </h3>
              <span
                className={`inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold uppercase tracking-wide ${
                  TYPE_BADGE_STYLES[item.type]
                }`}
              >
                {TYPE_LABELS[item.type]}
              </span>
              {item.urgent && (
                <span className="text-xs font-semibold uppercase tracking-wide text-otter-orange">
                  Urgent
                </span>
              )}
            </div>

            <p className="mt-2 text-sm text-otter-dark-text-muted">
              {item.description}
            </p>

            <div className="mt-3 flex flex-wrap items-center gap-3 text-xs text-otter-dark-text-muted">
              <span>{item.timestamp}</span>
              <span aria-hidden="true">•</span>
              <span>{item.agentName}</span>
            </div>
          </div>
        </div>

        <div className="text-xs text-otter-dark-text-muted">{item.timestamp}</div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {item.actions.map((action) => {
          const variant = action.variant ?? "secondary";
          return (
            <button
              key={action.id}
              type="button"
              onClick={action.onClick}
              className={`rounded-xl px-4 py-2 text-sm font-semibold transition focus:outline-none focus:ring-2 focus:ring-offset-0 ${
                ACTION_STYLES[variant]
              }`}
            >
              {action.label}
            </button>
          );
        })}
      </div>
    </article>
  );
}
