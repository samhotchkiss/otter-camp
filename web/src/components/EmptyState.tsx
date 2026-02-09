import type { ReactNode } from "react";

export type EmptyStateProps = {
  /** Main title */
  title: string;
  /** Description text */
  description?: string;
  /** Custom icon (defaults to empty box) */
  icon?: ReactNode;
  /** Action button */
  action?: {
    label: string;
    onClick: () => void;
  };
  /** Additional CSS classes */
  className?: string;
  /** Compact mode for inline usage */
  compact?: boolean;
};

/**
 * Default empty state icon (stylized otter paw / empty state).
 */
function DefaultIcon() {
  return (
    <svg
      className="h-12 w-12 text-slate-300 dark:text-slate-600"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={1.5}
        d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4"
      />
    </svg>
  );
}

/**
 * EmptyState - Placeholder for empty lists and no-content scenarios.
 *
 * Features:
 * - Customizable icon, title, and description
 * - Optional action button
 * - Compact mode for inline usage
 * - Consistent styling with the app
 */
export default function EmptyState({
  title,
  description,
  icon,
  action,
  className = "",
  compact = false,
}: EmptyStateProps) {
  if (compact) {
    return (
      <div
        className={`flex flex-col items-center justify-center py-8 text-center ${className}`}
      >
        <div className="mb-3">{icon || <DefaultIcon />}</div>
        <p className="text-sm font-medium text-slate-500 dark:text-slate-400">
          {title}
        </p>
        {description && (
          <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">
            {description}
          </p>
        )}
        {action && (
          <button
            type="button"
            onClick={action.onClick}
            className="mt-3 text-sm font-medium text-emerald-600 hover:text-emerald-700 dark:text-emerald-400 dark:hover:text-emerald-300"
          >
            {action.label}
          </button>
        )}
      </div>
    );
  }

  return (
    <div
      className={`flex min-h-[200px] flex-col items-center justify-center rounded-2xl border-2 border-dashed border-slate-200 bg-slate-50/50 p-8 text-center dark:border-slate-700 dark:bg-slate-900/50 ${className}`}
    >
      <div className="mb-4 flex h-20 w-20 items-center justify-center rounded-2xl bg-slate-100 dark:bg-slate-800">
        {icon || <DefaultIcon />}
      </div>

      <h3 className="text-lg font-semibold text-slate-700 dark:text-slate-300">
        {title}
      </h3>

      {description && (
        <p className="mt-2 max-w-sm text-sm text-slate-500 dark:text-slate-400">
          {description}
        </p>
      )}

      {action && (
        <button
          type="button"
          onClick={action.onClick}
          className="mt-4 inline-flex items-center gap-2 rounded-xl bg-emerald-600 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-emerald-700 focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 dark:focus:ring-offset-slate-900"
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
              d="M12 4v16m8-8H4"
            />
          </svg>
          {action.label}
        </button>
      )}
    </div>
  );
}

/**
 * Preset empty state variants for common use cases.
 */
export function NoAgentsEmpty({ onAdd }: { onAdd?: () => void }) {
  return (
    <EmptyState
      title="No agents found"
      description="Agents will appear here when they connect to the system."
      icon={
        <span className="text-4xl" role="img" aria-label="otter">
          🦦
        </span>
      }
      action={onAdd ? { label: "Add Agent", onClick: onAdd } : undefined}
    />
  );
}

export function NoProjectsEmpty({ onCreate }: { onCreate?: () => void }) {
  return (
    <EmptyState
      title="No projects yet"
      description="Create your first project to start organizing tasks."
      icon={
        <svg
          className="h-12 w-12 text-slate-300 dark:text-slate-600"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={1.5}
            d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"
          />
        </svg>
      }
      action={onCreate ? { label: "Create Project", onClick: onCreate } : undefined}
    />
  );
}

export function NoTasksEmpty({ onCreate }: { onCreate?: () => void }) {
  return (
    <EmptyState
      title="No issues yet"
      description="Get started by creating your first issue."
      icon={
        <svg
          className="h-12 w-12 text-slate-300 dark:text-slate-600"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={1.5}
            d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4"
          />
        </svg>
      }
      action={onCreate ? { label: "Create Task", onClick: onCreate } : undefined}
    />
  );
}

export function NoResultsEmpty({ query }: { query?: string }) {
  return (
    <EmptyState
      title="No results found"
      description={
        query
          ? `No matches for "${query}". Try a different search term.`
          : "Try adjusting your filters or search terms."
      }
      icon={
        <svg
          className="h-12 w-12 text-slate-300 dark:text-slate-600"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={1.5}
            d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
          />
        </svg>
      }
      compact
    />
  );
}
