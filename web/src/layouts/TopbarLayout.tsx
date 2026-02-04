import { type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import Footer from "../components/Footer";
import ShortcutsHelpModal from "../components/ShortcutsHelpModal";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useKeyboardShortcuts, type Shortcut } from "../hooks/useKeyboardShortcuts";

type NavTab = {
  id: string;
  label: string;
  href: string;
};

const NAV_TABS: NavTab[] = [
  { id: "dashboard", label: "Dashboard", href: "/" },
  { id: "inbox", label: "Inbox", href: "/notifications" },
  { id: "projects", label: "Projects", href: "/projects" },
  { id: "feed", label: "Feed", href: "/feed" },
  { id: "agents", label: "Agents", href: "/agents" },
];

type TopbarLayoutProps = {
  children: ReactNode;
};

export default function TopbarLayout({ children }: TopbarLayoutProps) {
  const location = useLocation();
  const {
    openCommandPalette,
    isShortcutsHelpOpen,
    openShortcutsHelp,
    closeShortcutsHelp,
    isCommandPaletteOpen,
    closeCommandPalette,
    selectedTaskIndex,
    setSelectedTaskIndex,
    taskCount,
    closeTaskDetail,
    selectedTaskId,
    openNewTask,
    closeNewTask,
    isNewTaskOpen,
  } = useKeyboardShortcutsContext();

  const shortcuts: Shortcut[] = [
    {
      key: "k",
      modifiers: { cmd: true },
      description: "Open command palette",
      category: "General",
      action: openCommandPalette,
    },
    {
      key: "/",
      description: "Open command palette",
      category: "General",
      skipInInput: true,
      action: openCommandPalette,
    },
    {
      key: "/",
      modifiers: { cmd: true },
      description: "Show keyboard shortcuts",
      category: "General",
      action: openShortcutsHelp,
    },
    {
      key: "Escape",
      description: "Close modals/panels",
      category: "General",
      action: () => {
        if (isCommandPaletteOpen) {
          closeCommandPalette();
        } else if (isShortcutsHelpOpen) {
          closeShortcutsHelp();
        } else if (selectedTaskId) {
          closeTaskDetail();
        } else if (isNewTaskOpen) {
          closeNewTask();
        }
      },
    },
    {
      key: "n",
      modifiers: { cmd: true },
      description: "Create new task",
      category: "Tasks",
      action: openNewTask,
    },
    {
      key: "j",
      description: "Move down in task list",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        if (taskCount > 0) {
          setSelectedTaskIndex(Math.min(selectedTaskIndex + 1, taskCount - 1));
        }
      },
    },
    {
      key: "k",
      description: "Move up in task list",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        if (taskCount > 0 && selectedTaskIndex > 0) {
          setSelectedTaskIndex(selectedTaskIndex - 1);
        }
      },
    },
    {
      key: "Enter",
      description: "Open selected task",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        const event = new CustomEvent("keyboard:open-task");
        window.dispatchEvent(event);
      },
    },
    {
      key: "1",
      description: "Set priority: Low",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        const event = new CustomEvent("keyboard:set-priority", { detail: "low" });
        window.dispatchEvent(event);
      },
    },
    {
      key: "2",
      description: "Set priority: Medium",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        const event = new CustomEvent("keyboard:set-priority", { detail: "medium" });
        window.dispatchEvent(event);
      },
    },
    {
      key: "3",
      description: "Set priority: High",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        const event = new CustomEvent("keyboard:set-priority", { detail: "high" });
        window.dispatchEvent(event);
      },
    },
    {
      key: "4",
      description: "Set priority: Critical",
      category: "Tasks",
      skipInInput: true,
      action: () => {
        const event = new CustomEvent("keyboard:set-priority", { detail: "critical" });
        window.dispatchEvent(event);
      },
    },
  ];

  useKeyboardShortcuts(shortcuts);

  const getActiveTabId = () => {
    const path = location.pathname;
    if (path === "/" || path.startsWith("/tasks")) return "dashboard";
    if (path.startsWith("/notifications")) return "inbox";
    const item = NAV_TABS.find((tab) => tab.href === path);
    return item?.id ?? "dashboard";
  };

  const activeTabId = getActiveTabId();

  return (
    <div className="flex min-h-screen flex-col bg-otter-bg dark:bg-otter-dark-bg">
      <header className="topbar" role="banner">
        <Link to="/" className="flex items-center gap-2 font-semibold">
          <span className="text-2xl" aria-hidden="true">
            🦦
          </span>
          <span className="text-lg font-semibold">otter.camp</span>
        </Link>

        <nav aria-label="Primary" className="flex items-center gap-2">
          {NAV_TABS.map((tab) => {
            const isActive = tab.id === activeTabId;
            return (
              <Link
                key={tab.id}
                to={tab.href}
                className={`topbar-tab${isActive ? " topbar-tab--active" : ""}`}
                aria-current={isActive ? "page" : undefined}
              >
                {tab.label}
              </Link>
            );
          })}
        </nav>

        <div className="flex flex-1 items-center justify-end gap-3">
          <button
            type="button"
            onClick={openCommandPalette}
            className="topbar-search"
          >
            <span className="text-base" aria-hidden="true">
              🔍
            </span>
            <span className="flex-1 text-left text-sm text-otter-dark-bg/70">
              Search or type a command...
            </span>
            <kbd className="rounded bg-white/20 px-2 py-0.5 text-xs text-otter-dark-bg">/</kbd>
          </button>

          <button
            type="button"
            className="topbar-icon"
            aria-label="Toggle theme"
            title="Toggle theme"
          >
            <span className="text-lg dark:hidden" aria-hidden="true">
              ☀️
            </span>
            <span className="hidden text-lg dark:block" aria-hidden="true">
              🌙
            </span>
          </button>

          <button type="button" className="topbar-avatar" aria-label="User menu">
            <span className="text-sm" aria-hidden="true">
              🦦
            </span>
          </button>
        </div>
      </header>

      <main
        id="main-content"
        role="main"
        aria-label="Main content"
        className="flex-1 overflow-y-auto p-4 md:p-6"
      >
        {children}
      </main>

      <Footer />

      <ShortcutsHelpModal isOpen={isShortcutsHelpOpen} onClose={closeShortcutsHelp} />
    </div>
  );
}
