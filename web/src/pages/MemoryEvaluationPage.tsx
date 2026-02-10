import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";

type EvaluationRun = {
  id: string;
  created_at: string;
  passed: boolean;
  failed_gates?: string[];
  metrics?: {
    precision_at_k?: number;
    false_injection_rate?: number;
    recovery_success_rate?: number;
    p95_latency_ms?: number;
  };
};

export default function MemoryEvaluationPage() {
  const [run, setRun] = useState<EvaluationRun | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);

    void apiFetch<{ run?: EvaluationRun }>("/api/memory/evaluations/latest")
      .then((payload) => {
        if (!active) {
          return;
        }
        setRun(payload?.run ?? null);
      })
      .catch((fetchError: unknown) => {
        if (!active) {
          return;
        }
        const message =
          fetchError instanceof Error
            ? fetchError.message
            : "Failed to load evaluation status";
        setError(message);
        setRun(null);
      })
      .finally(() => {
        if (!active) {
          return;
        }
        setLoading(false);
      });

    return () => {
      active = false;
    };
  }, []);

  return (
    <div className="space-y-4">
      <header className="space-y-2">
        <Link to="/knowledge" className="inline-flex text-sm font-medium text-[#C9A86C] hover:underline">
          Back to knowledge
        </Link>
        <h1 className="text-2xl font-semibold text-[var(--text)]">Memory Evaluation Dashboard</h1>
        <p className="text-sm text-[var(--text-muted)]">
          Latest benchmark gates and memory-quality metrics.
        </p>
      </header>

      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        {loading && <p className="text-sm text-[var(--text-muted)]">Loading evaluation status...</p>}
        {!loading && error && (
          <p className="text-sm text-rose-500">Unable to load memory evaluation status: {error}</p>
        )}
        {!loading && !error && !run && (
          <p className="text-sm text-[var(--text-muted)]">No evaluation runs available yet.</p>
        )}
        {!loading && !error && run && (
          <div className="space-y-3">
            <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
              <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Run Status</p>
              <p className={`mt-1 text-sm font-medium ${run.passed ? "text-emerald-600" : "text-rose-500"}`}>
                {run.passed ? "pass" : "fail"}
              </p>
              <p className="mt-1 text-xs text-[var(--text-muted)]">Run ID: {run.id}</p>
            </div>

            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Precision@k</p>
                <p className="mt-1 text-sm font-medium text-[var(--text)]">
                  {typeof run.metrics?.precision_at_k === "number" ? run.metrics.precision_at_k.toFixed(2) : "n/a"}
                </p>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">False inject</p>
                <p className="mt-1 text-sm font-medium text-[var(--text)]">
                  {typeof run.metrics?.false_injection_rate === "number"
                    ? run.metrics.false_injection_rate.toFixed(2)
                    : "n/a"}
                </p>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Recovery success</p>
                <p className="mt-1 text-sm font-medium text-[var(--text)]">
                  {typeof run.metrics?.recovery_success_rate === "number"
                    ? run.metrics.recovery_success_rate.toFixed(2)
                    : "n/a"}
                </p>
              </div>
              <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-3">
                <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">p95 latency</p>
                <p className="mt-1 text-sm font-medium text-[var(--text)]">
                  {typeof run.metrics?.p95_latency_ms === "number"
                    ? `${run.metrics.p95_latency_ms.toFixed(0)}ms`
                    : "n/a"}
                </p>
              </div>
            </div>

            {!run.passed && Array.isArray(run.failed_gates) && run.failed_gates.length > 0 && (
              <div className="rounded-lg border border-rose-300 bg-rose-50 p-3">
                <p className="text-xs uppercase tracking-wide text-rose-700">Failed gates</p>
                <ul className="mt-2 list-disc pl-5 text-sm text-rose-700">
                  {run.failed_gates.map((gate) => (
                    <li key={gate}>{gate}</li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
      </section>
    </div>
  );
}
