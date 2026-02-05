import { useCallback, useEffect, useMemo, useRef, useState } from "react";

type GitHubConnection = {
  username: string;
  avatarUrl: string | null;
  connectedAt: string; // ISO
  scopes: string[];
};

type GitHubRepo = {
  id: string;
  fullName: string; // owner/name
  private: boolean;
  defaultBranch: string;
};

type Project = {
  id: string;
  name: string;
  description: string | null;
  repoUrl: string | null;
};

type SyncMode = "push" | "sync";

type ProjectSyncSettings = {
  enabled: boolean;
  repoFullName: string | null;
  branch: string;
  mode: SyncMode;
  autoSync: boolean;
};

type SyncState = "idle" | "syncing" | "error";

type SyncStatus = {
  state: SyncState;
  lastSyncAt: string | null; // ISO
  message?: string;
};

type PersistedGitHubSettings = {
  connection: GitHubConnection | null;
  repos: GitHubRepo[];
  projectSettings: Record<string, ProjectSyncSettings>;
  projectStatus: Record<string, SyncStatus>;
};

const STORAGE_KEY = "otter-camp-github-settings-v1";

const SAMPLE_CONNECTION: GitHubConnection = {
  username: "octocat",
  avatarUrl: "https://github.com/octocat.png?size=80",
  connectedAt: new Date().toISOString(),
  scopes: ["read:user", "repo"],
};

const SAMPLE_REPOS: GitHubRepo[] = [
  {
    id: "r1",
    fullName: "otter-camp/otter-camp",
    private: false,
    defaultBranch: "main",
  },
  {
    id: "r2",
    fullName: "otter-camp/infra",
    private: true,
    defaultBranch: "main",
  },
  {
    id: "r3",
    fullName: "samhotchkiss/dotfiles",
    private: false,
    defaultBranch: "master",
  },
];

const SAMPLE_PROJECTS: Project[] = [
  {
    id: "p1",
    name: "Otter Camp",
    description: "Task management for AI-assisted workflows",
    repoUrl: null,
  },
  {
    id: "p2",
    name: "Pearl Proxy",
    description: "Memory and routing infrastructure",
    repoUrl: null,
  },
  {
    id: "p3",
    name: "ItsAlive",
    description: "Static site deployment platform",
    repoUrl: null,
  },
];

const DEFAULT_PROJECT_SETTINGS: ProjectSyncSettings = {
  enabled: false,
  repoFullName: null,
  branch: "main",
  mode: "sync",
  autoSync: true,
};

function safeJsonParse<T>(value: string | null): T | null {
  if (!value) return null;
  try {
    return JSON.parse(value) as T;
  } catch {
    return null;
  }
}

function formatDateTime(iso: string): string {
  return new Intl.DateTimeFormat("en-US", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(iso));
}

function buildRepoUrl(fullName: string): string {
  return `https://github.com/${fullName}`;
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
    "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium shadow-sm transition focus:outline-none focus:ring-2 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-slate-900";

  const variantClasses = {
    primary:
      "bg-emerald-500 text-white hover:bg-emerald-600 focus:ring-emerald-500",
    secondary:
      "border border-slate-200 bg-white text-slate-700 hover:bg-slate-50 focus:ring-slate-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700",
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
        className={`relative h-6 w-11 rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-slate-900 ${
          checked ? "bg-emerald-500" : "bg-slate-200 dark:bg-slate-700"
        }`}
      >
        <span
          className={`absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow-sm transition-transform ${
            checked ? "translate-x-5" : "translate-x-0"
          }`}
        />
      </button>
      {label && (
        <span className="text-sm text-slate-700 dark:text-slate-300">
          {label}
        </span>
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

function Select({
  label,
  value,
  onChange,
  disabled,
  children,
  helperText,
}: SelectProps) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-slate-700 dark:text-slate-300">
        {label}
      </span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-slate-900 shadow-sm transition focus:border-emerald-500 focus:outline-none focus:ring-1 focus:ring-emerald-500 disabled:cursor-not-allowed disabled:bg-slate-50 disabled:text-slate-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:focus:border-emerald-500 dark:disabled:bg-slate-900"
      >
        {children}
      </select>
      {helperText && (
        <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
          {helperText}
        </p>
      )}
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

function Input({
  label,
  value,
  onChange,
  placeholder,
  disabled,
  helperText,
}: InputProps) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-slate-700 dark:text-slate-300">
        {label}
      </span>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-slate-900 shadow-sm transition placeholder:text-slate-400 focus:border-emerald-500 focus:outline-none focus:ring-1 focus:ring-emerald-500 disabled:cursor-not-allowed disabled:bg-slate-50 disabled:text-slate-500 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:placeholder:text-slate-500 dark:focus:border-emerald-500 dark:disabled:bg-slate-900"
      />
      {helperText && (
        <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
          {helperText}
        </p>
      )}
    </label>
  );
}

function StatusBadge({
  tone,
  label,
}: {
  tone: "neutral" | "success" | "warning" | "danger" | "info";
  label: string;
}) {
  const toneClasses: Record<typeof tone, string> = {
    neutral: "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200",
    success:
      "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200",
    warning:
      "bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200",
    danger: "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-200",
    info: "bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-200",
  };

  return (
    <span
      className={`inline-flex items-center rounded-full px-3 py-1 text-xs font-medium ${toneClasses[tone]}`}
    >
      {label}
    </span>
  );
}

function parseProjects(data: unknown): Project[] {
  const candidates = Array.isArray(data)
    ? data
    : typeof data === "object" && data !== null && "projects" in data
      ? (data as { projects: unknown }).projects
      : [];

  if (!Array.isArray(candidates)) return [];

  return candidates
    .map((candidate) => {
      if (typeof candidate !== "object" || candidate === null) return null;
      const obj = candidate as Record<string, unknown>;
      const id = typeof obj.id === "string" ? obj.id : null;
      const name = typeof obj.name === "string" ? obj.name : null;
      if (!id || !name) return null;

      const description =
        typeof obj.description === "string" ? obj.description : null;
      const repoUrl =
        typeof obj.repoUrl === "string"
          ? obj.repoUrl
          : typeof obj.repo_url === "string"
            ? obj.repo_url
            : null;

      return { id, name, description, repoUrl };
    })
    .filter((project): project is Project => project !== null);
}

export default function GitHubSettings() {
  const [connection, setConnection] = useState<GitHubConnection | null>(null);
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [projectSettings, setProjectSettings] = useState<
    Record<string, ProjectSyncSettings>
  >({});
  const [projectStatus, setProjectStatus] = useState<Record<string, SyncStatus>>(
    {}
  );

  const [projects, setProjects] = useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);

  const [connecting, setConnecting] = useState(false);
  const [refreshingRepos, setRefreshingRepos] = useState(false);

  const syncTimersRef = useRef<Record<string, number>>({});

  const repoOptions = useMemo(() => {
    const sorted = [...repos].sort((a, b) => a.fullName.localeCompare(b.fullName));
    return sorted;
  }, [repos]);

  // Load persisted settings
  useEffect(() => {
    const persisted = safeJsonParse<PersistedGitHubSettings>(
      localStorage.getItem(STORAGE_KEY)
    );
    if (!persisted) return;

    setConnection(persisted.connection ?? null);
    setRepos(Array.isArray(persisted.repos) ? persisted.repos : []);
    setProjectSettings(
      persisted.projectSettings && typeof persisted.projectSettings === "object"
        ? persisted.projectSettings
        : {}
    );
    setProjectStatus(
      persisted.projectStatus && typeof persisted.projectStatus === "object"
        ? persisted.projectStatus
        : {}
    );
  }, []);

  // Persist settings locally (placeholder until backend is wired)
  useEffect(() => {
    const toPersist: PersistedGitHubSettings = {
      connection,
      repos,
      projectSettings,
      projectStatus,
    };
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(toPersist));
    } catch {
      // Ignore persistence errors (e.g. private mode / storage disabled)
    }
  }, [connection, projectSettings, projectStatus, repos]);

  // Load projects (falls back to sample data for demo)
  useEffect(() => {
    let canceled = false;

    const loadProjects = async () => {
      setProjectsLoading(true);
      try {
        const response = await fetch("/api/projects");
        if (!response.ok) throw new Error("Failed to fetch projects");
        const data: unknown = await response.json();
        const parsed = parseProjects(data);
        if (!canceled) {
          setProjects(parsed.length > 0 ? parsed : SAMPLE_PROJECTS);
        }
      } catch {
        if (!canceled) setProjects(SAMPLE_PROJECTS);
      } finally {
        if (!canceled) setProjectsLoading(false);
      }
    };

    loadProjects();

    return () => {
      canceled = true;
    };
  }, []);

  // Ensure each known project has an entry
  useEffect(() => {
    if (projects.length === 0) return;
    setProjectSettings((prev) => {
      let changed = false;
      const next: Record<string, ProjectSyncSettings> = { ...prev };
      for (const project of projects) {
        if (!next[project.id]) {
          next[project.id] = { ...DEFAULT_PROJECT_SETTINGS };
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [projects]);

  // Cleanup sync timers on unmount
  useEffect(() => {
    return () => {
      for (const timeoutId of Object.values(syncTimersRef.current)) {
        window.clearTimeout(timeoutId);
      }
    };
  }, []);

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
    try {
      // OAuth flow placeholder — replace with real backend integration.
      await new Promise((resolve) => window.setTimeout(resolve, 600));
      setConnection({ ...SAMPLE_CONNECTION, connectedAt: new Date().toISOString() });
      setRepos(SAMPLE_REPOS);
    } finally {
      setConnecting(false);
    }
  }, []);

  const handleDisconnect = useCallback(() => {
    setConnection(null);
    setRepos([]);
  }, []);

  const handleRefreshRepos = useCallback(async () => {
    if (!connection) return;
    setRefreshingRepos(true);
    try {
      // API placeholder — replace with /api/integrations/github/repos, etc.
      await new Promise((resolve) => window.setTimeout(resolve, 500));
      setRepos((prev) => (prev.length > 0 ? prev : SAMPLE_REPOS));
    } finally {
      setRefreshingRepos(false);
    }
  }, [connection]);

  const handleRepoSelection = useCallback(
    (projectId: string, repoFullName: string | null) => {
      const selectedRepo = repos.find((repo) => repo.fullName === repoFullName);
      updateProjectSettings(projectId, {
        repoFullName,
        branch: selectedRepo?.defaultBranch ?? "main",
      });
    },
    [repos, updateProjectSettings]
  );

  const handleSyncNow = useCallback(
    (projectId: string) => {
      const settings = projectSettings[projectId] ?? DEFAULT_PROJECT_SETTINGS;
      if (!connection || !settings.enabled || !settings.repoFullName) return;

      setProjectStatus((prev) => ({
        ...prev,
        [projectId]: {
          state: "syncing",
          lastSyncAt: prev[projectId]?.lastSyncAt ?? null,
          message: "Syncing…",
        },
      }));

      const existing = syncTimersRef.current[projectId];
      if (existing) window.clearTimeout(existing);

      syncTimersRef.current[projectId] = window.setTimeout(() => {
        setProjectStatus((prev) => ({
          ...prev,
          [projectId]: {
            state: "idle",
            lastSyncAt: new Date().toISOString(),
            message: "In sync",
          },
        }));
      }, 1400);
    },
    [connection, projectSettings]
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
    const sorted = timestamps.sort();
    return sorted[sorted.length - 1] ?? null;
  }, [projectStatus]);

  return (
    <section className="overflow-hidden rounded-2xl border border-slate-200 bg-white/90 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/90">
      <div className="border-b border-slate-200 px-6 py-4 dark:border-slate-800">
        <div className="flex items-center gap-3">
          <span className="text-2xl" aria-hidden="true">
            🐙
          </span>
          <div>
            <h2 className="text-lg font-semibold text-slate-900 dark:text-white">
              GitHub
            </h2>
            <p className="mt-0.5 text-sm text-slate-500 dark:text-slate-400">
              Connect your account, link repositories, and sync projects
            </p>
          </div>
        </div>
      </div>

      <div className="space-y-8 p-6">
        {/* Connection */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-950/20">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-3">
              {connection?.avatarUrl ? (
                <img
                  src={connection.avatarUrl}
                  alt="GitHub avatar"
                  loading="lazy"
                  decoding="async"
                  className="h-10 w-10 rounded-full object-cover"
                />
              ) : (
                <div className="flex h-10 w-10 items-center justify-center rounded-full bg-slate-200 text-sm font-semibold text-slate-600 dark:bg-slate-700 dark:text-slate-200">
                  GH
                </div>
              )}
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <p className="font-medium text-slate-900 dark:text-white">
                    {connection ? `@${connection.username}` : "Not connected"}
                  </p>
                  {connection ? (
                    <StatusBadge tone="success" label="Connected" />
                  ) : (
                    <StatusBadge tone="neutral" label="Disconnected" />
                  )}
                </div>
                <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                  OAuth flow placeholder — settings are stored locally until the
                  server integration is wired.
                </p>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
              {connection ? (
                <>
                  <Button variant="secondary" onClick={handleRefreshRepos} loading={refreshingRepos}>
                    Refresh repos
                  </Button>
                  <Button variant="danger" onClick={handleDisconnect}>
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
            <div className="mt-4 flex flex-wrap items-center gap-3 text-xs text-slate-500 dark:text-slate-400">
              <span>
                Connected {formatDateTime(connection.connectedAt)}
              </span>
              <span aria-hidden="true">•</span>
              <span>Scopes: {connection.scopes.join(", ")}</span>
              {lastSuccessfulSync && (
                <>
                  <span aria-hidden="true">•</span>
                  <span>Last sync {formatDateTime(lastSuccessfulSync)}</span>
                </>
              )}
            </div>
          )}
        </div>

        {/* Connected repos */}
        <div>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-slate-700 dark:text-slate-300">
              Connected repositories
            </h3>
            <Button
              variant="secondary"
              disabled={!connection}
              onClick={handleRefreshRepos}
              loading={refreshingRepos}
            >
              Refresh
            </Button>
          </div>

          {connection ? (
            repos.length > 0 ? (
              <div className="mt-3 divide-y divide-slate-100 rounded-lg border border-slate-200 dark:divide-slate-800 dark:border-slate-700">
                {repoOptions.map((repo) => (
                  <div
                    key={repo.id}
                    className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div>
                      <a
                        href={buildRepoUrl(repo.fullName)}
                        target="_blank"
                        rel="noreferrer"
                        className="font-medium text-slate-900 hover:underline dark:text-white"
                      >
                        {repo.fullName}
                      </a>
                      <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                        Default branch: <span className="font-mono">{repo.defaultBranch}</span>
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      {repo.private ? (
                        <StatusBadge tone="warning" label="Private" />
                      ) : (
                        <StatusBadge tone="neutral" label="Public" />
                      )}
                      <StatusBadge tone="success" label="Connected" />
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="mt-3 rounded-lg border border-dashed border-slate-200 bg-slate-50 px-6 py-8 text-center dark:border-slate-700 dark:bg-slate-800/50">
                <p className="text-sm text-slate-500 dark:text-slate-400">
                  No repositories connected yet
                </p>
                <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
                  Use “Refresh” to load repositories (placeholder).
                </p>
              </div>
            )
          ) : (
            <div className="mt-3 rounded-lg border border-dashed border-slate-200 bg-slate-50 px-6 py-8 text-center dark:border-slate-700 dark:bg-slate-800/50">
              <p className="text-sm text-slate-500 dark:text-slate-400">
                Connect GitHub to view repositories
              </p>
            </div>
          )}
        </div>

        {/* Project sync settings */}
        <div>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h3 className="text-sm font-medium text-slate-700 dark:text-slate-300">
                Project sync
              </h3>
              <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                {configuredCount} of {projects.length} projects configured
              </p>
            </div>
            <div className="text-xs text-slate-500 dark:text-slate-400">
              Sync status is simulated until the backend is implemented.
            </div>
          </div>

          {projectsLoading ? (
            <div className="mt-4 flex items-center gap-3 text-sm text-slate-500 dark:text-slate-400">
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
              Loading projects…
            </div>
          ) : projects.length === 0 ? (
            <div className="mt-3 rounded-lg border border-dashed border-slate-200 bg-slate-50 px-6 py-8 text-center dark:border-slate-700 dark:bg-slate-800/50">
              <p className="text-sm text-slate-500 dark:text-slate-400">
                No projects found
              </p>
            </div>
          ) : (
            <div className="mt-3 divide-y divide-slate-100 rounded-lg border border-slate-200 dark:divide-slate-800 dark:border-slate-700">
              {projects.map((project) => {
                const settings =
                  projectSettings[project.id] ?? DEFAULT_PROJECT_SETTINGS;

                const computedBadge = (() => {
                  if (!connection) return <StatusBadge tone="neutral" label="Disconnected" />;
                  if (!settings.enabled) return <StatusBadge tone="neutral" label="Disabled" />;
                  if (!settings.repoFullName) return <StatusBadge tone="warning" label="Select repo" />;

                  const status = projectStatus[project.id];
                  if (!status) return <StatusBadge tone="info" label="Ready" />;
                  if (status.state === "syncing") return <StatusBadge tone="info" label="Syncing" />;
                  if (status.state === "error") return <StatusBadge tone="danger" label="Error" />;
                  return <StatusBadge tone="success" label="In sync" />;
                })();

                const lastSyncAt = projectStatus[project.id]?.lastSyncAt ?? null;

                return (
                  <details key={project.id} className="group">
                    <summary className="flex cursor-pointer list-none items-center justify-between gap-4 px-4 py-4 hover:bg-slate-50 dark:hover:bg-slate-800/50 [&::-webkit-details-marker]:hidden">
                      <div className="min-w-0">
                        <p className="truncate font-medium text-slate-900 dark:text-white">
                          {project.name}
                        </p>
                        <p className="mt-0.5 truncate text-xs text-slate-500 dark:text-slate-400">
                          {settings.repoFullName ? (
                            <>
                              Linked to{" "}
                              <span className="font-mono">{settings.repoFullName}</span>
                            </>
                          ) : (
                            "No repository linked"
                          )}
                          {lastSyncAt ? (
                            <>
                              {" "}
                              • Last sync {formatDateTime(lastSyncAt)}
                            </>
                          ) : null}
                        </p>
                      </div>

                      <div className="flex shrink-0 flex-wrap items-center gap-3">
                        {computedBadge}
                        <Button
                          variant="secondary"
                          disabled={!connection || !settings.enabled || !settings.repoFullName}
                          onClick={(e) => {
                            e.preventDefault();
                            handleSyncNow(project.id);
                          }}
                        >
                          Sync now
                        </Button>
                      </div>
                    </summary>

                    <div className="border-t border-slate-200 bg-slate-50/60 px-4 py-4 dark:border-slate-800 dark:bg-slate-900/40">
                      <div className="grid gap-4 md:grid-cols-2">
                        <Toggle
                          checked={settings.enabled}
                          onChange={(enabled) =>
                            updateProjectSettings(project.id, { enabled })
                          }
                          label="Enable GitHub sync"
                          disabled={!connection}
                        />

                        <Select
                          label="Repository"
                          value={settings.repoFullName ?? ""}
                          onChange={(value) =>
                            handleRepoSelection(project.id, value || null)
                          }
                          disabled={!connection || !settings.enabled}
                          helperText={
                            settings.repoFullName
                              ? "Repo is linked to this project"
                              : "Select which repo this project syncs with"
                          }
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
                          onChange={(branch) =>
                            updateProjectSettings(project.id, { branch })
                          }
                          disabled={!connection || !settings.enabled || !settings.repoFullName}
                          placeholder="main"
                          helperText="Used for pushes and sync operations"
                        />

                        <Select
                          label="Mode"
                          value={settings.mode}
                          onChange={(mode) =>
                            updateProjectSettings(project.id, {
                              mode: mode as SyncMode,
                            })
                          }
                          disabled={!connection || !settings.enabled || !settings.repoFullName}
                          helperText={
                            settings.mode === "sync"
                              ? "Two-way sync (project ↔ GitHub)"
                              : "Push only (project → GitHub)"
                          }
                        >
                          <option value="sync">Two-way sync</option>
                          <option value="push">Push only</option>
                        </Select>

                        <div className="md:col-span-2">
                          <Toggle
                            checked={settings.autoSync}
                            onChange={(autoSync) =>
                              updateProjectSettings(project.id, { autoSync })
                            }
                            label="Auto-sync in background"
                            disabled={!connection || !settings.enabled || !settings.repoFullName}
                          />
                          {projectStatus[project.id]?.message ? (
                            <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
                              Status: {projectStatus[project.id]?.message}
                            </p>
                          ) : null}
                          {settings.repoFullName ? (
                            <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
                              Repo link:{" "}
                              <a
                                href={buildRepoUrl(settings.repoFullName)}
                                target="_blank"
                                rel="noreferrer"
                                className="font-mono text-slate-700 hover:underline dark:text-slate-200"
                              >
                                {buildRepoUrl(settings.repoFullName)}
                              </a>
                            </p>
                          ) : null}
                        </div>
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
