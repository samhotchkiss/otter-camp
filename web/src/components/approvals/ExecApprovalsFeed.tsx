import { useMemo, useState } from "react";
import useExecApprovals, {
  type ExecApprovalDecision,
  type ExecApprovalRequest,
} from "../../hooks/useExecApprovals";
import { useToast } from "../../contexts/ToastContext";
import ExecApprovalDialog from "./ExecApprovalDialog";

const formatTimestamp = (iso: string): string => {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;

  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);

  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;

  return date.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
};

type ActiveDialog = {
  request: ExecApprovalRequest;
  decision: ExecApprovalDecision;
};

export default function ExecApprovalsFeed() {
  const toast = useToast();
  const { orgId, requests, loading, error, refresh, respond } = useExecApprovals();
  const [active, setActive] = useState<ActiveDialog | null>(null);
  const [confirming, setConfirming] = useState(false);

  const sorted = useMemo(() => {
    return [...requests].sort((a, b) => {
      const at = Date.parse(a.created_at);
      const bt = Date.parse(b.created_at);
      if (Number.isNaN(at) || Number.isNaN(bt)) return 0;
      return bt - at;
    });
  }, [requests]);

  const hasVisibleContent = loading || error || sorted.length > 0;
  if (!hasVisibleContent) return null;

  const openDialog = (request: ExecApprovalRequest, decision: ExecApprovalDecision) => {
    setActive({ request, decision });
  };

  const closeDialog = () => {
    if (confirming) return;
    setActive(null);
  };

  const handleConfirm = async (comment: string) => {
    if (!active) return;

    setConfirming(true);
    try {
      const resolved = await respond(active.request.id, active.decision, comment);
      toast.success(
        active.decision === "approve" ? "Approved" : "Denied",
        resolved.command,
      );
      setActive(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to respond";
      toast.error("Approval failed", message);
    } finally {
      setConfirming(false);
    }
  };

  return (
    <section className="mb-4 rounded-2xl border border-amber-200 bg-amber-50/70 px-4 py-4 shadow-sm dark:border-amber-900/50 dark:bg-amber-950/20">
      <header className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-slate-900 dark:text-white">
            ⚠️ Pending Exec Approvals
          </h3>
          <p className="mt-1 text-xs text-slate-600 dark:text-slate-300">
            {sorted.length} request{sorted.length === 1 ? "" : "s"} {orgId ? "" : "(set org_id to receive approvals)"}
          </p>
        </div>
        <button
          type="button"
          onClick={() => refresh()}
          disabled={loading}
          className="rounded-lg border border-amber-200 bg-white px-3 py-2 text-xs font-medium text-amber-700 shadow-sm transition hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-amber-900/60 dark:bg-slate-900 dark:text-amber-200 dark:hover:bg-amber-950/40"
        >
          {loading ? "Refreshing..." : "Refresh"}
        </button>
      </header>

      {error && (
        <div className="mt-3 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-200">
          {error}
        </div>
      )}

      <div className="mt-4 space-y-3">
        {sorted.length === 0 ? (
          <p className="text-sm text-slate-600 dark:text-slate-300">
            No pending exec approvals.
          </p>
        ) : (
          sorted.map((req) => (
            <div
              key={req.id}
              className="rounded-xl border border-amber-200 bg-white/70 px-4 py-4 shadow-sm backdrop-blur dark:border-amber-900/40 dark:bg-slate-900/60"
            >
              <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-semibold uppercase tracking-[0.25em] text-amber-700 dark:text-amber-300">
                      Exec
                    </span>
                    <span className="text-xs text-slate-500 dark:text-slate-400">
                      {formatTimestamp(req.created_at)}
                    </span>
                  </div>
                  <pre className="mt-2 whitespace-pre-wrap break-words rounded-lg bg-slate-950/5 px-3 py-2 font-mono text-xs text-slate-800 dark:bg-slate-950 dark:text-slate-100">
                    {req.command}
                  </pre>
                  {req.message && (
                    <p className="mt-2 text-sm text-slate-700 dark:text-slate-200">
                      {req.message}
                    </p>
                  )}
                </div>

                <div className="flex shrink-0 flex-row gap-2 sm:flex-col sm:items-stretch">
                  <button
                    type="button"
                    onClick={() => openDialog(req, "approve")}
                    className="rounded-lg bg-emerald-600 px-3 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 dark:focus:ring-offset-slate-900"
                  >
                    Approve
                  </button>
                  <button
                    type="button"
                    onClick={() => openDialog(req, "deny")}
                    className="rounded-lg bg-red-600 px-3 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-red-500 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-offset-2 dark:focus:ring-offset-slate-900"
                  >
                    Deny
                  </button>
                </div>
              </div>

              <div className="mt-3 flex flex-wrap gap-2 text-xs text-slate-500 dark:text-slate-400">
                {req.cwd && (
                  <span className="rounded-full bg-slate-100 px-2 py-1 font-mono dark:bg-slate-800">
                    cwd: {req.cwd}
                  </span>
                )}
                {req.shell && (
                  <span className="rounded-full bg-slate-100 px-2 py-1 font-mono dark:bg-slate-800">
                    shell: {req.shell}
                  </span>
                )}
              </div>
            </div>
          ))
        )}
      </div>

      {active && (
        <ExecApprovalDialog
          isOpen={!!active}
          request={active.request}
          decision={active.decision}
          confirming={confirming}
          onClose={closeDialog}
          onConfirm={handleConfirm}
        />
      )}
    </section>
  );
}

