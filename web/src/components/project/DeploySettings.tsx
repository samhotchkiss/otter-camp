import { useEffect, useState } from "react";
import { apiFetch } from "../../lib/api";

type DeployMethod = "none" | "github_push" | "cli_command";

type DeployConfigResponse = {
  deployMethod?: string;
  githubRepoUrl?: string | null;
  githubBranch?: string;
  cliCommand?: string | null;
};

type DeploySettingsProps = {
  projectId: string;
};

function normalizeMethod(value: string): DeployMethod {
  if (value === "github_push" || value === "cli_command") {
    return value;
  }
  return "none";
}

export default function DeploySettings({ projectId }: DeploySettingsProps) {
  const [deployMethod, setDeployMethod] = useState<DeployMethod>("none");
  const [githubRepoUrl, setGithubRepoUrl] = useState("");
  const [githubBranch, setGithubBranch] = useState("main");
  const [cliCommand, setCliCommand] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function loadConfig() {
      setIsLoading(true);
      setError(null);
      try {
        const payload = await apiFetch<DeployConfigResponse>(
          `/api/projects/${encodeURIComponent(projectId)}/deploy-config`,
        );
        if (cancelled) {
          return;
        }
        setDeployMethod(normalizeMethod(payload.deployMethod ?? "none"));
        setGithubRepoUrl(payload.githubRepoUrl ?? "");
        setGithubBranch((payload.githubBranch ?? "main").trim() || "main");
        setCliCommand(payload.cliCommand ?? "");
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to load deployment settings");
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    }

    void loadConfig();
    return () => {
      cancelled = true;
    };
  }, [projectId]);

  const handleSave = async () => {
    setError(null);
    setSuccess(null);

    const normalizedBranch = githubBranch.trim() || "main";
    const normalizedRepo = githubRepoUrl.trim();
    const normalizedCommand = cliCommand.trim();

    if (deployMethod === "cli_command" && normalizedCommand === "") {
      setError("CLI command is required for CLI command deploy mode.");
      return;
    }

    setIsSaving(true);
    try {
      const payload = await apiFetch<DeployConfigResponse>(`/api/projects/${encodeURIComponent(projectId)}/deploy-config`, {
        method: "PUT",
        body: JSON.stringify({
          deployMethod: deployMethod,
          githubRepoUrl: deployMethod === "github_push" ? (normalizedRepo || null) : null,
          githubBranch: normalizedBranch,
          cliCommand: deployMethod === "cli_command" ? normalizedCommand : null,
        }),
      });
      setDeployMethod(normalizeMethod(payload.deployMethod ?? deployMethod));
      setGithubRepoUrl(payload.githubRepoUrl ?? "");
      setGithubBranch((payload.githubBranch ?? "main").trim() || "main");
      setCliCommand(payload.cliCommand ?? "");
      setSuccess("Deployment settings saved.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save deployment settings");
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-5">
      <h3 className="text-base font-semibold text-[var(--text)]">Deployment</h3>
      <p className="mt-1 text-sm text-[var(--text-muted)]">
        Select how completed work is deployed for this project.
      </p>

      {isLoading ? (
        <p className="mt-4 text-sm text-[var(--text-muted)]">Loading deployment settings...</p>
      ) : (
        <div className="mt-4 space-y-4">
          <div>
            <label htmlFor="deploy-method" className="mb-1 block text-sm font-medium text-[var(--text)]">
              Deploy method
            </label>
            <select
              id="deploy-method"
              value={deployMethod}
              onChange={(event) => setDeployMethod(normalizeMethod(event.target.value))}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
            >
              <option value="none">None</option>
              <option value="github_push">Push to GitHub</option>
              <option value="cli_command">Run CLI command</option>
            </select>
            <p className="mt-1 text-xs text-[var(--text-muted)]">
              None disables automated deployment after merge.
            </p>
          </div>

          {deployMethod === "github_push" ? (
            <>
              <div>
                <label htmlFor="deploy-github-repo-url" className="mb-1 block text-sm font-medium text-[var(--text)]">
                  GitHub repo URL
                </label>
                <input
                  id="deploy-github-repo-url"
                  type="text"
                  value={githubRepoUrl}
                  onChange={(event) => setGithubRepoUrl(event.target.value)}
                  placeholder="https://github.com/org/repo"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
                />
              </div>
              <div>
                <label htmlFor="deploy-github-branch" className="mb-1 block text-sm font-medium text-[var(--text)]">
                  GitHub branch
                </label>
                <input
                  id="deploy-github-branch"
                  type="text"
                  value={githubBranch}
                  onChange={(event) => setGithubBranch(event.target.value)}
                  placeholder="main"
                  className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
                />
              </div>
            </>
          ) : null}

          {deployMethod === "cli_command" ? (
            <div>
              <label htmlFor="deploy-cli-command" className="mb-1 block text-sm font-medium text-[var(--text)]">
                CLI command
              </label>
              <input
                id="deploy-cli-command"
                type="text"
                value={cliCommand}
                onChange={(event) => setCliCommand(event.target.value)}
                placeholder="npx itsalive-co"
                className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)]"
              />
              <p className="mt-1 text-xs text-[var(--text-muted)]">
                This command runs on the project owner&apos;s OpenClaw environment.
              </p>
            </div>
          ) : null}
        </div>
      )}

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
        {isSaving ? "Saving..." : "Save deployment settings"}
      </button>
    </section>
  );
}
