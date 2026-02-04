import { memo } from "react";

type LoadingSpinnerProps = {
  message?: string;
  size?: "sm" | "md" | "lg";
};

/**
 * LoadingSpinner - Reusable loading indicator for Suspense boundaries.
 */
function LoadingSpinnerComponent({ message = "Loading...", size = "md" }: LoadingSpinnerProps) {
  const sizeClasses = {
    sm: "h-4 w-4 border-2",
    md: "h-6 w-6 border-2",
    lg: "h-10 w-10 border-3",
  };

  return (
    <div className="flex min-h-[200px] flex-col items-center justify-center gap-3">
      <div
        className={`animate-spin rounded-full border-otter-border border-t-emerald-500 dark:border-otter-dark-border dark:border-t-emerald-400 ${sizeClasses[size]}`}
        role="status"
        aria-label="Loading"
      />
      <span className="text-sm text-otter-muted dark:text-otter-dark-muted">{message}</span>
    </div>
  );
}

const LoadingSpinner = memo(LoadingSpinnerComponent);

export default LoadingSpinner;
