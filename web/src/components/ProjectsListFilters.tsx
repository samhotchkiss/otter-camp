import { useMemo, useState, type ChangeEvent } from 'react';
import { useProjectListFilters, type UseProjectListFiltersReturn } from '../hooks/useProjectListFilters';
import type { ProjectSort } from '../types/projectFilters';

type FilterOption = {
  value: string;
  label: string;
};

type FilterDropdownProps = {
  label: string;
  value: string | null;
  options: FilterOption[];
  onChange: (value: string | null) => void;
  placeholder?: string;
  disabled?: boolean;
};

const FilterDropdown = ({
  label,
  value,
  options,
  onChange,
  placeholder = 'All',
  disabled,
}: FilterDropdownProps) => {
  const handleChange = (e: ChangeEvent<HTMLSelectElement>) => {
    const val = e.target.value;
    onChange(val === '' ? null : val);
  };

  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
        {label}
      </label>
      <select
        value={value ?? ''}
        onChange={handleChange}
        disabled={disabled}
        className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
      >
        <option value="">{placeholder}</option>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  );
};

type FilterChipProps = {
  label: string;
  value: string;
  onRemove: () => void;
};

const FilterChip = ({ label, value, onRemove }: FilterChipProps) => (
  <span className="inline-flex items-center gap-1.5 rounded-full bg-sky-100 px-3 py-1 text-sm font-medium text-sky-700 dark:bg-sky-900/40 dark:text-sky-300">
    <span className="text-xs text-sky-500 dark:text-sky-400">{label}:</span>
    {value}
    <button
      type="button"
      onClick={onRemove}
      className="ml-0.5 rounded-full p-0.5 transition hover:bg-sky-200 dark:hover:bg-sky-800"
      aria-label={`Remove ${label} filter`}
    >
      <svg className="h-3 w-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
      </svg>
    </button>
  </span>
);

export type ProjectsListFiltersProps = {
  statusOptions?: FilterOption[];
  assigneeOptions?: FilterOption[];
  priorityOptions?: FilterOption[];
  filterState?: UseProjectListFiltersReturn;
};

const sortOptions: Array<{ value: ProjectSort; label: string }> = [
  { value: 'updated', label: 'Updated (newest)' },
  { value: 'name', label: 'Name (A–Z)' },
  { value: 'priority', label: 'Priority (highest)' },
];

export default function ProjectsListFilters({
  statusOptions = [],
  assigneeOptions = [],
  priorityOptions = [],
  filterState,
}: ProjectsListFiltersProps) {
  const internalFilterState = useProjectListFilters();
  const {
    filters,
    presets,
    activePresetId,
    activePreset,
    isActivePresetDirty,
    setSearch,
    setStatus,
    setAssignee,
    setPriority,
    setSort,
    applyPreset,
    savePreset,
    updateActivePreset,
    deletePreset,
    clearFilter,
    clearAllFilters,
    hasActiveFilters,
  } = filterState ?? internalFilterState;

  const [isSavingPreset, setIsSavingPreset] = useState(false);
  const [presetName, setPresetName] = useState('');

  const presetSelectValue = useMemo(() => {
    if (activePresetId) return activePresetId;
    return hasActiveFilters ? '__custom__' : '__default__';
  }, [activePresetId, hasActiveFilters]);

  const presetNameConflict = useMemo(() => {
    const trimmed = presetName.trim().toLowerCase();
    if (!trimmed) return null;
    return presets.find((p) => p.name.toLowerCase() === trimmed) ?? null;
  }, [presetName, presets]);

  const activeChips = useMemo(() => {
    const chips: { key: string; label: string; value: string; onRemove: () => void }[] = [];

    if (filters.search) {
      chips.push({
        key: 'search',
        label: 'Search',
        value: filters.search,
        onRemove: () => clearFilter('search'),
      });
    }

    if (filters.status) {
      const status = statusOptions.find((s) => s.value === filters.status);
      chips.push({
        key: 'status',
        label: 'Status',
        value: status?.label ?? filters.status,
        onRemove: () => clearFilter('status'),
      });
    }

    if (filters.assignee) {
      const assignee = assigneeOptions.find((a) => a.value === filters.assignee);
      chips.push({
        key: 'assignee',
        label: 'Assignee',
        value: assignee?.label ?? filters.assignee,
        onRemove: () => clearFilter('assignee'),
      });
    }

    if (filters.priority) {
      const priority = priorityOptions.find((p) => p.value === filters.priority);
      chips.push({
        key: 'priority',
        label: 'Priority',
        value: priority?.label ?? filters.priority,
        onRemove: () => clearFilter('priority'),
      });
    }

    return chips;
  }, [filters, statusOptions, assigneeOptions, priorityOptions, clearFilter]);

  return (
    <div className="space-y-4">
      {/* Presets */}
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-1 flex-wrap items-end gap-3">
          <div className="min-w-[220px]">
            <label className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
              Preset{isActivePresetDirty ? ' (modified)' : ''}
            </label>
            <select
              value={presetSelectValue}
              onChange={(e) => {
                const value = e.target.value;
                if (value === '__default__') {
                  applyPreset(null);
                  return;
                }
                if (value === '__custom__') {
                  return;
                }
                applyPreset(value);
              }}
              className="mt-1 w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
            >
              <option value="__default__">All projects</option>
              <option value="__custom__">Custom</option>
              {presets.map((preset) => (
                <option key={preset.id} value={preset.id}>
                  {preset.name}
                </option>
              ))}
            </select>
          </div>

          <button
            type="button"
            onClick={() => setIsSavingPreset(true)}
            className="inline-flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Save preset
          </button>

          {activePreset && (
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => updateActivePreset()}
                disabled={!isActivePresetDirty}
                className="inline-flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
              >
                {isActivePresetDirty ? 'Update' : 'Up to date'}
              </button>
              <button
                type="button"
                onClick={() => deletePreset(activePreset.id)}
                className="inline-flex items-center gap-2 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm font-medium text-rose-700 shadow-sm transition hover:bg-rose-100 dark:border-rose-900/50 dark:bg-rose-900/20 dark:text-rose-200 dark:hover:bg-rose-900/30"
              >
                Delete
              </button>
            </div>
          )}
        </div>
      </div>

      {isSavingPreset && (
        <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-800">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
            <div className="flex-1">
              <label className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
                Preset name
              </label>
              <input
                type="text"
                value={presetName}
                onChange={(e) => setPresetName(e.target.value)}
                placeholder="e.g. Active high-priority"
                className="mt-1 w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition placeholder:text-slate-400 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 dark:placeholder:text-slate-500"
              />
              {presetNameConflict && (
                <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                  This will update the existing preset: <span className="font-medium">{presetNameConflict.name}</span>
                </p>
              )}
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  const saved = savePreset(presetName);
                  if (!saved) return;
                  setPresetName('');
                  setIsSavingPreset(false);
                }}
                className="inline-flex items-center justify-center rounded-lg bg-sky-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-sky-700 focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2 dark:focus:ring-offset-slate-900"
              >
                Save
              </button>
              <button
                type="button"
                onClick={() => {
                  setPresetName('');
                  setIsSavingPreset(false);
                }}
                className="inline-flex items-center justify-center rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Filters */}
      <div className="flex flex-wrap items-end gap-3">
        {/* Search Input */}
        <div className="min-w-[220px] flex-1">
          <label className="mb-1 block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Search
          </label>
          <div className="relative">
            <svg
              className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
              />
            </svg>
            <input
              type="text"
              value={filters.search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search projects..."
              className="w-full rounded-lg border border-slate-200 bg-white py-2 pl-10 pr-3 text-sm text-slate-700 shadow-sm transition placeholder:text-slate-400 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:placeholder:text-slate-500"
            />
          </div>
        </div>

        <FilterDropdown
          label="Status"
          value={filters.status}
          options={statusOptions}
          onChange={setStatus}
          placeholder={statusOptions.length > 0 ? 'Any status' : 'No statuses'}
          disabled={statusOptions.length === 0}
        />

        <FilterDropdown
          label="Assignee"
          value={filters.assignee}
          options={assigneeOptions}
          onChange={setAssignee}
          placeholder={assigneeOptions.length > 0 ? 'Any assignee' : 'No assignees'}
          disabled={assigneeOptions.length === 0}
        />

        <FilterDropdown
          label="Priority"
          value={filters.priority}
          options={priorityOptions}
          onChange={setPriority}
          placeholder={priorityOptions.length > 0 ? 'Any priority' : 'No priorities'}
          disabled={priorityOptions.length === 0}
        />

        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Sort
          </label>
          <select
            value={filters.sort}
            onChange={(e) => setSort(e.target.value as ProjectSort)}
            className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
          >
            {sortOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>
      </div>

      {/* Active Filter Chips */}
      {hasActiveFilters && (
        <div className="flex flex-wrap items-center gap-2">
          {activeChips.map((chip) => (
            <FilterChip key={chip.key} label={chip.label} value={chip.value} onRemove={chip.onRemove} />
          ))}
          <button
            type="button"
            onClick={clearAllFilters}
            className="rounded-full px-3 py-1 text-sm font-medium text-slate-500 transition hover:bg-slate-100 hover:text-slate-700 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-200"
          >
            Clear all
          </button>
        </div>
      )}
    </div>
  );
}

export { useProjectListFilters } from '../hooks/useProjectListFilters';
export type { UseProjectListFiltersReturn } from '../hooks/useProjectListFilters';
