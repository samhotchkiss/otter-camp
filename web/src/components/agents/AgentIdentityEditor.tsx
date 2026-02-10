import { useEffect, useMemo, useState } from "react";
import DocumentWorkspace from "../content-review/DocumentWorkspace";
import { apiFetch } from "../../lib/api";

type AgentIdentityEditorProps = {
  agentID: string;
};

type AgentTreeEntry = {
  name: string;
  type: "file" | "dir";
  path: string;
};

type AgentFilesResponse = {
  project_id?: string;
  ref?: string;
  entries?: AgentTreeEntry[];
};

type AgentBlobResponse = {
  ref?: string;
  path?: string;
  content?: string;
  encoding?: string;
  size?: number;
};

type ProjectCommitCreateResponse = {
  commit?: {
    sha?: string;
  };
};

function encodePathForURL(path: string): string {
  return path
    .split("/")
    .filter(Boolean)
    .map((segment) => encodeURIComponent(segment))
    .join("/");
}

function decodeBlobContent(blob: AgentBlobResponse | null): string {
  if (!blob || typeof blob.content !== "string") {
    return "";
  }
  if (blob.encoding === "utf-8" || !blob.encoding) {
    return blob.content;
  }
  if (blob.encoding === "base64") {
    try {
      return atob(blob.content);
    } catch {
      return "";
    }
  }
  return blob.content;
}

export default function AgentIdentityEditor({ agentID }: AgentIdentityEditorProps) {
  const [files, setFiles] = useState<AgentTreeEntry[]>([]);
  const [agentFilesProjectID, setAgentFilesProjectID] = useState<string>("");
  const [filesRef, setFilesRef] = useState<string>("");
  const [selectedPath, setSelectedPath] = useState<string>("");
  const [blob, setBlob] = useState<AgentBlobResponse | null>(null);
  const [draftContent, setDraftContent] = useState<string>("");
  const [isLoadingFiles, setIsLoadingFiles] = useState(true);
  const [isLoadingBlob, setIsLoadingBlob] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [saveMessage, setSaveMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      if (!agentID) {
        setFiles([]);
        setAgentFilesProjectID("");
        setIsLoadingFiles(false);
        return;
      }
      setIsLoadingFiles(true);
      setError(null);
      try {
        const payload = await apiFetch<AgentFilesResponse>(`/api/admin/agents/${encodeURIComponent(agentID)}/files`);
        if (cancelled) {
          return;
        }
        const identityFiles = (payload.entries || [])
          .filter((entry) => entry.type === "file" && !entry.path.startsWith("memory/"))
          .sort((a, b) => a.path.localeCompare(b.path));
        setFiles(identityFiles);
        setAgentFilesProjectID(String(payload.project_id || "").trim());
        setFilesRef(payload.ref || "");
        const preferred = identityFiles.find((entry) => entry.path.toUpperCase() === "SOUL.MD");
        setSelectedPath(preferred?.path || identityFiles[0]?.path || "");
      } catch (loadError) {
        if (!cancelled) {
          const message = loadError instanceof Error ? loadError.message : "Failed to load identity files";
          setError(message);
          setFiles([]);
          setAgentFilesProjectID("");
          setSelectedPath("");
        }
      } finally {
        if (!cancelled) {
          setIsLoadingFiles(false);
        }
      }
    };

    void load();
    return () => {
      cancelled = true;
    };
  }, [agentID]);

  useEffect(() => {
    let cancelled = false;
    const loadBlob = async () => {
      if (!agentID || !selectedPath) {
        setBlob(null);
        return;
      }
      setIsLoadingBlob(true);
      setError(null);
      try {
        const encodedPath = encodePathForURL(selectedPath);
        const payload = await apiFetch<AgentBlobResponse>(
          `/api/admin/agents/${encodeURIComponent(agentID)}/files/${encodedPath}`,
        );
        if (!cancelled) {
          setBlob(payload);
        }
      } catch (loadError) {
        if (!cancelled) {
          const message = loadError instanceof Error ? loadError.message : "Failed to load identity file";
          setError(message);
          setBlob(null);
        }
      } finally {
        if (!cancelled) {
          setIsLoadingBlob(false);
        }
      }
    };
    void loadBlob();
    return () => {
      cancelled = true;
    };
  }, [agentID, selectedPath]);

  const content = useMemo(() => decodeBlobContent(blob), [blob]);

  useEffect(() => {
    setDraftContent(content);
    setSaveMessage(null);
    setSaveError(null);
  }, [content, selectedPath]);

  const handleSave = async () => {
    if (!agentID || !selectedPath) {
      return;
    }
    setIsSaving(true);
    setSaveError(null);
    setSaveMessage(null);
    const nextContent = draftContent;

    try {
      const projectID = agentFilesProjectID;
      if (!projectID) {
        throw new Error("Agent Files project is not configured");
      }

      const commitPayload = await apiFetch<ProjectCommitCreateResponse>(
        `/api/projects/${encodeURIComponent(projectID)}/commits`,
        {
        method: "POST",
        body: JSON.stringify({
          path: `/agents/${agentID}/${selectedPath}`,
          content: nextContent,
          commit_subject: "Update agent identity file",
          commit_type: "system",
        }),
      },
      );
      const nextRef = String(commitPayload.commit?.sha || "").trim();
      if (nextRef) {
        setFilesRef(nextRef);
      }
      setBlob((previous) => ({
        ...(previous || {}),
        ref: nextRef || previous?.ref || filesRef,
        content: nextContent,
        encoding: "utf-8",
        size: new Blob([nextContent]).size,
      }));
      setDraftContent(nextContent);
      setSaveMessage("Saved identity file.");
    } catch (saveErr) {
      setSaveError(saveErr instanceof Error ? saveErr.message : "Failed to save identity file");
    } finally {
      setIsSaving(false);
    }
  };

  if (isLoadingFiles) {
    return (
      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
        Loading identity files...
      </section>
    );
  }

  if (error) {
    return (
      <section className="rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700">
        Failed to load identity files: {error}
      </section>
    );
  }

  if (files.length === 0) {
    return (
      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
        No identity files found for this agent.
      </section>
    );
  }

  return (
    <section className="grid gap-4 lg:grid-cols-[220px_1fr]">
      <aside className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-3">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Identity Files</p>
        <div className="space-y-2">
          {files.map((entry) => (
            <button
              key={entry.path}
              type="button"
              onClick={() => setSelectedPath(entry.path)}
              className={`w-full rounded-lg border px-3 py-2 text-left text-sm ${
                selectedPath === entry.path
                  ? "border-[#C9A86C] bg-[#C9A86C]/10 text-[#C9A86C]"
                  : "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text)]"
              }`}
            >
              {entry.path}
            </button>
          ))}
        </div>
      </aside>
      <div className="space-y-3">
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] px-4 py-2 text-xs text-[var(--text-muted)]">
          <span>Ref: {blob?.ref || filesRef || "n/a"}</span>
          <span className="mx-2">•</span>
          <span>Size: {blob?.size ?? 0} bytes</span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => void handleSave()}
            disabled={!selectedPath || isLoadingBlob || isSaving}
            className="rounded-lg border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-1.5 text-xs font-medium text-[#C9A86C] disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isSaving ? "Saving..." : "Save Identity File"}
          </button>
          {saveMessage && <p className="text-xs text-emerald-600">{saveMessage}</p>}
          {saveError && <p className="text-xs text-rose-500">{saveError}</p>}
        </div>
        {isLoadingBlob ? (
          <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
            Loading file...
          </div>
        ) : (
          <DocumentWorkspace
            path={selectedPath}
            content={draftContent}
            readOnly={false}
            onContentChange={setDraftContent}
          />
        )}
      </div>
    </section>
  );
}
