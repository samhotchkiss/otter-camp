import { useState, useEffect, useCallback, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import NotificationBell from "../components/NotificationBell";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useKeyboardShortcuts, type Shortcut } from "../hooks/useKeyboardShortcuts";
import ShortcutsHelpModal from "../components/ShortcutsHelpModal";

type NavItem = {
  id: string;
  label: string;
  icon: string;
  href: string;
};

const NAV_ITEMS: NavItem[] = [
  { id: "projects", label: "Projects", icon: "📁", href: "/projects" },
  { id: "tasks", label: "Tasks", icon: "✅", href: "/" },
  { id: "agents", label: "Agents", icon: "🤖", href: "/agents" },
  { id: "feed", label: "Feed", icon: "📡", href: "/feed" },
  { id: "settings", label: "Settings", icon: "⚙️", href: "/settings" },
];

type DashboardLayoutProps = {
  children: ReactNode;
};

export default function DashboardLayout({
  children,
}: DashboardLayoutProps) {
  const location = useLocation();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [isMobile, setIsMobile] = useState(false);
  
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
    openTaskDetail,
    closeTaskDetail,
    selectedTaskId,
    openNewTask,
    closeNewTask,
    isNewTaskOpen,
  } = useKeyboardShortcutsContext();

  // Define global keyboard shortcuts
  const shortcuts: Shortcut[] = [
    // General
    {
      key: "k",
      modifiers: { cmd: true },
      description: "Open command palette",
      category: "General",
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
        } else if (sidebarOpen && isMobile) {
          setSidebarOpen(false);
        }
      },
    },
    // Tasks
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
        // This will be handled by the KanbanBoard which knows the task IDs
        const event = new CustomEvent("keyboard:open-task");
        window.dispatchEvent(event);
      },
    },
    // Priority shortcuts (1-4)
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

  // Determine active nav item from current path
  const getActiveNavId = () => {
    const path = location.pathname;
    if (path === "/" || path === "/tasks") return "tasks";
    const item = NAV_ITEMS.find((item) => item.href === path);
    return item?.id ?? "tasks";
  };

  const activeNavId = getActiveNavId();

  // Detect mobile viewport
  useEffect(() => {
    const checkMobile = () => {
      setIsMobile(window.innerWidth < 768);
    };

    checkMobile();
    window.addEventListener("resize", checkMobile);
    return () => window.removeEventListener("resize", checkMobile);
  }, []);

  // Close sidebar on mobile when clicking outside
  const handleOverlayClick = useCallback(() => {
    if (isMobile && sidebarOpen) {
      setSidebarOpen(false);
    }
  }, [isMobile, sidebarOpen]);

  // Close sidebar on navigation (mobile)
  useEffect(() => {
    if (isMobile) {
      setSidebarOpen(false);
    }
  }, [location.pathname, isMobile]);

  return (
    <div className="flex h-screen bg-gradient-to-br from-sky-50 via-white to-emerald-50 dark:from-slate-900 dark:via-slate-950 dark:to-slate-900">
      {/* Mobile overlay */}
      {isMobile && sidebarOpen && (
        <div
          className="fixed inset-0 z-20 bg-slate-950/50 backdrop-blur-sm"
          onClick={handleOverlayClick}
          aria-hidden="true"
        />
      )}

      {/* Sidebar */}
      <aside
        className={`
          fixed inset-y-0 left-0 z-30 flex w-64 flex-col border-r border-slate-200 bg-white/80 backdrop-blur-sm
          transition-transform duration-300 ease-in-out
          dark:border-slate-800 dark:bg-slate-900/80
          md:relative md:translate-x-0
          ${isMobile && !sidebarOpen ? "-translate-x-full" : "translate-x-0"}
        `}
      >
        {/* Sidebar Header */}
        <div className="flex h-16 items-center gap-3 border-b border-slate-200 px-5 dark:border-slate-800">
          <span className="text-2xl">🦦</span>
          <span className="text-lg font-semibold text-slate-900 dark:text-white">
            Otter Camp
          </span>
          {isMobile && (
            <button
              type="button"
              onClick={() => setSidebarOpen(false)}
              className="ml-auto rounded-lg p-2 text-slate-500 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
              aria-label="Close sidebar"
            >
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>

        {/* Navigation */}
        <nav className="flex-1 space-y-1 px-3 py-4">
          {NAV_ITEMS.map((item) => {
            const isActive = item.id === activeNavId;
            return (
              <Link
                key={item.id}
                to={item.href}
                className={`
                  flex items-center gap-3 rounded-xl px-4 py-3 text-sm font-medium transition
                  ${
                    isActive
                      ? "bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300"
                      : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800"
                  }
                `}
              >
                <span className="text-lg">{item.icon}</span>
                {item.label}
              </Link>
            );
          })}
        </nav>

        {/* Sidebar Footer */}
        <div className="border-t border-slate-200 p-4 dark:border-slate-800">
          <div className="rounded-xl bg-emerald-50 p-4 dark:bg-emerald-900/20">
            <p className="text-xs font-semibold uppercase tracking-wide text-emerald-700 dark:text-emerald-400">
              Pro tip
            </p>
            <p className="mt-1 text-sm text-emerald-600 dark:text-emerald-300">
              Press <kbd className="rounded bg-emerald-100 px-1.5 py-0.5 text-xs font-semibold dark:bg-emerald-800">⌘K</kbd> for quick actions
            </p>
          </div>
        </div>
      </aside>

      {/* Main content area */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Header */}
        <header className="flex h-16 items-center justify-between border-b border-slate-200 bg-white/60 px-4 backdrop-blur-sm dark:border-slate-800 dark:bg-slate-900/60 md:px-6">
          {/* Mobile menu button */}
          <button
            type="button"
            onClick={() => setSidebarOpen(true)}
            className="rounded-lg p-2 text-slate-500 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800 md:hidden"
            aria-label="Open sidebar"
          >
            <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>

          {/* Command palette trigger */}
          <button
            type="button"
            onClick={openCommandPalette}
            data-tour="command-palette"
            className="hidden items-center gap-2 rounded-xl border border-slate-200 bg-white px-4 py-2 text-sm text-slate-500 shadow-sm transition hover:border-slate-300 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-400 dark:hover:border-slate-600 dark:hover:bg-slate-700 md:flex"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <span>Search or command...</span>
            <kbd className="rounded bg-slate-100 px-2 py-0.5 text-xs font-semibold text-slate-400 dark:bg-slate-700">⌘K</kbd>
          </button>

          {/* Right side actions */}
          <div className="flex items-center gap-2">
            {/* Notifications */}
            <NotificationBell />

            {/* User avatar */}
            <button
              type="button"
              className="flex items-center gap-2 rounded-xl p-1.5 transition hover:bg-slate-100 dark:hover:bg-slate-800"
              aria-label="User menu"
            >
              <div className="flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-emerald-400 text-sm font-semibold text-white">
                🦦
              </div>
              <span className="hidden text-sm font-medium text-slate-700 dark:text-slate-200 sm:block">
                Otter
              </span>
              <svg className="hidden h-4 w-4 text-slate-400 sm:block" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
              </svg>
            </button>
          </div>
        </header>

        {/* Main content */}
        <main
          id="main-content"
          role="main"
          aria-label="Main content"
          className="flex-1 overflow-y-auto p-4 md:p-6"
        >
          {children}
        </main>
      </div>

      {/* Keyboard Shortcuts Help Modal */}
      <ShortcutsHelpModal isOpen={isShortcutsHelpOpen} onClose={closeShortcutsHelp} />
    </div>
  );
}
