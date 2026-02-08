import { memo, useCallback, useMemo } from "react";
import type { AgentStatus } from "./AgentDM";
import AgentWorkingIndicator from "./AgentWorkingIndicator";

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
  lastActive?: string | number | null;
  lastEmission?: {
    summary: string;
    timestamp: string;
  };
};

export type AgentCardProps = {
  agent: AgentCardData;
  onClick?: (agent: AgentCardData) => void;
};

/**
 * Get initials from a name for avatar fallback.
 */
function getInitials(name: unknown): string {
  const safeName = typeof name === "string" ? name.trim() : "";
  if (!safeName) {
    return "?";
  }

  return safeName
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/**
 * Generate a consistent color for an agent based on their name.
 * Uses a hash to pick from a curated palette of distinct colors.
 */
const AVATAR_COLORS = [
  { bg: "bg-amber-600", text: "text-amber-100" },      // Gold
  { bg: "bg-emerald-600", text: "text-emerald-100" },  // Green  
  { bg: "bg-sky-600", text: "text-sky-100" },          // Blue
  { bg: "bg-rose-600", text: "text-rose-100" },        // Pink
  { bg: "bg-violet-600", text: "text-violet-100" },    // Purple
  { bg: "bg-orange-600", text: "text-orange-100" },    // Orange
  { bg: "bg-teal-600", text: "text-teal-100" },        // Teal
  { bg: "bg-indigo-600", text: "text-indigo-100" },    // Indigo
];

function getAvatarColor(name: string): { bg: string; text: string } {
  const safeName = typeof name === "string" ? name : "";
  let hash = 0;
  for (let i = 0; i < safeName.length; i++) {
    hash = ((hash << 5) - hash) + safeName.charCodeAt(i);
    hash = hash & hash;
  }
  const index = Math.abs(hash) % AVATAR_COLORS.length;
  return AVATAR_COLORS[index];
}

/**
 * Format relative time for "last active" display.
 */
export function formatLastActive(value?: string | number | null): string {
  if (value === null || value === undefined) {
    return "Never";
  }

  if (typeof value === "number") {
    if (!Number.isFinite(value) || value <= 0) {
      return "Never";
    }
    return formatLastActive(new Date(value).toISOString());
  }

  const trimmed = value.trim();
  if (!trimmed || trimmed === "0") {
    return "Never";
  }

  const date = new Date(trimmed);
  if (Number.isNaN(date.getTime())) {
    return "Never";
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

// Status styles - memoized outside component
// Using gold (#C9A86C) for online per DESIGN-SPEC.md
const STATUS_STYLES: Record<AgentStatus, string> = {
  online: "bg-[#C9A86C] shadow-[#C9A86C]/50 shadow-lg",
  busy: "bg-amber-500 shadow-amber-500/50 shadow-lg animate-pulse",
  offline: "bg-[var(--text-muted)]",
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
  const avatarColor = useMemo(() => getAvatarColor(agent.name), [agent.name]);
  const lastActiveText = useMemo(() => {
    if (agent.status === "online") {
      return "Now";
    }
    return formatLastActive(agent.lastActive);
  }, [agent.status, agent.lastActive]);

  const lastActiveClassName = useMemo(() => {
    const baseClass = "text-xs font-medium";
    if (agent.status === "online") {
      return `${baseClass} text-[#C9A86C]`;
    }
    if (agent.status === "busy") {
      return `${baseClass} text-amber-400`;
    }
    return `${baseClass} text-[var(--text-muted)]`;
  }, [agent.status]);

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      className="group cursor-pointer rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 shadow-lg transition-all duration-200 hover:border-[var(--accent)]/50 hover:bg-[var(--surface-alt)] hover:shadow-[var(--accent)]/10 focus:outline-none focus:ring-2 focus:ring-[var(--accent)] focus:ring-offset-2 focus:ring-offset-[var(--bg)]"
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
              className="h-14 w-14 rounded-xl object-cover ring-2 ring-[var(--border)] transition group-hover:ring-[var(--accent)]/50"
            />
          ) : (
            <div className={`flex h-14 w-14 items-center justify-center rounded-xl ${avatarColor.bg} ${avatarColor.text} text-lg font-semibold ring-2 ring-[var(--border)] transition group-hover:ring-[var(--accent)]/50`}>
              {initials}
            </div>
          )}
        </div>
        <StatusIndicator status={agent.status} />
      </div>

      {/* Name & Role */}
      <div className="mt-4">
        <h3 className="font-semibold text-[var(--text)] transition group-hover:text-[var(--accent)]">
          {agent.name}
        </h3>
        {agent.role && (
          <p className="mt-0.5 text-sm text-[var(--text-muted)]">{agent.role}</p>
        )}
      </div>

      {/* Current Task */}
      {agent.currentTask && agent.status !== "offline" && (
        <div className="mt-3 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2">
          <p className="text-xs font-medium uppercase tracking-wider text-[var(--text-muted)]">
            Current Task
          </p>
          <p className="mt-1 line-clamp-2 text-sm text-[var(--text)]">
            {agent.currentTask}
          </p>
        </div>
      )}

      {/* Live Emission Snippet */}
      {agent.lastEmission && (
        <div className="mt-3 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2">
          <p className="text-xs font-medium uppercase tracking-wider text-[var(--text-muted)]">
            Latest Emission
          </p>
          <p className="mt-1 line-clamp-2 text-sm text-[var(--text)]">
            {agent.lastEmission.summary}
          </p>
          <div className="mt-1 text-xs text-[var(--text-muted)]">
            <AgentWorkingIndicator
              latestEmission={{
                id: `${agent.id}-latest`,
                source_type: "agent",
                source_id: agent.id,
                kind: "status",
                summary: agent.lastEmission.summary,
                timestamp: agent.lastEmission.timestamp,
              }}
              activeWindowSeconds={60}
            />
          </div>
        </div>
      )}

      {/* Footer: Last Active */}
      <div className="mt-4 flex items-center justify-between border-t border-[var(--border)] pt-3">
        <span className="text-xs text-[var(--text-muted)]">Last active</span>
        <span className={lastActiveClassName}>{lastActiveText}</span>
      </div>
    </div>
  );
}

const AgentCard = memo(AgentCardComponent);

export default AgentCard;
