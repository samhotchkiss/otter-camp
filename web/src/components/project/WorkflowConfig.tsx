export type WorkflowConfigAgentOption = {
  id: string;
  name: string;
};

export type WorkflowConfigState = {
  enabled: boolean;
  scheduleKind: "cron" | "every" | "at";
  cronExpr: string;
  tz: string;
  everyMs: string;
  at: string;
  titlePattern: string;
  body: string;
  priority: "P0" | "P1" | "P2" | "P3";
  labels: string;
  pipeline: "none" | "auto_close" | "standard";
  autoClose: boolean;
  workflowAgentID: string;
};

type WorkflowConfigProps = {
  value: WorkflowConfigState;
  onChange: (next: WorkflowConfigState) => void;
  agents: WorkflowConfigAgentOption[];
};

export function defaultWorkflowConfigState(): WorkflowConfigState {
  return {
    enabled: false,
    scheduleKind: "cron",
    cronExpr: "0 6 * * *",
    tz: "America/Denver",
    everyMs: "900000",
    at: "",
    titlePattern: "",
    body: "",
    priority: "P2",
    labels: "automated",
    pipeline: "none",
    autoClose: true,
    workflowAgentID: "",
  };
}

export default function WorkflowConfig({ value, onChange, agents }: WorkflowConfigProps) {
  return (
    <section className="space-y-4 rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] p-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h3 className="text-sm font-semibold text-[var(--text)]">Workflow</h3>
          <p className="text-xs text-[var(--text-muted)]">
            Configure recurring schedule, task template, and workflow agent.
          </p>
        </div>
        <label className="flex items-center gap-2 text-sm text-[var(--text)]">
          <input
            aria-label="Workflow enabled"
            type="checkbox"
            checked={value.enabled}
            onChange={(event) => onChange({ ...value, enabled: event.target.checked })}
          />
          Enabled
        </label>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Schedule type</span>
          <select
            aria-label="Workflow schedule type"
            value={value.scheduleKind}
            onChange={(event) =>
              onChange({ ...value, scheduleKind: event.target.value as WorkflowConfigState["scheduleKind"] })
            }
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          >
            <option value="cron">Cron</option>
            <option value="every">Every</option>
            <option value="at">At</option>
          </select>
        </label>

        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Workflow agent</span>
          <select
            aria-label="Workflow agent"
            value={value.workflowAgentID}
            onChange={(event) => onChange({ ...value, workflowAgentID: event.target.value })}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          >
            <option value="">No workflow agent</option>
            {agents.map((agent) => (
              <option key={agent.id} value={agent.id}>
                {agent.name}
              </option>
            ))}
          </select>
        </label>
      </div>

      {value.scheduleKind === "cron" && (
        <div className="grid gap-3 md:grid-cols-2">
          <label className="text-sm text-[var(--text)]">
            <span className="mb-1 block text-xs text-[var(--text-muted)]">Cron expression</span>
            <input
              aria-label="Workflow cron expression"
              value={value.cronExpr}
              onChange={(event) => onChange({ ...value, cronExpr: event.target.value })}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
            />
          </label>
          <label className="text-sm text-[var(--text)]">
            <span className="mb-1 block text-xs text-[var(--text-muted)]">Timezone</span>
            <input
              aria-label="Workflow timezone"
              value={value.tz}
              onChange={(event) => onChange({ ...value, tz: event.target.value })}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
            />
          </label>
        </div>
      )}

      {value.scheduleKind === "every" && (
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Every (milliseconds)</span>
          <input
            aria-label="Workflow every milliseconds"
            value={value.everyMs}
            onChange={(event) => onChange({ ...value, everyMs: event.target.value })}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          />
        </label>
      )}

      {value.scheduleKind === "at" && (
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Run at (RFC3339)</span>
          <input
            aria-label="Workflow run at"
            value={value.at}
            onChange={(event) => onChange({ ...value, at: event.target.value })}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          />
        </label>
      )}

      <div className="grid gap-3 md:grid-cols-2">
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Task title pattern</span>
          <input
            aria-label="Workflow task title pattern"
            value={value.titlePattern}
            onChange={(event) => onChange({ ...value, titlePattern: event.target.value })}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          />
        </label>
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Priority</span>
          <select
            aria-label="Workflow task priority"
            value={value.priority}
            onChange={(event) =>
              onChange({ ...value, priority: event.target.value as WorkflowConfigState["priority"] })
            }
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          >
            <option value="P0">P0</option>
            <option value="P1">P1</option>
            <option value="P2">P2</option>
            <option value="P3">P3</option>
          </select>
        </label>
      </div>

      <label className="text-sm text-[var(--text)]">
        <span className="mb-1 block text-xs text-[var(--text-muted)]">Task body</span>
        <textarea
          aria-label="Workflow task body"
          value={value.body}
          onChange={(event) => onChange({ ...value, body: event.target.value })}
          className="h-20 w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
        />
      </label>

      <div className="grid gap-3 md:grid-cols-2">
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Labels (comma separated)</span>
          <input
            aria-label="Workflow task labels"
            value={value.labels}
            onChange={(event) => onChange({ ...value, labels: event.target.value })}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          />
        </label>
        <label className="text-sm text-[var(--text)]">
          <span className="mb-1 block text-xs text-[var(--text-muted)]">Pipeline</span>
          <select
            aria-label="Workflow pipeline"
            value={value.pipeline}
            onChange={(event) =>
              onChange({ ...value, pipeline: event.target.value as WorkflowConfigState["pipeline"] })
            }
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          >
            <option value="none">none</option>
            <option value="auto_close">auto_close</option>
            <option value="standard">standard</option>
          </select>
        </label>
      </div>

      <label className="flex items-center gap-2 text-sm text-[var(--text)]">
        <input
          aria-label="Workflow auto close"
          type="checkbox"
          checked={value.autoClose}
          onChange={(event) => onChange({ ...value, autoClose: event.target.checked })}
        />
        Auto close
      </label>
    </section>
  );
}
