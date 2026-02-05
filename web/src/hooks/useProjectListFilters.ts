import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  DEFAULT_PROJECT_FILTERS,
  type ProjectFilterPreset,
  type ProjectListFilters,
  type ProjectSort,
} from '../types/projectFilters';

const DEBOUNCE_MS = 250;

const PRESETS_STORAGE_KEY = 'ottercamp.projects.filters.presets';
const ACTIVE_PRESET_STORAGE_KEY = 'ottercamp.projects.filters.activePresetId';

const safeJsonParse = (raw: string): unknown => {
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
};

const isRecord = (value: unknown): value is Record<string, unknown> => {
  return !!value && typeof value === 'object' && !Array.isArray(value);
};

const isProjectSort = (value: unknown): value is ProjectSort => {
  return value === 'name' || value === 'updated' || value === 'priority';
};

const isProjectListFilters = (value: unknown): value is ProjectListFilters => {
  if (!isRecord(value)) return false;

  const search = value.search;
  const status = value.status;
  const assignee = value.assignee;
  const priority = value.priority;
  const sort = value.sort;

  const isNullableString = (v: unknown) => v === null || typeof v === 'string';

  return (
    typeof search === 'string' &&
    isNullableString(status) &&
    isNullableString(assignee) &&
    isNullableString(priority) &&
    isProjectSort(sort)
  );
};

const isPreset = (value: unknown): value is ProjectFilterPreset => {
  if (!isRecord(value)) return false;
  return (
    typeof value.id === 'string' &&
    typeof value.name === 'string' &&
    typeof value.createdAt === 'number' &&
    typeof value.updatedAt === 'number' &&
    isProjectListFilters(value.filters)
  );
};

const readPresets = (): ProjectFilterPreset[] => {
  if (typeof window === 'undefined') return [];
  const raw = window.localStorage.getItem(PRESETS_STORAGE_KEY);
  if (!raw) return [];

  const parsed = safeJsonParse(raw);
  if (!Array.isArray(parsed)) return [];

  return parsed.filter(isPreset);
};

const writePresets = (presets: ProjectFilterPreset[]) => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(PRESETS_STORAGE_KEY, JSON.stringify(presets));
  } catch {
    // Ignore quota/serialization errors
  }
};

const readActivePresetId = (): string | null => {
  if (typeof window === 'undefined') return null;
  const raw = window.localStorage.getItem(ACTIVE_PRESET_STORAGE_KEY);
  if (!raw) return null;
  return raw;
};

const writeActivePresetId = (id: string | null) => {
  if (typeof window === 'undefined') return;
  try {
    if (!id) {
      window.localStorage.removeItem(ACTIVE_PRESET_STORAGE_KEY);
      return;
    }
    window.localStorage.setItem(ACTIVE_PRESET_STORAGE_KEY, id);
  } catch {
    // Ignore
  }
};

const generatePresetId = () => {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `preset_${Date.now()}_${Math.random().toString(16).slice(2)}`;
};

const areFiltersEqual = (a: ProjectListFilters, b: ProjectListFilters) => {
  return (
    a.search === b.search &&
    a.status === b.status &&
    a.assignee === b.assignee &&
    a.priority === b.priority &&
    a.sort === b.sort
  );
};

export type UseProjectListFiltersReturn = {
  filters: ProjectListFilters;
  debouncedSearch: string;
  presets: ProjectFilterPreset[];
  activePresetId: string | null;
  activePreset: ProjectFilterPreset | null;
  isActivePresetDirty: boolean;
  setSearch: (value: string) => void;
  setStatus: (value: string | null) => void;
  setAssignee: (value: string | null) => void;
  setPriority: (value: string | null) => void;
  setSort: (value: ProjectSort) => void;
  applyPreset: (presetId: string | null) => void;
  savePreset: (name: string) => ProjectFilterPreset | null;
  updateActivePreset: () => ProjectFilterPreset | null;
  renamePreset: (presetId: string, name: string) => ProjectFilterPreset | null;
  deletePreset: (presetId: string) => void;
  clearFilter: (key: 'search' | 'status' | 'assignee' | 'priority') => void;
  clearAllFilters: () => void;
  activeFilterCount: number;
  hasActiveFilters: boolean;
};

export const useProjectListFilters = (): UseProjectListFiltersReturn => {
  const [filters, setFilters] = useState<ProjectListFilters>(DEFAULT_PROJECT_FILTERS);
  const [debouncedSearch, setDebouncedSearch] = useState(filters.search);
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [presets, setPresets] = useState<ProjectFilterPreset[]>([]);
  const [activePresetId, setActivePresetId] = useState<string | null>(null);
  const hasLoadedRef = useRef(false);

  useEffect(() => {
    if (hasLoadedRef.current) return;
    hasLoadedRef.current = true;

    const storedPresets = readPresets();
    setPresets(storedPresets);

    const storedActivePresetId = readActivePresetId();
    if (!storedActivePresetId) {
      return;
    }

    const preset = storedPresets.find((p) => p.id === storedActivePresetId);
    if (!preset) {
      writeActivePresetId(null);
      return;
    }

    setActivePresetId(preset.id);
    setFilters(preset.filters);
  }, []);

  const activePreset = useMemo(() => {
    if (!activePresetId) return null;
    return presets.find((p) => p.id === activePresetId) ?? null;
  }, [activePresetId, presets]);

  const isActivePresetDirty = useMemo(() => {
    if (!activePreset) return false;
    return !areFiltersEqual(filters, activePreset.filters);
  }, [filters, activePreset]);

  // Debounce search input for downstream filtering.
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

  // If the active preset is removed, clear the selection.
  useEffect(() => {
    if (!activePresetId) return;
    if (!presets.some((p) => p.id === activePresetId)) {
      setActivePresetId(null);
      writeActivePresetId(null);
    }
  }, [activePresetId, presets]);

  const setSearch = useCallback((value: string) => {
    setFilters((prev) => ({ ...prev, search: value }));
  }, []);

  const setStatus = useCallback((value: string | null) => {
    setFilters((prev) => ({ ...prev, status: value }));
  }, []);

  const setAssignee = useCallback((value: string | null) => {
    setFilters((prev) => ({ ...prev, assignee: value }));
  }, []);

  const setPriority = useCallback((value: string | null) => {
    setFilters((prev) => ({ ...prev, priority: value }));
  }, []);

  const setSort = useCallback((value: ProjectSort) => {
    setFilters((prev) => ({ ...prev, sort: value }));
  }, []);

  const applyPreset = useCallback(
    (presetId: string | null) => {
      if (!presetId) {
        setActivePresetId(null);
        writeActivePresetId(null);
        setFilters(DEFAULT_PROJECT_FILTERS);
        return;
      }

      const preset = presets.find((p) => p.id === presetId);
      if (!preset) {
        setActivePresetId(null);
        writeActivePresetId(null);
        return;
      }

      setActivePresetId(preset.id);
      writeActivePresetId(preset.id);
      setFilters(preset.filters);
    },
    [presets]
  );

  const savePreset = useCallback(
    (name: string) => {
      const trimmed = name.trim();
      if (!trimmed) return null;

      const now = Date.now();
      const existing = presets.find(
        (p) => p.name.toLowerCase() === trimmed.toLowerCase()
      );

      if (existing) {
        const updated: ProjectFilterPreset = {
          ...existing,
          name: trimmed,
          filters,
          updatedAt: now,
        };
        const next = presets.map((p) => (p.id === existing.id ? updated : p));
        setPresets(next);
        writePresets(next);
        setActivePresetId(updated.id);
        writeActivePresetId(updated.id);
        return updated;
      }

      const preset: ProjectFilterPreset = {
        id: generatePresetId(),
        name: trimmed,
        filters,
        createdAt: now,
        updatedAt: now,
      };

      const next = [preset, ...presets];
      setPresets(next);
      writePresets(next);
      setActivePresetId(preset.id);
      writeActivePresetId(preset.id);
      return preset;
    },
    [filters, presets]
  );

  const updateActivePreset = useCallback(() => {
    if (!activePresetId) return null;
    const existing = presets.find((p) => p.id === activePresetId);
    if (!existing) return null;

    const now = Date.now();
    const updated: ProjectFilterPreset = {
      ...existing,
      filters,
      updatedAt: now,
    };
    const next = presets.map((p) => (p.id === existing.id ? updated : p));
    setPresets(next);
    writePresets(next);
    return updated;
  }, [activePresetId, filters, presets]);

  const renamePreset = useCallback(
    (presetId: string, name: string) => {
      const trimmed = name.trim();
      if (!trimmed) return null;

      const existing = presets.find((p) => p.id === presetId);
      if (!existing) return null;

      const now = Date.now();
      const updated: ProjectFilterPreset = {
        ...existing,
        name: trimmed,
        updatedAt: now,
      };

      const next = presets.map((p) => (p.id === presetId ? updated : p));
      setPresets(next);
      writePresets(next);
      return updated;
    },
    [presets]
  );

  const deletePreset = useCallback(
    (presetId: string) => {
      const next = presets.filter((p) => p.id !== presetId);
      setPresets(next);
      writePresets(next);
      if (activePresetId === presetId) {
        setActivePresetId(null);
        writeActivePresetId(null);
      }
    },
    [presets, activePresetId]
  );

  const clearFilter = useCallback((key: 'search' | 'status' | 'assignee' | 'priority') => {
    setFilters((prev) => ({
      ...prev,
      [key]: key === 'search' ? '' : null,
    }));
  }, []);

  const clearAllFilters = useCallback(() => {
    setActivePresetId(null);
    writeActivePresetId(null);
    setFilters(DEFAULT_PROJECT_FILTERS);
  }, []);

  const activeFilterCount = useMemo(() => {
    let count = 0;
    if (filters.search) count++;
    if (filters.status) count++;
    if (filters.assignee) count++;
    if (filters.priority) count++;
    return count;
  }, [filters]);

  const hasActiveFilters = activeFilterCount > 0;

  return {
    filters,
    debouncedSearch,
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
    renamePreset,
    deletePreset,
    clearFilter,
    clearAllFilters,
    activeFilterCount,
    hasActiveFilters,
  };
};
