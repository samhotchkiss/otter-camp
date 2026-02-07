import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useProjectListFilters } from './useProjectListFilters';

const PRESETS_KEY = 'ottercamp.projects.filters.presets';
const ACTIVE_KEY = 'ottercamp.projects.filters.activePresetId';

describe('useProjectListFilters', () => {
  beforeEach(() => {
    localStorage.removeItem(PRESETS_KEY);
    localStorage.removeItem(ACTIVE_KEY);
  });

  it('initializes with default filters', () => {
    const { result } = renderHook(() => useProjectListFilters());

    expect(result.current.filters).toEqual({
      search: '',
      status: null,
      assignee: null,
      priority: null,
      sort: 'updated',
    });
    expect(result.current.presets).toEqual([]);
    expect(result.current.activePresetId).toBe(null);
    expect(result.current.isActivePresetDirty).toBe(false);
  });

  it('updates filters', () => {
    const { result } = renderHook(() => useProjectListFilters());

    act(() => {
      result.current.setSearch('otter');
      result.current.setStatus('active');
      result.current.setAssignee('scout');
      result.current.setPriority('urgent');
      result.current.setSort('priority');
    });

    expect(result.current.filters).toEqual({
      search: 'otter',
      status: 'active',
      assignee: 'scout',
      priority: 'urgent',
      sort: 'priority',
    });
  });

  it('debounces search value', () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useProjectListFilters());

    act(() => {
      result.current.setSearch('hello');
    });

    expect(result.current.debouncedSearch).toBe('');

    act(() => {
      vi.advanceTimersByTime(250);
    });

    expect(result.current.debouncedSearch).toBe('hello');
    vi.useRealTimers();
  });

  it('saves presets to localStorage and selects them', () => {
    const { result } = renderHook(() => useProjectListFilters());

    act(() => {
      result.current.setStatus('active');
      result.current.setPriority('high');
    });

    let presetId = '';
    act(() => {
      const preset = result.current.savePreset('Active high');
      expect(preset).not.toBeNull();
      presetId = preset?.id ?? '';
    });

    expect(presetId).not.toBe('');
    expect(result.current.activePresetId).toBe(presetId);
    expect(result.current.presets.some((p) => p.id === presetId)).toBe(true);
    expect(localStorage.getItem(PRESETS_KEY)).toContain('Active high');
    expect(localStorage.getItem(ACTIVE_KEY)).toBe(presetId);
  });

  it('marks a preset as modified and can update it', async () => {
    const { result } = renderHook(() => useProjectListFilters());

    act(() => {
      result.current.setStatus('active');
    });

    act(() => {
      result.current.savePreset('Active');
    });

    expect(result.current.activePreset).not.toBeNull();
    expect(result.current.isActivePresetDirty).toBe(false);

    act(() => {
      result.current.setPriority('urgent');
    });

    expect(result.current.activePresetId).not.toBeNull();
    expect(result.current.isActivePresetDirty).toBe(true);

    act(() => {
      const updated = result.current.updateActivePreset();
      expect(updated).not.toBeNull();
    });

    await waitFor(() => {
      expect(result.current.isActivePresetDirty).toBe(false);
    });
    expect(result.current.activePreset?.filters.priority).toBe('urgent');
  });

  it('applies a preset', () => {
    const { result } = renderHook(() => useProjectListFilters());

    act(() => {
      result.current.setStatus('active');
      result.current.setPriority('high');
    });

    let presetId = '';
    act(() => {
      const preset = result.current.savePreset('Saved');
      expect(preset).not.toBeNull();
      presetId = preset?.id ?? '';
    });

    act(() => {
      result.current.setStatus('archived');
      result.current.setPriority('low');
    });

    expect(result.current.isActivePresetDirty).toBe(true);

    act(() => {
      result.current.applyPreset(presetId);
    });

    expect(result.current.filters.status).toBe('active');
    expect(result.current.filters.priority).toBe('high');
    expect(result.current.isActivePresetDirty).toBe(false);
  });

  it('loads presets + active preset selection from localStorage', async () => {
    const preset = {
      id: 'preset-1',
      name: 'Stored',
      filters: {
        search: 'camp',
        status: 'active',
        assignee: null,
        priority: null,
        sort: 'name',
      },
      createdAt: Date.now() - 1000,
      updatedAt: Date.now() - 1000,
    };

    localStorage.setItem(PRESETS_KEY, JSON.stringify([preset]));
    localStorage.setItem(ACTIVE_KEY, preset.id);

    const { result } = renderHook(() => useProjectListFilters());

    await waitFor(() => {
      expect(result.current.activePresetId).toBe('preset-1');
    });

    expect(result.current.filters.search).toBe('camp');
    expect(result.current.filters.status).toBe('active');
    expect(result.current.filters.sort).toBe('name');
  });
});
