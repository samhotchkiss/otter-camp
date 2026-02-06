import { useState, useEffect } from "react";
import { withDemoParam } from "../lib/demo";

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

interface WorkflowTrigger {
  type: string;
  every?: string;
  cron?: string;
  event?: string;
  label?: string;
}

interface WorkflowStep {
  id: string;
  name: string;
  kind?: string;
}

interface Workflow {
  id: string;
  name: string;
  trigger: WorkflowTrigger;
  steps: WorkflowStep[];
  status: string;
  last_run?: string;
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

function formatTrigger(trigger?: WorkflowTrigger): string {
  if (!trigger) return "Manual";
  if (trigger.label) return trigger.label;
  if (trigger.type === "cron") {
    if (trigger.every) return `Every ${trigger.every}`;
    if (trigger.cron) return `Cron: ${trigger.cron}`;
  }
  if (trigger.type === "event" && trigger.event) {
    return `On ${trigger.event}`;
  }
  if (trigger.type === "manual") return "Manual";
  return trigger.type || "Manual";
}

function triggerIcon(trigger?: WorkflowTrigger): string {
  if (!trigger) return "⚙️";
  switch (trigger.type) {
    case "cron":
      return "⏱️";
    case "event":
      return "⚡";
    case "manual":
      return "🧩";
    default:
      return "⚙️";
  }
}

export default function WorkflowsPage() {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function fetchWorkflows() {
      try {
        const res = await fetch(withDemoParam(`${API_URL}/api/workflows`));
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

  if (workflows.length === 0) {
    return (
      <div className="workflows-page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Workflows</h1>
            <p className="page-subtitle">Ongoing agent processes</p>
          </div>
        </div>
        <div className="workflows-empty">
          <h3>No workflows configured</h3>
          <p>Connect OpenClaw or create a new automation to get started.</p>
        </div>
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
              <div className={`workflow-icon ${workflow.trigger?.type || "default"}`}>
                {triggerIcon(workflow.trigger)}
              </div>
              <div className="workflow-info">
                <div className="workflow-name">{workflow.name}</div>
                <div className="workflow-trigger">{formatTrigger(workflow.trigger)}</div>
              </div>
              <div className={`workflow-status ${workflow.status}`}>
                <span className="status-dot"></span>
                {workflow.status === "active" ? "Active" : "Paused"}
              </div>
            </div>

            <div className="workflow-card-body">
              <div className="workflow-latest-label">Last run</div>
              <div className="workflow-latest">
                {workflow.last_run ? formatTime(workflow.last_run) : "Never"}
              </div>

              {workflow.steps && workflow.steps.length > 0 && (
                <div className="workflow-steps">
                  <div className="workflow-latest-label">Steps</div>
                  <ul className="workflow-steps-list">
                    {workflow.steps.map((step) => (
                      <li key={step.id}>{step.name}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>

            <div className="workflow-card-footer">
              <div className="workflow-schedule">
                <span>🕐</span>
                {formatTrigger(workflow.trigger)}
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
