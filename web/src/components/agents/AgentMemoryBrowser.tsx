import { FormEvent, useEffect, useMemo, useState } from "react";
import { API_URL } from "../../lib/api";

type AgentMemoryBrowserProps = {
  agentID: string;
  workspaceAgentID?: string;
};

type AgentMemoryRecord = {
  id?: string;
  kind?: string;
  date?: string;
  content?: string;
  updated_at?: string;
};

type AgentMemoryListResponse = {
  daily?: AgentMemoryRecord[];
  long_term?: AgentMemoryRecord[];
};

type MemoryKind = "daily" | "long_term" | "note";

function todayDateISO(): string {
  return new Date().toISOString().slice(0, 10);
}

function normalizeRecords(records: AgentMemoryRecord[] | undefined, fallbackKind: MemoryKind): AgentMemoryRecord[] {
  if (!Array.isArray(records)) {
    return [];
  }
  return records
    .map((record) => ({
      ...record,
      kind: String(record.kind || fallbackKind).trim() || fallbackKind,
      date: String(record.date || "").trim(),
      content: String(record.content || "").trim(),
      updated_at: String(record.updated_at || "").trim(),
    }))
    .filter((record) => record.content);
}

export default function AgentMemoryBrowser({ agentID, workspaceAgentID }: AgentMemoryBrowserProps) {
  const [dailyEntries, setDailyEntries] = useState<AgentMemoryRecord[]>([]);
  const [longTermEntries, setLongTermEntries] = useState<AgentMemoryRecord[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saveMessage, setSaveMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const [kind, setKind] = useState<MemoryKind>("daily");
  const [date, setDate] = useState<string>(todayDateISO());
  const [content, setContent] = useState<string>("");

  const canSave = useMemo(() => {
    if (!workspaceAgentID || isSaving) {
      return false;
    }
    if (!content.trim()) {
      return false;
    }
    if (kind === "daily" && !date.trim()) {
      return false;
    }
    return true;
  }, [workspaceAgentID, isSaving, content, kind, date]);

  const loadEntries = async () => {
    if (!workspaceAgentID) {
      setDailyEntries([]);
      setLongTermEntries([]);
      setIsLoading(false);
      setError(null);
      return;
    }

    setIsLoading(true);
    setError(null);
    try {
      const response = await fetch(
        `${API_URL}/api/agents/${encodeURIComponent(workspaceAgentID)}/memory?days=14&include_long_term=true`,
      );
      if (!response.ok) {
        throw new Error(`Failed to load memory entries (${response.status})`);
      }
      const payload = (await response.json()) as AgentMemoryListResponse;
      setDailyEntries(normalizeRecords(payload.daily, "daily"));
      setLongTermEntries(normalizeRecords(payload.long_term, "long_term"));
    } catch (loadError) {
      const message = loadError instanceof Error ? loadError.message : "Failed to load memory entries";
      setError(message);
      setDailyEntries([]);
      setLongTermEntries([]);
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    void loadEntries();
  }, [workspaceAgentID]);

  const handleSave = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!workspaceAgentID || !canSave) {
      return;
    }
    setIsSaving(true);
    setSaveMessage(null);
    setSaveError(null);
    try {
      const payload: Record<string, string> = {
        kind,
        content: content.trim(),
      };
      if (kind === "daily") {
        payload.date = date.trim();
      }
      const response = await fetch(`${API_URL}/api/agents/${encodeURIComponent(workspaceAgentID)}/memory`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        const errorPayload = (await response.json().catch(() => ({}))) as { error?: string };
        throw new Error(errorPayload.error || `Failed to save memory entry (${response.status})`);
      }
      setContent("");
      if (kind !== "daily") {
        setDate(todayDateISO());
      }
      setSaveMessage("Saved memory entry.");
      await loadEntries();
    } catch (saveErr) {
      setSaveError(saveErr instanceof Error ? saveErr.message : "Failed to save memory entry");
    } finally {
      setIsSaving(false);
    }
  };

  if (isLoading) {
    return (
      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
        Loading memory entries...
      </section>
    );
  }

  if (error) {
    return (
      <section className="rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700">
        Failed to load memory entries: {error}
      </section>
    );
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
          <label className="text-sm text-[var(--text-muted)]" htmlFor="agent-memory-kind">
            Kind
            <select
              id="agent-memory-kind"
              value={kind}
              onChange={(event) => setKind(event.target.value as MemoryKind)}
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            >
              <option value="daily">daily</option>
              <option value="long_term">long_term</option>
              <option value="note">note</option>
            </select>
          </label>
          <label className="text-sm text-[var(--text-muted)]" htmlFor="agent-memory-date">
            Date
            <input
              id="agent-memory-date"
              type="date"
              value={date}
              disabled={kind !== "daily"}
              onChange={(event) => setDate(event.target.value)}
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)] disabled:opacity-60"
            />
          </label>
        </div>
        <label className="mt-3 block text-sm text-[var(--text-muted)]" htmlFor="agent-memory-content">
          Memory Content
          <textarea
            id="agent-memory-content"
            value={content}
            onChange={(event) => setContent(event.target.value)}
            placeholder="Summarize what the agent should remember."
            className="mt-1 min-h-[110px] w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
          />
        </label>
        <div className="mt-3 flex items-center gap-2">
          <button
            type="submit"
            disabled={!canSave}
            className="rounded-lg border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-1.5 text-xs font-medium text-[#C9A86C] disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isSaving ? "Saving..." : "Save Memory"}
          </button>
          {saveMessage && <p className="text-xs text-emerald-600">{saveMessage}</p>}
          {saveError && <p className="text-xs text-rose-500">{saveError}</p>}
        </div>
      </form>

      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <h3 className="text-sm font-semibold text-[var(--text)]">Daily Memory</h3>
        {dailyEntries.length === 0 ? (
          <p className="mt-2 text-sm text-[var(--text-muted)]">No daily memory entries.</p>
        ) : (
          <div className="mt-3 space-y-2">
            {dailyEntries.map((entry) => (
              <article key={String(entry.id || `${entry.kind}-${entry.date}-${entry.content}`)} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <p className="text-xs text-[var(--text-muted)]">{entry.date || "undated"}</p>
                <p className="mt-1 whitespace-pre-wrap text-sm text-[var(--text)]">{entry.content}</p>
              </article>
            ))}
          </div>
        )}
      </div>

      <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <h3 className="text-sm font-semibold text-[var(--text)]">Long-term Memory</h3>
        {longTermEntries.length === 0 ? (
          <p className="mt-2 text-sm text-[var(--text-muted)]">No long-term memory entries.</p>
        ) : (
          <div className="mt-3 space-y-2">
            {longTermEntries.map((entry) => (
              <article key={String(entry.id || `${entry.kind}-${entry.content}`)} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <p className="mt-1 whitespace-pre-wrap text-sm text-[var(--text)]">{entry.content}</p>
              </article>
            ))}
          </div>
        )}
      </div>

      {dailyEntries.length === 0 && longTermEntries.length === 0 && (
        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
          No memory entries for this agent yet.
        </section>
      )}

      {!agentID && (
        <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4 text-sm text-[var(--text-muted)]">
          Missing agent identifier.
        </section>
      )}
    </section>
  );
}
