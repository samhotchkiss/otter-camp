/**
 * DashboardLayout - Main layout component matching Jeff G's design mockups
 * 
 * Key design elements:
 * - Topbar with accent background (warm gold)
 * - Horizontal navigation in topbar
 * - Search trigger as centered pill
 * - Connection status + avatar on right
 * - No sidebar - clean topbar-based navigation
 */

import { useState, useEffect, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useKeyboardShortcuts, type Shortcut } from "../hooks/useKeyboardShortcuts";
import { useWS } from "../contexts/WebSocketContext";
import ShortcutsHelpModal from "../components/ShortcutsHelpModal";
import DemoBanner from "../components/DemoBanner";
import GlobalSearch from "../components/GlobalSearch";
import { isDemoMode } from "../lib/demo";
import { api } from "../lib/api";

type NavItem = {
  id: string;
  label: string;
  href: string;
};

const NAV_ITEMS: NavItem[] = [
  { id: "dashboard", label: "Dashboard", href: "/" },
  { id: "inbox", label: "Inbox", href: "/inbox" },
  { id: "projects", label: "Projects", href: "/projects" },
  { id: "agents", label: "Agents", href: "/agents" },
  { id: "workflows", label: "Workflows", href: "/workflows" },
  { id: "knowledge", label: "Knowledge", href: "/knowledge" },
  { id: "feed", label: "Feed", href: "/feed" },
];

type DashboardLayoutProps = {
  children: ReactNode;
};

export default function DashboardLayout({ children }: DashboardLayoutProps) {
  const location = useLocation();
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [inboxCount, setInboxCount] = useState<number | null>(null);
  const { connected: wsConnected } = useWS();
  // In demo mode, always show as connected for better UX
  const connected = isDemoMode() || wsConnected;
  
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

  // Keyboard shortcuts
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
      action: () => {
        window.location.href = "/inbox";
      },
    },
  ];

  useKeyboardShortcuts(shortcuts);

  // Get active nav item
  const getActiveNavId = () => {
    const path = location.pathname;
    if (path === "/") return "dashboard";
    const item = NAV_ITEMS.find((item) => path.startsWith(item.href) && item.href !== "/");
    return item?.id ?? "dashboard";
  };

  const activeNavId = getActiveNavId();

  // Close mobile menu on navigation
  useEffect(() => {
    setMobileMenuOpen(false);
  }, [location.pathname]);

  // Load inbox count for nav badge
  useEffect(() => {
    let cancelled = false;

    async function loadInboxCount() {
      try {
        if (isDemoMode()) {
          setInboxCount(null);
          return;
        }
        const response = await api.inbox();
        if (cancelled) return;
        setInboxCount(response.items?.length ?? 0);
      } catch {
        if (!cancelled) setInboxCount(null);
      }
    }

    loadInboxCount();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="app">
      {/* ========== DEMO BANNER ========== */}
      <DemoBanner />
      
      {/* ========== TOPBAR ========== */}
      <header className="topbar">
        {/* Logo */}
        <Link to="/" className="logo">
          <span className="logo-icon">🦦</span>
          <span className="logo-text">otter.camp</span>
        </Link>

        {/* Search Trigger */}
        <button
          type="button"
          onClick={openCommandPalette}
          className="search-trigger"
        >
          <span className="search-icon">🔍</span>
          <span className="search-text">Search or command...</span>
          <kbd>⌘K</kbd>
        </button>

        {/* Desktop Navigation */}
        <nav className="nav-links">
          {NAV_ITEMS.map((item) => (
            <Link
              key={item.id}
              to={item.href}
              className={`nav-link ${activeNavId === item.id ? "active" : ""}`}
            >
              {item.label}
              {item.id === "inbox" && inboxCount && inboxCount > 0 && (
                <span className="nav-badge">({inboxCount})</span>
              )}
            </Link>
          ))}
        </nav>

        {/* Right Side */}
        <div className="topbar-right">
          {/* Connection Status */}
          <div className={`connection-status ${connected ? 'connected' : 'disconnected'}`}>
            <span className={`status-dot ${connected ? 'status-live' : 'status-offline'}`}></span>
            <span className="status-text">{connected ? 'Live' : 'Offline'}</span>
          </div>

          {/* User Avatar */}
          <button type="button" className="avatar" aria-label="User menu">
            S
          </button>
        </div>

        {/* Mobile Menu Button */}
        <button
          type="button"
          onClick={() => setMobileMenuOpen(!mobileMenuOpen)}
          className="mobile-menu-btn"
          aria-label="Toggle menu"
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            {mobileMenuOpen ? (
              <path d="M6 18L18 6M6 6l12 12" />
            ) : (
              <path d="M4 6h16M4 12h16M4 18h16" />
            )}
          </svg>
        </button>
      </header>

      {/* Mobile Navigation */}
      {mobileMenuOpen && (
        <nav className="mobile-nav">
          {NAV_ITEMS.map((item) => (
            <Link
              key={item.id}
              to={item.href}
              className={`mobile-nav-link ${activeNavId === item.id ? "active" : ""}`}
            >
              {item.label}
              {item.id === "inbox" && inboxCount && inboxCount > 0 && (
                <span className="nav-badge">({inboxCount})</span>
              )}
            </Link>
          ))}
        </nav>
      )}

      {/* ========== MAIN CONTENT ========== */}
      <main className="main" id="main-content">
        {children}
      </main>

      {/* ========== FOOTER ========== */}
      <footer className="footer">
        <div className="footer-fact">
          <span>🦦</span>
          <span>
            <strong>Otter fact:</strong> Sea otters have the densest fur of any mammal — about 1 million hairs per square inch.
          </span>
        </div>
      </footer>

      {/* Keyboard Shortcuts Help Modal */}
      <ShortcutsHelpModal isOpen={isShortcutsHelpOpen} onClose={closeShortcutsHelp} />

      {/* Global Search / Command Palette */}
      <GlobalSearch
        isOpen={isCommandPaletteOpen}
        onOpenChange={(open) => open ? openCommandPalette() : closeCommandPalette()}
        orgId={localStorage.getItem("otter-camp-org-id") || undefined}
      />
    </div>
  );
}
