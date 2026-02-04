import { useEffect, useMemo, useState } from "react";
import { WebSocketProvider } from "./contexts/WebSocketContext";
import CommandPalette from "./components/CommandPalette";
import type { Command } from "./hooks/useCommandPalette";

export default function App() {
  const [isPaletteOpen, setIsPaletteOpen] = useState(false);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const isCmdOrCtrl = event.metaKey || event.ctrlKey;
      if (!isCmdOrCtrl) {
        return;
      }

      if (event.key.toLowerCase() === "k") {
        event.preventDefault();
        setIsPaletteOpen((open) => !open);
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  const commands = useMemo<Command[]>(
    () => [
      {
        id: "nav-home",
        label: "Go to Home",
        category: "Navigation",
        keywords: ["landing", "main"],
        action: () =>
          window.scrollTo({ top: 0, behavior: "smooth" }),
      },
      {
        id: "nav-updates",
        label: "Show Basecamp Updates",
        category: "Navigation",
        keywords: ["news", "status"],
        action: () => window.alert("Basecamp updates coming soon."),
      },
      {
        id: "task-create",
        label: "Create Campfire Task",
        category: "Tasks",
        keywords: ["new", "todo", "task"],
        action: () => window.alert("Task creation flow launching soon."),
      },
      {
        id: "task-prioritize",
        label: "Prioritize Expedition Tasks",
        category: "Tasks",
        keywords: ["triage", "plan"],
        action: () => window.alert("Task prioritization queued."),
      },
      {
        id: "agent-scout",
        label: "Summon Scout Agent",
        category: "Agents",
        keywords: ["research", "assistant"],
        action: () => window.alert("Scout agent is getting ready."),
      },
      {
        id: "agent-builder",
        label: "Summon Builder Agent",
        category: "Agents",
        keywords: ["ship", "deliver"],
        action: () => window.alert("Builder agent is on the way."),
      },
      {
        id: "settings-theme",
        label: "Toggle Night Mode",
        category: "Settings",
        keywords: ["dark", "theme"],
        action: () =>
          document.documentElement.classList.toggle("dark"),
      },
      {
        id: "settings-profile",
        label: "Open Profile Settings",
        category: "Settings",
        keywords: ["account", "preferences"],
        action: () => window.alert("Profile settings coming soon."),
      },
    ],
    []
  );

  return (
    <WebSocketProvider>
      <main className="min-h-screen bg-gradient-to-br from-sky-100 via-white to-emerald-100 px-6 py-16 dark:from-slate-900 dark:via-slate-950 dark:to-slate-900">
        <div className="mx-auto max-w-3xl">
          <div className="inline-flex items-center gap-3 rounded-full border border-slate-200 bg-white/80 px-5 py-2 text-sm font-medium text-slate-600 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60 dark:text-slate-300">
            Camp basecamp is live
          </div>
          <h1 className="mt-6 text-5xl font-semibold tracking-tight text-slate-900 dark:text-white sm:text-6xl">
            🦦 Otter Camp
          </h1>
          <p className="mt-4 text-lg text-slate-600 dark:text-slate-300">
            Cozy, collaborative, and ready for the next adventure.
          </p>
          <div className="mt-10 flex flex-wrap gap-3">
            <span className="rounded-full bg-sky-500/10 px-4 py-2 text-sm font-medium text-sky-700 dark:bg-sky-500/20 dark:text-sky-200">
              Vite + React + TypeScript
            </span>
            <span className="rounded-full bg-emerald-500/10 px-4 py-2 text-sm font-medium text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-200">
              Tailwind Ready
            </span>
            <span className="rounded-full bg-slate-900/10 px-4 py-2 text-sm font-medium text-slate-700 dark:bg-white/10 dark:text-slate-200">
              TanStack Query
            </span>
          </div>
          <div className="mt-10 flex items-center gap-3 text-sm text-slate-500 dark:text-slate-400">
            <span className="rounded-full border border-slate-200 px-3 py-1 text-xs uppercase tracking-[0.2em] dark:border-slate-700">
              Tip
            </span>
            Press <span className="font-semibold">Cmd/Ctrl + K</span> to open the
            command palette.
          </div>
        </div>

        <CommandPalette
          commands={commands}
          isOpen={isPaletteOpen}
          onOpenChange={setIsPaletteOpen}
        />
      </main>
    </WebSocketProvider>
  );
}
