import { memo, useMemo } from "react";
import type { AgentStatus } from "./types";

const STATUS_STYLES: Record<AgentStatus, string> = {
  online: "bg-emerald-500 shadow-emerald-500/50",
  busy: "bg-amber-500 shadow-amber-500/50 animate-pulse",
  offline: "bg-slate-500",
};

const STATUS_LABELS: Record<AgentStatus, string> = {
  online: "Online",
  busy: "Busy",
  offline: "Offline",
};

const SIZE_STYLES = {
  xs: "h-2 w-2",
  sm: "h-3 w-3",
  md: "h-4 w-4",
} as const;

export type AgentStatusIndicatorProps = {
  status: AgentStatus;
  size?: keyof typeof SIZE_STYLES;
  showLabel?: boolean;
  className?: string;
};

function AgentStatusIndicatorComponent({
  status,
  size = "sm",
  showLabel = false,
  className = "",
}: AgentStatusIndicatorProps) {
  const label = STATUS_LABELS[status];

  const dotClassName = useMemo(() => {
    const base = `${SIZE_STYLES[size]} rounded-full shadow-lg`;
    return `${base} ${STATUS_STYLES[status]} ${className}`.trim();
  }, [className, size, status]);

  if (showLabel) {
    return (
      <span className="inline-flex items-center gap-2" role="status" aria-label={label}>
        <span className={dotClassName} aria-hidden="true" />
        <span className="text-xs text-slate-400">{label}</span>
      </span>
    );
  }

  return (
    <span
      className={dotClassName}
      role="status"
      aria-label={label}
      title={label}
    />
  );
}

const AgentStatusIndicator = memo(AgentStatusIndicatorComponent);

export default AgentStatusIndicator;

