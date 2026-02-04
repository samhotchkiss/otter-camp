import { useCallback, useEffect, useMemo, useState } from "react";
import { useWS } from "../contexts/WebSocketContext";

// Activity types that can be filtered
export type ActivityType =
  | "task"
  | "message"
  | "agent_status"
  | "comment";

export type Activity = {
  id: string;
  type: ActivityType;
  actor: {
    name: string;
    avatar?: string;
  };
  description: string;
  timestamp: Date;
  metadata?: Record<string, unknown>;
};

type TimeGroup = "Today" | "Yesterday" | "This Week" | "Older";

const ACTIVITY_TYPE_LABELS: Record<ActivityType, string> = {
  task: "Tasks",
  message: "Messages",
  agent_status: "Agent Status",
  comment: "Comments",
};

const ACTIVITY_TYPE_ICONS: Record<ActivityType, string> = {
  task: "📋",
  message: "💬",
  agent_status: "🤖",
  comment: "📝",
};

const ITEMS_PER_PAGE = 10;

const getTimeGroup = (date: Date): TimeGroup => {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const weekAgo = new Date(today);
  weekAgo.setDate(weekAgo.getDate() - 7);

  if (date >= today) {
    return "Today";
  }
  if (date >= yesterday) {
    return "Yesterday";
  }
  if (date >= weekAgo) {
    return "This Week";
  }
  return "Older";
};

const formatTimestamp = (date: Date): string => {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);

  if (diffMins < 1) {
    return "Just now";
  }
  if (diffMins < 60) {
    return `${diffMins}m ago`;
  }
  if (diffHours < 24) {
    return `${diffHours}h ago`;
  }

  return date.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
};

const getInitials = (name: string): string => {
  return name
    .split(" ")
    .map((part) => part[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);
};

// Map WebSocket message types to activity types
const mapWebSocketToActivity = (
  type: string,
  data: unknown
): Activity | null => {
  const id = crypto.randomUUID();
  const timestamp = new Date();

  const record = (data && typeof data === "object" ? data : {}) as Record<
    string,
    unknown
  >;
  const actorName =
    (record.actor as string) ??
    (record.user as string) ??
    (record.agentName as string) ??
    "System";

  switch (type) {
    case "TaskCreated":
      return {
        id,
        type: "task",
        actor: { name: actorName },
        description: `Created task: ${(record.title as string) ?? "Untitled"}`,
        timestamp,
        metadata: record,
      };
    case "TaskUpdated":
      return {
        id,
        type: "task",
        actor: { name: actorName },
        description: `Updated task: ${(record.title as string) ?? "Untitled"}`,
        timestamp,
        metadata: record,
      };
    case "TaskStatusChanged":
      return {
        id,
        type: "task",
        actor: { name: actorName },
        description: `Changed status to ${(record.status as string) ?? "unknown"}`,
        timestamp,
        metadata: record,
      };
    case "CommentAdded":
      return {
        id,
        type: "comment",
        actor: { name: actorName },
        description: `Added a comment: "${((record.text as string) ?? "").slice(0, 50)}${((record.text as string) ?? "").length > 50 ? "…" : ""}"`,
        timestamp,
        metadata: record,
      };
    default:
      return null;
  }
};

type ActivityItemProps = {
  activity: Activity;
};

function ActivityItem({ activity }: ActivityItemProps) {
  return (
    <div className="flex items-start gap-3 rounded-xl px-3 py-3 transition hover:bg-slate-100 dark:hover:bg-slate-800/50">
      <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-slate-200 text-lg dark:bg-slate-700">
        {activity.actor.avatar ? (
          <img
            src={activity.actor.avatar}
            alt={activity.actor.name}
            className="h-full w-full rounded-full object-cover"
          />
        ) : (
          <span className="text-xs font-semibold text-slate-600 dark:text-slate-300">
            {getInitials(activity.actor.name)}
          </span>
        )}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-base" aria-hidden="true">
            {ACTIVITY_TYPE_ICONS[activity.type]}
          </span>
          <span className="font-medium text-slate-900 dark:text-slate-100">
            {activity.actor.name}
          </span>
          <span className="text-xs text-slate-500 dark:text-slate-400">
            {formatTimestamp(activity.timestamp)}
          </span>
        </div>
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
          {activity.description}
        </p>
      </div>
    </div>
  );
}

type ActivityPanelProps = {
  className?: string;
};

export default function ActivityPanel({ className = "" }: ActivityPanelProps) {
  const { connected, lastMessage } = useWS();
  const [activities, setActivities] = useState<Activity[]>([]);
  const [filterType, setFilterType] = useState<ActivityType | "all">("all");
  const [visibleCount, setVisibleCount] = useState(ITEMS_PER_PAGE);

  // Process incoming WebSocket messages
  useEffect(() => {
    if (!lastMessage || lastMessage.type === "Unknown") {
      return;
    }

    const activity = mapWebSocketToActivity(
      lastMessage.type,
      lastMessage.data
    );
    if (activity) {
      setActivities((prev) => [activity, ...prev]);
    }
  }, [lastMessage]);

  // Filtered activities based on type filter
  const filteredActivities = useMemo(() => {
    if (filterType === "all") {
      return activities;
    }
    return activities.filter((activity) => activity.type === filterType);
  }, [activities, filterType]);

  // Visible activities with pagination
  const visibleActivities = useMemo(() => {
    return filteredActivities.slice(0, visibleCount);
  }, [filteredActivities, visibleCount]);

  // Group activities by time
  const groupedActivities = useMemo(() => {
    const groups = new Map<TimeGroup, Activity[]>();
    const order: TimeGroup[] = ["Today", "Yesterday", "This Week", "Older"];
    order.forEach((group) => groups.set(group, []));

    visibleActivities.forEach((activity) => {
      const group = getTimeGroup(activity.timestamp);
      groups.get(group)?.push(activity);
    });

    return groups;
  }, [visibleActivities]);

  const hasMore = visibleCount < filteredActivities.length;

  const handleLoadMore = useCallback(() => {
    setVisibleCount((count) => count + ITEMS_PER_PAGE);
  }, []);

  const handleFilterChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      setFilterType(event.target.value as ActivityType | "all");
      setVisibleCount(ITEMS_PER_PAGE);
    },
    []
  );

  return (
    <div
      className={`overflow-hidden rounded-2xl border border-slate-200 bg-white/90 shadow-lg backdrop-blur dark:border-slate-800 dark:bg-slate-900/90 ${className}`}
    >
      {/* Header */}
      <div className="flex items-center justify-between border-b border-slate-200 px-5 py-4 dark:border-slate-800">
        <div className="flex items-center gap-3">
          <div className="inline-flex h-10 w-10 items-center justify-center rounded-full bg-emerald-100 text-lg dark:bg-emerald-900/50">
            🦦
          </div>
          <div>
            <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
              Activity Feed
            </h2>
            <div className="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
              <span
                className={`inline-block h-2 w-2 rounded-full ${
                  connected ? "bg-emerald-500" : "bg-slate-400"
                }`}
              />
              {connected ? "Live" : "Reconnecting…"}
            </div>
          </div>
        </div>

        {/* Filter Dropdown */}
        <select
          value={filterType}
          onChange={handleFilterChange}
          className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:border-slate-300 focus:border-emerald-500 focus:outline-none focus:ring-1 focus:ring-emerald-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:border-slate-600"
        >
          <option value="all">All Activity</option>
          {(Object.keys(ACTIVITY_TYPE_LABELS) as ActivityType[]).map((type) => (
            <option key={type} value={type}>
              {ACTIVITY_TYPE_LABELS[type]}
            </option>
          ))}
        </select>
      </div>

      {/* Activity List */}
      <div className="max-h-[60vh] overflow-y-auto px-3 py-4">
        {filteredActivities.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-12 text-center">
            <div className="text-4xl">🦦</div>
            <p className="mt-3 text-sm font-medium text-slate-600 dark:text-slate-300">
              No activity yet
            </p>
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              Activity will appear here as it happens.
            </p>
          </div>
        ) : (
          <div className="space-y-6">
            {(["Today", "Yesterday", "This Week", "Older"] as TimeGroup[]).map(
              (group) => {
                const items = groupedActivities.get(group) ?? [];
                if (items.length === 0) {
                  return null;
                }

                return (
                  <section key={group}>
                    <p className="mb-3 px-3 text-xs font-semibold uppercase tracking-[0.25em] text-slate-500 dark:text-slate-400">
                      {group}
                    </p>
                    <div className="space-y-1">
                      {items.map((activity) => (
                        <ActivityItem key={activity.id} activity={activity} />
                      ))}
                    </div>
                  </section>
                );
              }
            )}
          </div>
        )}

        {/* Load More */}
        {hasMore && (
          <div className="mt-6 flex justify-center">
            <button
              type="button"
              onClick={handleLoadMore}
              className="rounded-full border border-slate-200 bg-white px-5 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:border-slate-300 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:border-slate-600 dark:hover:bg-slate-700"
            >
              Load more ({filteredActivities.length - visibleCount} remaining)
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
