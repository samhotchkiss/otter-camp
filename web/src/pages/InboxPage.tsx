import { useState, useEffect, useCallback } from "react";
import api, { Approval } from "../lib/api";

type ItemType = "approval" | "review" | "decision" | "blocked";
type InboxFilter = "all" | "unread" | "urgent";

interface InboxItem extends Approval {
  itemType: ItemType;
  unread: boolean;
  urgent: boolean;
  description: string;
}

function getItemType(approval: Approval): ItemType {
  const type = approval.type?.toLowerCase() || "";
  if (type.includes("review")) return "review";
  if (type.includes("decision")) return "decision";
  if (type.includes("blocked") || approval.status === "blocked") return "blocked";
  return "approval";
}

function getItemIcon(itemType: ItemType): string {
  switch (itemType) {
    case "approval":
      return "📋";
    case "review":
      return "👀";
    case "decision":
      return "🤔";
    case "blocked":
      return "🚫";
    default:
      return "📋";
  }
}

function getChipVariant(itemType: ItemType): string {
  switch (itemType) {
    case "review":
      return "oc-chip--info";
    case "decision":
      return "oc-chip--success";
    case "blocked":
      return "oc-chip--danger";
    case "approval":
    default:
      return "oc-chip--warning";
  }
}

function mapToInboxItem(approval: Approval): InboxItem {
  const itemType = getItemType(approval);
  const unread = approval.status === "pending";
  const urgent = unread || approval.status === "blocked" || itemType === "blocked";
  return {
    ...approval,
    itemType,
    unread,
    urgent,
    description: approval.command || `${approval.type} request from ${approval.agent}`,
  };
}

function formatTime(dateString: string): string {
  try {
    const date = new Date(dateString);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMins / 60);
    const diffDays = Math.floor(diffHours / 24);

    if (diffMins < 1) return "Just now";
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    if (diffDays < 7) return `${diffDays}d ago`;
    return date.toLocaleDateString();
  } catch {
    return dateString;
  }
}

export default function InboxPage() {
  const [items, setItems] = useState<InboxItem[]>([]);
  const [filter, setFilter] = useState<InboxFilter>("all");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function fetchApprovals() {
      try {
        setLoading(true);
        setError(null);
        const response = await api.inbox();
        const inboxItems = response.items.map(mapToInboxItem);
        setItems(inboxItems);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to fetch approvals");
      } finally {
        setLoading(false);
      }
    }

    fetchApprovals();
  }, []);

  const [processingId, setProcessingId] = useState<string | null>(null);
  const unreadCount = items.filter((item) => item.unread).length;
  const urgentCount = items.filter((item) => item.urgent).length;
  const filteredItems = items.filter((item) => {
    if (filter === "unread") return item.unread;
    if (filter === "urgent") return item.urgent;
    return true;
  });

  const handleApprove = useCallback(async (id: string) => {
    if (processingId) return; // Prevent double-click
    
    setProcessingId(id);
    try {
      await api.approveItem(id);
      // Remove item from list with smooth transition
      setItems((prev) => prev.filter((item) => item.id !== id));
    } catch (err) {
      console.error("Approve failed:", err);
      setError(err instanceof Error ? err.message : "Failed to approve");
    } finally {
      setProcessingId(null);
    }
  }, [processingId]);

  const handleReject = useCallback(async (id: string) => {
    if (processingId) return; // Prevent double-click
    
    setProcessingId(id);
    try {
      await api.rejectItem(id);
      // Remove item from list with smooth transition
      setItems((prev) => prev.filter((item) => item.id !== id));
    } catch (err) {
      console.error("Reject failed:", err);
      setError(err instanceof Error ? err.message : "Failed to reject");
    } finally {
      setProcessingId(null);
    }
  }, [processingId]);

  if (loading) {
    return (
      <div className="inbox-container min-w-0">
        <div className="mb-6">
          <h1 className="page-title">
            Inbox
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Pending approvals and items requiring your attention
          </p>
        </div>
        <div className="inbox-list">
          <div className="inbox-loading oc-panel">Loading...</div>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="inbox-container min-w-0">
        <div className="mb-6">
          <h1 className="page-title">
            Inbox
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Pending approvals and items requiring your attention
          </p>
        </div>
        <div className="inbox-list">
          <div className="inbox-error oc-panel">
            <p>Error: {error}</p>
            <button
              className="btn btn-secondary oc-toolbar-button mt-4"
              onClick={() => window.location.reload()}
            >
              Retry
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="inbox-container min-w-0">
      <div className="inbox-header" data-testid="inbox-header">
        <div className="inbox-header-main min-w-0 flex-1">
          <h1 className="page-title">
            Inbox
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            {filteredItems.length} items requiring your attention
          </p>
        </div>
        <div
          className="inbox-header-actions w-full sm:w-auto sm:justify-end"
          aria-label="Inbox actions"
          role="toolbar"
        >
          <button type="button" className="inbox-icon-action" aria-label="Filter inbox" disabled>
            Filter
          </button>
          <button type="button" className="inbox-icon-action" aria-label="Archive inbox" disabled>
            Archive
          </button>
        </div>
      </div>

      <div
        className="inbox-filter-tabs overflow-x-auto whitespace-nowrap"
        role="tablist"
        aria-label="Inbox filters"
      >
        <button
          type="button"
          role="tab"
          aria-selected={filter === "all"}
          className={`inbox-filter-tab ${filter === "all" ? "active" : ""}`}
          onClick={() => setFilter("all")}
        >
          All ({items.length})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={filter === "unread"}
          className={`inbox-filter-tab ${filter === "unread" ? "active" : ""}`}
          onClick={() => setFilter("unread")}
        >
          Unread ({unreadCount})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={filter === "urgent"}
          className={`inbox-filter-tab ${filter === "urgent" ? "active" : ""}`}
          onClick={() => setFilter("urgent")}
        >
          Urgent ({urgentCount})
        </button>
      </div>

      <div className="inbox-list-container oc-panel min-w-0" data-testid="inbox-list-container">
        <div className="inbox-list">
        {filteredItems.length === 0 ? (
          <div className="inbox-empty oc-panel">
            <span className="inbox-empty-icon">📭</span>
            <p>No pending items</p>
          </div>
        ) : (
          filteredItems.map((item) => (
            <div
              key={item.id}
              className={`inbox-row inbox-item oc-card oc-card-interactive min-w-0 ${item.urgent ? "urgent" : ""}`}
              data-testid="inbox-row"
            >
              <div className="item-header">
                <div className={`item-icon ${item.itemType}`}>
                  {getItemIcon(item.itemType)}
                </div>
                <div className="item-main">
                  <h3 className="item-title">
                    {item.type} — {item.agent}
                  </h3>
                  <div className="item-meta">
                    <span className={`badge-type badge-${item.itemType} oc-chip ${getChipVariant(item.itemType)}`}>
                      {item.itemType}
                    </span>
                    {item.unread ? <span className="item-unread">Unread</span> : null}
                    <span className={`item-status ${item.status}`}>
                      {item.status}
                    </span>
                    <span className="item-time">{formatTime(item.createdAt)}</span>
                  </div>
                </div>
              </div>
              <div className="inbox-row-meta" data-testid="inbox-row-meta">
                <span className="inbox-row-type">{item.type}</span>
                <span className="inbox-row-from">from {item.agent}</span>
                <span className="inbox-row-timestamp">{formatTime(item.createdAt)}</span>
              </div>
              <div className="item-body">
                <p className="item-desc">{item.description}</p>
                <div className="item-actions oc-toolbar">
                  <button
                    className="btn btn-primary oc-toolbar-button oc-toolbar-button--primary"
                    onClick={() => handleApprove(item.id)}
                    disabled={!!processingId}
                  >
                    {processingId === item.id ? "Processing..." : "Approve"}
                  </button>
                  <button
                    className="btn btn-secondary oc-toolbar-button"
                    onClick={() => handleReject(item.id)}
                    disabled={!!processingId}
                  >
                    {processingId === item.id ? "Processing..." : "Reject"}
                  </button>
                </div>
              </div>
            </div>
          ))
        )}
        </div>
      </div>
    </div>
  );
}
