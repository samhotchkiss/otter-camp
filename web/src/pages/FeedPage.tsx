import { useMemo, useState } from "react";
import ActivityPanel from "../components/ActivityPanel";
import AgentActivityTimeline from "../components/agents/AgentActivityTimeline";
import { useAgentActivity } from "../hooks/useAgentActivity";

export default function FeedPage() {
  const [mode, setMode] = useState<"realtime" | "agent">("realtime");
  const { events, isLoading, isLoadingMore, error, hasMore, loadMore } = useAgentActivity({
    mode: "recent",
    limit: 100,
  });

  const agentEvents = useMemo(
    () =>
      events.map((event) => ({
        ...event,
        summary: `${event.agentId} · ${event.summary}`,
      })),
    [events],
  );

  return (
    <div className="w-full">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-[var(--text)]">
          Activity Feed
        </h1>
        <p className="mt-1 text-sm text-[var(--text-muted)]">
          Real-time updates from across your projects and agents
        </p>
      </div>

      <div className="mb-4 inline-flex rounded-lg border border-[var(--border)] bg-[var(--surface)] p-1">
        <button
          type="button"
          onClick={() => setMode("realtime")}
          className={`rounded-md px-3 py-1.5 text-sm font-medium ${
            mode === "realtime"
              ? "bg-[#C9A86C] text-white"
              : "text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
          }`}
        >
          Realtime
        </button>
        <button
          type="button"
          onClick={() => setMode("agent")}
          className={`rounded-md px-3 py-1.5 text-sm font-medium ${
            mode === "agent"
              ? "bg-[#C9A86C] text-white"
              : "text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
          }`}
        >
          Agent Activity
        </button>
      </div>

      {mode === "realtime" ? (
        <ActivityPanel className="min-h-[70vh]" />
      ) : (
        <AgentActivityTimeline
          events={agentEvents}
          isLoading={isLoading}
          isLoadingMore={isLoadingMore}
          error={error}
          hasMore={hasMore}
          onLoadMore={() => void loadMore()}
          emptyMessage="No agent activity yet."
        />
      )}
    </div>
  );
}
