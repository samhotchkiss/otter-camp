import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";

type CoverageSummary = {
  extractedUpTo?: string | null;
};

type CoverageDay = {
  day: string;
  totalMessages: number;
  processedMessages: number;
  windows: number;
  windowsOK: number;
  windowsFailed: number;
  retries: number;
  insertedTotal: number;
  insertedMemories: number;
  insertedProjects: number;
  insertedIssues: number;
  lastOKAt?: string | null;
};

type CoverageResponse = {
  summary?: CoverageSummary;
  days?: CoverageDay[];
  diagnostics?: {
    models?: {
      model: string;
      windows: number;
      windowsOK: number;
      windowsFailed: number;
      retries: number;
      insertedTotal: number;
    }[];
    recentFailures?: {
      at: string;
      roomId: string;
      model: string;
      llmAttempts: number;
      messageCount: number;
      tokenCount: number;
      error: string;
    }[];
  };
};

function pct(n: number, d: number): string {
  if (d <= 0) return "0%";
  const v = Math.round((n / d) * 100);
  return `${Math.max(0, Math.min(100, v))}%`;
}

export default function EllieIngestionCoveragePage() {
  const [payload, setPayload] = useState<CoverageResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);

    void apiFetch<CoverageResponse>("/api/admin/ellie/ingestion/coverage?days=60")
      .then((resp) => {
        if (!active) return;
        setPayload(resp ?? null);
      })
      .catch((err: unknown) => {
        if (!active) return;
        const message = err instanceof Error ? err.message : "Failed to load ingestion coverage";
        setError(message);
        setPayload(null);
      })
      .finally(() => {
        if (!active) return;
        setLoading(false);
      });

    return () => {
      active = false;
    };
  }, []);

  const days = payload?.days ?? [];
  const extractedUpTo = payload?.summary?.extractedUpTo ?? null;
  const models = payload?.diagnostics?.models ?? [];
  const recentFailures = payload?.diagnostics?.recentFailures ?? [];

  const totals = useMemo(() => {
    let totalMessages = 0;
    let processedMessages = 0;
    let windows = 0;
    let windowsOK = 0;
    let windowsFailed = 0;
    let retries = 0;
    let insertedTotal = 0;
    for (const d of days) {
      totalMessages += d.totalMessages ?? 0;
      processedMessages += d.processedMessages ?? 0;
      windows += d.windows ?? 0;
      windowsOK += d.windowsOK ?? 0;
      windowsFailed += d.windowsFailed ?? 0;
      retries += d.retries ?? 0;
      insertedTotal += d.insertedTotal ?? 0;
    }
    return { totalMessages, processedMessages, windows, windowsOK, windowsFailed, retries, insertedTotal };
  }, [days]);

  return (
    <div className="space-y-4">
      <header className="space-y-2">
        <Link to="/knowledge" className="inline-flex text-sm font-medium text-[#C9A86C] hover:underline">
          Back to knowledge
        </Link>
        <h1 className="text-2xl font-semibold text-[var(--text)]">Ingestion Coverage</h1>
        <p className="text-sm text-[var(--text-muted)]">
          Day-by-day visibility into Ellie’s memory ingestion pipeline: messages, windows, retries, and extracted memories.
        </p>
      </header>

      <section className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Extracted up to</p>
          <p className="mt-1 text-sm font-medium text-[var(--text)]">{extractedUpTo ? new Date(extractedUpTo).toLocaleString() : "n/a"}</p>
        </div>
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Processed messages</p>
          <p className="mt-1 text-sm font-medium text-[var(--text)]">
            {totals.processedMessages.toLocaleString()} / {totals.totalMessages.toLocaleString()} ({pct(totals.processedMessages, totals.totalMessages)})
          </p>
        </div>
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Windows</p>
          <p className="mt-1 text-sm font-medium text-[var(--text)]">
            {totals.windows.toLocaleString()} ({totals.windowsOK.toLocaleString()} ok, {totals.windowsFailed.toLocaleString()} failed)
          </p>
        </div>
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Inserted memories</p>
          <p className="mt-1 text-sm font-medium text-[var(--text)]">{totals.insertedTotal.toLocaleString()}</p>
          <p className="mt-1 text-xs text-[var(--text-muted)]">Retries: {totals.retries.toLocaleString()}</p>
        </div>
      </section>

      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        {loading && <p className="text-sm text-[var(--text-muted)]">Loading ingestion coverage...</p>}
        {!loading && error && <p className="text-sm text-rose-500">Unable to load ingestion coverage: {error}</p>}
        {!loading && !error && days.length === 0 && <p className="text-sm text-[var(--text-muted)]">No coverage data yet.</p>}

        {!loading && !error && days.length > 0 && (
          <div className="overflow-auto">
            <table className="min-w-full border-separate border-spacing-y-2">
              <thead>
                <tr className="text-left text-xs uppercase tracking-wide text-[var(--text-muted)]">
                  <th className="px-3 py-2">Day</th>
                  <th className="px-3 py-2">Messages</th>
                  <th className="px-3 py-2">Processed</th>
                  <th className="px-3 py-2">Windows</th>
                  <th className="px-3 py-2">Retries</th>
                  <th className="px-3 py-2">Memories</th>
                  <th className="px-3 py-2">Projects</th>
                  <th className="px-3 py-2">Issues</th>
                  <th className="px-3 py-2">Last ok</th>
                </tr>
              </thead>
              <tbody>
                {days.map((d) => {
                  const processedPct = pct(d.processedMessages ?? 0, d.totalMessages ?? 0);
                  return (
                    <tr key={d.day} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)]">
                      <td className="whitespace-nowrap px-3 py-2 text-sm font-medium text-[var(--text)]">
                        {new Date(d.day).toLocaleDateString()}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(d.totalMessages ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">
                        {(d.processedMessages ?? 0).toLocaleString()} <span className="text-xs text-[var(--text-muted)]">({processedPct})</span>
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">
                        {(d.windows ?? 0).toLocaleString()}{" "}
                        <span className="text-xs text-[var(--text-muted)]">
                          ({(d.windowsOK ?? 0).toLocaleString()} ok, {(d.windowsFailed ?? 0).toLocaleString()} fail)
                        </span>
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(d.retries ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(d.insertedMemories ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(d.insertedProjects ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(d.insertedIssues ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-xs text-[var(--text-muted)]">
                        {d.lastOKAt ? new Date(d.lastOKAt).toLocaleString() : "n/a"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {!loading && !error && (models.length > 0 || recentFailures.length > 0) && (
        <section className="space-y-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
          <h2 className="text-base font-semibold text-[var(--text)]">LLM Diagnostics</h2>

          {models.length > 0 && (
            <div className="overflow-auto">
              <table className="min-w-full border-separate border-spacing-y-2">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-[var(--text-muted)]">
                    <th className="px-3 py-2">Model</th>
                    <th className="px-3 py-2">Windows</th>
                    <th className="px-3 py-2">Ok</th>
                    <th className="px-3 py-2">Failed</th>
                    <th className="px-3 py-2">Retries</th>
                    <th className="px-3 py-2">Inserted</th>
                  </tr>
                </thead>
                <tbody>
                  {models.map((m) => (
                    <tr key={m.model} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)]">
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{m.model || "unknown"}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(m.windows ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-emerald-300">{(m.windowsOK ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-rose-300">{(m.windowsFailed ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(m.retries ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(m.insertedTotal ?? 0).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {recentFailures.length > 0 && (
            <div className="overflow-auto">
              <h3 className="mb-2 text-sm font-medium text-[var(--text)]">Recent failed windows</h3>
              <table className="min-w-full border-separate border-spacing-y-2">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-[var(--text-muted)]">
                    <th className="px-3 py-2">At</th>
                    <th className="px-3 py-2">Model</th>
                    <th className="px-3 py-2">Attempts</th>
                    <th className="px-3 py-2">Msgs</th>
                    <th className="px-3 py-2">Error</th>
                  </tr>
                </thead>
                <tbody>
                  {recentFailures.map((f) => (
                    <tr key={`${f.at}:${f.roomId}:${f.error}`} className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)]">
                      <td className="whitespace-nowrap px-3 py-2 text-xs text-[var(--text-muted)]">
                        {f.at ? new Date(f.at).toLocaleString() : "n/a"}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{f.model || "unknown"}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(f.llmAttempts ?? 0).toLocaleString()}</td>
                      <td className="whitespace-nowrap px-3 py-2 text-sm text-[var(--text)]">{(f.messageCount ?? 0).toLocaleString()}</td>
                      <td className="max-w-[32rem] px-3 py-2 text-xs text-rose-300">{f.error || "unknown error"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}
    </div>
  );
}
