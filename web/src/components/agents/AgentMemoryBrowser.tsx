import { useEffect, useMemo, useState } from "react";
import DocumentWorkspace from "../content-review/DocumentWorkspace";

const API_URL = import.meta.env.VITE_API_URL || "https://api.otter.camp";

type AgentMemoryBrowserProps = {
  agentID: string;
};

type AgentMemoryEntry = {
  name: string;
  type: "file" | "dir";
  path: string;
};

type AgentMemoryListResponse = {
  ref?: string;
  entries?: AgentMemoryEntry[];
};

type AgentBlobResponse = {
  ref?: string;
  path?: string;
  content?: string;
  encoding?: string;
  size?: number;
};

function parseMemoryDate(path: string): string {
  const trimmed = path.trim();
  if (!trimmed.endsWith(".md")) {
    return trimmed;
  }
  return trimmed.slice(0, -3);
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

export default function AgentMemoryBrowser({ agentID }: AgentMemoryBrowserProps) {
  const [entries, setEntries] = useState<AgentMemoryEntry[]>([]);
  const [ref, setRef] = useState<string>("");
  const [selectedPath, setSelectedPath] = useState<string>("");
  const [blob, setBlob] = useState<AgentBlobResponse | null>(null);
  const [isLoadingEntries, setIsLoadingEntries] = useState(true);
  const [isLoadingBlob, setIsLoadingBlob] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const loadEntries = async () => {
      if (!agentID) {
        setEntries([]);
        setIsLoadingEntries(false);
        return;
      }
      setIsLoadingEntries(true);
      setError(null);
      try {
        const response = await fetch(`${API_URL}/api/admin/agents/${encodeURIComponent(agentID)}/memory`);
        if (!response.ok) {
          throw new Error(`Failed to load memory files (${response.status})`);
        }
        const payload = (await response.json()) as AgentMemoryListResponse;
        if (cancelled) {
          return;
        }
        const files = (payload.entries || [])
          .filter((entry) => entry.type === "file")
          .sort((a, b) => b.path.localeCompare(a.path));
        setEntries(files);
        setRef(payload.ref || "");
        setSelectedPath(files[0]?.path || "");
      } catch (loadError) {
        if (!cancelled) {
          const message = loadError instanceof Error ? loadError.message : "Failed to load memory files";
          setError(message);
          setEntries([]);
          setSelectedPath("");
        }
      } finally {
        if (!cancelled) {
          setIsLoadingEntries(false);
        }
      }
    };
    void loadEntries();
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
      const date = parseMemoryDate(selectedPath);
      if (!date) {
        setBlob(null);
        return;
      }
      setIsLoadingBlob(true);
      setError(null);
      try {
        const response = await fetch(`${API_URL}/api/admin/agents/${encodeURIComponent(agentID)}/memory/${date}`);
        if (!response.ok) {
          throw new Error(`Failed to load memory entry (${response.status})`);
        }
        const payload = (await response.json()) as AgentBlobResponse;
        if (!cancelled) {
          setBlob(payload);
        }
      } catch (loadError) {
        if (!cancelled) {
          const message = loadError instanceof Error ? loadError.message : "Failed to load memory entry";
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

  if (isLoadingEntries) {
    return (
      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
        Loading memory files...
      </section>
    );
  }

  if (error) {
    return (
      <section className="rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700">
        Failed to load memory files: {error}
      </section>
    );
  }

  if (entries.length === 0) {
    return (
      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
        No memory files found for this agent.
      </section>
    );
  }

  return (
    <section className="grid gap-4 lg:grid-cols-[220px_1fr]">
      <aside className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-3">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">Memory Dates</p>
        <div className="space-y-2">
          {entries.map((entry) => (
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
          <span>Ref: {blob?.ref || ref || "n/a"}</span>
          <span className="mx-2">•</span>
          <span>Size: {blob?.size ?? 0} bytes</span>
        </div>
        {isLoadingBlob ? (
          <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
            Loading memory entry...
          </div>
        ) : (
          <DocumentWorkspace path={selectedPath} content={content} readOnly />
        )}
      </div>
    </section>
  );
}
