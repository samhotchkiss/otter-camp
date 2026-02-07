import { useMemo, useRef, useState, type RefObject } from "react";
import { useFocusTrap } from "../../hooks/useFocusTrap";
import type {
  ExecApprovalDecision,
  ExecApprovalRequest,
} from "../../hooks/useExecApprovals";

type ExecApprovalDialogProps = {
  isOpen: boolean;
  request: ExecApprovalRequest;
  decision: ExecApprovalDecision;
  confirming: boolean;
  onClose: () => void;
  onConfirm: (comment: string) => Promise<void>;
};

const formatJson = (value: unknown): string | null => {
  if (value === undefined || value === null) return null;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
};

const formatTimestamp = (iso: string): string => {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
};

export default function ExecApprovalDialog({
  isOpen,
  request,
  decision,
  confirming,
  onClose,
  onConfirm,
}: ExecApprovalDialogProps) {
  const [comment, setComment] = useState("");
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const { containerRef } = useFocusTrap({
    isActive: isOpen,
    onEscape: onClose,
    returnFocusOnClose: true,
    initialFocusRef: commentRef as unknown as RefObject<HTMLElement>,
  });

  const title = useMemo(() => {
    return decision === "approve" ? "Approve Command?" : "Deny Command?";
  }, [decision]);

  const confirmLabel = useMemo(() => {
    return decision === "approve" ? "Approve" : "Deny";
  }, [decision]);

  const confirmClasses = useMemo(() => {
    return decision === "approve"
      ? "bg-emerald-600 hover:bg-emerald-500 focus:ring-emerald-500"
      : "bg-red-600 hover:bg-red-500 focus:ring-red-500";
  }, [decision]);

  const argsJson = useMemo(() => formatJson(request.args), [request.args]);
  const envJson = useMemo(() => formatJson(request.env), [request.env]);
  const rawJson = useMemo(() => formatJson(request.request), [request.request]);

  const handleConfirm = async () => {
    await onConfirm(comment);
    setComment("");
  };

  if (!isOpen) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 px-4 py-6 backdrop-blur-sm"
      onClick={() => !confirming && onClose()}
      aria-hidden="true"
    >
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="w-full max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl dark:border-slate-800 dark:bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-4 border-b border-slate-200 px-6 py-4 dark:border-slate-800">
          <div className="min-w-0">
            <p className="text-xs font-semibold uppercase tracking-[0.3em] text-amber-600 dark:text-amber-300">
              Exec Approval
            </p>
            <h2 className="mt-1 truncate text-lg font-semibold text-slate-900 dark:text-white">
              {title}
            </h2>
            <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
              Requested {formatTimestamp(request.created_at)}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={confirming}
            className="rounded-lg p-2 text-slate-400 transition hover:bg-slate-100 hover:text-slate-600 disabled:cursor-not-allowed disabled:opacity-60 dark:hover:bg-slate-800 dark:hover:text-slate-200"
            aria-label="Close"
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div className="max-h-[70vh] overflow-y-auto px-6 py-5">
          <div className="space-y-5">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                Command
              </p>
              <pre className="mt-2 whitespace-pre-wrap break-words rounded-xl border border-slate-200 bg-slate-50 px-4 py-3 font-mono text-xs text-slate-800 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100">
                {request.command}
              </pre>
              {request.message && (
                <p className="mt-2 text-sm text-slate-700 dark:text-slate-200">
                  {request.message}
                </p>
              )}
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <div className="rounded-xl border border-slate-200 bg-white px-4 py-3 dark:border-slate-800 dark:bg-slate-900">
                <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                  Working Dir
                </p>
                <p className="mt-2 break-all font-mono text-xs text-slate-700 dark:text-slate-200">
                  {request.cwd || "—"}
                </p>
              </div>
              <div className="rounded-xl border border-slate-200 bg-white px-4 py-3 dark:border-slate-800 dark:bg-slate-900">
                <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                  Shell
                </p>
                <p className="mt-2 break-all font-mono text-xs text-slate-700 dark:text-slate-200">
                  {request.shell || "—"}
                </p>
              </div>
            </div>

            {(argsJson || envJson) && (
              <div className="grid gap-3 sm:grid-cols-2">
                {argsJson && (
                  <div className="rounded-xl border border-slate-200 bg-white px-4 py-3 dark:border-slate-800 dark:bg-slate-900">
                    <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                      Args
                    </p>
                    <pre className="mt-2 max-h-56 overflow-auto rounded-lg bg-slate-50 p-3 font-mono text-[11px] text-slate-700 dark:bg-slate-950 dark:text-slate-200">
                      {argsJson}
                    </pre>
                  </div>
                )}
                {envJson && (
                  <div className="rounded-xl border border-slate-200 bg-white px-4 py-3 dark:border-slate-800 dark:bg-slate-900">
                    <p className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400">
                      Env
                    </p>
                    <pre className="mt-2 max-h-56 overflow-auto rounded-lg bg-slate-50 p-3 font-mono text-[11px] text-slate-700 dark:bg-slate-950 dark:text-slate-200">
                      {envJson}
                    </pre>
                  </div>
                )}
              </div>
            )}

            <div>
              <label
                htmlFor="exec-approval-comment"
                className="text-xs font-semibold uppercase tracking-[0.3em] text-slate-400"
              >
                Comment (Optional)
              </label>
              <textarea
                ref={commentRef}
                id="exec-approval-comment"
                value={comment}
                onChange={(e) => setComment(e.target.value)}
                className="mt-2 w-full resize-y rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-200 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100 dark:focus:border-sky-400 dark:focus:ring-sky-900/40"
                placeholder="Why are you approving/denying?"
                rows={3}
                disabled={confirming}
              />
            </div>

            {rawJson && (
              <details className="rounded-xl border border-slate-200 bg-white px-4 py-3 dark:border-slate-800 dark:bg-slate-900">
                <summary className="cursor-pointer text-sm font-medium text-slate-700 dark:text-slate-200">
                  View raw request JSON
                </summary>
                <pre className="mt-3 max-h-64 overflow-auto rounded-lg bg-slate-50 p-3 font-mono text-[11px] text-slate-700 dark:bg-slate-950 dark:text-slate-200">
                  {rawJson}
                </pre>
              </details>
            )}
          </div>
        </div>

        <div className="flex items-center justify-end gap-3 border-t border-slate-200 px-6 py-4 dark:border-slate-800">
          <button
            type="button"
            onClick={onClose}
            disabled={confirming}
            className="rounded-lg px-4 py-2 text-sm font-medium text-slate-600 transition hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60 dark:text-slate-300 dark:hover:bg-slate-800"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleConfirm}
            disabled={confirming}
            className={`rounded-lg px-4 py-2 text-sm font-semibold text-white shadow-sm transition focus:outline-none focus:ring-2 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60 dark:focus:ring-offset-slate-900 ${confirmClasses}`}
          >
            {confirming ? "Working..." : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
