import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useTaskFilters } from './useTaskFilters';

describe('useTaskFilters', () => {
  const originalLocation = window.location;
  const originalHistory = window.history;

  beforeEach(() => {
    // Reset URL before each test
    vi.stubGlobal('location', {
      ...originalLocation,
      pathname: '/',
      search: '',
    });
    vi.stubGlobal('history', {
      ...originalHistory,
      replaceState: vi.fn(),
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('initializes with default filters', () => {
    const { result } = renderHook(() => useTaskFilters());

    expect(result.current.filters).toEqual({
      search: '',
      assignee: null,
      priority: null,
      status: null,
      project: null,
    });
    expect(result.current.hasActiveFilters).toBe(false);
    expect(result.current.activeFilterCount).toBe(0);
  });

  it('sets search filter', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setSearch('test query');
    });

    expect(result.current.filters.search).toBe('test query');
    expect(result.current.hasActiveFilters).toBe(true);
    expect(result.current.activeFilterCount).toBe(1);
  });

  it('sets assignee filter', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setAssignee('user-123');
    });

    expect(result.current.filters.assignee).toBe('user-123');
  });

  it('sets priority filter', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setPriority('high');
    });

    expect(result.current.filters.priority).toBe('high');
  });

  it('sets status filter', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setStatus('in-progress');
    });

    expect(result.current.filters.status).toBe('in-progress');
  });

  it('sets project filter', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setProject('project-abc');
    });

    expect(result.current.filters.project).toBe('project-abc');
  });

  it('clears a specific filter', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setPriority('high');
      result.current.setStatus('todo');
    });

    expect(result.current.activeFilterCount).toBe(2);

    act(() => {
      result.current.clearFilter('priority');
    });

    expect(result.current.filters.priority).toBe(null);
    expect(result.current.filters.status).toBe('todo');
    expect(result.current.activeFilterCount).toBe(1);
  });

  it('clears all filters', () => {
    const { result } = renderHook(() => useTaskFilters());

    act(() => {
      result.current.setSearch('test');
      result.current.setPriority('urgent');
      result.current.setStatus('done');
      result.current.setAssignee('user-1');
    });

    expect(result.current.activeFilterCount).toBe(4);

    act(() => {
      result.current.clearAllFilters();
    });

    expect(result.current.filters).toEqual({
      search: '',
      assignee: null,
      priority: null,
      status: null,
      project: null,
    });
    expect(result.current.hasActiveFilters).toBe(false);
  });

  it('counts active filters correctly', () => {
    const { result } = renderHook(() => useTaskFilters());

    expect(result.current.activeFilterCount).toBe(0);

    act(() => {
      result.current.setSearch('test');
    });
    expect(result.current.activeFilterCount).toBe(1);

    act(() => {
      result.current.setPriority('low');
    });
    expect(result.current.activeFilterCount).toBe(2);

    act(() => {
      result.current.setStatus('backlog');
    });
    expect(result.current.activeFilterCount).toBe(3);
  });
});
