import { useEffect, useState } from "react";
import type { Toast as ToastType, ToastAction } from "../contexts/ToastContext";

const ICONS: Record<ToastType["type"], string> = {
  success: "✅",
  error: "❌",
  warning: "⚠️",
  info: "ℹ️",
};

const COLORS: Record<
  ToastType["type"],
  { bg: string; border: string; text: string; progress: string }
> = {
  success: {
    bg: "bg-emerald-50 dark:bg-emerald-900/20",
    border: "border-emerald-200 dark:border-emerald-800",
    text: "text-emerald-800 dark:text-emerald-200",
    progress: "bg-emerald-500",
  },
  error: {
    bg: "bg-red-50 dark:bg-red-900/20",
    border: "border-red-200 dark:border-red-800",
    text: "text-red-800 dark:text-red-200",
    progress: "bg-red-500",
  },
  warning: {
    bg: "bg-amber-50 dark:bg-amber-900/20",
    border: "border-amber-200 dark:border-amber-800",
    text: "text-amber-800 dark:text-amber-200",
    progress: "bg-amber-500",
  },
  info: {
    bg: "bg-sky-50 dark:bg-sky-900/20",
    border: "border-sky-200 dark:border-sky-800",
    text: "text-sky-800 dark:text-sky-200",
    progress: "bg-sky-500",
  },
};

interface ToastProps {
  toast: ToastType;
  onDismiss: (id: string) => void;
}

export default function Toast({ toast, onDismiss }: ToastProps) {
  const [isExiting, setIsExiting] = useState(false);
  const [progress, setProgress] = useState(100);

  const colors = COLORS[toast.type];
  const icon = ICONS[toast.type];

  useEffect(() => {
    if (!toast.duration || toast.duration <= 0) return;

    const startTime = Date.now();
    const endTime = startTime + toast.duration;

    const updateProgress = () => {
      const now = Date.now();
      const remaining = Math.max(0, endTime - now);
      const percent = (remaining / toast.duration!) * 100;
      setProgress(percent);

      if (percent > 0) {
        requestAnimationFrame(updateProgress);
      }
    };

    const frameId = requestAnimationFrame(updateProgress);
    return () => cancelAnimationFrame(frameId);
  }, [toast.duration]);

  const handleDismiss = () => {
    setIsExiting(true);
    setTimeout(() => onDismiss(toast.id), 200);
  };

  const handleAction = (action: ToastAction) => {
    action.onClick();
    handleDismiss();
  };

  return (
    <div
      role="alert"
      aria-live="polite"
      className={`
        relative w-80 overflow-hidden rounded-xl border shadow-lg backdrop-blur-sm
        transition-all duration-200 ease-out
        ${colors.bg} ${colors.border}
        ${isExiting ? "translate-x-full opacity-0" : "translate-x-0 opacity-100"}
      `}
    >
      <div className="flex items-start gap-3 p-4">
        {/* Icon */}
        <span className="flex-shrink-0 text-lg">{icon}</span>

        {/* Content */}
        <div className="min-w-0 flex-1">
          <p className={`font-medium ${colors.text}`}>{toast.title}</p>
          {toast.message && (
            <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
              {toast.message}
            </p>
          )}
          {toast.action && (
            <button
              type="button"
              onClick={() => handleAction(toast.action!)}
              className={`mt-2 text-sm font-medium underline-offset-2 hover:underline ${colors.text}`}
            >
              {toast.action.label}
            </button>
          )}
        </div>

        {/* Close button */}
        <button
          type="button"
          onClick={handleDismiss}
          className="flex-shrink-0 rounded-lg p-1 text-slate-400 transition hover:bg-slate-200/50 hover:text-slate-600 dark:hover:bg-slate-700/50 dark:hover:text-slate-300"
          aria-label="Dismiss notification"
        >
          <svg
            className="h-4 w-4"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M6 18L18 6M6 6l12 12"
            />
          </svg>
        </button>
      </div>

      {/* Progress bar */}
      {toast.duration && toast.duration > 0 && (
        <div className="absolute bottom-0 left-0 h-1 w-full bg-slate-200/50 dark:bg-slate-700/50">
          <div
            className={`h-full transition-none ${colors.progress}`}
            style={{ width: `${progress}%` }}
          />
        </div>
      )}
    </div>
  );
}
