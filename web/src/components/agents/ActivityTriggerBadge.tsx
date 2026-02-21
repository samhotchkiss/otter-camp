import { memo } from "react";

type TriggerMeta = {
  icon: string;
  label: string;
};

const TRIGGER_META: Record<string, TriggerMeta> = {
  "chat.slack": { icon: "💬", label: "Slack" },
  "chat.telegram": { icon: "💬", label: "Telegram" },
  "chat.tui": { icon: "💬", label: "TUI" },
  "chat.discord": { icon: "💬", label: "Discord" },
  "cron.scheduled": { icon: "⏰", label: "Cron" },
  "cron.manual": { icon: "⏱️", label: "Manual Cron" },
  heartbeat: { icon: "💓", label: "Heartbeat" },
  "spawn.sub_agent": { icon: "🧬", label: "Sub-agent" },
  "spawn.isolated": { icon: "🧪", label: "Isolated" },
  "system.event": { icon: "🔧", label: "System" },
  "dispatch.dm": { icon: "📨", label: "DM Dispatch" },
  "dispatch.project_chat": { icon: "📨", label: "Project Dispatch" },
  "dispatch.issue": { icon: "📨", label: "Task Dispatch" },
};

const STATUS_STYLE: Record<string, string> = {
  started: "bg-sky-100 text-sky-700",
  completed: "bg-emerald-100 text-emerald-700",
  failed: "bg-rose-100 text-rose-700",
  timeout: "bg-amber-100 text-amber-700",
};

function toTitleCase(raw: string): string {
  return raw
    .split(/[._-]/g)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

function getTriggerMeta(trigger: string, channel?: string): TriggerMeta {
  const normalized = trigger.trim().toLowerCase();
  if (TRIGGER_META[normalized]) {
    return TRIGGER_META[normalized];
  }

  if (normalized.startsWith("chat.")) {
    const suffix = normalized.split(".")[1] || channel || "chat";
    return { icon: "💬", label: toTitleCase(suffix) };
  }

  if (normalized.startsWith("cron.")) {
    const suffix = normalized.split(".")[1] || "cron";
    return { icon: "⏰", label: toTitleCase(suffix) };
  }

  return {
    icon: "📍",
    label: trigger,
  };
}

export type ActivityTriggerBadgeProps = {
  trigger: string;
  channel?: string;
  status?: string;
  className?: string;
};

function ActivityTriggerBadgeComponent({
  trigger,
  channel,
  status,
  className = "",
}: ActivityTriggerBadgeProps) {
  const meta = getTriggerMeta(trigger, channel);
  const statusLabel = status?.trim().toLowerCase();
  const statusClassName = statusLabel ? STATUS_STYLE[statusLabel] || "bg-slate-100 text-slate-700" : "";

  return (
    <div
      className={`inline-flex items-center gap-2 rounded-full border border-slate-200 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 ${className}`}
      data-testid="activity-trigger-badge"
    >
      <span aria-hidden="true">{meta.icon}</span>
      <span>{meta.label}</span>
      {statusLabel ? (
        <span className={`rounded-full px-2 py-0.5 text-[11px] font-semibold ${statusClassName}`}>
          {statusLabel}
        </span>
      ) : null}
    </div>
  );
}

const ActivityTriggerBadge = memo(ActivityTriggerBadgeComponent);

export default ActivityTriggerBadge;
