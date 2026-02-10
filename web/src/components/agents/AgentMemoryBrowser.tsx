import { FormEvent, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../../lib/api";

type AgentMemoryBrowserProps = {
  agentID: string;
  workspaceAgentID?: string;
};

type MemoryEntry = {
  id: string;
  agent_id: string;
  kind: string;
  title: string;
  content: string;
  importance: number;
  confidence: number;
  sensitivity: string;
  status: string;
  occurred_at: string;
  updated_at: string;
  relevance?: number;
};

type MemoryListResponse = {
  items?: MemoryEntry[];
  total?: number;
};

type MemoryRecallResponse = {
  context?: string;
};

type MemoryKind =
  | "summary"
  | "decision"
  | "action_item"
  | "lesson"
  | "preference"
  | "fact"
  | "feedback"
  | "context";

const MEMORY_KIND_OPTIONS: MemoryKind[] = [
  "summary",
  "decision",
  "action_item",
  "lesson",
  "preference",
  "fact",
  "feedback",
  "context",
];

function toMemoryList(payload: MemoryListResponse | null | undefined): MemoryEntry[] {
  if (!Array.isArray(payload?.items)) {
    return [];
  }
  return payload.items.filter((entry) => Boolean(entry?.id && entry?.content));
}

function formatOccurredAt(raw: string): string {
  if (!raw) {
    return "unknown";
  }
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return raw;
  }
  return date.toLocaleString();
}

function formatKind(kind: string): string {
  return kind.replace(/_/g, " ");
}

export default function AgentMemoryBrowser({ agentID, workspaceAgentID }: AgentMemoryBrowserProps) {
  const [entries, setEntries] = useState<MemoryEntry[]>([]);
  const [timelineLoading, setTimelineLoading] = useState(true);
  const [timelineError, setTimelineError] = useState<string | null>(null);

  const [isSaving, setIsSaving] = useState(false);
  const [saveMessage, setSaveMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<MemoryEntry[]>([]);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [isSearching, setIsSearching] = useState(false);

  const [recallState, setRecallState] = useState<"idle" | "loading" | "ready" | "empty" | "error">("idle");
  const [recallContext, setRecallContext] = useState("");
  const [recallError, setRecallError] = useState<string | null>(null);

  const [kind, setKind] = useState<MemoryKind>("context");
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [importance, setImportance] = useState("3");

  const canWrite = useMemo(() => {
    if (!workspaceAgentID || isSaving) {
      return false;
    }
    if (!title.trim() || !content.trim()) {
      return false;
    }
    return true;
  }, [workspaceAgentID, isSaving, title, content]);

  async function loadTimeline(signal?: AbortSignal): Promise<void> {
    if (!workspaceAgentID) {
      setEntries([]);
      setTimelineLoading(false);
      setTimelineError(null);
      return;
    }

    setTimelineLoading(true);
    setTimelineError(null);
    try {
      const payload = await apiFetch<MemoryListResponse>(
        `/api/memory/entries?agent_id=${encodeURIComponent(workspaceAgentID)}&limit=50`,
        { signal },
      );
      if (signal?.aborted) {
        return;
      }
      setEntries(toMemoryList(payload));
    } catch (error) {
      if (signal?.aborted || (error instanceof Error && error.name === "AbortError")) {
        return;
      }
      const message = error instanceof Error ? error.message : "Failed to load memory entries";
      setTimelineError(message);
      setEntries([]);
    } finally {
      if (!signal?.aborted) {
        setTimelineLoading(false);
      }
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    void loadTimeline(controller.signal);
    return () => {
      controller.abort();
    };
  }, [workspaceAgentID]);

  async function handleSave(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (!workspaceAgentID || !canWrite) {
      return;
    }

    setIsSaving(true);
    setSaveMessage(null);
    setSaveError(null);
    try {
      const parsedImportance = Number.parseInt(importance, 10);
      await apiFetch<MemoryEntry>("/api/memory/entries", {
        method: "POST",
        body: JSON.stringify({
          agent_id: workspaceAgentID,
          kind,
          title: title.trim(),
          content: content.trim(),
          importance: Number.isFinite(parsedImportance) ? parsedImportance : 3,
          confidence: 0.75,
          sensitivity: "internal",
        }),
      });
      setTitle("");
      setContent("");
      setImportance("3");
      setSaveMessage("Saved memory entry.");
      await loadTimeline();
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : "Failed to save memory entry");
    } finally {
      setIsSaving(false);
    }
  }

  async function handleSearch(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (!workspaceAgentID) {
      return;
    }

    const query = searchQuery.trim();
    if (!query) {
      setSearchResults([]);
      setSearchError(null);
      setRecallState("idle");
      setRecallContext("");
      setRecallError(null);
      return;
    }

    setIsSearching(true);
    setSearchError(null);
    setRecallState("loading");
    setRecallContext("");
    setRecallError(null);
    try {
      const [searchPayload, recallPayload] = await Promise.all([
        apiFetch<MemoryListResponse>(
          `/api/memory/search?agent_id=${encodeURIComponent(workspaceAgentID)}&q=${encodeURIComponent(query)}&limit=12&min_relevance=0.7`,
        ),
        apiFetch<MemoryRecallResponse>(
          `/api/memory/recall?agent_id=${encodeURIComponent(workspaceAgentID)}&q=${encodeURIComponent(query)}&max_results=3&min_relevance=0.7&max_chars=1200`,
        ),
      ]);
      setSearchResults(toMemoryList(searchPayload));

      const contextText = String(recallPayload?.context || "").trim();
      if (contextText) {
        setRecallContext(contextText);
        setRecallState("ready");
      } else {
        setRecallContext("");
        setRecallState("empty");
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "Memory search failed";
      setSearchError(message);
      setSearchResults([]);
      setRecallState("error");
      setRecallContext("");
      setRecallError(message);
    } finally {
      setIsSearching(false);
    }
  }

  return (
    <section className="space-y-4">
      <form className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4" onSubmit={handleSave}>
        <h3 className="text-sm font-semibold text-[var(--text)]">Write Memory</h3>
        {!workspaceAgentID && (
          <p className="mt-2 text-xs text-[var(--text-muted)]">
            Memory editing is unavailable until this agent has a workspace UUID.
          </p>
        )}

        <div className="mt-3 grid gap-3 md:grid-cols-2">
          <label className="text-sm text-[var(--text-muted)]" htmlFor="memory-kind">
            Kind
            <select
              id="memory-kind"
              value={kind}
              onChange={(event) => setKind(event.target.value as MemoryKind)}
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            >
              {MEMORY_KIND_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {formatKind(option)}
                </option>
              ))}
            </select>
          </label>

          <label className="text-sm text-[var(--text-muted)]" htmlFor="memory-importance">
            Importance
            <select
              id="memory-importance"
              value={importance}
              onChange={(event) => setImportance(event.target.value)}
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            >
              <option value="1">1</option>
              <option value="2">2</option>
              <option value="3">3</option>
              <option value="4">4</option>
              <option value="5">5</option>
            </select>
          </label>
        </div>

        <label className="mt-3 block text-sm text-[var(--text-muted)]" htmlFor="memory-title">
          Title
          <input
            id="memory-title"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="Short label for this memory"
            className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
          />
        </label>

        <label className="mt-3 block text-sm text-[var(--text-muted)]" htmlFor="memory-content">
          Content
          <textarea
            id="memory-content"
            value={content}
            onChange={(event) => setContent(event.target.value)}
            placeholder="Detailed memory content"
            className="mt-1 min-h-[110px] w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
          />
        </label>

        <div className="mt-3 flex items-center gap-2">
          <button
            type="submit"
            disabled={!canWrite}
            className="rounded-lg border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-1.5 text-xs font-medium text-[#C9A86C] disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isSaving ? "Saving..." : "Save Memory"}
          </button>
          {saveMessage && <p className="text-xs text-emerald-600">{saveMessage}</p>}
          {saveError && <p className="text-xs text-rose-500">{saveError}</p>}
        </div>
      </form>

      <form className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4" onSubmit={handleSearch}>
        <h3 className="text-sm font-semibold text-[var(--text)]">Search Memory</h3>
        <label className="mt-2 block text-sm text-[var(--text-muted)]" htmlFor="memory-search">
          Query
          <input
            id="memory-search"
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder="Search semantic memory"
            className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
          />
        </label>
        <div className="mt-3 flex items-center gap-2">
          <button
            type="submit"
            disabled={!workspaceAgentID || isSearching}
            className="rounded-lg border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-1.5 text-xs font-medium text-[#C9A86C] disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isSearching ? "Searching..." : "Search"}
          </button>
          {searchError && <p className="text-xs text-rose-500">Search failed: {searchError}</p>}
        </div>

        {recallState === "loading" && (
          <p className="mt-3 text-xs text-[var(--text-muted)]">Building recall preview...</p>
        )}
        {recallState === "ready" && (
          <pre className="mt-3 whitespace-pre-wrap rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3 text-xs text-[var(--text)]">
            {recallContext}
          </pre>
        )}
        {recallState === "empty" && (
          <p className="mt-3 text-xs text-[var(--text-muted)]">No recall context passed quality gates.</p>
        )}
        {recallState === "error" && (
          <p className="mt-3 text-xs text-rose-500">Recall preview unavailable: {recallError || "unknown error"}</p>
        )}

        {searchResults.length > 0 && (
          <div className="mt-3 space-y-2">
            {searchResults.map((entry) => (
              <article key={entry.id} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <div className="flex items-center justify-between gap-2">
                  <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">{formatKind(entry.kind)}</p>
                  <p className="text-xs text-[var(--text-muted)]">
                    relevance {typeof entry.relevance === "number" ? entry.relevance.toFixed(2) : "n/a"}
                  </p>
                </div>
                <p className="mt-1 text-sm font-medium text-[var(--text)]">{entry.title}</p>
                <p className="mt-1 whitespace-pre-wrap text-sm text-[var(--text)]">{entry.content}</p>
              </article>
            ))}
          </div>
        )}
      </form>

      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <h3 className="text-sm font-semibold text-[var(--text)]">Memory Timeline</h3>
        {timelineLoading ? (
          <p className="mt-2 text-sm text-[var(--text-muted)]">Loading memory timeline...</p>
        ) : (
          <>
            {timelineError && (
              <p className="mt-2 text-sm text-rose-500">Failed to load memory entries: {timelineError}</p>
            )}

            {!timelineError && entries.length === 0 && (
              <p className="mt-2 text-sm text-[var(--text-muted)]">No memory entries for this agent yet.</p>
            )}

            {entries.length > 0 && (
              <div className="mt-3 space-y-2">
                {entries.map((entry) => (
                  <article key={entry.id} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                    <div className="flex items-center justify-between gap-2">
                      <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">{formatKind(entry.kind)}</p>
                      <p className="text-xs text-[var(--text-muted)]">{formatOccurredAt(entry.occurred_at)}</p>
                    </div>
                    <p className="mt-1 text-sm font-medium text-[var(--text)]">{entry.title}</p>
                    <p className="mt-1 whitespace-pre-wrap text-sm text-[var(--text)]">{entry.content}</p>
                  </article>
                ))}
              </div>
            )}
          </>
        )}
      </div>

      {!agentID && (
        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
          Missing agent identifier.
        </section>
      )}
    </section>
  );
}
