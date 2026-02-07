import { useMemo, type ChangeEvent } from 'react';
import { PRIORITY_OPTIONS, STATUS_OPTIONS, type Priority, type Status } from '../types/filters';
import { useTaskFilters, type UseTaskFiltersReturn } from '../hooks/useTaskFilters';

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
};

const FilterDropdown = ({
  label,
  value,
  options,
  onChange,
  placeholder = 'All',
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
        className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 shadow-sm transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
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

export type ProjectFiltersProps = {
  /** Optional assignee options - provide to show assignee dropdown */
  assignees?: FilterOption[];
  /** Optional project options - provide to show project dropdown */
  projects?: FilterOption[];
  /** External filter state (if you want to manage it yourself) */
  filterState?: UseTaskFiltersReturn;
};

export default function ProjectFilters({
  assignees = [],
  projects = [],
  filterState,
}: ProjectFiltersProps) {
  const internalFilterState = useTaskFilters();
  const {
    filters,
    setSearch,
    setAssignee,
    setPriority,
    setStatus,
    setProject,
    clearFilter,
    clearAllFilters,
    hasActiveFilters,
  } = filterState ?? internalFilterState;

  const priorityOptions = useMemo(
    () => PRIORITY_OPTIONS.map((p) => ({ value: p.value, label: p.label })),
    []
  );

  const statusOptions = useMemo(
    () => STATUS_OPTIONS.map((s) => ({ value: s.value, label: s.label })),
    []
  );

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

    if (filters.assignee) {
      const assignee = assignees.find((a) => a.value === filters.assignee);
      chips.push({
        key: 'assignee',
        label: 'Assignee',
        value: assignee?.label ?? filters.assignee,
        onRemove: () => clearFilter('assignee'),
      });
    }

    if (filters.priority) {
      const priority = PRIORITY_OPTIONS.find((p) => p.value === filters.priority);
      chips.push({
        key: 'priority',
        label: 'Priority',
        value: priority?.label ?? filters.priority,
        onRemove: () => clearFilter('priority'),
      });
    }

    if (filters.status) {
      const status = STATUS_OPTIONS.find((s) => s.value === filters.status);
      chips.push({
        key: 'status',
        label: 'Status',
        value: status?.label ?? filters.status,
        onRemove: () => clearFilter('status'),
      });
    }

    if (filters.project) {
      const project = projects.find((p) => p.value === filters.project);
      chips.push({
        key: 'project',
        label: 'Project',
        value: project?.label ?? filters.project,
        onRemove: () => clearFilter('project'),
      });
    }

    return chips;
  }, [filters, assignees, projects, clearFilter]);

  return (
    <div className="space-y-4">
      {/* Filter Bar */}
      <div className="flex flex-wrap items-end gap-3">
        {/* Search Input */}
        <div className="min-w-[200px] flex-1">
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
              placeholder="Search tasks..."
              className="w-full rounded-lg border border-slate-200 bg-white py-2 pl-10 pr-3 text-sm text-slate-700 shadow-sm transition placeholder:text-slate-400 focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:placeholder:text-slate-500"
            />
          </div>
        </div>

        {/* Assignee Dropdown */}
        {assignees.length > 0 && (
          <FilterDropdown
            label="Assignee"
            value={filters.assignee}
            options={assignees}
            onChange={setAssignee}
            placeholder="Any assignee"
          />
        )}

        {/* Priority Dropdown */}
        <FilterDropdown
          label="Priority"
          value={filters.priority}
          options={priorityOptions}
          onChange={(v) => setPriority(v as Priority | null)}
          placeholder="Any priority"
        />

        {/* Status Dropdown */}
        <FilterDropdown
          label="Status"
          value={filters.status}
          options={statusOptions}
          onChange={(v) => setStatus(v as Status | null)}
          placeholder="Any status"
        />

        {/* Project Dropdown */}
        {projects.length > 0 && (
          <FilterDropdown
            label="Project"
            value={filters.project}
            options={projects}
            onChange={setProject}
            placeholder="All projects"
          />
        )}
      </div>

      {/* Active Filter Chips */}
      {hasActiveFilters && (
        <div className="flex flex-wrap items-center gap-2">
          {activeChips.map((chip) => (
            <FilterChip
              key={chip.key}
              label={chip.label}
              value={chip.value}
              onRemove={chip.onRemove}
            />
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

// Re-export hook for consumers who want direct access
export { useTaskFilters } from '../hooks/useTaskFilters';
export type { UseTaskFiltersReturn } from '../hooks/useTaskFilters';
