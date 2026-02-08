import { memo, useMemo } from "react";
import ActivityTriggerBadge from "./ActivityTriggerBadge";

export type AgentLastActionData = {
  summary: string;
  trigger: string;
  channel?: string;
  status?: string;
  startedAt?: string | number | Date | null;
};

export type AgentLastActionProps = {
  activity?: AgentLastActionData;
  summary?: string;
  trigger?: string;
  channel?: string;
  status?: string;
  startedAt?: string | number | Date | null;
  fallbackText?: string;
  className?: string;
};

function formatRelative(value?: string | number | Date | null): string {
  if (value === null || value === undefined) {
    return "Unknown";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "Unknown";
  }

  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  const diffHour = Math.floor(diffMin / 60);
  const diffDay = Math.floor(diffHour / 24);

  if (diffMin < 1) {
    return "Just now";
  }
  if (diffMin < 60) {
    return `${diffMin}m ago`;
  }
  if (diffHour < 24) {
    return `${diffHour}h ago`;
  }
  if (diffDay < 7) {
    return `${diffDay}d ago`;
  }

  return date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function AgentLastActionComponent({
  activity,
  summary,
  trigger,
  channel,
  status,
  startedAt,
  fallbackText = "No recent activity",
  className = "",
}: AgentLastActionProps) {
  const resolvedSummary = activity?.summary || summary;
  const resolvedTrigger = activity?.trigger || trigger;
  const resolvedChannel = activity?.channel || channel;
  const resolvedStatus = activity?.status || status;
  const resolvedStartedAt = activity?.startedAt ?? startedAt;

  const relativeTime = useMemo(() => formatRelative(resolvedStartedAt), [resolvedStartedAt]);

  if (!resolvedSummary || !resolvedTrigger) {
    return (
      <div className={`rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 ${className}`}>
        <p className="text-xs text-[var(--text-muted)]">{fallbackText}</p>
      </div>
    );
  }

  return (
    <div className={`rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 ${className}`}>
      <div className="flex items-center justify-between gap-2">
        <ActivityTriggerBadge
          trigger={resolvedTrigger}
          channel={resolvedChannel}
          status={resolvedStatus}
          className="border-[var(--border)] bg-[var(--surface)] text-[var(--text-muted)]"
        />
        <span className="text-xs font-medium text-[var(--text-muted)]">{relativeTime}</span>
      </div>
      <p className="mt-2 line-clamp-2 text-sm text-[var(--text)]">{resolvedSummary}</p>
    </div>
  );
}

const AgentLastAction = memo(AgentLastActionComponent);

export default AgentLastAction;
