import { memo, useCallback, useMemo } from "react";
import type { AgentStatus } from "./AgentDM";

/**
 * Extended agent info for card display.
 */
export type AgentCardData = {
  id: string;
  name: string;
  avatarUrl?: string;
  status: AgentStatus;
  role?: string;
  currentTask?: string;
  lastActive?: string;
};

export type AgentCardProps = {
  agent: AgentCardData;
  onClick?: (agent: AgentCardData) => void;
};

/**
 * Get initials from a name for avatar fallback.
 */
function getInitials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/**
 * Format relative time for "last active" display.
 */
function formatLastActive(isoString?: string): string {
  if (!isoString) {
    return "Never";
  }

  const date = new Date(isoString);
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

// Status styles - memoized outside component
const STATUS_STYLES: Record<AgentStatus, string> = {
  online: "bg-emerald-500 shadow-emerald-500/50 shadow-lg",
  busy: "bg-amber-500 shadow-amber-500/50 shadow-lg animate-pulse",
  offline: "bg-slate-500",
};

const STATUS_LABELS: Record<AgentStatus, string> = {
  online: "Online",
  busy: "Busy",
  offline: "Offline",
};

/**
 * Status indicator dot component - Memoized.
 */
const StatusIndicator = memo(function StatusIndicator({ status }: { status: AgentStatus }) {
  return (
    <span
      className={`h-3 w-3 rounded-full ${STATUS_STYLES[status]}`}
      title={STATUS_LABELS[status]}
    />
  );
});

/**
 * AgentCard - Displays agent info in a compact card format.
 * Memoized for performance in large lists.
 *
 * Features:
 * - Avatar with status indicator (lazy loaded images)
 * - Name and role
 * - Current task (if any)
 * - Last active timestamp
 * - Click to open DM
 */
function AgentCardComponent({ agent, onClick }: AgentCardProps) {
  const handleClick = useCallback(() => {
    onClick?.(agent);
  }, [onClick, agent]);

  const handleKeyDown = useCallback((event: React.KeyboardEvent) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onClick?.(agent);
    }
  }, [onClick, agent]);

  // Memoize computed values
  const initials = useMemo(() => getInitials(agent.name), [agent.name]);
  const lastActiveText = useMemo(() => {
    if (agent.status === "online") {
      return "Now";
    }
    return formatLastActive(agent.lastActive);
  }, [agent.status, agent.lastActive]);

  const lastActiveClassName = useMemo(() => {
    const baseClass = "text-xs font-medium";
    if (agent.status === "online") {
      return `${baseClass} text-emerald-400`;
    }
    if (agent.status === "busy") {
      return `${baseClass} text-amber-400`;
    }
    return `${baseClass} text-slate-500`;
  }, [agent.status]);

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      className="group cursor-pointer rounded-2xl border border-slate-700 bg-slate-900/80 p-5 shadow-lg transition-all duration-200 hover:border-amber-600/50 hover:bg-slate-900 hover:shadow-amber-600/10 focus:outline-none focus:ring-2 focus:ring-amber-600 focus:ring-offset-2 focus:ring-offset-slate-950"
    >
      {/* Header: Avatar + Status */}
      <div className="flex items-start justify-between">
        <div className="relative">
          {agent.avatarUrl ? (
            <img
              src={agent.avatarUrl}
              alt={agent.name}
              loading="lazy"
              decoding="async"
              className="h-14 w-14 rounded-xl object-cover ring-2 ring-slate-700 transition group-hover:ring-amber-600/50"
            />
          ) : (
            <div className="flex h-14 w-14 items-center justify-center rounded-xl bg-slate-800 text-lg font-semibold text-amber-200 ring-2 ring-slate-700 transition group-hover:ring-amber-600/50">
              {initials}
            </div>
          )}
        </div>
        <StatusIndicator status={agent.status} />
      </div>

      {/* Name & Role */}
      <div className="mt-4">
        <h3 className="font-semibold text-slate-100 transition group-hover:text-amber-200">
          {agent.name}
        </h3>
        {agent.role && (
          <p className="mt-0.5 text-sm text-slate-500">{agent.role}</p>
        )}
      </div>

      {/* Current Task */}
      {agent.currentTask && agent.status !== "offline" && (
        <div className="mt-3 rounded-lg border border-slate-700 bg-slate-800/50 px-3 py-2">
          <p className="text-xs font-medium uppercase tracking-wider text-slate-500">
            Current Task
          </p>
          <p className="mt-1 line-clamp-2 text-sm text-slate-300">
            {agent.currentTask}
          </p>
        </div>
      )}

      {/* Footer: Last Active */}
      <div className="mt-4 flex items-center justify-between border-t border-slate-800 pt-3">
        <span className="text-xs text-slate-600">Last active</span>
        <span className={lastActiveClassName}>{lastActiveText}</span>
      </div>
    </div>
  );
}

const AgentCard = memo(AgentCardComponent);

export default AgentCard;
