import { memo, useMemo } from "react";
import {
  formatRelativeTime,
  getActivityDescription,
  getMetadataString,
  getTypeConfig,
  normalizeMetadata,
  truncate,
} from "./activityFormat";
import type { ActivityFeedItem } from "./sampleActivity";

type DetailRow = {
  label: string;
  value: string;
};

function buildDetailRows(item: ActivityFeedItem): DetailRow[] {
  const rows: DetailRow[] = [];

  if (item.taskTitle) rows.push({ label: "Task", value: item.taskTitle });
  if (item.taskId) rows.push({ label: "Task ID", value: item.taskId });
  if (item.agentId) rows.push({ label: "Agent ID", value: item.agentId });
  if (item.priority) rows.push({ label: "Priority", value: item.priority });

  const metadata = normalizeMetadata(item.metadata);
  const repo = getMetadataString(metadata, "repo");
  const sha = getMetadataString(metadata, "sha");
  const branch = getMetadataString(metadata, "branch");
  const url = getMetadataString(metadata, "url");
  const previousStatus = getMetadataString(metadata, "previous_status");
  const newStatus = getMetadataString(metadata, "new_status");

  if (repo) rows.push({ label: "Repo", value: repo });
  if (branch) rows.push({ label: "Branch", value: branch });
  if (sha) rows.push({ label: "SHA", value: sha });
  if (url) rows.push({ label: "Link", value: url });
  if (previousStatus) rows.push({ label: "Previous Status", value: previousStatus });
  if (newStatus) rows.push({ label: "New Status", value: newStatus });

  const text =
    getMetadataString(metadata, "text") ||
    getMetadataString(metadata, "comment") ||
    getMetadataString(metadata, "content") ||
    getMetadataString(metadata, "preview");
  if (text) rows.push({ label: "Text", value: text });

  return rows;
}

export type ActivityItemRowProps = {
  item: ActivityFeedItem;
  expanded: boolean;
  onToggle: () => void;
};

function ActivityItemRow({ item, expanded, onToggle }: ActivityItemRowProps) {
  const typeConfig = useMemo(() => getTypeConfig(item.type), [item.type]);
  const timestamp = useMemo(() => formatRelativeTime(item.createdAt), [item.createdAt]);
  const description = useMemo(
    () =>
      getActivityDescription({
        type: item.type,
        actorName: item.actorName,
        taskTitle: item.taskTitle,
        summary: item.summary,
        metadata: item.metadata,
      }),
    [item],
  );
  const details = useMemo(() => buildDetailRows(item), [item]);

  return (
    <div className="rounded-xl border border-slate-200 bg-white/70 shadow-sm backdrop-blur transition hover:border-slate-300 dark:border-slate-800 dark:bg-slate-900/60 dark:hover:border-slate-700">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="flex w-full items-start gap-3 rounded-xl px-4 py-3 text-left focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
      >
        <div
          className="mt-0.5 inline-flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-slate-100 text-lg dark:bg-slate-800"
          aria-hidden="true"
          title={typeConfig.label}
        >
          {typeConfig.icon}
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="font-medium text-slate-900 dark:text-white">
                  {item.actorName}
                </span>
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                  {typeConfig.label}
                </span>
              </div>
              <p className="mt-1 text-sm text-slate-700 dark:text-slate-200">
                {truncate(description, expanded ? 500 : 160)}
              </p>
            </div>

            <div className="flex flex-shrink-0 items-center gap-2">
              <span className="text-xs text-slate-500 dark:text-slate-400">
                {timestamp}
              </span>
              <span
                aria-hidden="true"
                className="inline-flex h-6 w-6 items-center justify-center rounded-md text-slate-400 dark:text-slate-500"
                title={expanded ? "Collapse" : "Expand"}
              >
                {expanded ? "▾" : "▸"}
              </span>
            </div>
          </div>
        </div>
      </button>

      {expanded ? (
        <div className="border-t border-slate-200 px-4 py-3 text-sm dark:border-slate-800">
          {details.length > 0 ? (
            <dl className="grid gap-2 sm:grid-cols-2">
              {details.map((row) => (
                <div key={row.label} className="min-w-0">
                  <dt className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
                    {row.label}
                  </dt>
                  <dd className="mt-0.5 break-words text-slate-700 dark:text-slate-200">
                    {row.value}
                  </dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="text-sm text-slate-600 dark:text-slate-300">
              No additional details.
            </p>
          )}

          <div className="mt-3">
            <div className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
              Metadata
            </div>
            <pre className="mt-1 max-h-56 overflow-auto rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs text-slate-700 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-200">
              {JSON.stringify(item.metadata ?? {}, null, 2)}
            </pre>
          </div>
        </div>
      ) : null}
    </div>
  );
}

export default memo(ActivityItemRow);
