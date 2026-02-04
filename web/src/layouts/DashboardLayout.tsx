import { useState, useEffect, useCallback, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";

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
  onCommandPaletteOpen?: () => void;
};

export default function DashboardLayout({
  children,
  onCommandPaletteOpen,
}: DashboardLayoutProps) {
  const location = useLocation();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [isMobile, setIsMobile] = useState(false);

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

  // Handle Cmd+K shortcut
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const isCmdOrCtrl = event.metaKey || event.ctrlKey;
      if (isCmdOrCtrl && event.key.toLowerCase() === "k") {
        event.preventDefault();
        onCommandPaletteOpen?.();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onCommandPaletteOpen]);

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
            onClick={onCommandPaletteOpen}
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
            <button
              type="button"
              className="relative rounded-xl p-2.5 text-slate-500 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
              aria-label="Notifications"
            >
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
              </svg>
              {/* Notification badge */}
              <span className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-red-500" />
            </button>

            {/* User menu */}
            <UserMenu />
          </div>
        </header>

        {/* Main content */}
        <main className="flex-1 overflow-y-auto p-4 md:p-6">
          {children}
        </main>
      </div>
    </div>
  );
}
