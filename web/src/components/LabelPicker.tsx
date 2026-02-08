import { useMemo, useState } from "react";

import type { LabelOption } from "./LabelPill";
import { labelPillStyles } from "./LabelPill";

export type { LabelOption } from "./LabelPill";

const DEFAULT_COLOR_OPTIONS = [
  "#3b82f6",
  "#8b5cf6",
  "#6b7280",
  "#f59e0b",
  "#ef4444",
  "#22c55e",
  "#ec4899",
  "#f97316",
  "#eab308",
  "#06b6d4",
];

type LabelPickerProps = {
  labels: LabelOption[];
  selectedLabelIDs: string[];
  onAdd: (labelID: string) => void;
  onRemove: (labelID: string) => void;
  onCreate: (name: string, color: string) => void | Promise<void>;
  buttonLabel?: string;
  colorOptions?: string[];
};

export default function LabelPicker({
  labels,
  selectedLabelIDs,
  onAdd,
  onRemove,
  onCreate,
  buttonLabel = "Manage labels",
  colorOptions = DEFAULT_COLOR_OPTIONS,
}: LabelPickerProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState("");
  const [createColor, setCreateColor] = useState(colorOptions[0] ?? "#6b7280");
  const selectedSet = useMemo(() => new Set(selectedLabelIDs), [selectedLabelIDs]);
  const normalizedSearch = searchTerm.trim().toLowerCase();

  const filteredLabels = useMemo(() => {
    if (!normalizedSearch) {
      return labels;
    }
    return labels.filter((label) => label.name.toLowerCase().includes(normalizedSearch));
  }, [labels, normalizedSearch]);

  const hasExactMatch = useMemo(
    () => labels.some((label) => label.name.toLowerCase() === normalizedSearch),
    [labels, normalizedSearch],
  );
  const canCreate = normalizedSearch.length > 0 && !hasExactMatch;

  return (
    <div className="relative inline-block">
      <button
        type="button"
        aria-label={buttonLabel}
        aria-expanded={isOpen}
        className="inline-flex items-center gap-1 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800"
        onClick={() => setIsOpen((open) => !open)}
      >
        <span>{buttonLabel}</span>
      </button>

      {isOpen && (
        <div
          data-testid="label-picker-popover"
          className="absolute z-20 mt-2 w-72 rounded-xl border border-slate-200 bg-white p-3 shadow-xl dark:border-slate-700 dark:bg-slate-900"
        >
          <label htmlFor="label-picker-search" className="sr-only">
            Search labels
          </label>
          <input
            id="label-picker-search"
            aria-label="Search labels"
            type="text"
            value={searchTerm}
            onChange={(event) => setSearchTerm(event.target.value)}
            placeholder="Search labels..."
            className="mb-3 w-full rounded-lg border border-slate-200 px-2.5 py-2 text-sm text-slate-700 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
          />

          <div className="max-h-56 space-y-1 overflow-y-auto">
            {filteredLabels.map((label) => {
              const selected = selectedSet.has(label.id);
              const action = selected ? "Remove" : "Add";
              return (
                <button
                  key={label.id}
                  type="button"
                  aria-label={`${action} label ${label.name}`}
                  onClick={() => {
                    if (selected) {
                      onRemove(label.id);
                    } else {
                      onAdd(label.id);
                    }
                  }}
                  className="flex w-full items-center justify-between rounded-lg px-2 py-1.5 text-left text-sm hover:bg-slate-100 dark:hover:bg-slate-800"
                >
                  <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium" style={labelPillStyles(label.color)}>
                    {label.name}
                  </span>
                  {selected && (
                    <span
                      data-testid={`label-picker-selected-${label.id}`}
                      className="text-xs font-semibold text-emerald-600"
                    >
                      Selected
                    </span>
                  )}
                </button>
              );
            })}

            {filteredLabels.length === 0 && (
              <p className="rounded-lg bg-slate-50 px-2 py-2 text-xs text-slate-500 dark:bg-slate-800 dark:text-slate-400">
                No matching labels.
              </p>
            )}
          </div>

          {canCreate && (
            <div className="mt-3 border-t border-slate-200 pt-3 dark:border-slate-700">
              <div className="mb-2 flex flex-wrap gap-1.5">
                {colorOptions.map((color) => (
                  <button
                    key={color}
                    type="button"
                    aria-label={`Color ${color}`}
                    aria-pressed={createColor === color}
                    className="h-5 w-5 rounded-full ring-1 ring-slate-300 ring-offset-1 ring-offset-white dark:ring-slate-600 dark:ring-offset-slate-900"
                    style={{ backgroundColor: color }}
                    onClick={() => setCreateColor(color)}
                  />
                ))}
              </div>
              <button
                type="button"
                aria-label={`Create label "${searchTerm.trim()}"`}
                className="w-full rounded-lg border border-slate-200 bg-slate-50 px-2.5 py-2 text-left text-sm font-medium text-slate-700 hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
                onClick={() => {
                  const name = searchTerm.trim();
                  if (!name) {
                    return;
                  }
                  void onCreate(name, createColor);
                  setSearchTerm("");
                }}
              >
                Create label "{searchTerm.trim()}"
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
