import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { isDemoMode } from "../lib/demo";

// --- Types ---

export type SearchResultType = "task" | "project" | "agent" | "message";

export type SearchResult = {
  id: string;
  type: SearchResultType;
  title: string;
  titleHighlight?: string;
  subtitle?: string;
  subtitleHighlight?: string;
  meta?: string;
  url?: string;
};

export type GroupedResults = {
  type: SearchResultType;
  label: string;
  icon: string;
  results: SearchResult[];
};

type RecentSearch = {
  query: string;
  timestamp: number;
};

type GlobalSearchProps = {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  orgId?: string;
  onSelect?: (result: SearchResult) => void;
};

// --- Constants ---

const STORAGE_KEY = "ottercamp.globalSearch.recent";
const MAX_RECENT_SEARCHES = 5;
const DEBOUNCE_MS = 200;
const API_BASE = import.meta.env.VITE_API_URL || "";

const TYPE_CONFIG: Record<SearchResultType, { label: string; icon: string }> = {
  task: { label: "Tasks", icon: "✅" },
  project: { label: "Projects", icon: "📁" },
  agent: { label: "Agents", icon: "🤖" },
  message: { label: "Messages", icon: "💬" },
};

const TYPE_ORDER: SearchResultType[] = ["task", "project", "agent", "message"];

// --- Fuzzy Matching ---

const normalize = (text: string) => text.toLowerCase().trim();

const highlightMatch = (text: string, query: string): string => {
  if (!query.trim()) return text;

  const needle = normalize(query);
  const source = normalize(text);

  // Try exact substring first
  const exactIndex = source.indexOf(needle);
  if (exactIndex !== -1) {
    return (
      text.slice(0, exactIndex) +
      "<mark>" +
      text.slice(exactIndex, exactIndex + needle.length) +
      "</mark>" +
      text.slice(exactIndex + needle.length)
    );
  }

  // Fuzzy highlight
  let result = "";
  let lastIndex = 0;
  let sourceIndex = 0;

  for (const char of needle) {
    const index = source.indexOf(char, sourceIndex);
    if (index === -1) return text;

    result += text.slice(lastIndex, index);
    result += "<mark>" + text[index] + "</mark>";
    lastIndex = index + 1;
    sourceIndex = index + 1;
  }

  result += text.slice(lastIndex);
  return result;
};

// --- LocalStorage helpers ---

const readRecentSearches = (): RecentSearch[] => {
  if (typeof window === "undefined") return [];
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
};

const saveRecentSearch = (query: string) => {
  if (typeof window === "undefined" || !query.trim()) return;

  const recent = readRecentSearches();
  const filtered = recent.filter((r) => r.query !== query);
  const updated = [{ query, timestamp: Date.now() }, ...filtered].slice(
    0,
    MAX_RECENT_SEARCHES
  );

  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(updated));
  } catch {
    // Ignore quota errors
  }
};

const clearRecentSearches = () => {
  if (typeof window === "undefined") return;
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Ignore
  }
};

// --- API ---

type ApiSearchResponse = {
  query: string;
  results: {
    tasks: Array<{
      id: string;
      number: number;
      title: string;
      title_highlight?: string;
      description_highlight?: string;
      status: string;
      priority: string;
    }>;
    projects: Array<{
      id: string;
      name: string;
      name_highlight?: string;
      description?: string;
      description_highlight?: string;
      status: string;
    }>;
    agents: Array<{
      id: string;
      slug: string;
      display_name: string;
      display_name_highlight?: string;
      status: string;
    }>;
    messages: Array<{
      id: string;
      content: string;
      content_highlight?: string;
      task_id: string;
      created_at: string;
    }>;
  };
};

const fetchSearchResults = async (
  query: string,
  orgId?: string
): Promise<SearchResult[]> => {
  const url = new URL(`${API_BASE}/api/search`, window.location.origin);
  url.searchParams.set("q", query);
  
  // Use demo mode if no orgId or on demo subdomain
  if (!orgId || isDemoMode()) {
    url.searchParams.set("demo", "true");
  } else {
    url.searchParams.set("org_id", orgId);
  }

  const response = await fetch(url.toString());
  if (!response.ok) {
    throw new Error(`Search failed: ${response.status}`);
  }

  const data: ApiSearchResponse = await response.json();
  const results: SearchResult[] = [];

  // Map tasks
  for (const task of data.results?.tasks ?? []) {
    results.push({
      id: task.id,
      type: "task",
      title: task.title,
      titleHighlight: task.title_highlight,
      subtitle: task.description_highlight?.replace(/<\/?mark>/g, "") ?? undefined,
      subtitleHighlight: task.description_highlight,
      meta: `#${task.number} · ${task.status} · ${task.priority}`,
      url: `/tasks/${task.id}`,
    });
  }

  // Map projects
  for (const project of data.results?.projects ?? []) {
    results.push({
      id: project.id,
      type: "project",
      title: project.name,
      titleHighlight: project.name_highlight,
      subtitle: project.description,
      subtitleHighlight: project.description_highlight,
      meta: project.status,
      url: `/projects/${project.id}`,
    });
  }

  // Map agents
  for (const agent of data.results?.agents ?? []) {
    results.push({
      id: agent.id,
      type: "agent",
      title: agent.display_name,
      titleHighlight: agent.display_name_highlight,
      subtitle: `@${agent.slug}`,
      meta: agent.status,
      url: `/agents/${agent.id}`,
    });
  }

  // Map messages (comments)
  for (const msg of data.results?.messages ?? []) {
    const preview =
      msg.content.length > 80 ? msg.content.slice(0, 80) + "…" : msg.content;
    results.push({
      id: msg.id,
      type: "message",
      title: preview,
      titleHighlight: msg.content_highlight,
      meta: new Date(msg.created_at).toLocaleDateString(),
      url: `/tasks/${msg.task_id}#comment-${msg.id}`,
    });
  }

  return results;
};

// --- Component ---

export default function GlobalSearch({
  isOpen,
  onOpenChange,
  orgId,
  onSelect,
}: GlobalSearchProps) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [recentSearches, setRecentSearches] = useState<RecentSearch[]>([]);
  const [useLocalFuzzy] = useState(false); // Always use API (supports demo mode)

  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  // Load recent searches on mount
  useEffect(() => {
    setRecentSearches(readRecentSearches());
  }, []);

  // Focus input when opened
  useEffect(() => {
    if (!isOpen) {
      setQuery("");
      setResults([]);
      setActiveIndex(0);
      return;
    }

    const handle = requestAnimationFrame(() => {
      inputRef.current?.focus();
    });

    return () => cancelAnimationFrame(handle);
  }, [isOpen]);

  // Debounced search
  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
    }

    const trimmed = query.trim();
    if (!trimmed) {
      setResults([]);
      setIsLoading(false);
      return;
    }

    if (useLocalFuzzy) {
      // Client-side fuzzy matching only
      setIsLoading(false);
      return;
    }

    setIsLoading(true);

    debounceRef.current = setTimeout(async () => {
      try {
        // Will use demo mode if no orgId
        const apiResults = await fetchSearchResults(trimmed, orgId);
        setResults(apiResults);
      } catch (err) {
        console.error("Search error:", err);
        setResults([]);
      } finally {
        setIsLoading(false);
      }
    }, DEBOUNCE_MS);

    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, [query, orgId, useLocalFuzzy]);

  // Apply local fuzzy highlighting if using local mode
  const processedResults = useMemo(() => {
    if (!query.trim()) return results;

    return results.map((result) => ({
      ...result,
      titleHighlight: result.titleHighlight || highlightMatch(result.title, query),
      subtitleHighlight:
        result.subtitleHighlight ||
        (result.subtitle ? highlightMatch(result.subtitle, query) : undefined),
    }));
  }, [results, query]);

  // Group results by type
  const groupedResults = useMemo<GroupedResults[]>(() => {
    const groups = new Map<SearchResultType, SearchResult[]>();

    for (const result of processedResults) {
      const existing = groups.get(result.type) || [];
      existing.push(result);
      groups.set(result.type, existing);
    }

    return TYPE_ORDER.filter((type) => groups.has(type) && groups.get(type)!.length > 0)
      .map((type) => ({
        type,
        label: TYPE_CONFIG[type].label,
        icon: TYPE_CONFIG[type].icon,
        results: groups.get(type)!,
      }));
  }, [processedResults]);

  // Flat list for keyboard navigation
  const flatResults = useMemo(() => {
    return groupedResults.flatMap((group) => group.results);
  }, [groupedResults]);

  // Reset active index when results change
  useEffect(() => {
    setActiveIndex(0);
  }, [flatResults.length]);

  // Scroll active item into view
  useEffect(() => {
    const activeElement = listRef.current?.querySelector(
      `[data-index="${activeIndex}"]`
    );
    activeElement?.scrollIntoView({ block: "nearest" });
  }, [activeIndex]);

  const handleSelect = useCallback(
    (result: SearchResult) => {
      saveRecentSearch(query);
      setRecentSearches(readRecentSearches());

      if (onSelect) {
        onSelect(result);
      } else if (result.url) {
        window.location.href = result.url;
      }

      onOpenChange(false);
    },
    [query, onSelect, onOpenChange]
  );

  const handleRecentClick = useCallback((recentQuery: string) => {
    setQuery(recentQuery);
    inputRef.current?.focus();
  }, []);

  const handleClearRecent = useCallback(() => {
    clearRecentSearches();
    setRecentSearches([]);
  }, []);

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onOpenChange(false);
        return;
      }

      if (flatResults.length === 0) return;

      if (event.key === "ArrowDown") {
        event.preventDefault();
        setActiveIndex((i) => Math.min(i + 1, flatResults.length - 1));
        return;
      }

      if (event.key === "ArrowUp") {
        event.preventDefault();
        setActiveIndex((i) => Math.max(i - 1, 0));
        return;
      }

      if (event.key === "Enter") {
        event.preventDefault();
        const result = flatResults[activeIndex];
        if (result) {
          handleSelect(result);
        }
        return;
      }
    },
    [flatResults, activeIndex, handleSelect, onOpenChange]
  );

  if (!isOpen) return null;

  const showRecent = !query.trim() && recentSearches.length > 0;
  const showResults = query.trim() && !isLoading;
  const showEmpty = query.trim() && !isLoading && flatResults.length === 0;

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-slate-950/70 px-4 pt-[15vh] backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label="Global search"
      onClick={() => onOpenChange(false)}
    >
      <div
        className="w-full max-w-2xl overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/95 shadow-2xl shadow-slate-950/40"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Search Input */}
        <div className="flex items-center gap-3 border-b border-slate-800 px-5 py-4">
          <div className="flex h-10 w-10 items-center justify-center rounded-full bg-slate-800 text-lg">
            🔍
          </div>
          <div className="flex-1">
            <input
              ref={inputRef}
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Search tasks, projects, agents, messages…"
              className="w-full bg-transparent text-lg font-medium text-slate-100 outline-none placeholder:text-slate-500"
              aria-label="Search"
              autoComplete="off"
              autoCorrect="off"
              spellCheck={false}
            />
          </div>
          {isLoading && (
            <div className="h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-slate-300" />
          )}
          <kbd className="hidden rounded-lg border border-slate-700 bg-slate-800 px-2 py-1 text-xs text-slate-400 sm:block">
            ESC
          </kbd>
        </div>

        {/* Results Area */}
        <div ref={listRef} className="max-h-[60vh] overflow-y-auto">
          {/* Recent Searches */}
          {showRecent && (
            <div className="px-5 py-4">
              <div className="flex items-center justify-between">
                <p className="text-xs font-semibold uppercase tracking-widest text-slate-500">
                  Recent Searches
                </p>
                <button
                  type="button"
                  onClick={handleClearRecent}
                  className="text-xs text-slate-500 hover:text-slate-300"
                >
                  Clear
                </button>
              </div>
              <div className="mt-3 space-y-1">
                {recentSearches.map((recent) => (
                  <button
                    key={recent.query}
                    type="button"
                    onClick={() => handleRecentClick(recent.query)}
                    className="flex w-full items-center gap-3 rounded-xl px-3 py-2 text-left text-slate-300 transition hover:bg-slate-800"
                  >
                    <span className="text-slate-500">🕐</span>
                    <span className="truncate">{recent.query}</span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Loading State */}
          {isLoading && query.trim() && (
            <div className="px-5 py-8 text-center text-slate-500">
              Searching…
            </div>
          )}

          {/* Empty State */}
          {showEmpty && (
            <div className="px-5 py-8 text-center">
              <p className="text-slate-400">No results found for "{query}"</p>
              <p className="mt-1 text-sm text-slate-500">
                Try a different search term
              </p>
            </div>
          )}

          {/* Grouped Results */}
          {showResults && flatResults.length > 0 && (
            <div className="py-2">
              {groupedResults.map((group) => (
                <div key={group.type} className="px-5 py-2">
                  <p className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-widest text-slate-500">
                    <span>{group.icon}</span>
                    {group.label}
                  </p>
                  <div className="space-y-1">
                    {group.results.map((result) => {
                      const globalIndex = flatResults.indexOf(result);
                      const isActive = globalIndex === activeIndex;

                      return (
                        <button
                          key={result.id}
                          type="button"
                          data-index={globalIndex}
                          onClick={() => handleSelect(result)}
                          onMouseEnter={() => setActiveIndex(globalIndex)}
                          className={`flex w-full items-start gap-3 rounded-xl px-3 py-3 text-left transition ${
                            isActive
                              ? "bg-slate-800 text-white"
                              : "text-slate-200 hover:bg-slate-800/50"
                          }`}
                        >
                          <div className="min-w-0 flex-1">
                            <p
                              className="truncate text-sm font-medium"
                              dangerouslySetInnerHTML={{
                                __html:
                                  result.titleHighlight ||
                                  escapeHtml(result.title),
                              }}
                            />
                            {result.subtitleHighlight && (
                              <p
                                className="mt-0.5 truncate text-xs text-slate-400"
                                dangerouslySetInnerHTML={{
                                  __html: result.subtitleHighlight,
                                }}
                              />
                            )}
                          </div>
                          {result.meta && (
                            <span className="shrink-0 text-xs text-slate-500">
                              {result.meta}
                            </span>
                          )}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Keyboard Hints */}
          {(showResults || showRecent) && (
            <div className="flex items-center justify-center gap-4 border-t border-slate-800 px-5 py-3 text-xs text-slate-500">
              <span>
                <kbd className="mr-1 rounded bg-slate-800 px-1.5 py-0.5">↑</kbd>
                <kbd className="mr-1 rounded bg-slate-800 px-1.5 py-0.5">↓</kbd>
                Navigate
              </span>
              <span>
                <kbd className="mr-1 rounded bg-slate-800 px-1.5 py-0.5">↵</kbd>
                Select
              </span>
              <span>
                <kbd className="mr-1 rounded bg-slate-800 px-1.5 py-0.5">esc</kbd>
                Close
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// Helper to escape HTML for safe rendering
function escapeHtml(text: string): string {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}
