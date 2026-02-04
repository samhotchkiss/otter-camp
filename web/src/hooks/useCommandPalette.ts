import { useCallback, useEffect, useMemo, useRef, useState } from "react";

export type CommandCategory = "Navigation" | "Tasks" | "Agents" | "Settings";

export type Command = {
  id: string;
  label: string;
  action: () => void;
  category: CommandCategory;
  keywords?: string[];
};

type RecentCommand = {
  id: string;
  label: string;
  category: CommandCategory;
};

const STORAGE_KEY = "ottercamp.commandPalette.recent";
const MAX_RECENT = 6;

const normalize = (value: string) => value.toLowerCase().trim();

const getFuzzyScore = (text: string, query: string) => {
  const source = normalize(text);
  const needle = normalize(query);

  if (!needle) {
    return 1;
  }

  let score = 0;
  let lastIndex = -1;

  for (const char of needle) {
    const index = source.indexOf(char, lastIndex + 1);
    if (index === -1) {
      return -1;
    }

    score += 12 - Math.min(index - lastIndex, 12);
    lastIndex = index;
  }

  score -= Math.max(source.length - needle.length, 0) * 0.15;
  return score;
};

const scoreCommand = (command: Command, query: string) => {
  if (!query) {
    return 1;
  }

  const fields = [
    command.label,
    command.category,
    ...(command.keywords ?? []),
  ];

  return Math.max(...fields.map((field) => getFuzzyScore(field, query)));
};

const readRecent = (): RecentCommand[] => {
  if (typeof window === "undefined") {
    return [];
  }

  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return [];
    }

    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }

    return parsed.filter(
      (item) =>
        item &&
        typeof item.id === "string" &&
        typeof item.label === "string" &&
        typeof item.category === "string"
    ) as RecentCommand[];
  } catch {
    return [];
  }
};

export const useCommandPalette = () => {
  const [commands, setCommands] = useState<Command[]>([]);
  const [query, setQuery] = useState("");
  const [recent, setRecent] = useState<RecentCommand[]>([]);
  const hasLoaded = useRef(false);

  useEffect(() => {
    if (hasLoaded.current) {
      return;
    }

    setRecent(readRecent());
    hasLoaded.current = true;
  }, []);

  const registerCommands = useCallback((next: Command[]) => {
    setCommands((prev) => {
      const map = new Map(prev.map((command) => [command.id, command]));
      next.forEach((command) => {
        map.set(command.id, command);
      });
      return Array.from(map.values());
    });
  }, []);

  const executeCommand = useCallback(
    (id: string) => {
      const command = commands.find((item) => item.id === id);
      if (!command) {
        return;
      }

      command.action();
      setRecent((prev) => {
        const nextRecent = [
          { id: command.id, label: command.label, category: command.category },
          ...prev.filter((item) => item.id !== command.id),
        ].slice(0, MAX_RECENT);

        if (typeof window !== "undefined") {
          window.localStorage.setItem(STORAGE_KEY, JSON.stringify(nextRecent));
        }

        return nextRecent;
      });
    },
    [commands]
  );

  const filteredCommands = useMemo(() => {
    const trimmedQuery = query.trim();
    const scored = commands
      .map((command) => ({
        command,
        score: scoreCommand(command, trimmedQuery),
      }))
      .filter((entry) => entry.score >= 0);

    return scored
      .sort((a, b) => b.score - a.score)
      .map((entry) => entry.command);
  }, [commands, query]);

  const recentCommands = useMemo(() => {
    return recent.map((item) => {
      const live = commands.find((command) => command.id === item.id);
      return (
        live ?? {
          id: item.id,
          label: item.label,
          category: item.category,
          action: () => {},
        }
      );
    });
  }, [commands, recent]);

  return {
    commands,
    registerCommands,
    executeCommand,
    filteredCommands,
    recentCommands,
    query,
    setQuery,
  } as const;
};
