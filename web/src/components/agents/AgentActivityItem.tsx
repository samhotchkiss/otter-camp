import { memo, useMemo, useState } from "react";
import { formatLastActive } from "../AgentCard";
import type { AgentActivityEvent } from "../../hooks/useAgentActivity";
import ActivityTriggerBadge from "./ActivityTriggerBadge";

function formatDuration(durationMs: number): string {
  if (!Number.isFinite(durationMs) || durationMs <= 0) {
    return "0s";
  }
  if (durationMs < 1000) {
    return `${Math.round(durationMs)}ms`;
  }
  if (durationMs < 60_000) {
    return `${Math.round(durationMs / 1000)}s`;
  }
  return `${Math.round(durationMs / 60_000)}m`;
}

export type AgentActivityItemProps = {
  event: AgentActivityEvent;
  defaultExpanded?: boolean;
};

function AgentActivityItemComponent({ event, defaultExpanded = false }: AgentActivityItemProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  const startedAtLabel = useMemo(
    () => formatLastActive(event.startedAt.toISOString()),
    [event.startedAt],
  );

  const hasExpandableDetail = Boolean(
    event.detail || event.projectId || event.issueId || event.issueNumber || event.threadId,
  );

  return (
    <article className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm" data-testid="agent-activity-item">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <ActivityTriggerBadge
          trigger={event.trigger}
          channel={event.channel}
          status={event.status}
        />
        <time className="text-xs font-medium text-slate-500" dateTime={event.startedAt.toISOString()}>
          {startedAtLabel}
        </time>
      </div>

      <p className="mt-3 text-sm font-semibold text-slate-900">{event.summary}</p>

      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
        <span>{event.modelUsed || "model n/a"}</span>
        <span aria-hidden="true">•</span>
        <span>{event.tokensUsed} tokens</span>
        <span aria-hidden="true">•</span>
        <span>{formatDuration(event.durationMs)}</span>
      </div>

      {hasExpandableDetail ? (
        <div className="mt-3">
          <button
            type="button"
            onClick={() => setExpanded((prev) => !prev)}
            className="text-xs font-semibold text-slate-600 underline-offset-2 hover:underline"
          >
            {expanded ? "Hide details" : "Show details"}
          </button>

          {expanded ? (
            <div className="mt-2 space-y-1 text-xs text-slate-600">
              {event.detail ? <p>{event.detail}</p> : null}
              {event.projectId ? <p>Project: {event.projectId}</p> : null}
              {event.issueId ? <p>Issue: {event.issueId}</p> : null}
              {event.issueNumber ? <p>Issue #: {event.issueNumber}</p> : null}
              {event.threadId ? <p>Thread: {event.threadId}</p> : null}
            </div>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

const AgentActivityItem = memo(AgentActivityItemComponent);

export default AgentActivityItem;
