import { useEffect, useMemo, useState } from "react";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark, oneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import { useNavigate } from "react-router-dom";
import MarkdownPreview from "../content-review/MarkdownPreview";
import { resolveEditorForPath } from "../content-review/editorModeResolver";
import ProjectCommitBrowser from "./ProjectCommitBrowser";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";
const ORG_STORAGE_KEY = "otter-camp-org-id";
const NO_REPO_CONFIGURED_MESSAGE = "No repository configured for this project";
const EMPTY_REPO_TREE_MESSAGE = "No files found in this repository yet.";

type ProjectFileBrowserProps = {
  projectId: string;
};

type BrowserMode = "files" | "commits";
type MarkdownViewMode = "render" | "source";

type ProjectTreeEntry = {
  name: string;
  type: "dir" | "file";
  path: string;
  size?: number;
};

type ProjectTreeResponse = {
  ref: string;
  path: string;
  entries: ProjectTreeEntry[];
};

type ProjectBlobResponse = {
  ref: string;
  path: string;
  content: string;
  size: number;
  encoding: "utf-8" | "base64";
};

function getOrgID(): string {
  try {
    return (localStorage.getItem(ORG_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

function normalizeAbsolutePath(input: string): string {
  const trimmed = input.trim();
  if (!trimmed || trimmed === "/") return "/";
  return `/${trimmed.replace(/^\/+/, "").replace(/\/+/g, "/")}`;
}

function formatBytes(bytes: number | undefined): string {
  if (typeof bytes !== "number" || Number.isNaN(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function detectLanguage(filePath: string): string {
  const lower = filePath.toLowerCase();
  if (lower.endsWith(".ts") || lower.endsWith(".tsx")) return "typescript";
  if (lower.endsWith(".js") || lower.endsWith(".jsx")) return "javascript";
  if (lower.endsWith(".py")) return "python";
  if (lower.endsWith(".go")) return "go";
  if (lower.endsWith(".json")) return "json";
  if (lower.endsWith(".sql")) return "sql";
  if (lower.endsWith(".sh")) return "bash";
  return "text";
}

function mimeTypeForPath(filePath: string): string {
  const lower = filePath.toLowerCase();
  if (lower.endsWith(".png")) return "image/png";
  if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) return "image/jpeg";
  if (lower.endsWith(".gif")) return "image/gif";
  if (lower.endsWith(".webp")) return "image/webp";
  if (lower.endsWith(".svg")) return "image/svg+xml";
  return "application/octet-stream";
}

function isNoRepositoryConfiguredError(message: string | null): boolean {
  if (!message) return false;
  const lower = message.toLowerCase();
  return (
    lower.includes("no repository configured") ||
    lower.includes("repository path is not configured") ||
    lower.includes("project has no local repo path configured")
  );
}

function normalizeTreeErrorMessage(message: string): string {
  if (isNoRepositoryConfiguredError(message)) {
    return NO_REPO_CONFIGURED_MESSAGE;
  }
  const lower = message.toLowerCase();
  if (lower.includes("ref or path not found")) {
    return EMPTY_REPO_TREE_MESSAGE;
  }
  return message;
}

export default function ProjectFileBrowser({ projectId }: ProjectFileBrowserProps) {
  const [mode, setMode] = useState<BrowserMode>("files");
  const [currentPath, setCurrentPath] = useState("/");
  const [entries, setEntries] = useState<ProjectTreeEntry[]>([]);
  const [treeLoading, setTreeLoading] = useState(true);
  const [treeError, setTreeError] = useState<string | null>(null);
  const [treeRefreshKey, setTreeRefreshKey] = useState(0);

  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null);
  const [blob, setBlob] = useState<ProjectBlobResponse | null>(null);
  const [blobLoading, setBlobLoading] = useState(false);
  const [blobError, setBlobError] = useState<string | null>(null);
  const [blobRefreshKey, setBlobRefreshKey] = useState(0);
  const [markdownViewMode, setMarkdownViewMode] = useState<MarkdownViewMode>("render");
  const [creatingIssue, setCreatingIssue] = useState(false);
  const [createIssueError, setCreateIssueError] = useState<string | null>(null);

  const navigate = useNavigate();
  const orgID = getOrgID();

  useEffect(() => {
    if (mode !== "files") return;
    if (!projectId || !orgID) {
      setEntries([]);
      setTreeError("Missing project or organization context");
      setTreeLoading(false);
      return;
    }

    let cancelled = false;
    setTreeLoading(true);
    setTreeError(null);

    async function loadTree() {
      try {
        const url = new URL(`${API_URL}/api/projects/${projectId}/tree`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("path", currentPath);

        const response = await fetch(url.toString(), {
          headers: { "Content-Type": "application/json" },
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load files");
        }
        const payload = (await response.json()) as ProjectTreeResponse;
        if (!cancelled) {
          setEntries(Array.isArray(payload.entries) ? payload.entries : []);
        }
      } catch (error) {
        if (!cancelled) {
          const message = error instanceof Error ? error.message : "Failed to load files";
          setTreeError(normalizeTreeErrorMessage(message));
        }
      } finally {
        if (!cancelled) {
          setTreeLoading(false);
        }
      }
    }

    void loadTree();
    return () => {
      cancelled = true;
    };
  }, [currentPath, mode, orgID, projectId, treeRefreshKey]);

  useEffect(() => {
    if (mode !== "files") return;
    if (!projectId || !orgID || !selectedFilePath) {
      setBlob(null);
      setBlobError(null);
      setBlobLoading(false);
      return;
    }
    const requestedPath = selectedFilePath;

    let cancelled = false;
    setBlobLoading(true);
    setBlobError(null);

    async function loadBlob() {
      try {
        const url = new URL(`${API_URL}/api/projects/${projectId}/blob`);
        url.searchParams.set("org_id", orgID);
        url.searchParams.set("path", requestedPath);
        const response = await fetch(url.toString(), {
          headers: { "Content-Type": "application/json" },
        });
        if (!response.ok) {
          const payload = await response.json().catch(() => null);
          throw new Error(payload?.error ?? "Failed to load file");
        }
        const payload = (await response.json()) as ProjectBlobResponse;
        if (!cancelled) {
          setBlob(payload);
        }
      } catch (error) {
        if (!cancelled) {
          setBlobError(error instanceof Error ? error.message : "Failed to load file");
        }
      } finally {
        if (!cancelled) {
          setBlobLoading(false);
        }
      }
    }

    void loadBlob();
    return () => {
      cancelled = true;
    };
  }, [blobRefreshKey, mode, orgID, projectId, selectedFilePath]);

  const breadcrumbs = useMemo(() => {
    const parts = currentPath.split("/").filter(Boolean);
    const crumbs: { label: string; path: string }[] = [{ label: "Root", path: "/" }];
    let assembled = "";
    for (const part of parts) {
      assembled += `/${part}`;
      crumbs.push({ label: part, path: assembled });
    }
    return crumbs;
  }, [currentPath]);

  const selectedResolution = useMemo(
    () => (selectedFilePath ? resolveEditorForPath(selectedFilePath) : null),
    [selectedFilePath],
  );
  const canCreateLinkedIssue = useMemo(
    () => Boolean(selectedFilePath && /^\/posts\/.+\.md$/i.test(selectedFilePath)),
    [selectedFilePath],
  );

  const prefersDark = useMemo(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia?.("(prefers-color-scheme: dark)")?.matches ?? false;
  }, []);

  useEffect(() => {
    setMarkdownViewMode("render");
    setCreateIssueError(null);
  }, [selectedFilePath]);

  function handleOpenEntry(entry: ProjectTreeEntry): void {
    if (entry.type === "dir") {
      const nextPath = normalizeAbsolutePath(entry.path.replace(/\/$/, ""));
      setCurrentPath(nextPath);
      return;
    }
    setSelectedFilePath(normalizeAbsolutePath(entry.path));
  }

  async function handleCreateIssueForFile(): Promise<void> {
    if (!projectId || !orgID || !selectedFilePath || !canCreateLinkedIssue) {
      return;
    }

    setCreatingIssue(true);
    setCreateIssueError(null);
    try {
      const url = new URL(`${API_URL}/api/projects/${projectId}/issues/link`);
      url.searchParams.set("org_id", orgID);
      const response = await fetch(url.toString(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ document_path: selectedFilePath }),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => null);
        throw new Error(payload?.error ?? "Failed to create linked issue");
      }
      const payload = (await response.json()) as { id: string };
      if (!payload.id) {
        throw new Error("Issue creation succeeded but response was missing id");
      }
      navigate(`/projects/${projectId}/issues/${payload.id}`);
    } catch (error) {
      setCreateIssueError(error instanceof Error ? error.message : "Failed to create linked issue");
    } finally {
      setCreatingIssue(false);
    }
  }

  if (mode === "commits") {
    return (
      <section className="space-y-3" data-testid="project-file-browser">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-[var(--text)]">Files</h3>
          <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-1">
            <button
              type="button"
              className="rounded-md px-3 py-1 text-xs font-medium text-[var(--text-muted)] hover:text-[var(--text)]"
              onClick={() => setMode("files")}
            >
              Files
            </button>
            <button
              type="button"
              className="rounded-md bg-[var(--surface)] px-3 py-1 text-xs font-medium text-[var(--text)]"
              onClick={() => setMode("commits")}
            >
              Commit history
            </button>
          </div>
        </div>
        <ProjectCommitBrowser projectId={projectId} />
      </section>
    );
  }

  return (
    <section className="space-y-3" data-testid="project-file-browser">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-[var(--text)]">Files</h3>
        <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-1">
          <button
            type="button"
            className="rounded-md bg-[var(--surface)] px-3 py-1 text-xs font-medium text-[var(--text)]"
            onClick={() => setMode("files")}
          >
            Files
          </button>
          <button
            type="button"
            className="rounded-md px-3 py-1 text-xs font-medium text-[var(--text-muted)] hover:text-[var(--text)]"
            onClick={() => setMode("commits")}
          >
            Commit history
          </button>
        </div>
      </div>

      <div className="grid gap-3 lg:grid-cols-[minmax(260px,320px)_1fr]">
        <aside className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-3">
          <div className="mb-2 flex flex-wrap gap-1 text-xs">
            {breadcrumbs.map((crumb, index) => (
              <button
                type="button"
                key={`${crumb.path}-${index}`}
                onClick={() => setCurrentPath(crumb.path)}
                className="rounded border border-[var(--border)] px-2 py-0.5 text-[var(--text-muted)] hover:bg-[var(--surface-alt)] hover:text-[var(--text)]"
              >
                {crumb.label}
              </button>
            ))}
          </div>

          {treeLoading && (
            <p className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text-muted)]">
              Loading files...
            </p>
          )}

          {!treeLoading && treeError && (
            isNoRepositoryConfiguredError(treeError) ? (
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2">
                <p className="text-sm text-[var(--text-muted)]">{NO_REPO_CONFIGURED_MESSAGE}</p>
              </div>
            ) : treeError === EMPTY_REPO_TREE_MESSAGE ? (
              <div className="space-y-2 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2">
                <p className="text-sm text-[var(--text-muted)]">{EMPTY_REPO_TREE_MESSAGE}</p>
                <button
                  type="button"
                  className="rounded border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--surface)] hover:text-[var(--text)]"
                  onClick={() => setTreeRefreshKey((value) => value + 1)}
                >
                  Retry
                </button>
              </div>
            ) : (
              <div className="space-y-2 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2">
                <p className="text-sm text-red-300">{treeError}</p>
                <button
                  type="button"
                  className="rounded border border-red-500/40 px-2 py-1 text-xs text-red-200 hover:bg-red-500/20"
                  onClick={() => setTreeRefreshKey((value) => value + 1)}
                >
                  Retry
                </button>
              </div>
            )
          )}

          {!treeLoading && !treeError && entries.length === 0 && (
            <p className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text-muted)]">
              No files found.
            </p>
          )}

          {!treeLoading && !treeError && entries.length > 0 && (
            <ul className="space-y-1">
              {entries.map((entry) => {
                const isActive = selectedFilePath === normalizeAbsolutePath(entry.path);
                return (
                  <li key={`${entry.type}-${entry.path}`}>
                    <button
                      type="button"
                      onClick={() => handleOpenEntry(entry)}
                      className={`flex w-full items-center justify-between rounded-lg border px-2 py-1.5 text-left text-sm ${
                        isActive
                          ? "border-[#C9A86C] bg-[#C9A86C]/20 text-[var(--text)]"
                          : "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text)] hover:bg-[var(--surface)]"
                      }`}
                    >
                      <span className="truncate">
                        {entry.type === "dir" ? "📁 " : "📄 "}
                        {entry.name}
                      </span>
                      {entry.type === "file" && (
                        <span className="ml-2 text-[11px] text-[var(--text-muted)]">
                          {formatBytes(entry.size)}
                        </span>
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </aside>

        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-3">
          {!selectedFilePath && (
            <p className="text-sm text-[var(--text-muted)]">
              Select a file from the left panel to preview it.
            </p>
          )}
          {selectedFilePath && (
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <p className="truncate text-sm font-medium text-[var(--text)]">{selectedFilePath}</p>
                <div className="flex items-center gap-2">
                  {canCreateLinkedIssue && (
                    <button
                      type="button"
                      className="rounded border border-[#C9A86C] bg-[#C9A86C]/20 px-2 py-1 text-xs text-[#C9A86C] hover:bg-[#C9A86C]/30 disabled:opacity-60"
                      onClick={() => void handleCreateIssueForFile()}
                      disabled={creatingIssue}
                    >
                      {creatingIssue ? "Creating issue..." : "Create issue for this file"}
                    </button>
                  )}
                  <button
                    type="button"
                    className="rounded border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)] hover:bg-[var(--surface-alt)]"
                    onClick={() => setBlobRefreshKey((value) => value + 1)}
                  >
                    Reload
                  </button>
                </div>
              </div>

              {createIssueError && (
                <div className="rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-300">
                  {createIssueError}
                </div>
              )}

              {blobLoading && (
                <p className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text-muted)]">
                  Loading file...
                </p>
              )}

              {!blobLoading && blobError && (
                <div className="space-y-2 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2">
                  <p className="text-sm text-red-300">{blobError}</p>
                  <button
                    type="button"
                    className="rounded border border-red-500/40 px-2 py-1 text-xs text-red-200 hover:bg-red-500/20"
                    onClick={() => setBlobRefreshKey((value) => value + 1)}
                  >
                    Retry
                  </button>
                </div>
              )}

              {!blobLoading && !blobError && blob?.encoding === "utf-8" && selectedResolution?.editorMode === "markdown" && (
                <div className="space-y-3">
                  <div className="inline-flex rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-1">
                    <button
                      type="button"
                      className={`rounded-md px-3 py-1 text-xs font-medium ${
                        markdownViewMode === "render"
                          ? "bg-[var(--surface)] text-[var(--text)]"
                          : "text-[var(--text-muted)] hover:text-[var(--text)]"
                      }`}
                      onClick={() => setMarkdownViewMode("render")}
                    >
                      Render
                    </button>
                    <button
                      type="button"
                      className={`rounded-md px-3 py-1 text-xs font-medium ${
                        markdownViewMode === "source"
                          ? "bg-[var(--surface)] text-[var(--text)]"
                          : "text-[var(--text-muted)] hover:text-[var(--text)]"
                      }`}
                      onClick={() => setMarkdownViewMode("source")}
                    >
                      Source
                    </button>
                  </div>
                  {markdownViewMode === "render" ? (
                    <div data-testid="file-markdown-render">
                      <MarkdownPreview markdown={blob.content} />
                    </div>
                  ) : (
                    <pre
                      className="max-h-[560px] overflow-auto rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3 text-xs text-[var(--text)]"
                      data-testid="file-markdown-source"
                    >
                      {blob.content}
                    </pre>
                  )}
                </div>
              )}

              {!blobLoading && !blobError && blob?.encoding === "utf-8" && selectedResolution?.editorMode === "code" && (
                <div
                  className="overflow-hidden rounded-lg border border-[var(--border)] bg-[var(--surface-alt)]"
                  data-testid="file-code-preview"
                >
                  <SyntaxHighlighter
                    language={detectLanguage(selectedFilePath)}
                    style={prefersDark ? oneDark : oneLight}
                    customStyle={{ margin: 0, borderRadius: 0, minHeight: "120px" }}
                  >
                    {blob.content}
                  </SyntaxHighlighter>
                </div>
              )}

              {!blobLoading && !blobError && blob?.encoding === "utf-8" && (selectedResolution?.editorMode === "text" || !selectedResolution) && (
                <pre className="max-h-[560px] overflow-auto rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3 text-xs text-[var(--text)]">
                  {blob.content}
                </pre>
              )}

              {!blobLoading && !blobError && blob?.encoding === "base64" && selectedResolution?.editorMode === "image" && (
                <div className="space-y-2 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                  <img
                    src={`data:${mimeTypeForPath(selectedFilePath)};base64,${blob.content}`}
                    alt={selectedFilePath}
                    className="max-h-[560px] w-full rounded-lg border border-[var(--border)] object-contain"
                    data-testid="file-image-preview"
                  />
                  <p className="text-[11px] text-[var(--text-muted)]">{formatBytes(blob.size)}</p>
                </div>
              )}

              {!blobLoading && !blobError && blob && (
                (blob.encoding === "utf-8" && selectedResolution?.editorMode === "image") ||
                (blob.encoding === "base64" && selectedResolution?.editorMode !== "image")
              ) && (
                <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-300">
                  Unable to render file preview for this payload.
                </div>
              )}
            </div>
          )}
        </section>
      </div>
    </section>
  );
}
