import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import type { PipelineSettingsAgentOption } from "../components/project/PipelineSettings";
import { apiFetch } from "../lib/api";
import ProjectSettingsPage from "./project/ProjectSettingsPage";

type ProjectSettingsPayload = {
  id?: string;
  name?: string;
  primary_agent_id?: string | null;
  require_human_review?: boolean;
};

type AgentListPayload = {
  agents?: Array<{
    id?: string;
    name?: string;
  }>;
};

function normalizeError(error: unknown): string {
  if (error instanceof Error && error.message.trim() !== "") {
    return error.message;
  }
  return "Failed to load project settings";
}

function normalizeAgents(payload: AgentListPayload): PipelineSettingsAgentOption[] {
  const rows = Array.isArray(payload.agents) ? payload.agents : [];
  const normalized = rows
    .map((row) => {
      const id = (row.id ?? "").trim();
      const name = (row.name ?? "").trim();
      if (id === "") {
        return null;
      }
      return {
        id,
        name: name || id,
      };
    })
    .filter((row): row is PipelineSettingsAgentOption => row !== null);

  normalized.sort((left, right) => left.name.localeCompare(right.name));
  return normalized;
}

export default function ProjectSettingsRoutePage() {
  const { id: projectID = "" } = useParams<{ id?: string }>();
  const [projectName, setProjectName] = useState("Project");
  const [availableAgents, setAvailableAgents] = useState<PipelineSettingsAgentOption[]>([]);
  const [selectedPrimaryAgentID, setSelectedPrimaryAgentID] = useState("");
  const [requireHumanReview, setRequireHumanReview] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  const [isSavingGeneralSettings, setIsSavingGeneralSettings] = useState(false);
  const [generalError, setGeneralError] = useState<string | null>(null);
  const [generalSuccess, setGeneralSuccess] = useState<string | null>(null);

  const loadSettings = useCallback(async () => {
    if (!projectID) {
      setLoadError("Missing project id");
      setIsLoading(false);
      return;
    }

    setIsLoading(true);
    setLoadError(null);
    try {
      const [projectPayload, agentPayload] = await Promise.all([
        apiFetch<ProjectSettingsPayload>(`/api/projects/${encodeURIComponent(projectID)}`),
        apiFetch<AgentListPayload>("/api/agents"),
      ]);
      setProjectName((projectPayload.name ?? "").trim() || "Project");
      setSelectedPrimaryAgentID((projectPayload.primary_agent_id ?? "").trim());
      setRequireHumanReview(projectPayload.require_human_review === true);
      setAvailableAgents(normalizeAgents(agentPayload));
    } catch (error: unknown) {
      setLoadError(normalizeError(error));
    } finally {
      setIsLoading(false);
    }
  }, [projectID]);

  useEffect(() => {
    void loadSettings();
  }, [loadSettings, refreshKey]);

  const handlePrimaryAgentChange = (value: string) => {
    setSelectedPrimaryAgentID(value);
    setGeneralError(null);
    setGeneralSuccess(null);
  };

  const handleSaveGeneralSettings = async () => {
    if (!projectID) {
      setGeneralError("Missing project id");
      return;
    }

    setGeneralError(null);
    setGeneralSuccess(null);
    setIsSavingGeneralSettings(true);
    try {
      const payload = await apiFetch<ProjectSettingsPayload>(
        `/api/projects/${encodeURIComponent(projectID)}/settings`,
        {
          method: "PATCH",
          body: JSON.stringify({
            primary_agent_id: selectedPrimaryAgentID.trim() || null,
          }),
        },
      );
      const persistedPrimaryAgentID = (payload.primary_agent_id ?? "").trim();
      setSelectedPrimaryAgentID(persistedPrimaryAgentID);
      setGeneralSuccess("Project settings saved.");
    } catch (error: unknown) {
      setGeneralError(normalizeError(error));
    } finally {
      setIsSavingGeneralSettings(false);
    }
  };

  if (!projectID) {
    return (
      <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 p-4 text-sm text-rose-300">
        Missing project id.
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-[1280px] space-y-4">
      <nav className="flex items-center gap-2 text-sm text-[var(--text-muted)]">
        <Link to="/projects" className="hover:text-[var(--text)]">
          Projects
        </Link>
        <span>›</span>
        <Link to={`/projects/${encodeURIComponent(projectID)}`} className="hover:text-[var(--text)]">
          {projectName}
        </Link>
        <span>›</span>
        <span className="font-medium text-[var(--text)]">Settings</span>
      </nav>

      <header className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold text-[var(--text)]">Project Settings</h1>
            <p className="mt-1 text-sm text-[var(--text-muted)]">{projectName}</p>
          </div>
          <Link
            to={`/projects/${encodeURIComponent(projectID)}`}
            className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] hover:bg-[var(--surface)]"
          >
            Back to project
          </Link>
        </div>
      </header>

      {isLoading ? (
        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
          <p className="text-sm text-[var(--text-muted)]">Loading project settings...</p>
        </section>
      ) : null}

      {!isLoading && loadError ? (
        <section className="space-y-3 rounded-xl border border-rose-500/40 bg-rose-500/10 p-5">
          <p className="text-sm text-rose-300">{loadError}</p>
          <button
            type="button"
            onClick={() => setRefreshKey((current) => current + 1)}
            className="rounded-lg border border-rose-400/40 bg-rose-500/20 px-3 py-2 text-sm font-medium text-rose-200 hover:bg-rose-500/30"
          >
            Retry
          </button>
        </section>
      ) : null}

      {!isLoading && !loadError ? (
        <ProjectSettingsPage
          projectID={projectID}
          availableAgents={availableAgents}
          selectedPrimaryAgentID={selectedPrimaryAgentID}
          onPrimaryAgentChange={handlePrimaryAgentChange}
          onSaveGeneralSettings={() => void handleSaveGeneralSettings()}
          isSavingGeneralSettings={isSavingGeneralSettings}
          generalError={generalError}
          generalSuccess={generalSuccess}
          initialRequireHumanReview={requireHumanReview}
          onRequireHumanReviewSaved={setRequireHumanReview}
        />
      ) : null}
    </div>
  );
}
