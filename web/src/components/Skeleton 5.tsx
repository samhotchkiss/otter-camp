import type { HTMLAttributes } from "react";

export type SkeletonProps = HTMLAttributes<HTMLDivElement> & {
  /** Width (CSS value or Tailwind class) */
  width?: string;
  /** Height (CSS value or Tailwind class) */
  height?: string;
  /** Make it circular */
  circle?: boolean;
  /** Animation style */
  animation?: "pulse" | "shimmer" | "none";
};

/**
 * Skeleton - Base loading placeholder with animation.
 */
export default function Skeleton({
  width,
  height,
  circle = false,
  animation = "pulse",
  className = "",
  style,
  ...props
}: SkeletonProps) {
  const baseClasses = "bg-slate-200 dark:bg-slate-700";
  const shapeClasses = circle ? "rounded-full" : "rounded-lg";
  const animationClasses =
    animation === "pulse"
      ? "animate-pulse"
      : animation === "shimmer"
        ? "animate-shimmer bg-gradient-to-r from-slate-200 via-slate-100 to-slate-200 dark:from-slate-700 dark:via-slate-600 dark:to-slate-700 bg-[length:200%_100%]"
        : "";

  return (
    <div
      className={`${baseClasses} ${shapeClasses} ${animationClasses} ${className}`}
      style={{
        width: width,
        height: height,
        ...style,
      }}
      aria-hidden="true"
      {...props}
    />
  );
}

/**
 * SkeletonText - Text line placeholder.
 */
export function SkeletonText({
  lines = 1,
  className = "",
}: {
  lines?: number;
  className?: string;
}) {
  return (
    <div className={`space-y-2 ${className}`}>
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton
          key={i}
          height="1rem"
          className={i === lines - 1 && lines > 1 ? "w-3/4" : "w-full"}
        />
      ))}
    </div>
  );
}

/**
 * SkeletonAvatar - Avatar placeholder.
 */
export function SkeletonAvatar({
  size = "md",
}: {
  size?: "sm" | "md" | "lg" | "xl";
}) {
  const sizeClasses = {
    sm: "h-8 w-8",
    md: "h-10 w-10",
    lg: "h-14 w-14",
    xl: "h-20 w-20",
  };

  return <Skeleton circle className={sizeClasses[size]} />;
}

/**
 * SkeletonCard - Card placeholder matching the app's card style.
 */
export function SkeletonCard({ className = "" }: { className?: string }) {
  return (
    <div
      className={`rounded-2xl border border-slate-200 bg-white/80 p-5 dark:border-slate-800 dark:bg-slate-900/80 ${className}`}
    >
      {/* Header */}
      <div className="flex items-start justify-between">
        <Skeleton className="h-12 w-12 rounded-xl" />
        <Skeleton className="h-6 w-16 rounded-full" />
      </div>

      {/* Title & description */}
      <div className="mt-4 space-y-2">
        <Skeleton height="1.25rem" className="w-2/3" />
        <Skeleton height="0.875rem" className="w-full" />
      </div>

      {/* Progress bar area */}
      <div className="mt-4 space-y-2">
        <div className="flex justify-between">
          <Skeleton height="0.75rem" className="w-16" />
          <Skeleton height="0.75rem" className="w-20" />
        </div>
        <Skeleton height="0.5rem" className="w-full rounded-full" />
      </div>
    </div>
  );
}

/**
 * SkeletonAgentCard - Agent card placeholder.
 */
export function SkeletonAgentCard({ className = "" }: { className?: string }) {
  return (
    <div
      className={`rounded-2xl border border-slate-800 bg-slate-900/80 p-5 ${className}`}
    >
      {/* Header: Avatar + Status */}
      <div className="flex items-start justify-between">
        <Skeleton className="h-14 w-14 rounded-xl" />
        <Skeleton circle className="h-3 w-3" />
      </div>

      {/* Name & Role */}
      <div className="mt-4 space-y-2">
        <Skeleton height="1.25rem" className="w-1/2" />
        <Skeleton height="0.875rem" className="w-1/3" />
      </div>

      {/* Current Task box */}
      <div className="mt-3 rounded-lg bg-slate-800/80 p-3">
        <Skeleton height="0.625rem" className="mb-2 w-20" />
        <Skeleton height="0.875rem" className="w-full" />
        <Skeleton height="0.875rem" className="mt-1 w-3/4" />
      </div>

      {/* Footer */}
      <div className="mt-4 flex items-center justify-between border-t border-slate-800 pt-3">
        <Skeleton height="0.75rem" className="w-16" />
        <Skeleton height="0.75rem" className="w-12" />
      </div>
    </div>
  );
}

/**
 * SkeletonProjectCard - Project card placeholder.
 */
export function SkeletonProjectCard({ className = "" }: { className?: string }) {
  return (
    <div
      className={`rounded-2xl border border-slate-200 bg-white/80 p-5 dark:border-slate-800 dark:bg-slate-900/80 ${className}`}
    >
      {/* Header */}
      <div className="flex items-start justify-between">
        <Skeleton className="h-12 w-12 rounded-xl" />
        <Skeleton className="h-8 w-8 rounded-lg" />
      </div>

      {/* Title & description */}
      <div className="mt-4 space-y-2">
        <Skeleton height="1.25rem" className="w-3/4" />
        <Skeleton height="0.875rem" className="w-full" />
      </div>

      {/* Progress */}
      <div className="mt-4 space-y-2">
        <div className="flex justify-between">
          <Skeleton height="0.75rem" className="w-14" />
          <Skeleton height="0.75rem" className="w-20" />
        </div>
        <Skeleton height="0.5rem" className="w-full rounded-full" />
      </div>
    </div>
  );
}

/**
 * SkeletonList - List of skeleton items.
 */
export function SkeletonList({
  count = 3,
  variant = "card",
  className = "",
}: {
  count?: number;
  variant?: "card" | "agent" | "project" | "row";
  className?: string;
}) {
  const gridClasses = {
    card: "grid gap-4 sm:grid-cols-2 lg:grid-cols-3",
    agent: "grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4",
    project: "grid gap-5 sm:grid-cols-2 lg:grid-cols-3",
    row: "space-y-3",
  };

  const renderItem = (index: number) => {
    switch (variant) {
      case "agent":
        return <SkeletonAgentCard key={index} />;
      case "project":
        return <SkeletonProjectCard key={index} />;
      case "row":
        return (
          <div
            key={index}
            className="flex items-center gap-4 rounded-xl border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900"
          >
            <SkeletonAvatar size="md" />
            <div className="flex-1 space-y-2">
              <Skeleton height="1rem" className="w-1/3" />
              <Skeleton height="0.75rem" className="w-1/2" />
            </div>
            <Skeleton height="1.5rem" className="w-16 rounded-full" />
          </div>
        );
      default:
        return <SkeletonCard key={index} />;
    }
  };

  return (
    <div className={`${gridClasses[variant]} ${className}`}>
      {Array.from({ length: count }).map((_, i) => renderItem(i))}
    </div>
  );
}

/**
 * SkeletonKanbanColumn - Kanban column placeholder.
 */
export function SkeletonKanbanColumn() {
  return (
    <div className="w-80 flex-shrink-0 rounded-xl bg-slate-100 p-3 dark:bg-slate-800/50">
      {/* Column header */}
      <div className="mb-3 flex items-center justify-between">
        <Skeleton height="1rem" className="w-24" />
        <Skeleton height="1.25rem" className="w-6 rounded-full" />
      </div>

      {/* Cards */}
      <div className="space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <div
            key={i}
            className="rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-700 dark:bg-slate-800"
          >
            <Skeleton height="0.875rem" className="w-3/4" />
            <Skeleton height="0.75rem" className="mt-2 w-1/2" />
            <div className="mt-3 flex items-center justify-between">
              <SkeletonAvatar size="sm" />
              <Skeleton height="1rem" className="w-12 rounded-full" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
