export type Priority = 'low' | 'medium' | 'high' | 'urgent';
export type Status = 'backlog' | 'todo' | 'in-progress' | 'review' | 'done';

export type TaskFilters = {
  search: string;
  assignee: string | null;
  priority: Priority | null;
  status: Status | null;
  project: string | null;
};

export const DEFAULT_FILTERS: TaskFilters = {
  search: '',
  assignee: null,
  priority: null,
  status: null,
  project: null,
};

export const PRIORITY_OPTIONS: { value: Priority; label: string }[] = [
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'urgent', label: 'Urgent' },
];

export const STATUS_OPTIONS: { value: Status; label: string }[] = [
  { value: 'backlog', label: 'Backlog' },
  { value: 'todo', label: 'To Do' },
  { value: 'in-progress', label: 'In Progress' },
  { value: 'review', label: 'Review' },
  { value: 'done', label: 'Done' },
];
