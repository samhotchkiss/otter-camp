export type ProjectSort = 'name' | 'updated' | 'priority';

export type ProjectListFilters = {
  search: string;
  status: string | null;
  assignee: string | null;
  priority: string | null;
  sort: ProjectSort;
};

export const DEFAULT_PROJECT_FILTERS: ProjectListFilters = {
  search: '',
  status: null,
  assignee: null,
  priority: null,
  sort: 'updated',
};

export type ProjectFilterPreset = {
  id: string;
  name: string;
  filters: ProjectListFilters;
  createdAt: number;
  updatedAt: number;
};

