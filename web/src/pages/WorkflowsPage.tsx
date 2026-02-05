import { useState, useEffect } from "react";

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

interface WorkflowItem {
  id: string;
  content: string;
  priority: string;
  action_required: boolean;
  created_at: string;
}

interface Workflow {
  id: string;
  name: string;
  agent: string;
  agent_emoji: string;
  icon: string;
  icon_type: string;
  status: string;
  schedule: string;
  display_mode: string;
  latest_item?: WorkflowItem;
  stacked_items?: WorkflowItem[];
  created_at: string;
}

function formatTime(dateString: string): string {
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMins / 60);

  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  return date.toLocaleDateString();
}

function getPriorityBadge(priority: string, actionRequired: boolean) {
  if (actionRequired) {
    return <span className="escalation-badge action">⚡ Action Required</span>;
  }
  if (priority === "urgent") {
    return <span className="escalation-badge urgent">🔴 Urgent</span>;
  }
  if (priority === "high") {
    return <span className="escalation-badge high">🟡 High</span>;
  }
  return null;
}

export default function WorkflowsPage() {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function fetchWorkflows() {
      try {
        const res = await fetch(`${API_URL}/api/workflows?demo=true`);
        if (!res.ok) throw new Error("Failed to fetch workflows");
        const data = await res.json();
        setWorkflows(data);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load workflows");
      } finally {
        setLoading(false);
      }
    }
    fetchWorkflows();
  }, []);

  if (loading) {
    return (
      <div className="workflows-page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Workflows</h1>
            <p className="page-subtitle">Ongoing agent processes</p>
          </div>
        </div>
        <div className="workflows-loading">Loading workflows...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="workflows-page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Workflows</h1>
            <p className="page-subtitle">Ongoing agent processes</p>
          </div>
        </div>
        <div className="workflows-error">{error}</div>
      </div>
    );
  }

  return (
    <div className="workflows-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Workflows</h1>
          <p className="page-subtitle">{workflows.length} ongoing processes</p>
        </div>
        <button className="btn btn-primary">
          <span>+</span> New Workflow
        </button>
      </div>

      <div className="workflows-grid">
        {workflows.map((workflow) => (
          <div key={workflow.id} className="workflow-card">
            <div className="workflow-card-header">
              <div className={`workflow-icon ${workflow.icon_type}`}>
                {workflow.icon}
              </div>
              <div className="workflow-info">
                <div className="workflow-name">{workflow.name}</div>
                <div className="workflow-agent">
                  <span>{workflow.agent_emoji}</span>
                  <span>{workflow.agent}</span>
                </div>
              </div>
              <div className={`workflow-status ${workflow.status}`}>
                <span className="status-dot"></span>
                {workflow.status === "active" ? "Active" : "Paused"}
              </div>
            </div>

            <div className="workflow-card-body">
              {workflow.latest_item && (
                <>
                  <div className="workflow-latest-label">
                    Latest {workflow.display_mode === "replace" ? "" : "• " + formatTime(workflow.latest_item.created_at)}
                    {getPriorityBadge(workflow.latest_item.priority, workflow.latest_item.action_required)}
                  </div>
                  <div className="workflow-latest">
                    {workflow.latest_item.content}
                  </div>
                </>
              )}

              {workflow.display_mode === "stack" && workflow.stacked_items && workflow.stacked_items.length > 0 && (
                <div className="stacked-items">
                  {workflow.stacked_items.map((item) => (
                    <div key={item.id} className="stacked-item">
                      <span className="stacked-item-icon">
                        {item.priority === "low" ? "📄" : "📌"}
                      </span>
                      <div className="stacked-item-content">
                        <div className="stacked-item-text">{item.content}</div>
                        <div className="stacked-item-time">{formatTime(item.created_at)}</div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="workflow-card-footer">
              <div className="workflow-schedule">
                <span>🕐</span>
                {workflow.schedule}
              </div>
              <div className="workflow-actions">
                {workflow.status === "active" ? (
                  <button className="btn btn-ghost">Pause</button>
                ) : (
                  <button className="btn btn-ghost">Resume</button>
                )}
                <button className="btn btn-ghost">View</button>
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
