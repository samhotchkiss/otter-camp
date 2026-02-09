import PipelineSettings, { type PipelineSettingsAgentOption } from "../../components/project/PipelineSettings";

type ProjectSettingsPageProps = {
  projectID: string;
  availableAgents: PipelineSettingsAgentOption[];
  selectedPrimaryAgentID: string;
  onPrimaryAgentChange: (value: string) => void;
  onSaveGeneralSettings: () => void;
  isSavingGeneralSettings: boolean;
  generalError?: string | null;
  generalSuccess?: string | null;
  initialRequireHumanReview: boolean;
  onRequireHumanReviewSaved?: (value: boolean) => void;
};

export default function ProjectSettingsPage({
  projectID,
  availableAgents,
  selectedPrimaryAgentID,
  onPrimaryAgentChange,
  onSaveGeneralSettings,
  isSavingGeneralSettings,
  generalError = null,
  generalSuccess = null,
  initialRequireHumanReview,
  onRequireHumanReviewSaved,
}: ProjectSettingsPageProps) {
  return (
    <div className="space-y-5">
      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
        <h2 className="text-base font-semibold text-[var(--text)]">General</h2>
        <p className="mt-1 text-sm text-[var(--text-muted)]">Manage core project defaults.</p>

        <div className="mt-4 max-w-xl">
          <label htmlFor="primary-agent" className="mb-2 block text-sm font-medium text-[var(--text)]">
            Primary Agent
          </label>
          <p className="mb-2 text-xs text-[var(--text-muted)]">
            This agent is used as the default owner for project chat routing.
          </p>
          <select
            id="primary-agent"
            value={selectedPrimaryAgentID}
            onChange={(event) => onPrimaryAgentChange(event.target.value)}
            className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
          >
            <option value="">No primary agent</option>
            {availableAgents.map((agent) => (
              <option key={agent.id} value={agent.id}>
                {agent.name}
              </option>
            ))}
          </select>
        </div>

        {generalError ? (
          <div className="mt-4 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-300">
            {generalError}
          </div>
        ) : null}
        {generalSuccess ? (
          <div className="mt-4 rounded-lg border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
            {generalSuccess}
          </div>
        ) : null}

        <button
          type="button"
          disabled={isSavingGeneralSettings}
          onClick={onSaveGeneralSettings}
          className="mt-4 rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-medium text-white transition hover:bg-[#B8975B] disabled:cursor-not-allowed disabled:opacity-70"
        >
          {isSavingGeneralSettings ? "Saving..." : "Save general settings"}
        </button>
      </section>

      <PipelineSettings
        projectId={projectID}
        agents={availableAgents}
        initialRequireHumanReview={initialRequireHumanReview}
        onRequireHumanReviewSaved={onRequireHumanReviewSaved}
      />
    </div>
  );
}
