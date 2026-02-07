import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { DEFAULT_FILTERS, type Priority, type Status, type TaskFilters } from '../types/filters';

const DEBOUNCE_MS = 300;

const parseUrlFilters = (): Partial<TaskFilters> => {
  if (typeof window === 'undefined') return {};
  
  const params = new URLSearchParams(window.location.search);
  const filters: Partial<TaskFilters> = {};
  
  const search = params.get('search');
  if (search) filters.search = search;
  
  const assignee = params.get('assignee');
  if (assignee) filters.assignee = assignee;
  
  const priority = params.get('priority') as Priority | null;
  if (priority && ['low', 'medium', 'high', 'urgent'].includes(priority)) {
    filters.priority = priority;
  }
  
  const status = params.get('status') as Status | null;
  if (status && ['backlog', 'todo', 'in-progress', 'review', 'done'].includes(status)) {
    filters.status = status;
  }
  
  const project = params.get('project');
  if (project) filters.project = project;
  
  return filters;
};

const updateUrl = (filters: TaskFilters) => {
  if (typeof window === 'undefined') return;
  
  const params = new URLSearchParams();
  
  if (filters.search) params.set('search', filters.search);
  if (filters.assignee) params.set('assignee', filters.assignee);
  if (filters.priority) params.set('priority', filters.priority);
  if (filters.status) params.set('status', filters.status);
  if (filters.project) params.set('project', filters.project);
  
  const newUrl = params.toString()
    ? `${window.location.pathname}?${params.toString()}`
    : window.location.pathname;
  
  window.history.replaceState({}, '', newUrl);
};

export type UseTaskFiltersReturn = {
  filters: TaskFilters;
  debouncedSearch: string;
  setSearch: (value: string) => void;
  setAssignee: (value: string | null) => void;
  setPriority: (value: Priority | null) => void;
  setStatus: (value: Status | null) => void;
  setProject: (value: string | null) => void;
  clearFilter: (key: keyof TaskFilters) => void;
  clearAllFilters: () => void;
  activeFilterCount: number;
  hasActiveFilters: boolean;
};

export const useTaskFilters = (): UseTaskFiltersReturn => {
  const [filters, setFilters] = useState<TaskFilters>(() => ({
    ...DEFAULT_FILTERS,
    ...parseUrlFilters(),
  }));
  
  const [debouncedSearch, setDebouncedSearch] = useState(filters.search);
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Debounce search input
  useEffect(() => {
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current);
    }
    
    debounceTimerRef.current = setTimeout(() => {
      setDebouncedSearch(filters.search);
    }, DEBOUNCE_MS);
    
    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current);
      }
    };
  }, [filters.search]);

  // Sync to URL when filters change (except during search debounce)
  useEffect(() => {
    const filtersForUrl = { ...filters, search: debouncedSearch };
    updateUrl(filtersForUrl);
  }, [filters.assignee, filters.priority, filters.status, filters.project, debouncedSearch]);

  const setSearch = useCallback((value: string) => {
    setFilters((prev) => ({ ...prev, search: value }));
  }, []);

  const setAssignee = useCallback((value: string | null) => {
    setFilters((prev) => ({ ...prev, assignee: value }));
  }, []);

  const setPriority = useCallback((value: Priority | null) => {
    setFilters((prev) => ({ ...prev, priority: value }));
  }, []);

  const setStatus = useCallback((value: Status | null) => {
    setFilters((prev) => ({ ...prev, status: value }));
  }, []);

  const setProject = useCallback((value: string | null) => {
    setFilters((prev) => ({ ...prev, project: value }));
  }, []);

  const clearFilter = useCallback((key: keyof TaskFilters) => {
    setFilters((prev) => ({
      ...prev,
      [key]: key === 'search' ? '' : null,
    }));
  }, []);

  const clearAllFilters = useCallback(() => {
    setFilters(DEFAULT_FILTERS);
  }, []);

  const activeFilterCount = useMemo(() => {
    let count = 0;
    if (filters.search) count++;
    if (filters.assignee) count++;
    if (filters.priority) count++;
    if (filters.status) count++;
    if (filters.project) count++;
    return count;
  }, [filters]);

  const hasActiveFilters = activeFilterCount > 0;

  return {
    filters,
    debouncedSearch,
    setSearch,
    setAssignee,
    setPriority,
    setStatus,
    setProject,
    clearFilter,
    clearAllFilters,
    activeFilterCount,
    hasActiveFilters,
  };
};
