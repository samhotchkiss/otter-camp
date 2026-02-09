import { useEffect, useState } from "react";
import { apiFetch } from "../../lib/api";

type PipelineRoleMember = {
  agentId: string | null;
};

type PipelineRolesResponse = {
  planner?: PipelineRoleMember;
  worker?: PipelineRoleMember;
  reviewer?: PipelineRoleMember;
};

export type PipelineSettingsAgentOption = {
  id: string;
  name: string;
};

type PipelineSettingsProps = {
  projectId: string;
  agents: PipelineSettingsAgentOption[];
  initialRequireHumanReview: boolean;
  onRequireHumanReviewSaved?: (value: boolean) => void;
};

type RoleFormState = {
  planner: string;
  worker: string;
  reviewer: string;
};

const EMPTY_ROLES: RoleFormState = {
  planner: "",
  worker: "",
  reviewer: "",
};

function normalizeAgentID(value: string): string | null {
  const trimmed = value.trim();
  return trimmed === "" ? null : trimmed;
}

export default function PipelineSettings({
  projectId,
  agents,
  initialRequireHumanReview,
  onRequireHumanReviewSaved,
}: PipelineSettingsProps) {
  const [roles, setRoles] = useState<RoleFormState>(EMPTY_ROLES);
  const [requireHumanReview, setRequireHumanReview] = useState(initialRequireHumanReview);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  useEffect(() => {
    setRequireHumanReview(initialRequireHumanReview);
  }, [initialRequireHumanReview]);

  useEffect(() => {
    let cancelled = false;

    async function loadRoles() {
      setIsLoading(true);
      setError(null);

      try {
        const payload = await apiFetch<PipelineRolesResponse>(
          `/api/projects/${encodeURIComponent(projectId)}/pipeline-roles`,
        );
        if (cancelled) {
          return;
        }
        setRoles({
          planner: payload.planner?.agentId ?? "",
          worker: payload.worker?.agentId ?? "",
          reviewer: payload.reviewer?.agentId ?? "",
        });
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to load pipeline settings");
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    }

    void loadRoles();
    return () => {
      cancelled = true;
    };
  }, [projectId]);

  const handleSave = async () => {
    setError(null);
    setSuccess(null);
    setIsSaving(true);
    let rolesSaved = false;

    try {
      await apiFetch<PipelineRolesResponse>(`/api/projects/${encodeURIComponent(projectId)}/pipeline-roles`, {
        method: "PUT",
        body: JSON.stringify({
          planner: { agentId: normalizeAgentID(roles.planner) },
          worker: { agentId: normalizeAgentID(roles.worker) },
          reviewer: { agentId: normalizeAgentID(roles.reviewer) },
        }),
      });
      rolesSaved = true;

      await apiFetch(`/api/projects/${encodeURIComponent(projectId)}`, {
        method: "PATCH",
        body: JSON.stringify({
          require_human_review: requireHumanReview,
        }),
      });

      onRequireHumanReviewSaved?.(requireHumanReview);
      setSuccess("Pipeline settings saved.");
    } catch (err) {
      if (rolesSaved) {
        setError("Roles saved, but failed to update human review setting.");
      } else {
        setError(err instanceof Error ? err.message : "Failed to save pipeline settings");
      }
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
      <h3 className="text-base font-semibold text-[var(--text)]">Pipeline</h3>
      <p className="mt-1 text-sm text-[var(--text-muted)]">
        Assign planner/worker/reviewer roles and control approval flow.
      </p>

      {isLoading ? (
        <p className="mt-4 text-sm text-[var(--text-muted)]">Loading pipeline settings...</p>
      ) : (
        <div className="mt-4 grid gap-4 md:grid-cols-3">
          <div>
            <label htmlFor="pipeline-role-planner" className="mb-1 block text-sm font-medium text-[var(--text)]">
              Planner role
            </label>
            <select
              id="pipeline-role-planner"
              value={roles.planner}
              onChange={(event) => setRoles((prev) => ({ ...prev, planner: event.target.value }))}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="">Manual (no agent)</option>
              {agents.map((agent) => (
                <option key={agent.id} value={agent.id}>
                  {agent.name}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label htmlFor="pipeline-role-worker" className="mb-1 block text-sm font-medium text-[var(--text)]">
              Worker role
            </label>
            <select
              id="pipeline-role-worker"
              value={roles.worker}
              onChange={(event) => setRoles((prev) => ({ ...prev, worker: event.target.value }))}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="">Manual (no agent)</option>
              {agents.map((agent) => (
                <option key={agent.id} value={agent.id}>
                  {agent.name}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label htmlFor="pipeline-role-reviewer" className="mb-1 block text-sm font-medium text-[var(--text)]">
              Reviewer role
            </label>
            <select
              id="pipeline-role-reviewer"
              value={roles.reviewer}
              onChange={(event) => setRoles((prev) => ({ ...prev, reviewer: event.target.value }))}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="">Manual (no agent)</option>
              {agents.map((agent) => (
                <option key={agent.id} value={agent.id}>
                  {agent.name}
                </option>
              ))}
            </select>
          </div>
        </div>
      )}

      <div className="mt-4">
        <label className="inline-flex items-center gap-2 text-sm text-[var(--text)]">
          <input
            type="checkbox"
            checked={requireHumanReview}
            onChange={(event) => setRequireHumanReview(event.target.checked)}
          />
          Require human approval before merge
        </label>
      </div>

      {error ? (
        <div className="mt-4 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-300">
          {error}
        </div>
      ) : null}
      {success ? (
        <div className="mt-4 rounded-lg border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
          {success}
        </div>
      ) : null}

      <button
        type="button"
        disabled={isSaving || isLoading}
        onClick={handleSave}
        className="mt-4 rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-medium text-white transition hover:bg-[#B8975B] disabled:cursor-not-allowed disabled:opacity-70"
      >
        {isSaving ? "Saving..." : "Save pipeline settings"}
      </button>
    </section>
  );
}
