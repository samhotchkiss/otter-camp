import { useMemo, useState } from "react";
import EmptyState from "../components/EmptyState";
import InboxItem, { type InboxItemData, type InboxItemType } from "../components/InboxItem";

const FILTERS: Array<{ id: "all" | InboxItemType; label: string }> = [
  { id: "all", label: "All" },
  { id: "approval", label: "Approvals" },
  { id: "review", label: "Reviews" },
  { id: "decision", label: "Decisions" },
  { id: "done", label: "Done" },
];

const SAMPLE_ITEMS: InboxItemData[] = [
  {
    id: "approval-1",
    icon: "🧾",
    project: "Supply Cache Refill",
    type: "approval",
    timestamp: "12m ago",
    description: "Approve the extra $420 for emergency rations and lantern fuel.",
    agentName: "Scout Otter",
    urgent: true,
    actions: [
      { id: "approve", label: "Approve", variant: "primary" },
      { id: "reject", label: "Reject", variant: "danger" },
    ],
  },
  {
    id: "review-1",
    icon: "📄",
    project: "Campfire Launch Plan",
    type: "review",
    timestamp: "45m ago",
    description: "Review the launch checklist draft before sharing with the raft.",
    agentName: "Builder Otter",
    actions: [
      { id: "review", label: "Review", variant: "primary" },
      { id: "later", label: "Later", variant: "ghost" },
    ],
  },
  {
    id: "decision-1",
    icon: "🧭",
    project: "Trailhead Logistics",
    type: "decision",
    timestamp: "2h ago",
    description: "Select the preferred supply drop option for next week.",
    agentName: "Guide Otter",
    actions: [
      { id: "select", label: "Select Option", variant: "primary" },
      { id: "context", label: "View Context", variant: "secondary" },
    ],
  },
  {
    id: "approval-2",
    icon: "🧪",
    project: "Water Quality Audit",
    type: "approval",
    timestamp: "4h ago",
    description: "Approve the field test results summary before filing.",
    agentName: "Research Otter",
    actions: [
      { id: "approve", label: "Approve", variant: "primary" },
      { id: "reject", label: "Reject", variant: "danger" },
    ],
  },
  {
    id: "review-2",
    icon: "🪵",
    project: "Timber Inventory",
    type: "review",
    timestamp: "5h ago",
    description: "Review the inventory notes and flag any missing bundles.",
    agentName: "Ops Otter",
    actions: [
      { id: "review", label: "Review", variant: "primary" },
      { id: "later", label: "Later", variant: "ghost" },
    ],
  },
  {
    id: "decision-2",
    icon: "🗺️",
    project: "North Ridge Expedition",
    type: "decision",
    timestamp: "Yesterday",
    description: "Choose the backup route option for the summit push.",
    agentName: "Lead Otter",
    actions: [
      { id: "select", label: "Select Option", variant: "primary" },
      { id: "context", label: "View Context", variant: "secondary" },
    ],
  },
  {
    id: "done-1",
    icon: "✅",
    project: "Campfire Safety Review",
    type: "done",
    timestamp: "2 days ago",
    description: "Decision recorded and shared with the safety squad.",
    agentName: "Coordinator Otter",
    actions: [
      { id: "archive", label: "Archive", variant: "secondary" },
      { id: "reopen", label: "Reopen", variant: "ghost" },
    ],
  },
];

export default function InboxPage() {
  const [activeFilter, setActiveFilter] = useState<"all" | InboxItemType>("all");

  const counts = useMemo(() => {
    return SAMPLE_ITEMS.reduce(
      (acc, item) => {
        acc.all += 1;
        acc[item.type] += 1;
        return acc;
      },
      { all: 0, approval: 0, review: 0, decision: 0, done: 0 }
    );
  }, []);

  const filteredItems = useMemo(() => {
    if (activeFilter === "all") return SAMPLE_ITEMS;
    return SAMPLE_ITEMS.filter((item) => item.type === activeFilter);
  }, [activeFilter]);

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 pb-12">
      <header className="mt-4 flex flex-wrap items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <span className="text-3xl" aria-hidden="true">
              📥
            </span>
            <h1 className="text-2xl font-semibold text-otter-dark-text">Inbox</h1>
            <span className="rounded-full border border-otter-dark-border bg-otter-dark-surface-alt px-3 py-1 text-xs font-semibold uppercase tracking-wide text-otter-dark-text-muted">
              {counts.all}
            </span>
          </div>
          <p className="mt-2 text-sm text-otter-dark-text-muted">
            Actions that need your attention from agents across camp.
          </p>
        </div>
        <div className="rounded-2xl border border-otter-dark-border bg-otter-dark-surface-alt px-4 py-3 text-sm text-otter-dark-text-muted">
          Last sync: 4 minutes ago
        </div>
      </header>

      <section className="flex flex-wrap items-center gap-2 rounded-2xl border border-otter-dark-border bg-otter-dark-surface p-2">
        {FILTERS.map((filter) => {
          const isActive = activeFilter === filter.id;
          const count = counts[filter.id];
          return (
            <button
              key={filter.id}
              type="button"
              onClick={() => setActiveFilter(filter.id)}
              className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm font-semibold transition ${
                isActive
                  ? "bg-otter-dark-accent text-otter-dark-bg"
                  : "text-otter-dark-text-muted hover:bg-otter-dark-surface-alt hover:text-otter-dark-text"
              }`}
            >
              {filter.label}
              <span
                className={`rounded-full px-2 py-0.5 text-xs font-semibold ${
                  isActive
                    ? "bg-otter-dark-bg/20 text-otter-dark-bg"
                    : "bg-otter-dark-border text-otter-dark-text"
                }`}
              >
                {count}
              </span>
            </button>
          );
        })}
      </section>

      <section className="flex flex-col gap-4">
        {filteredItems.length === 0 ? (
          <EmptyState
            title="All caught up"
            description="No inbox items match this filter right now."
            icon={
              <span className="text-4xl" role="img" aria-label="otter inbox">
                🦦
              </span>
            }
            className="border-otter-dark-border bg-otter-dark-surface"
          />
        ) : (
          filteredItems.map((item) => <InboxItem key={item.id} item={item} />)
        )}
      </section>
    </div>
  );
}
