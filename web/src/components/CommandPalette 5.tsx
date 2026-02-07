import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { Command, CommandCategory } from "../hooks/useCommandPalette";
import { useCommandPalette } from "../hooks/useCommandPalette";
import { useFocusTrap } from "../hooks/useFocusTrap";

const CATEGORY_ORDER: CommandCategory[] = [
  "Navigation",
  "Tasks",
  "Agents",
  "Settings",
];

type CommandPaletteProps = {
  commands: Command[];
  isOpen: boolean;
  onOpenChange: (next: boolean) => void;
};

export default function CommandPalette({
  commands,
  isOpen,
  onOpenChange,
}: CommandPaletteProps) {
  const {
    registerCommands,
    executeCommand,
    filteredCommands,
    recentCommands,
    query,
    setQuery,
  } = useCommandPalette();
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus trap for modal accessibility
  const { containerRef } = useFocusTrap({
    isActive: isOpen,
    onEscape: () => onOpenChange(false),
    returnFocusOnClose: true,
    initialFocusRef: inputRef,
  });

  useEffect(() => {
    registerCommands(commands);
  }, [commands, registerCommands]);

  useEffect(() => {
    if (!isOpen) {
      setQuery("");
      setActiveIndex(0);
      return;
    }

    const handle = window.requestAnimationFrame(() => {
      inputRef.current?.focus();
    });

    return () => window.cancelAnimationFrame(handle);
  }, [isOpen, setQuery]);

  const recentIds = useMemo(
    () => new Set(recentCommands.map((command) => command.id)),
    [recentCommands]
  );

  const visibleCommands = useMemo(() => {
    if (query.trim()) {
      return filteredCommands;
    }

    return [
      ...recentCommands,
      ...filteredCommands.filter((command) => !recentIds.has(command.id)),
    ];
  }, [filteredCommands, query, recentCommands, recentIds]);

  const commandIndexMap = useMemo(() => {
    return new Map(
      visibleCommands.map((command, index) => [command.id, index])
    );
  }, [visibleCommands]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }

    setActiveIndex(0);
  }, [isOpen, query]);

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!visibleCommands.length) {
      return;
    }

    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveIndex((index) => Math.min(index + 1, visibleCommands.length - 1));
      return;
    }

    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveIndex((index) => Math.max(index - 1, 0));
      return;
    }

    if (event.key === "Enter") {
      event.preventDefault();
      const command = visibleCommands[activeIndex];
      if (command) {
        executeCommand(command.id);
        onOpenChange(false);
      }
      return;
    }

    if (event.key === "Escape") {
      event.preventDefault();
      onOpenChange(false);
    }
  };

  const handleSelect = (command: Command) => {
    executeCommand(command.id);
    onOpenChange(false);
  };

  const grouped = useMemo(() => {
    const groups = new Map<CommandCategory, Command[]>();
    CATEGORY_ORDER.forEach((category) => groups.set(category, []));
    const source = query.trim()
      ? filteredCommands
      : filteredCommands.filter((command) => !recentIds.has(command.id));

    source.forEach((command) => {
      groups.get(command.category)?.push(command);
    });
    return groups;
  }, [filteredCommands, query, recentIds]);

  if (!isOpen) {
    return null;
  }

  return (
    <div
      className="command-palette-overlay fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 px-4 py-6 text-slate-100 backdrop-blur-sm"
      onClick={() => onOpenChange(false)}
      aria-hidden="true"
    >
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        aria-describedby="command-palette-description"
        className="command-palette-panel w-full max-w-2xl overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/95 shadow-2xl shadow-slate-950/40"
        onClick={(event) => event.stopPropagation()}
      >
        <span id="command-palette-description" className="sr-only">
          Search for commands, pages, and tasks. Use arrow keys to navigate, Enter to select.
        </span>
        <div className="flex items-center gap-3 border-b border-slate-800 px-5 py-4">
          <div className="inline-flex h-10 w-10 items-center justify-center rounded-full bg-slate-800 text-lg">
            ⌘
          </div>
          <div className="flex-1">
            <p className="text-sm uppercase tracking-[0.2em] text-slate-400">
              Command Palette
            </p>
            <input
              ref={inputRef}
              type="text"
              role="combobox"
              aria-expanded="true"
              aria-controls="command-palette-listbox"
              aria-autocomplete="list"
              aria-activedescendant={visibleCommands[activeIndex] ? `command-${visibleCommands[activeIndex].id}` : undefined}
              aria-label="Search commands"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Search commands, pages, tasks…"
              className="mt-1 w-full bg-transparent text-lg font-semibold text-slate-100 outline-none placeholder:text-slate-500"
            />
          </div>
          <div className="hidden rounded-full border border-slate-700 px-3 py-1 text-xs text-slate-400 sm:block">
            Ctrl/Cmd + K
          </div>
        </div>

        <div
          id="command-palette-listbox"
          role="listbox"
          aria-label="Command suggestions"
          className="max-h-[60vh] overflow-y-auto px-5 py-4"
        >
          {!query.trim() && recentCommands.length > 0 ? (
            <section className="mb-6" aria-labelledby="recent-commands-heading">
              <p id="recent-commands-heading" className="text-xs font-semibold uppercase tracking-[0.25em] text-slate-500">
                Recent
              </p>
              <div className="mt-3 space-y-2" role="group">
                {recentCommands.map((command) => (
                  <button
                    key={command.id}
                    id={`command-${command.id}`}
                    type="button"
                    role="option"
                    aria-selected={activeIndex === commandIndexMap.get(command.id)}
                    onClick={() => handleSelect(command)}
                    className={`flex w-full items-center justify-between rounded-xl px-3 py-3 text-left transition hover:bg-slate-800/80 ${
                      activeIndex === commandIndexMap.get(command.id)
                        ? "bg-slate-800 text-white"
                        : "text-slate-200"
                    }`}
                  >
                    <div>
                      <p className="text-sm font-semibold">{command.label}</p>
                      <p className="text-xs text-slate-500">{command.category}</p>
                    </div>
                    <span className="text-xs text-slate-500" aria-hidden="true">Recent</span>
                  </button>
                ))}
              </div>
            </section>
          ) : null}

          {query.trim() ? (
            <section aria-labelledby="search-results-heading">
              <p id="search-results-heading" className="text-xs font-semibold uppercase tracking-[0.25em] text-slate-500">
                Results
              </p>
              <div className="mt-3 space-y-2" role="group">
                {filteredCommands.length === 0 ? (
                  <div role="status" className="rounded-xl border border-dashed border-slate-800 px-4 py-6 text-center text-sm text-slate-500">
                    No matches. Try a different keyword.
                  </div>
                ) : (
                  filteredCommands.map((command) => (
                    <button
                      key={command.id}
                      id={`command-${command.id}`}
                      type="button"
                      role="option"
                      aria-selected={activeIndex === commandIndexMap.get(command.id)}
                      onClick={() => handleSelect(command)}
                      className={`flex w-full items-center justify-between rounded-xl px-3 py-3 text-left transition hover:bg-slate-800/80 ${
                        activeIndex === commandIndexMap.get(command.id)
                          ? "bg-slate-800 text-white"
                          : "text-slate-200"
                      }`}
                    >
                      <div>
                        <p className="text-sm font-semibold">{command.label}</p>
                        <p className="text-xs text-slate-500">
                          {command.category}
                        </p>
                      </div>
                      <span className="text-xs text-slate-500" aria-hidden="true">Enter</span>
                    </button>
                  ))
                )}
              </div>
            </section>
          ) : (
            <section className="space-y-6">
              {CATEGORY_ORDER.map((category) => {
                const items = grouped.get(category) ?? [];
                if (items.length === 0) {
                  return null;
                }

                const categoryId = `category-${category.toLowerCase().replace(/\s+/g, "-")}`;

                return (
                  <div key={category} role="group" aria-labelledby={categoryId}>
                    <p id={categoryId} className="text-xs font-semibold uppercase tracking-[0.25em] text-slate-500">
                      {category}
                    </p>
                    <div className="mt-3 space-y-2">
                      {items.map((command) => {
                        const isActive =
                          activeIndex === commandIndexMap.get(command.id);

                        return (
                          <button
                            key={command.id}
                            id={`command-${command.id}`}
                            type="button"
                            role="option"
                            aria-selected={isActive}
                            onClick={() => handleSelect(command)}
                            className={`flex w-full items-center justify-between rounded-xl px-3 py-3 text-left transition hover:bg-slate-800/80 ${
                              isActive
                                ? "bg-slate-800 text-white"
                                : "text-slate-200"
                            }`}
                          >
                            <div>
                              <p className="text-sm font-semibold">
                                {command.label}
                              </p>
                              <p className="text-xs text-slate-500">
                                {command.category}
                              </p>
                            </div>
                            <span className="text-xs text-slate-500" aria-hidden="true">Enter</span>
                          </button>
                        );
                      })}
                    </div>
                  </div>
                );
              })}
            </section>
          )}
        </div>
      </div>
    </div>
  );
}
