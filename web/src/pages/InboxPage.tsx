import { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import api, { type InboxItem as ApiInboxItem } from "../lib/api";

type ItemType = "approval" | "review" | "decision" | "blocked";

interface InboxItem extends ApiInboxItem {
  itemType: ItemType;
  urgent: boolean;
  description: string;
}

function getItemType(approval: ApiInboxItem): ItemType {
  if (approval.type === "task_blocker") return "blocked";
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

function mapToInboxItem(approval: ApiInboxItem): InboxItem {
  const itemType = getItemType(approval);
  const taskLabel =
    approval.task_number && approval.task_title
      ? `Task #${approval.task_number}: ${approval.task_title}`
      : approval.task_title || "";
  const description =
    approval.type === "task_blocker"
      ? approval.summary || taskLabel || "Task is blocked and waiting on human input"
      : approval.command || `${approval.type} request from ${approval.agent}`;
  return {
    ...approval,
    itemType,
    urgent: approval.status === "pending" || approval.status === "on_hold" || itemType === "blocked",
    description,
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
  const navigate = useNavigate();
  const [items, setItems] = useState<InboxItem[]>([]);
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

  const handleResolveBlocker = useCallback(async (item: InboxItem) => {
    if (processingId || item.type !== "task_blocker" || !item.task_id) return;

    setProcessingId(item.id);
    try {
      await api.resolveTaskBlocker(item.task_id, item.id);
      setItems((prev) => prev.filter((candidate) => candidate.id !== item.id));
    } catch (err) {
      console.error("Resolve blocker failed:", err);
      setError(err instanceof Error ? err.message : "Failed to resolve blocker");
    } finally {
      setProcessingId(null);
    }
  }, [processingId]);

  if (loading) {
    return (
      <div className="inbox-container">
        <div className="mb-6">
          <h1 className="page-title">
            Inbox
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Pending approvals and items requiring your attention
          </p>
        </div>
        <div className="inbox-list">
          <div className="inbox-loading">Loading...</div>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="inbox-container">
        <div className="mb-6">
          <h1 className="page-title">
            Inbox
          </h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
            Pending approvals and items requiring your attention
          </p>
        </div>
        <div className="inbox-list">
          <div className="inbox-error">
            <p>Error: {error}</p>
            <button
              className="btn btn-secondary mt-4"
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
    <div className="inbox-container">
      <div className="mb-6">
        <h1 className="page-title">
          Inbox
        </h1>
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
          Pending approvals and items requiring your attention
        </p>
      </div>

      <div className="inbox-list">
        {items.length === 0 ? (
          <div className="inbox-empty">
            <span className="inbox-empty-icon">📭</span>
            <p>No pending items</p>
          </div>
        ) : (
          items.map((item) => (
            <div
              key={item.id}
              className={`inbox-item ${item.urgent ? "urgent" : ""}`}
            >
              <div className="item-header">
                <div className={`item-icon ${item.itemType}`}>
                  {getItemIcon(item.itemType)}
                </div>
                <div className="item-main">
                  <h3 className="item-title">
                    {item.type === "task_blocker" && item.task_number
                      ? `Task #${item.task_number} blocker`
                      : `${item.type} — ${item.agent}`}
                  </h3>
                  <div className="item-meta">
                    <span className={`badge-type badge-${item.itemType}`}>
                      {item.itemType}
                    </span>
                    <span className={`item-status ${item.status}`}>
                      {item.status}
                    </span>
                    <span className="item-time">{formatTime(item.createdAt)}</span>
                  </div>
                </div>
              </div>
              <div className="item-body">
                <p className="item-desc">{item.description}</p>
                <div className="item-actions">
                  {item.type === "task_blocker" ? (
                    <>
                      <button
                        className="btn btn-primary"
                        onClick={() => {
                          if (item.project_id && item.task_id) {
                            navigate(`/projects/${item.project_id}/tasks/${item.task_id}`);
                          }
                        }}
                      >
                        Open task
                      </button>
                      <button
                        className="btn btn-secondary"
                        onClick={() => handleResolveBlocker(item)}
                        disabled={processingId === item.id}
                      >
                        {processingId === item.id ? "Processing..." : "Mark resolved"}
                      </button>
                    </>
                  ) : (
                    <>
                      <button
                        className="btn btn-primary"
                        onClick={() => handleApprove(item.id)}
                        disabled={processingId === item.id}
                      >
                        {processingId === item.id ? "Processing..." : "Approve"}
                      </button>
                      <button
                        className="btn btn-secondary"
                        onClick={() => handleReject(item.id)}
                        disabled={processingId === item.id}
                      >
                        {processingId === item.id ? "Processing..." : "Reject"}
                      </button>
                    </>
                  )}
                </div>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
