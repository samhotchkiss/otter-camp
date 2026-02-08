import { memo } from "react";
import type { AgentActivityEvent } from "../../hooks/useAgentActivity";
import AgentActivityItem from "./AgentActivityItem";

export type AgentActivityTimelineProps = {
  events: AgentActivityEvent[];
  isLoading?: boolean;
  isLoadingMore?: boolean;
  error?: string | null;
  hasMore?: boolean;
  onLoadMore?: () => void;
  emptyMessage?: string;
  className?: string;
};

function AgentActivityTimelineComponent({
  events,
  isLoading = false,
  isLoadingMore = false,
  error = null,
  hasMore = false,
  onLoadMore,
  emptyMessage = "No activity yet.",
  className = "",
}: AgentActivityTimelineProps) {
  if (isLoading && events.length === 0) {
    return (
      <div className={`rounded-xl border border-slate-200 bg-white p-6 text-sm text-slate-500 ${className}`}>
        Loading activity...
      </div>
    );
  }

  if (error && events.length === 0) {
    return (
      <div className={`rounded-xl border border-rose-200 bg-rose-50 p-6 text-sm text-rose-700 ${className}`}>
        {error}
      </div>
    );
  }

  if (events.length === 0) {
    return (
      <div className={`rounded-xl border border-slate-200 bg-white p-6 text-sm text-slate-500 ${className}`}>
        {emptyMessage}
      </div>
    );
  }

  return (
    <section className={`space-y-3 ${className}`} data-testid="agent-activity-timeline">
      {events.map((event) => (
        <AgentActivityItem key={event.id} event={event} />
      ))}

      {hasMore ? (
        <button
          type="button"
          onClick={onLoadMore}
          disabled={isLoadingMore}
          className="w-full rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {isLoadingMore ? "Loading more..." : "Load more"}
        </button>
      ) : null}
    </section>
  );
}

const AgentActivityTimeline = memo(AgentActivityTimelineComponent);

export default AgentActivityTimeline;
