import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useKeyboardShortcuts, type Shortcut } from "../hooks/useKeyboardShortcuts";
import ShortcutsHelpModal from "../components/ShortcutsHelpModal";
import DemoBanner from "../components/DemoBanner";
import GlobalSearch from "../components/GlobalSearch";
import GlobalChatDock from "../components/chat/GlobalChatDock";

type DashboardLayoutProps = {
  children: ReactNode;
};

type NavItem = {
  id: string;
  label: string;
  href: string;
};

type SidebarInboxItem = {
  id: string;
  title: string;
  project: string;
  priority: "critical" | "high" | "medium" | "low";
};

type SidebarProjectItem = {
  id: string;
  name: string;
  status: "active" | "review" | "idle";
  issueCount: number;
  lastAccessed: string;
};

const AVATAR_MENU_ITEMS: NavItem[] = [
  { id: "agents", label: "Agents", href: "/agents" },
  { id: "connections", label: "Connections", href: "/connections" },
  { id: "feed", label: "Feed", href: "/feed" },
  { id: "settings", label: "Settings", href: "/settings" },
];

const SIDEBAR_INBOX_ITEMS: SidebarInboxItem[] = [
  {
    id: "ISS-209",
    title: "Fix API rate limiting",
    project: "API Gateway",
    priority: "critical",
  },
  {
    id: "ISS-234",
    title: "Auth flow refactor",
    project: "Customer Portal",
    priority: "high",
  },
  {
    id: "ISS-445",
    title: "Memory sync error",
    project: "Internal Tools",
    priority: "medium",
  },
];

const SIDEBAR_PROJECT_ITEMS: SidebarProjectItem[] = [
  {
    id: "project-api-gateway",
    name: "API Gateway",
    status: "active",
    issueCount: 5,
    lastAccessed: "2 days ago",
  },
  {
    id: "project-customer-portal",
    name: "Customer Portal",
    status: "review",
    issueCount: 3,
    lastAccessed: "1 week ago",
  },
  {
    id: "project-internal-tools",
    name: "Internal Tools",
    status: "active",
    issueCount: 8,
    lastAccessed: "3 days ago",
  },
];

function logOut() {
  document.cookie = "otter_auth=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;";
  const keysToRemove = ["otter-camp-org-id", "otter-camp-token", "otter_camp_token", "otter-camp-theme"];
  keysToRemove.forEach((key) => localStorage.removeItem(key));
  window.location.href = "/";
}

function priorityClass(priority: SidebarInboxItem["priority"]): string {
  if (priority === "critical") {
    return "bg-rose-500";
  }
  if (priority === "high") {
    return "bg-orange-500";
  }
  if (priority === "medium") {
    return "bg-amber-500";
  }
  return "bg-stone-600";
}

function projectStatusClass(status: SidebarProjectItem["status"]): string {
  if (status === "active") {
    return "bg-lime-500";
  }
  if (status === "review") {
    return "bg-amber-500";
  }
  return "bg-stone-600";
}

function routeLabel(pathname: string): string {
  const firstSegment = pathname.split("/").filter(Boolean)[0] ?? "inbox";
  return decodeURIComponent(firstSegment);
}

export default function DashboardLayout({ children }: DashboardLayoutProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [chatOpen, setChatOpen] = useState(true);
  const [avatarMenuOpen, setAvatarMenuOpen] = useState(false);
  const avatarMenuRef = useRef<HTMLDivElement>(null);

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
      key: "?",
      description: "Show keyboard shortcuts",
      category: "General",
      skipInInput: true,
      action: openShortcutsHelp,
    },
    {
      key: "Escape",
      description: "Close modals/panels",
      category: "General",
      action: () => {
        if (isCommandPaletteOpen) closeCommandPalette();
        else if (isShortcutsHelpOpen) closeShortcutsHelp();
        else if (selectedTaskId) closeTaskDetail();
        else if (isNewTaskOpen) closeNewTask();
        else if (avatarMenuOpen) setAvatarMenuOpen(false);
        else if (chatOpen) setChatOpen(false);
        else if (mobileMenuOpen) setMobileMenuOpen(false);
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
      description: "Move down",
      category: "Navigation",
      skipInInput: true,
      action: () => {
        if (taskCount > 0) {
          setSelectedTaskIndex(Math.min(selectedTaskIndex + 1, taskCount - 1));
        }
      },
    },
    {
      key: "k",
      description: "Move up",
      category: "Navigation",
      skipInInput: true,
      action: () => {
        if (taskCount > 0 && selectedTaskIndex > 0) {
          setSelectedTaskIndex(selectedTaskIndex - 1);
        }
      },
    },
    {
      key: "g",
      description: "Go to inbox",
      category: "Navigation",
      skipInInput: true,
      action: () => navigate("/inbox"),
    },
  ];

  useKeyboardShortcuts(shortcuts);

  useEffect(() => {
    setMobileMenuOpen(false);
    setAvatarMenuOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!avatarMenuOpen) return;
    const handleClickOutside = (event: MouseEvent) => {
      if (avatarMenuRef.current && !avatarMenuRef.current.contains(event.target as Node)) {
        setAvatarMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
    };
  }, [avatarMenuOpen]);

  const currentRouteLabel = useMemo(() => routeLabel(location.pathname), [location.pathname]);

  return (
    <div className="shell-layout flex h-screen overflow-hidden bg-stone-950 text-stone-200 font-sans" data-testid="shell-layout">
      <DemoBanner />

      {mobileMenuOpen && (
        <button
          type="button"
          className="shell-sidebar-overlay fixed inset-0 z-40 bg-black/50 lg:hidden"
          aria-label="Close navigation"
          onClick={() => setMobileMenuOpen(false)}
        />
      )}

      <aside
        className={`shell-sidebar ${mobileMenuOpen ? "open translate-x-0" : "-translate-x-full lg:translate-x-0"} fixed lg:relative z-50 h-full w-[240px] shrink-0 overflow-hidden border-r border-stone-800 bg-stone-900 transition-transform duration-200`}
        data-testid="shell-sidebar"
      >
        <div className="flex h-16 items-center gap-3 border-b border-stone-800 p-4">
          <div className="relative h-8 w-8 shrink-0 overflow-hidden rounded-lg">
            <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg" className="h-full w-full">
              <defs>
                <linearGradient id="otterGradient" x1="0%" y1="0%" x2="100%" y2="100%">
                  <stop offset="0%" stopColor="#d97706" />
                  <stop offset="100%" stopColor="#84cc16" />
                </linearGradient>
              </defs>
              <circle cx="16" cy="14" r="6" fill="url(#otterGradient)" opacity="0.3" />
              <ellipse cx="16" cy="20" rx="8" ry="6" fill="url(#otterGradient)" opacity="0.3" />
              <circle cx="13" cy="12" r="1.5" fill="white" />
              <circle cx="19" cy="12" r="1.5" fill="white" />
              <path d="M 12 18 Q 16 20 20 18" stroke="white" strokeWidth="1.5" fill="none" strokeLinecap="round" />
              <circle cx="8" cy="16" r="3" fill="url(#otterGradient)" opacity="0.5" />
              <circle cx="24" cy="16" r="3" fill="url(#otterGradient)" opacity="0.5" />
            </svg>
          </div>
          <div className="min-w-0 overflow-hidden">
            <h1 className="truncate text-sm font-bold tracking-tight text-stone-100">Otter Camp</h1>
            <p className="truncate text-[10px] font-mono uppercase tracking-wider text-stone-500">Agent Ops</p>
          </div>
          <button
            type="button"
            className="ml-auto rounded-md p-2 text-stone-400 hover:bg-stone-800 lg:hidden"
            aria-label="Close menu"
            onClick={() => setMobileMenuOpen(false)}
          >
            <span aria-hidden="true">✕</span>
          </button>
        </div>

        <nav className="flex-1 space-y-1 overflow-y-auto p-3">
          <NavLink
            to="/inbox"
            className="flex items-center justify-between px-3 py-2 text-xs font-semibold uppercase tracking-wider text-stone-400 transition-colors hover:text-stone-200"
          >
            <span className="flex items-center gap-2">
              <span aria-hidden="true">📥</span>
              <span>Inbox</span>
            </span>
            <span aria-hidden="true" className="text-xs opacity-60">›</span>
          </NavLink>

          <div className="mb-6 space-y-0.5">
            {SIDEBAR_INBOX_ITEMS.map((item) => (
              <Link
                key={item.id}
                to={`/issue/${encodeURIComponent(item.id)}`}
                className="group flex items-start gap-2 rounded-md px-3 py-2 text-left text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200"
              >
                <span className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${priorityClass(item.priority)}`} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-medium text-stone-300 group-hover:text-stone-100">{item.title}</span>
                  <span className="mt-0.5 flex items-center gap-1.5">
                    <span className="text-[10px] font-mono text-stone-600">{item.id}</span>
                    <span className="text-[10px] text-stone-600">•</span>
                    <span className="truncate text-[10px] text-stone-500">{item.project}</span>
                  </span>
                </span>
              </Link>
            ))}
          </div>

          <NavLink
            to="/projects"
            className="flex items-center justify-between px-3 py-2 text-xs font-semibold uppercase tracking-wider text-stone-400 transition-colors hover:text-stone-200"
          >
            <span className="flex items-center gap-2">
              <span aria-hidden="true">📁</span>
              <span>Projects</span>
            </span>
            <span aria-hidden="true" className="text-xs opacity-60">›</span>
          </NavLink>

          <div className="space-y-0.5">
            {SIDEBAR_PROJECT_ITEMS.map((project) => (
              <NavLink
                key={project.id}
                to={`/projects/${encodeURIComponent(project.id)}`}
                className="group flex items-center gap-2 rounded-md px-3 py-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200"
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${projectStatusClass(project.status)}`} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-medium text-stone-300 group-hover:text-stone-100">{project.name}</span>
                  <span className="mt-0.5 flex items-center gap-1.5">
                    <span className="text-[10px] text-stone-500">{project.issueCount} issues</span>
                    <span className="text-[10px] text-stone-600">•</span>
                    <span className="text-[10px] text-stone-600">{project.lastAccessed}</span>
                  </span>
                </span>
              </NavLink>
            ))}
          </div>
        </nav>

        <div className="shrink-0 border-t border-stone-800">
          <div className="flex items-center justify-around border-b border-stone-800 px-3 py-3">
            <NavLink
              to="/projects"
              className={({ isActive }) =>
                `rounded-md p-2 transition-colors ${
                  isActive ? "bg-amber-500/10 text-amber-400" : "text-stone-500 hover:bg-stone-800 hover:text-stone-300"
                }`
              }
              title="Projects quick nav"
              aria-label="Projects quick nav"
            >
              <span aria-hidden="true">📁</span>
            </NavLink>
            <NavLink
              to="/agents"
              className={({ isActive }) =>
                `rounded-md p-2 transition-colors ${
                  isActive ? "bg-amber-500/10 text-amber-400" : "text-stone-500 hover:bg-stone-800 hover:text-stone-300"
                }`
              }
              title="Agents quick nav"
              aria-label="Agents quick nav"
            >
              <span aria-hidden="true">🤖</span>
            </NavLink>
            <NavLink
              to="/knowledge"
              className={({ isActive }) =>
                `rounded-md p-2 transition-colors ${
                  isActive ? "bg-amber-500/10 text-amber-400" : "text-stone-500 hover:bg-stone-800 hover:text-stone-300"
                }`
              }
              title="Memory quick nav"
              aria-label="Memory quick nav"
            >
              <span aria-hidden="true">🧠</span>
            </NavLink>
            <NavLink
              to="/connections"
              className={({ isActive }) =>
                `rounded-md p-2 transition-colors ${
                  isActive ? "bg-amber-500/10 text-amber-400" : "text-stone-500 hover:bg-stone-800 hover:text-stone-300"
                }`
              }
              title="Operations quick nav"
              aria-label="Operations quick nav"
            >
              <span aria-hidden="true">⚙</span>
            </NavLink>
          </div>

          <div className="p-4" ref={avatarMenuRef}>
            <button
              type="button"
              className="flex w-full items-center gap-3 rounded-md border border-stone-800 bg-stone-950 px-3 py-2 text-left"
              aria-label="Sidebar user menu"
              aria-expanded={avatarMenuOpen}
              onClick={() => setAvatarMenuOpen((open) => !open)}
            >
              <span className="flex h-8 w-8 items-center justify-center rounded-full bg-stone-800 text-xs font-mono">JS</span>
              <span className="min-w-0 overflow-hidden">
                <span className="block truncate text-xs font-medium text-stone-300">Jane Smith</span>
                <span className="block truncate text-[10px] text-stone-500">Admin</span>
              </span>
            </button>
            {avatarMenuOpen && (
              <div className="avatar-dropdown mt-2 rounded-md border border-stone-800 bg-stone-900 p-1">
                {AVATAR_MENU_ITEMS.map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    className="avatar-dropdown-item w-full rounded px-3 py-2 text-left text-sm text-stone-300 hover:bg-stone-800"
                    onClick={() => {
                      navigate(item.href);
                      setAvatarMenuOpen(false);
                    }}
                  >
                    {item.label}
                  </button>
                ))}
                <div className="avatar-dropdown-divider my-1 border-t border-stone-800" />
                <button
                  type="button"
                  className="avatar-dropdown-item avatar-dropdown-logout w-full rounded px-3 py-2 text-left text-sm text-rose-300 hover:bg-stone-800"
                  onClick={logOut}
                >
                  Log Out
                </button>
              </div>
            )}
          </div>
        </div>
      </aside>

      <div className="flex flex-1 flex-col overflow-hidden" data-testid="shell-main">
        <header className="shell-header flex h-16 items-center justify-between border-b border-stone-800 bg-stone-950/80 px-4 backdrop-blur-md md:px-6" data-testid="shell-header">
          <div className="flex items-center gap-4">
            <button
              type="button"
              aria-label="Toggle menu"
              onClick={() => setMobileMenuOpen((open) => !open)}
              className="rounded-md p-2 text-stone-400 hover:bg-stone-800 md:hidden"
            >
              <span aria-hidden="true">☰</span>
            </button>
            <div className="flex items-center gap-2 text-sm text-stone-400">
              <span className="text-stone-600">/</span>
              <span className="capitalize text-stone-200" data-testid="shell-route-label">
                {currentRouteLabel}
              </span>
            </div>
          </div>

          <div className="flex items-center gap-3">
            <div className="group relative hidden md:block">
              <span aria-hidden="true" className="absolute left-3 top-1/2 -translate-y-1/2 text-stone-500">🔎</span>
              <input
                aria-label="Search"
                type="text"
                placeholder="Search..."
                readOnly
                onFocus={openCommandPalette}
                className="w-64 rounded-full border border-stone-800 bg-stone-900 py-1.5 pl-9 pr-12 text-sm text-stone-300 outline-none transition-all placeholder:text-stone-600 focus:border-amber-500/50 focus:ring-1 focus:ring-amber-500/50"
              />
              <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 rounded border border-stone-700 px-1 text-[10px] font-mono text-stone-600">
                ⌘K
              </span>
            </div>

            <button
              type="button"
              aria-label="Toggle chat panel"
              title={chatOpen ? "Hide Chat" : "Show Chat"}
              onClick={() => setChatOpen((open) => !open)}
              className="rounded-full p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200"
            >
              <span aria-hidden="true">{chatOpen ? "◧" : "◨"}</span>
            </button>

            <button
              type="button"
              className="rounded-full border border-stone-700 bg-stone-900 px-3 py-1 text-xs text-stone-300"
              aria-label="User menu"
              aria-expanded={avatarMenuOpen}
              onClick={() => setAvatarMenuOpen((open) => !open)}
            >
              JS
            </button>
          </div>
        </header>

        <div className="shell-workspace flex flex-1 overflow-hidden" data-testid="shell-workspace">
          <main className="shell-content flex-1 overflow-y-auto bg-stone-950 p-4 md:p-6" id="main-content">
            <div className="mx-auto max-w-6xl space-y-4 md:space-y-6">{children}</div>
          </main>
          <aside
            className={`shell-chat-slot ${chatOpen ? "" : "hidden"} w-96 shrink-0 border-l border-stone-800 bg-stone-900 max-lg:hidden`.trim()}
            data-testid="shell-chat-slot"
            aria-hidden={!chatOpen}
          >
            {chatOpen ? <GlobalChatDock /> : null}
          </aside>
        </div>
      </div>

      <ShortcutsHelpModal isOpen={isShortcutsHelpOpen} onClose={closeShortcutsHelp} />
      <GlobalSearch
        isOpen={isCommandPaletteOpen}
        onOpenChange={(open) => (open ? openCommandPalette() : closeCommandPalette())}
        orgId={localStorage.getItem("otter-camp-org-id") || undefined}
      />
    </div>
  );
}
