import { createContext, useContext, useState, useCallback, type ReactNode } from "react";

/**
 * ARIA live region politeness levels
 * - polite: Announced after current speech (use for most updates)
 * - assertive: Interrupts current speech (use for urgent updates)
 */
type LiveRegionPoliteness = "polite" | "assertive";

type Announcement = {
  message: string;
  politeness: LiveRegionPoliteness;
  id: number;
};

type LiveRegionContextValue = {
  /** Announce a message to screen readers */
  announce: (message: string, politeness?: LiveRegionPoliteness) => void;
  /** Announce a polite message (does not interrupt) */
  announcePolite: (message: string) => void;
  /** Announce an assertive message (interrupts current speech) */
  announceAssertive: (message: string) => void;
};

const LiveRegionContext = createContext<LiveRegionContextValue | null>(null);

let announcementId = 0;

/**
 * LiveRegionProvider - Context provider for screen reader announcements
 *
 * Provides methods to announce dynamic content changes to screen readers.
 * Uses ARIA live regions to make announcements without moving focus.
 *
 * @example
 * ```tsx
 * // In your app root
 * <LiveRegionProvider>
 *   <App />
 * </LiveRegionProvider>
 *
 * // In any component
 * const { announce } = useLiveRegion();
 * announce("Task moved to Done");
 * ```
 *
 * @see https://www.w3.org/WAI/WCAG21/Understanding/status-messages.html
 */
export function LiveRegionProvider({ children }: { children: ReactNode }) {
  const [politeMessage, setPoliteMessage] = useState<Announcement | null>(null);
  const [assertiveMessage, setAssertiveMessage] = useState<Announcement | null>(null);

  const announce = useCallback((message: string, politeness: LiveRegionPoliteness = "polite") => {
    const announcement: Announcement = {
      message,
      politeness,
      id: ++announcementId,
    };

    if (politeness === "assertive") {
      setAssertiveMessage(announcement);
    } else {
      setPoliteMessage(announcement);
    }

    // Clear after announcement (allows same message to be repeated)
    setTimeout(() => {
      if (politeness === "assertive") {
        setAssertiveMessage((current) =>
          current?.id === announcement.id ? null : current
        );
      } else {
        setPoliteMessage((current) =>
          current?.id === announcement.id ? null : current
        );
      }
    }, 1000);
  }, []);

  const announcePolite = useCallback(
    (message: string) => announce(message, "polite"),
    [announce]
  );

  const announceAssertive = useCallback(
    (message: string) => announce(message, "assertive"),
    [announce]
  );

  const value: LiveRegionContextValue = {
    announce,
    announcePolite,
    announceAssertive,
  };

  return (
    <LiveRegionContext.Provider value={value}>
      {children}

      {/* Polite live region - for non-urgent updates */}
      <div
        role="status"
        aria-live="polite"
        aria-atomic="true"
        className="sr-only"
      >
        {politeMessage?.message}
      </div>

      {/* Assertive live region - for urgent updates */}
      <div
        role="alert"
        aria-live="assertive"
        aria-atomic="true"
        className="sr-only"
      >
        {assertiveMessage?.message}
      </div>
    </LiveRegionContext.Provider>
  );
}

/**
 * useLiveRegion - Hook to access screen reader announcement methods
 *
 * @throws Error if used outside LiveRegionProvider
 */
export function useLiveRegion(): LiveRegionContextValue {
  const context = useContext(LiveRegionContext);

  if (!context) {
    throw new Error("useLiveRegion must be used within a LiveRegionProvider");
  }

  return context;
}

export default LiveRegionProvider;
