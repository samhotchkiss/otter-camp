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

import { useState, useEffect, useRef, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useKeyboardShortcuts, type Shortcut } from "../hooks/useKeyboardShortcuts";
import { useWS } from "../contexts/WebSocketContext";
import ShortcutsHelpModal from "../components/ShortcutsHelpModal";
import DemoBanner from "../components/DemoBanner";
import GlobalSearch from "../components/GlobalSearch";
import GlobalChatDock from "../components/chat/GlobalChatDock";
import { isDemoMode } from "../lib/demo";
import { api } from "../lib/api";

type NavItem = {
  id: string;
  label: string;
  href: string;
};

const NAV_ITEMS: NavItem[] = [
  { id: "inbox", label: "Inbox", href: "/inbox" },
  { id: "projects", label: "Projects", href: "/projects" },
  { id: "workflows", label: "Workflows", href: "/workflows" },
  { id: "knowledge", label: "Knowledge", href: "/knowledge" },
];

const AVATAR_MENU_ITEMS: NavItem[] = [
  { id: "agents", label: "Agents", href: "/agents" },
  { id: "connections", label: "Connections", href: "/connections" },
  { id: "feed", label: "Feed", href: "/feed" },
  { id: "settings", label: "Settings", href: "/settings" },
];

// All nav items for active detection
const ALL_NAV_ITEMS = [...NAV_ITEMS, ...AVATAR_MENU_ITEMS];

type DashboardLayoutProps = {
  children: ReactNode;
};

type BridgeStatus = "healthy" | "degraded" | "unhealthy";

type AdminConnectionsBridgePayload = {
  connected?: boolean;
  sync_healthy?: boolean;
  status?: string;
  last_sync?: string;
  last_sync_age_seconds?: number;
};

type AdminConnectionsPayload = {
  bridge?: AdminConnectionsBridgePayload;
};

function normalizeBridgeStatus(
  bridge: AdminConnectionsBridgePayload | undefined,
  wsConnectedFallback: boolean,
): BridgeStatus {
  const rawStatus = (bridge?.status || "").trim().toLowerCase();
  if (rawStatus === "healthy" || rawStatus === "degraded" || rawStatus === "unhealthy") {
    return rawStatus;
  }
  if (bridge?.connected === false) {
    return "unhealthy";
  }
  if (bridge?.sync_healthy === true) {
    return "healthy";
  }
  if (bridge?.connected === true || wsConnectedFallback) {
    return "degraded";
  }
  return "unhealthy";
}

function getBridgeStatusLabel(status: BridgeStatus): string {
  if (status === "healthy") {
    return "Bridge healthy";
  }
  if (status === "degraded") {
    return "Bridge connected, OpenClaw unreachable";
  }
  return "Bridge offline";
}

function getBridgeDelayBannerMessage(status: BridgeStatus): string {
  if (status === "degraded") {
    return "Bridge connected but OpenClaw unreachable";
  }
  return "Bridge offline - reconnecting";
}

function normalizeLastSyncAgeSeconds(bridge: AdminConnectionsBridgePayload | undefined): number | null {
  if (typeof bridge?.last_sync_age_seconds === "number" && Number.isFinite(bridge.last_sync_age_seconds) && bridge.last_sync_age_seconds >= 0) {
    return Math.floor(bridge.last_sync_age_seconds);
  }
  const lastSyncISO = (bridge?.last_sync || "").trim();
  if (!lastSyncISO) {
    return null;
  }
  const parsed = Date.parse(lastSyncISO);
  if (!Number.isFinite(parsed)) {
    return null;
  }
  return Math.max(0, Math.floor((Date.now() - parsed) / 1000));
}

function formatLastSyncLabel(ageSeconds: number | null): string | null {
  if (!Number.isFinite(ageSeconds) || ageSeconds === null || ageSeconds < 0) {
    return null;
  }
  if (ageSeconds < 60) {
    return `Last successful sync ${ageSeconds}s ago`;
  }
  const minutes = Math.floor(ageSeconds / 60);
  if (minutes < 60) {
    return `Last successful sync ${minutes}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `Last successful sync ${hours}h ago`;
  }
  const days = Math.floor(hours / 24);
  return `Last successful sync ${days}d ago`;
}

function logOut() {
  // Clear auth cookies
  document.cookie = "otter_auth=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;";
  // Clear localStorage
  const keysToRemove = ["otter-camp-org-id", "otter-camp-token", "otter_camp_token", "otter-camp-theme"];
  keysToRemove.forEach((key) => localStorage.removeItem(key));
  // Redirect
  window.location.href = "/";
}

export default function DashboardLayout({ children }: DashboardLayoutProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [avatarMenuOpen, setAvatarMenuOpen] = useState(false);
  const avatarMenuRef = useRef<HTMLDivElement>(null);
  const [inboxCount, setInboxCount] = useState<number | null>(null);
  const { connected: wsConnected } = useWS();
  // In demo mode, always show as connected for better UX
  const connected = isDemoMode() || wsConnected;
  const [bridgeStatus, setBridgeStatus] = useState<BridgeStatus>(connected ? "healthy" : "unhealthy");
  const [bridgeLastSyncAgeSeconds, setBridgeLastSyncAgeSeconds] = useState<number | null>(null);
  
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
    if (path === "/") return null;
    const item = ALL_NAV_ITEMS.find((item) => path.startsWith(item.href) && item.href !== "/");
    return item?.id ?? null;
  };

  const activeNavId = getActiveNavId();

  // Close menus on navigation
  useEffect(() => {
    setMobileMenuOpen(false);
    setAvatarMenuOpen(false);
  }, [location.pathname]);

  // Close avatar menu on click outside or Escape
  useEffect(() => {
    if (!avatarMenuOpen) return;
    const handleClickOutside = (e: MouseEvent) => {
      if (avatarMenuRef.current && !avatarMenuRef.current.contains(e.target as Node)) {
        setAvatarMenuOpen(false);
      }
    };
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === "Escape") setAvatarMenuOpen(false);
    };
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [avatarMenuOpen]);

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

  useEffect(() => {
    let cancelled = false;
    let pollTimer: number | undefined;

    async function loadBridgeStatus() {
      if (isDemoMode()) {
        if (!cancelled) {
          setBridgeStatus("healthy");
        }
        return;
      }
      try {
        const payload = await api.adminConnections() as AdminConnectionsPayload;
        if (cancelled) {
          return;
        }
        setBridgeStatus(normalizeBridgeStatus(payload.bridge, connected));
        setBridgeLastSyncAgeSeconds(normalizeLastSyncAgeSeconds(payload.bridge));
      } catch {
        if (!cancelled) {
          setBridgeStatus((previous) => previous || (connected ? "healthy" : "unhealthy"));
        }
      }
    }

    void loadBridgeStatus();
    pollTimer = window.setInterval(() => {
      void loadBridgeStatus();
    }, 15000);

    return () => {
      cancelled = true;
      if (pollTimer) {
        window.clearInterval(pollTimer);
      }
    };
  }, [connected]);

  const bridgeStatusLabel = getBridgeStatusLabel(bridgeStatus);
  const bridgeStatusClass = bridgeStatus === "healthy"
    ? "connected"
    : bridgeStatus === "degraded"
      ? "degraded"
      : "disconnected";
  const bridgeDotClass = bridgeStatus === "healthy"
    ? "status-live"
    : bridgeStatus === "degraded"
      ? "status-degraded"
      : "status-offline";
  const isLocalDev = typeof window !== "undefined" &&
    (window.location.hostname === "localhost" || window.location.hostname === "127.0.0.1");
  const showBridgeDelayBanner = bridgeStatus !== "healthy" && !isLocalDev;
  const bridgeDelayBannerMessage = getBridgeDelayBannerMessage(bridgeStatus);
  const bridgeLastSyncLabel = formatLastSyncLabel(bridgeLastSyncAgeSeconds);

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
              {item.id === "inbox" && inboxCount !== null && (
                <span className="nav-badge" aria-label={`Inbox count ${inboxCount}`}>
                  {inboxCount}
                </span>
              )}
            </Link>
          ))}
        </nav>

        {/* Right Side */}
        <div className="topbar-right">
          {/* Connection Status */}
          <div
            className={`connection-status ${bridgeStatusClass}`}
            aria-label={bridgeStatusLabel}
            role="status"
          >
            <span className={`status-dot ${bridgeDotClass}`}></span>
            <span className="status-text">{bridgeStatusLabel}</span>
          </div>

          {/* User Avatar + Dropdown */}
          <div className="avatar-menu-container" ref={avatarMenuRef}>
            <button
              type="button"
              className="avatar"
              aria-label="User menu"
              aria-expanded={avatarMenuOpen}
              onClick={() => setAvatarMenuOpen(!avatarMenuOpen)}
            >
              S
            </button>
            {avatarMenuOpen && (
              <div className="avatar-dropdown">
                {AVATAR_MENU_ITEMS.map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    className={`avatar-dropdown-item ${activeNavId === item.id ? "active" : ""}`}
                    onClick={() => {
                      navigate(item.href);
                      setAvatarMenuOpen(false);
                    }}
                  >
                    {item.label}
                  </button>
                ))}
                <div className="avatar-dropdown-divider" />
                <button
                  type="button"
                  className="avatar-dropdown-item avatar-dropdown-logout"
                  onClick={logOut}
                >
                  Log Out
                </button>
              </div>
            )}
          </div>
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

      {showBridgeDelayBanner && (
        <div className={`bridge-delay-banner ${bridgeStatus}`} role="status" aria-live="polite">
          <span>{bridgeDelayBannerMessage}</span>
          {bridgeLastSyncLabel && (
            <span className="bridge-delay-detail">{bridgeLastSyncLabel}</span>
          )}
        </div>
      )}

      {/* Mobile Navigation */}
      {mobileMenuOpen && (
        <nav className="mobile-nav">
          {[...NAV_ITEMS, ...AVATAR_MENU_ITEMS].map((item) => (
            <Link
              key={item.id}
              to={item.href}
              className={`mobile-nav-link ${activeNavId === item.id ? "active" : ""}`}
            >
              {item.label}
              {item.id === "inbox" && inboxCount !== null && (
                <span className="nav-badge" aria-label={`Inbox count ${inboxCount}`}>
                  {inboxCount}
                </span>
              )}
            </Link>
          ))}
          <button type="button" className="mobile-nav-link mobile-logout" onClick={logOut}>
            Log Out
          </button>
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

      <GlobalChatDock />

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
