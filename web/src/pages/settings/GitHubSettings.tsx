import { useCallback, useEffect, useMemo, useState } from "react";

type GitHubConnection = {
  installationId: number;
  accountLogin: string;
  accountType: string;
  connectedAt: string;
};

type GitHubRepo = {
  id: string;
  fullName: string;
  private: boolean;
  defaultBranch: string;
};

type Project = {
  id: string;
  name: string;
  description: string | null;
};

type SyncMode = "push" | "sync";
type WorkflowMode = "github_pr_sync" | "local_issue_review";

type ProjectSyncSettings = {
  enabled: boolean;
  repoFullName: string | null;
  branch: string;
  mode: SyncMode;
  autoSync: boolean;
  activeBranches: string[];
  workflowMode: WorkflowMode;
  githubPREnabled: boolean;
};

type SyncState = "idle" | "syncing" | "error";

type SyncStatus = {
  state: SyncState;
  lastSyncAt: string | null;
  message?: string;
};

type PublishCheck = {
  name: string;
  status: "pass" | "fail" | "info";
  detail: string;
  blocking: boolean;
};

type PublishResponse = {
  project_id: string;
  dry_run: boolean;
  status: "dry_run" | "blocked" | "no_changes" | "published";
  checks: PublishCheck[];
  local_head_sha?: string | null;
  remote_head_sha?: string | null;
  commits_ahead: number;
  published_at?: string | null;
  force_push_required?: boolean;
  force_push_confirmed?: boolean;
};

type PublishRunState = {
  running: null | "dry_run" | "publish";
  lastDryRun: PublishResponse | null;
  lastPublish: PublishResponse | null;
  logs: string[];
  error: string | null;
};

type IntegrationStatusPayload = {
  connected: boolean;
  installation?: {
    installation_id: number;
    account_login: string;
    account_type: string;
    connected_at: string;
  };
};

type RepoListPayload = {
  repos: Array<{
    id: string;
    full_name: string;
    default_branch: string;
    private: boolean;
  }>;
};

type SettingsListPayload = {
  projects: Array<{
    project_id: string;
    project_name: string;
    description: string | null;
    enabled: boolean;
    repo_full_name?: string | null;
    default_branch: string;
    sync_mode: SyncMode;
    auto_sync: boolean;
    active_branches?: string[];
    last_synced_at?: string | null;
    conflict_state?: string;
    force_push_required?: boolean;
    workflow_mode?: WorkflowMode;
    github_pr_enabled?: boolean;
  }>;
};

const API_URL = import.meta.env.VITE_API_URL || "";

const DEFAULT_PROJECT_SETTINGS: ProjectSyncSettings = {
  enabled: false,
  repoFullName: null,
  branch: "main",
  mode: "sync",
  autoSync: true,
  activeBranches: [],
  workflowMode: "github_pr_sync",
  githubPREnabled: true,
};

const DEFAULT_PUBLISH_STATE: PublishRunState = {
  running: null,
  lastDryRun: null,
  lastPublish: null,
  logs: [],
  error: null,
};

function formatDateTime(iso: string): string {
  return new Intl.DateTimeFormat("en-US", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(iso));
}

function buildRepoUrl(fullName: string): string {
  return `https://github.com/${fullName}`;
}

function publishLogLine(message: string): string {
  const stamp = new Intl.DateTimeFormat("en-US", { timeStyle: "medium" }).format(new Date());
  return `[${stamp}] ${message}`;
}

function publishFailureGuidance(errorMessage: string): string {
  const normalized = errorMessage.toLowerCase();
  if (normalized.includes("unresolved sync conflicts")) {
    return "Resolve sync conflicts in project settings, then retry publish.";
  }
  if (normalized.includes("publish push failed")) {
    return "Run Sync now, verify branch state, and retry publish.";
  }
  if (normalized.includes("local repo path")) {
    return "Verify local clone setup before publishing again.";
  }
  return "Run a dry run first and resolve any blocking checks before retrying publish.";
}

function getRequestHeaders(): Record<string, string> {
  const token = localStorage.getItem("otter_camp_token");
  const orgId = localStorage.getItem("otter-camp-org-id");

  return {
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(orgId ? { "X-Org-ID": orgId } : {}),
  };
}

async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      ...getRequestHeaders(),
      ...(init?.headers || {}),
    },
  });

  if (!response.ok) {
    const payload = (await response.json().catch(() => null)) as
      | { error?: string; message?: string }
      | null;
    throw new Error(payload?.error || payload?.message || `Request failed (${response.status})`);
  }

  return response.json() as Promise<T>;
}

function extractErrorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  if (typeof error === "string") {
    return error;
  }
  return "Failed to load GitHub settings";
}

function isRecoverableAuthLoadError(error: unknown): boolean {
  const message = extractErrorMessage(error).toLowerCase();
  return (
    message.includes("invalid session token") ||
    message.includes("missing authentication") ||
    message.includes("workspace mismatch") ||
    message.includes("forbidden") ||
    message.includes("request failed (401)") ||
    message.includes("request failed (403)")
  );
}

type ButtonProps = {
  children: React.ReactNode;
  onClick?: React.MouseEventHandler<HTMLButtonElement>;
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  loading?: boolean;
  type?: "button" | "submit";
};

function Button({
  children,
  onClick,
  variant = "primary",
  disabled,
  loading,
  type = "button",
}: ButtonProps) {
  const baseClasses =
    "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium shadow-sm transition focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-50";

  const variantClasses = {
    primary:
      "bg-emerald-500 text-white hover:bg-emerald-600 focus:ring-emerald-500",
    secondary:
      "border border-[var(--border)] bg-[var(--surface)] text-[var(--text)] hover:bg-[var(--surface-alt)] focus:ring-[var(--accent)]",
    danger: "bg-red-500 text-white hover:bg-red-600 focus:ring-red-500",
  };

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled || loading}
      className={`${baseClasses} ${variantClasses[variant]}`}
    >
      {loading && (
        <svg className="h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle
            className="opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            strokeWidth="4"
          />
          <path
            className="opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
          />
        </svg>
      )}
      {children}
    </button>
  );
}

type ToggleProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label?: string;
  disabled?: boolean;
};

function Toggle({ checked, onChange, label, disabled }: ToggleProps) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-3">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`relative h-6 w-11 rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--accent)] focus:ring-offset-2 focus:ring-offset-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-50 ${
          checked ? "bg-emerald-500" : "bg-[var(--border)]"
        }`}
      >
        <span
          className={`absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-[var(--surface)] shadow-sm transition-transform ${
            checked ? "translate-x-5" : "translate-x-0"
          }`}
        />
      </button>
      {label && (
        <span className="text-sm text-[var(--text)]">{label}</span>
      )}
    </label>
  );
}

type SelectProps = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  children: React.ReactNode;
  helperText?: string;
};

function Select({ label, value, onChange, disabled, children, helperText }: SelectProps) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-[var(--text)]">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="mt-1 block w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 text-[var(--text)] shadow-sm transition focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:cursor-not-allowed disabled:bg-[var(--surface-alt)] disabled:text-[var(--text-muted)]"
      >
        {children}
      </select>
      {helperText && <p className="mt-1.5 text-xs text-[var(--text-muted)]">{helperText}</p>}
    </label>
  );
}

type InputProps = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  helperText?: string;
};

function Input({ label, value, onChange, placeholder, disabled, helperText }: InputProps) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-[var(--text)]">{label}</span>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        className="mt-1 block w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] px-4 py-2.5 text-[var(--text)] shadow-sm transition placeholder:text-[var(--text-muted)] focus:border-[var(--accent)] focus:outline-none focus:ring-1 focus:ring-[var(--accent)] disabled:cursor-not-allowed disabled:bg-[var(--surface-alt)] disabled:text-[var(--text-muted)]"
      />
      {helperText && <p className="mt-1.5 text-xs text-[var(--text-muted)]">{helperText}</p>}
    </label>
  );
}

function StatusBadge({ tone, label }: { tone: "neutral" | "success" | "warning" | "danger" | "info"; label: string }) {
  const toneClasses: Record<string, string> = {
    neutral: "bg-[var(--surface-alt)] text-[var(--text)]",
    success: "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200",
    warning: "bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200",
    danger: "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-200",
    info: "bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-200",
  };

  return (
    <span className={`inline-flex items-center rounded-full px-3 py-1 text-xs font-medium ${toneClasses[tone]}`}>
      {label}
    </span>
  );
}

export default function GitHubSettings() {
  const [connection, setConnection] = useState<GitHubConnection | null>(null);
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [projectSettings, setProjectSettings] = useState<Record<string, ProjectSyncSettings>>({});
  const [projectStatus, setProjectStatus] = useState<Record<string, SyncStatus>>({});
  const [projects, setProjects] = useState<Project[]>([]);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [refreshingRepos, setRefreshingRepos] = useState(false);
  const [savingProjectId, setSavingProjectId] = useState<string | null>(null);
  const [syncingProjectId, setSyncingProjectId] = useState<string | null>(null);
  const [publishingProjectId, setPublishingProjectId] = useState<string | null>(null);
  const [publishStateByProject, setPublishStateByProject] = useState<Record<string, PublishRunState>>({});

  const repoOptions = useMemo(() => {
    const sorted = [...repos];
    sorted.sort((a, b) => a.fullName.localeCompare(b.fullName));
    return sorted;
  }, [repos]);

  const hydrateFromApi = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [statusPayload, reposPayload, settingsPayload] = await Promise.all([
        apiRequest<IntegrationStatusPayload>("/api/github/integration/status"),
        apiRequest<RepoListPayload>("/api/github/integration/repos"),
        apiRequest<SettingsListPayload>("/api/github/integration/settings"),
      ]);

      if (statusPayload.connected && statusPayload.installation) {
        setConnection({
          installationId: statusPayload.installation.installation_id,
          accountLogin: statusPayload.installation.account_login,
          accountType: statusPayload.installation.account_type,
          connectedAt: statusPayload.installation.connected_at,
        });
      } else {
        setConnection(null);
      }

      setRepos(
        (reposPayload.repos || []).map((repo) => ({
          id: repo.id,
          fullName: repo.full_name,
          defaultBranch: repo.default_branch || "main",
          private: Boolean(repo.private),
        }))
      );

      const projectRows = settingsPayload.projects || [];
      setProjects(
        projectRows.map((row) => ({
          id: row.project_id,
          name: row.project_name,
          description: row.description ?? null,
        }))
      );

      const nextSettings: Record<string, ProjectSyncSettings> = {};
      const nextStatus: Record<string, SyncStatus> = {};

      for (const row of projectRows) {
        nextSettings[row.project_id] = {
          enabled: row.enabled,
          repoFullName: row.repo_full_name ?? null,
          branch: row.default_branch || "main",
          mode: row.sync_mode || "sync",
          autoSync: row.auto_sync,
          activeBranches: row.active_branches || [],
          workflowMode: row.workflow_mode || "github_pr_sync",
          githubPREnabled: typeof row.github_pr_enabled === "boolean" ? row.github_pr_enabled : true,
        };

        const conflict = row.conflict_state || "none";
        nextStatus[row.project_id] = {
          state: conflict === "needs_decision" ? "error" : "idle",
          lastSyncAt: row.last_synced_at ?? null,
          message:
            conflict === "needs_decision"
              ? "Sync conflict needs decision"
              : row.last_synced_at
                ? "In sync"
                : "Not synced yet",
        };
      }

      setProjectSettings(nextSettings);
      setProjectStatus(nextStatus);
    } catch (err) {
      if (isRecoverableAuthLoadError(err)) {
        setConnection(null);
        setRepos([]);
        setProjects([]);
        setProjectSettings({});
        setProjectStatus({});
        setError(null);
      } else {
        setError(extractErrorMessage(err));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    hydrateFromApi();
  }, [hydrateFromApi]);

  const updateProjectSettings = useCallback(
    (projectId: string, patch: Partial<ProjectSyncSettings>) => {
      setProjectSettings((prev) => ({
        ...prev,
        [projectId]: {
          ...DEFAULT_PROJECT_SETTINGS,
          ...prev[projectId],
          ...patch,
        },
      }));
    },
    []
  );

  const handleConnect = useCallback(async () => {
    setConnecting(true);
    setError(null);
    try {
      const payload = await apiRequest<{ install_url: string }>("/api/github/connect/start", {
        method: "POST",
      });

      if (payload.install_url) {
        window.open(payload.install_url, "_blank", "noopener,noreferrer");
      }

      await hydrateFromApi();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to start GitHub connect flow");
    } finally {
      setConnecting(false);
    }
  }, [hydrateFromApi]);

  const handleDisconnect = useCallback(async () => {
    setDisconnecting(true);
    setError(null);
    try {
      await apiRequest<{ disconnected: boolean }>("/api/github/integration/connection", {
        method: "DELETE",
      });
      await hydrateFromApi();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to disconnect GitHub");
    } finally {
      setDisconnecting(false);
    }
  }, [hydrateFromApi]);

  const handleRefreshRepos = useCallback(async () => {
    setRefreshingRepos(true);
    setError(null);
    try {
      const payload = await apiRequest<RepoListPayload>("/api/github/integration/repos");
      setRepos(
        (payload.repos || []).map((repo) => ({
          id: repo.id,
          fullName: repo.full_name,
          defaultBranch: repo.default_branch || "main",
          private: Boolean(repo.private),
        }))
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to refresh repositories");
    } finally {
      setRefreshingRepos(false);
    }
  }, []);

  const handleRepoSelection = useCallback(
    (projectId: string, repoFullName: string | null) => {
      const selected = repos.find((repo) => repo.fullName === repoFullName);
      updateProjectSettings(projectId, {
        repoFullName,
        branch: selected?.defaultBranch ?? "main",
      });
    },
    [repos, updateProjectSettings]
  );

  const handleSaveProjectSettings = useCallback(
    async (projectId: string) => {
      const settings = projectSettings[projectId] ?? DEFAULT_PROJECT_SETTINGS;
      setSavingProjectId(projectId);
      setError(null);
      try {
        await apiRequest(`/api/github/integration/settings/${encodeURIComponent(projectId)}`, {
          method: "PUT",
          body: JSON.stringify({
            enabled: settings.enabled,
            repo_full_name: settings.repoFullName,
            default_branch: settings.branch,
            sync_mode: settings.mode,
            auto_sync: settings.autoSync,
            active_branches: settings.activeBranches,
          }),
        });

        setProjectStatus((prev) => ({
          ...prev,
          [projectId]: {
            state: "idle",
            lastSyncAt: prev[projectId]?.lastSyncAt ?? null,
            message: "Saved",
          },
        }));
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to save project settings");
      } finally {
        setSavingProjectId(null);
      }
    },
    [projectSettings]
  );

  const handleSyncNow = useCallback(async (projectId: string) => {
    setSyncingProjectId(projectId);
    setError(null);
    setProjectStatus((prev) => ({
      ...prev,
      [projectId]: {
        state: "syncing",
        lastSyncAt: prev[projectId]?.lastSyncAt ?? null,
        message: "Syncing…",
      },
    }));

    try {
      await apiRequest(`/api/projects/${encodeURIComponent(projectId)}/repo/sync`, {
        method: "POST",
      });

      setProjectStatus((prev) => ({
        ...prev,
        [projectId]: {
          state: "idle",
          lastSyncAt: new Date().toISOString(),
          message: "Sync job queued",
        },
      }));
    } catch (err) {
      setProjectStatus((prev) => ({
        ...prev,
        [projectId]: {
          state: "error",
          lastSyncAt: prev[projectId]?.lastSyncAt ?? null,
          message: "Sync failed",
        },
      }));
      setError(err instanceof Error ? err.message : "Failed to trigger sync");
    } finally {
      setSyncingProjectId(null);
    }
  }, []);

  const updatePublishState = useCallback(
    (projectId: string, updater: (current: PublishRunState) => PublishRunState) => {
      setPublishStateByProject((prev) => {
        const current = prev[projectId] ?? DEFAULT_PUBLISH_STATE;
        return {
          ...prev,
          [projectId]: updater(current),
        };
      });
    },
    []
  );

  const runPublish = useCallback(
    async (projectId: string, dryRun: boolean) => {
      const runMode: PublishRunState["running"] = dryRun ? "dry_run" : "publish";
      setPublishingProjectId(projectId);
      setError(null);
      updatePublishState(projectId, (current) => ({
        ...current,
        running: runMode,
        error: null,
        logs: [...current.logs, publishLogLine(dryRun ? "Starting dry run." : "Starting publish.")].slice(-20),
      }));

      try {
        const response = await apiRequest<PublishResponse>(
          `/api/projects/${encodeURIComponent(projectId)}/publish`,
          {
            method: "POST",
            body: JSON.stringify({ dry_run: dryRun }),
          }
        );

        const checkLines = response.checks.map((check) =>
          publishLogLine(
            `${check.status.toUpperCase()} ${check.name}: ${check.detail}${check.blocking ? " (blocking)" : ""}`
          )
        );
        const nextLogs = [
          ...checkLines,
          publishLogLine(`Publish API status: ${response.status}`),
        ];
        if (response.status === "blocked") {
          nextLogs.push(
            publishLogLine("Action required: resolve blocking checks, then retry publish.")
          );
        }
        if (response.status === "published" && response.published_at) {
          nextLogs.push(publishLogLine(`Published at ${formatDateTime(response.published_at)}.`));
        }

        updatePublishState(projectId, (current) => ({
          ...current,
          running: null,
          error: null,
          lastDryRun: dryRun ? response : current.lastDryRun,
          lastPublish: dryRun ? current.lastPublish : response,
          logs: [...current.logs, ...nextLogs].slice(-20),
        }));

        if (!dryRun) {
          setProjectStatus((prev) => ({
            ...prev,
            [projectId]: {
              state: response.status === "blocked" ? "error" : "idle",
              lastSyncAt: new Date().toISOString(),
              message:
                response.status === "published"
                  ? "Published"
                  : response.status === "blocked"
                    ? "Publish blocked"
                    : "Publish complete",
            },
          }));
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : "Publish request failed";
        const guidance = publishFailureGuidance(message);
        setError(message);
        updatePublishState(projectId, (current) => ({
          ...current,
          running: null,
          error: message,
          logs: [...current.logs, publishLogLine(`Publish failed: ${message}`), publishLogLine(guidance)].slice(-20),
        }));
      } finally {
        setPublishingProjectId(null);
      }
    },
    [updatePublishState]
  );

  const handlePublishDryRun = useCallback(
    (projectId: string) => {
      void runPublish(projectId, true);
    },
    [runPublish]
  );

  const handlePublishExecute = useCallback(
    (projectId: string) => {
      void runPublish(projectId, false);
    },
    [runPublish]
  );

  const configuredCount = useMemo(() => {
    return projects.filter((project) => {
      const settings = projectSettings[project.id] ?? DEFAULT_PROJECT_SETTINGS;
      return Boolean(connection && settings.enabled && settings.repoFullName);
    }).length;
  }, [connection, projectSettings, projects]);

  const lastSuccessfulSync = useMemo(() => {
    const timestamps = Object.values(projectStatus)
      .filter((status) => status.state === "idle" && status.lastSyncAt)
      .map((status) => status.lastSyncAt as string);
    if (timestamps.length === 0) return null;
    timestamps.sort();
    return timestamps[timestamps.length - 1] ?? null;
  }, [projectStatus]);

  if (loading) {
    return (
      <section className="overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-sm">
        <p className="text-sm text-[var(--text-muted)]">Loading GitHub settings…</p>
      </section>
    );
  }

  return (
    <section className="overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-sm backdrop-blur">
      <div className="border-b border-[var(--border)] px-6 py-4">
        <div className="flex items-center gap-3">
          <span className="text-2xl" aria-hidden="true">
            🐙
          </span>
          <div>
            <h2 className="text-lg font-semibold text-[var(--text)]">GitHub</h2>
            <p className="mt-0.5 text-sm text-[var(--text-muted)]">
              Connect your GitHub App installation and map repos to projects.
            </p>
          </div>
        </div>
      </div>

      <div className="space-y-8 p-6">
        {error && (
          <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-900/20 dark:text-red-200">
            {error}
          </div>
        )}

        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 items-center justify-center rounded-full bg-[var(--surface-alt)] text-sm font-semibold text-[var(--text-muted)]">
                GH
              </div>
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <p className="font-medium text-[var(--text)]">
                    {connection ? `@${connection.accountLogin}` : "Not connected"}
                  </p>
                  {connection ? <StatusBadge tone="success" label="Connected" /> : <StatusBadge tone="neutral" label="Disconnected" />}
                </div>
                <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                  {connection
                    ? `Installation #${connection.installationId} (${connection.accountType})`
                    : "Connect GitHub to configure project sync settings."}
                </p>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
              {connection ? (
                <>
                  <Button variant="secondary" onClick={handleRefreshRepos} loading={refreshingRepos}>
                    Refresh repos
                  </Button>
                  <Button variant="danger" onClick={handleDisconnect} loading={disconnecting}>
                    Disconnect
                  </Button>
                </>
              ) : (
                <Button onClick={handleConnect} loading={connecting}>
                  Connect GitHub
                </Button>
              )}
            </div>
          </div>

          {connection && (
            <div className="mt-4 flex flex-wrap items-center gap-3 text-xs text-[var(--text-muted)]">
              <span>Connected {formatDateTime(connection.connectedAt)}</span>
              {lastSuccessfulSync && (
                <>
                  <span aria-hidden="true">•</span>
                  <span>Last sync {formatDateTime(lastSuccessfulSync)}</span>
                </>
              )}
            </div>
          )}
        </div>

        <div>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-[var(--text)]">Available repositories</h3>
            <Button variant="secondary" disabled={!connection} onClick={handleRefreshRepos} loading={refreshingRepos}>
              Refresh
            </Button>
          </div>

          {connection ? (
            repos.length > 0 ? (
              <div className="mt-3 divide-y divide-[var(--border)]/60 rounded-lg border border-[var(--border)]">
                {repoOptions.map((repo) => (
                  <div key={repo.id} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                      <a href={buildRepoUrl(repo.fullName)} target="_blank" rel="noreferrer" className="font-medium text-[var(--text)] hover:underline">
                        {repo.fullName}
                      </a>
                      <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                        Default branch: <span className="font-mono">{repo.defaultBranch}</span>
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      {repo.private ? <StatusBadge tone="warning" label="Private" /> : <StatusBadge tone="neutral" label="Public" />}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="mt-3 rounded-lg border border-dashed border-[var(--border)] bg-[var(--surface-alt)] px-6 py-8 text-center">
                <p className="text-sm text-[var(--text-muted)]">No repositories found for this installation.</p>
              </div>
            )
          ) : (
            <div className="mt-3 rounded-lg border border-dashed border-[var(--border)] bg-[var(--surface-alt)] px-6 py-8 text-center">
              <p className="text-sm text-[var(--text-muted)]">Connect GitHub to view repositories</p>
            </div>
          )}
        </div>

        <div>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h3 className="text-sm font-medium text-[var(--text)]">Project sync</h3>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                {configuredCount} of {projects.length} projects configured
              </p>
            </div>
            <div className="text-xs text-[var(--text-muted)]">Sync state is backed by the GitHub integration APIs.</div>
          </div>

          {projects.length === 0 ? (
            <div className="mt-3 rounded-lg border border-dashed border-[var(--border)] bg-[var(--surface-alt)] px-6 py-8 text-center">
              <p className="text-sm text-[var(--text-muted)]">No projects found</p>
            </div>
          ) : (
            <div className="mt-3 divide-y divide-[var(--border)]/60 rounded-lg border border-[var(--border)]">
              {projects.map((project) => {
                const settings = projectSettings[project.id] ?? DEFAULT_PROJECT_SETTINGS;
                const status = projectStatus[project.id];
                const publishState = publishStateByProject[project.id] ?? DEFAULT_PUBLISH_STATE;
                const latestPublishSummary = publishState.lastPublish ?? publishState.lastDryRun;
                const lastSyncAt = status?.lastSyncAt ?? null;
                const modeLabel =
                  settings.workflowMode === "local_issue_review"
                    ? "Issue Review (local only)"
                    : "GitHub PR Sync";

                const badge = (() => {
                  if (!connection) return <StatusBadge tone="neutral" label="Disconnected" />;
                  if (!settings.enabled) return <StatusBadge tone="neutral" label="Disabled" />;
                  if (!settings.repoFullName) return <StatusBadge tone="warning" label="Select repo" />;
                  if (status?.state === "syncing") return <StatusBadge tone="info" label="Syncing" />;
                  if (status?.state === "error") return <StatusBadge tone="danger" label="Needs attention" />;
                  return <StatusBadge tone="success" label="Ready" />;
                })();

                return (
                  <details key={project.id} className="group">
                    <summary className="flex cursor-pointer list-none items-center justify-between gap-4 px-4 py-4 hover:bg-[var(--surface-alt)] [&::-webkit-details-marker]:hidden">
                      <div className="min-w-0">
                        <p className="truncate font-medium text-[var(--text)]">{project.name}</p>
                        <p className="mt-0.5 truncate text-xs text-[var(--text-muted)]">
                          {settings.repoFullName ? (
                            <>
                              Linked to <span className="font-mono">{settings.repoFullName}</span>
                            </>
                          ) : (
                            "No repository linked"
                          )}
                          {settings.repoFullName ? <> • {modeLabel}</> : null}
                          {lastSyncAt ? <> • Last sync {formatDateTime(lastSyncAt)}</> : null}
                        </p>
                      </div>

                      <div className="flex shrink-0 flex-wrap items-center gap-3">
                        {badge}
                        <Button
                          variant="secondary"
                          disabled={!connection || !settings.enabled || !settings.repoFullName}
                          loading={syncingProjectId === project.id}
                          onClick={(e) => {
                            e.preventDefault();
                            void handleSyncNow(project.id);
                          }}
                        >
                          Sync now
                        </Button>
                      </div>
                    </summary>

                    <div className="border-t border-[var(--border)] bg-[var(--surface-alt)] px-4 py-4">
                      <div className="grid gap-4 md:grid-cols-2">
                        <Toggle
                          checked={settings.enabled}
                          onChange={(enabled) => updateProjectSettings(project.id, { enabled })}
                          label="Enable GitHub sync"
                          disabled={!connection}
                        />

                        <Select
                          label="Repository"
                          value={settings.repoFullName ?? ""}
                          onChange={(value) => handleRepoSelection(project.id, value || null)}
                          disabled={!connection || !settings.enabled}
                          helperText={settings.repoFullName ? "Repo is linked to this project" : "Select which repo this project syncs with"}
                        >
                          <option value="">Not linked</option>
                          {repoOptions.map((repo) => (
                            <option key={repo.id} value={repo.fullName}>
                              {repo.fullName}
                              {repo.private ? " (private)" : ""}
                            </option>
                          ))}
                        </Select>

                        <Input
                          label="Branch"
                          value={settings.branch}
                          onChange={(value) => updateProjectSettings(project.id, { branch: value })}
                          disabled={!connection || !settings.enabled}
                          placeholder="main"
                          helperText="Default branch for sync/publish"
                        />

                        <Select
                          label="Mode"
                          value={settings.mode}
                          onChange={(value) => {
                            const mode = value as SyncMode;
                            updateProjectSettings(project.id, {
                              mode,
                              workflowMode:
                                mode === "push" ? "local_issue_review" : "github_pr_sync",
                              githubPREnabled: mode !== "push",
                            });
                          }}
                          disabled={!connection || !settings.enabled}
                          helperText={
                            settings.mode === "push"
                              ? "Local issue-review mode: GitHub PR creation is disabled."
                              : "GitHub PR sync mode: PR operations remain enabled."
                          }
                        >
                          <option value="sync">GitHub PR Sync</option>
                          <option value="push">Issue Review (local only)</option>
                        </Select>

                        <Toggle
                          checked={settings.autoSync}
                          onChange={(autoSync) => updateProjectSettings(project.id, { autoSync })}
                          label="Auto-sync on webhook updates"
                          disabled={!connection || !settings.enabled}
                        />
                      </div>

                      <div className="mt-4 flex items-center justify-end">
                        <Button
                          variant="primary"
                          loading={savingProjectId === project.id}
                          onClick={() => void handleSaveProjectSettings(project.id)}
                          disabled={!connection}
                        >
                          Save settings
                        </Button>
                      </div>

                      <div className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--surface)] p-4">
                        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                          <div>
                            <h4 className="text-sm font-semibold text-[var(--text)]">Publish</h4>
                            <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                              Run preflight checks and publish commits to GitHub.
                            </p>
                          </div>
                          <div className="flex flex-wrap items-center gap-2">
                            <Button
                              variant="secondary"
                              loading={publishingProjectId === project.id && publishState.running === "dry_run"}
                              disabled={!connection || !settings.enabled || !settings.repoFullName}
                              onClick={() => handlePublishDryRun(project.id)}
                            >
                              Dry run
                            </Button>
                            <Button
                              variant="primary"
                              loading={publishingProjectId === project.id && publishState.running === "publish"}
                              disabled={!connection || !settings.enabled || !settings.repoFullName}
                              onClick={() => handlePublishExecute(project.id)}
                            >
                              Publish
                            </Button>
                          </div>
                        </div>

                        {latestPublishSummary && (
                          <div className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-3">
                            <p className="text-xs font-medium text-[var(--text)]">
                              Dry-run summary
                            </p>
                            <p className="mt-1 text-xs text-[var(--text-muted)]">
                              Status: <span className="font-semibold">{latestPublishSummary.status}</span> • Commits ahead:{" "}
                              {latestPublishSummary.commits_ahead}
                            </p>
                            {publishState.lastPublish?.published_at && (
                              <p className="mt-1 text-xs text-emerald-700 dark:text-emerald-300">
                                Published at {formatDateTime(publishState.lastPublish.published_at)}
                              </p>
                            )}
                            <ul className="mt-2 space-y-1 text-xs text-[var(--text-muted)]">
                              {latestPublishSummary.checks.map((check) => (
                                <li key={`${project.id}-${check.name}`}>
                                  <span className="font-semibold">{check.name}</span>: {check.detail} •{" "}
                                  {check.blocking ? "Blocking" : "Non-blocking"}
                                </li>
                              ))}
                            </ul>
                            {latestPublishSummary.status === "blocked" && (
                              <p className="mt-2 text-xs text-amber-700 dark:text-amber-300">
                                Action required: resolve blocking checks, run sync/conflict resolution, then retry publish.
                              </p>
                            )}
                          </div>
                        )}

                        {publishState.error && (
                          <div className="mt-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/40 dark:bg-red-900/20 dark:text-red-200">
                            <p className="font-semibold">Publish failed</p>
                            <p className="mt-1">{publishFailureGuidance(publishState.error)}</p>
                          </div>
                        )}

                        {publishState.logs.length > 0 && (
                          <div className="mt-3">
                            <p className="text-xs font-medium text-[var(--text)]">Publish progress log</p>
                            <ul className="mt-1 space-y-1 rounded-md border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-xs text-[var(--text-muted)]">
                              {publishState.logs.map((line, index) => (
                                <li key={`${project.id}-publish-log-${index}`}>{line}</li>
                              ))}
                            </ul>
                          </div>
                        )}
                      </div>
                    </div>
                  </details>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
