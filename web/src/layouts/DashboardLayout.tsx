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

import { useState, useEffect, useCallback, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useKeyboardShortcuts, type Shortcut } from "../hooks/useKeyboardShortcuts";
import ShortcutsHelpModal from "../components/ShortcutsHelpModal";

type NavItem = {
  id: string;
  label: string;
  href: string;
  badge?: number;
};

const NAV_ITEMS: NavItem[] = [
  { id: "dashboard", label: "Dashboard", href: "/" },
  { id: "inbox", label: "Inbox", href: "/inbox", badge: 3 },
  { id: "projects", label: "Projects", href: "/projects" },
  { id: "agents", label: "Agents", href: "/agents" },
  { id: "feed", label: "Feed", href: "/feed" },
];

type DashboardLayoutProps = {
  children: ReactNode;
};

export default function DashboardLayout({ children }: DashboardLayoutProps) {
  const location = useLocation();
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  
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

  return (
    <div className="app">
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
          <kbd className="search-kbd">⌘K</kbd>
        </button>

        {/* Desktop Navigation */}
        <nav className="topbar-nav">
          {NAV_ITEMS.map((item) => (
            <Link
              key={item.id}
              to={item.href}
              className={`nav-link ${activeNavId === item.id ? "active" : ""}`}
            >
              {item.label}
              {item.badge && (
                <span className="nav-badge">({item.badge})</span>
              )}
            </Link>
          ))}
        </nav>

        {/* Right Side */}
        <div className="topbar-right">
          {/* Connection Status */}
          <div className="connection-status">
            <span className="status-dot"></span>
            <span className="status-text">Live</span>
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
              {item.badge && <span className="nav-badge">({item.badge})</span>}
            </Link>
          ))}
        </nav>
      )}

      {/* ========== MAIN CONTENT ========== */}
      <main className="main-content" id="main-content">
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

      <style>{`
        /* ========== LAYOUT STYLES (from Jeff G's mockups) ========== */
        
        .app {
          min-height: 100vh;
          display: flex;
          flex-direction: column;
          background: var(--otter-bg);
          color: var(--otter-text);
        }
        
        /* ========== TOPBAR ========== */
        .topbar {
          background: var(--otter-accent);
          color: var(--otter-bg);
          padding: 12px 24px;
          display: flex;
          align-items: center;
          gap: 24px;
          position: sticky;
          top: 0;
          z-index: 100;
        }
        
        .logo {
          display: flex;
          align-items: center;
          gap: 10px;
          font-weight: 700;
          font-size: 18px;
          text-decoration: none;
          color: inherit;
        }
        
        .logo-icon {
          font-size: 24px;
        }
        
        .search-trigger {
          flex: 1;
          max-width: 400px;
          background: rgba(255, 255, 255, 0.15);
          border: 1px solid rgba(255, 255, 255, 0.2);
          border-radius: 8px;
          padding: 10px 16px;
          display: flex;
          align-items: center;
          gap: 12px;
          cursor: pointer;
          color: inherit;
          font-family: inherit;
          font-size: 14px;
          transition: background 0.15s;
        }
        
        .search-trigger:hover {
          background: rgba(255, 255, 255, 0.2);
        }
        
        .search-text {
          flex: 1;
          opacity: 0.7;
          text-align: left;
        }
        
        .search-kbd {
          font-size: 12px;
          opacity: 0.6;
          font-family: 'JetBrains Mono', monospace;
          background: none;
          border: none;
          padding: 0;
        }
        
        .topbar-nav {
          display: flex;
          gap: 8px;
        }
        
        .nav-link {
          color: inherit;
          text-decoration: none;
          padding: 8px 12px;
          border-radius: 6px;
          font-size: 14px;
          font-weight: 500;
          opacity: 0.8;
          transition: all 0.15s;
        }
        
        .nav-link:hover {
          opacity: 1;
          background: rgba(255, 255, 255, 0.1);
        }
        
        .nav-link.active {
          opacity: 1;
          background: rgba(255, 255, 255, 0.15);
        }
        
        .nav-badge {
          opacity: 0.6;
          margin-left: 4px;
        }
        
        .topbar-right {
          display: flex;
          align-items: center;
          gap: 16px;
          margin-left: auto;
        }
        
        .connection-status {
          display: flex;
          align-items: center;
          gap: 6px;
          font-size: 12px;
          opacity: 0.8;
        }
        
        .status-dot {
          width: 8px;
          height: 8px;
          border-radius: 50%;
          background: var(--otter-green);
        }
        
        .avatar {
          width: 32px;
          height: 32px;
          border-radius: 50%;
          background: rgba(255, 255, 255, 0.2);
          display: flex;
          align-items: center;
          justify-content: center;
          font-size: 14px;
          font-weight: 600;
          border: none;
          cursor: pointer;
          color: inherit;
        }
        
        .avatar:hover {
          background: rgba(255, 255, 255, 0.3);
        }
        
        .mobile-menu-btn {
          display: none;
          background: none;
          border: none;
          color: inherit;
          padding: 8px;
          cursor: pointer;
          border-radius: 6px;
        }
        
        .mobile-menu-btn:hover {
          background: rgba(255, 255, 255, 0.1);
        }
        
        /* ========== MOBILE NAV ========== */
        .mobile-nav {
          display: none;
          background: var(--otter-surface);
          border-bottom: 1px solid var(--otter-border);
          padding: 8px;
        }
        
        .mobile-nav-link {
          display: block;
          padding: 12px 16px;
          color: var(--otter-text);
          text-decoration: none;
          border-radius: 8px;
          font-size: 15px;
        }
        
        .mobile-nav-link:hover,
        .mobile-nav-link.active {
          background: var(--otter-surface-alt);
        }
        
        /* ========== MAIN CONTENT ========== */
        .main-content {
          flex: 1;
          padding: 24px;
          max-width: 1400px;
          margin: 0 auto;
          width: 100%;
        }
        
        /* ========== FOOTER ========== */
        .footer {
          padding: 20px 24px;
          border-top: 1px solid var(--otter-border);
          background: var(--otter-surface-alt);
          text-align: center;
        }
        
        .footer-fact {
          font-size: 13px;
          color: var(--otter-text-muted);
          display: flex;
          align-items: center;
          justify-content: center;
          gap: 8px;
        }
        
        .footer-fact strong {
          color: var(--otter-accent);
        }
        
        /* ========== RESPONSIVE ========== */
        @media (max-width: 900px) {
          .topbar-nav {
            display: none;
          }
          
          .mobile-menu-btn {
            display: block;
          }
          
          .mobile-nav {
            display: block;
          }
          
          .search-trigger {
            display: none;
          }
        }
        
        @media (max-width: 640px) {
          .topbar {
            padding: 12px 16px;
          }
          
          .main-content {
            padding: 16px;
          }
          
          .connection-status .status-text {
            display: none;
          }
        }
      `}</style>
    </div>
  );
}
