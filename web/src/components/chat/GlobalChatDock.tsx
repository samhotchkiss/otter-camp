import { useEffect, useMemo, useState } from "react";
import { useLocation } from "react-router-dom";
import {
  useGlobalChat,
  type GlobalChatConversation,
  type GlobalIssueConversation,
  type GlobalProjectConversation,
} from "../../contexts/GlobalChatContext";
import GlobalChatSurface from "./GlobalChatSurface";
import { getChatContextCue } from "./chatContextCue";

function fallbackConversationFromRoute(pathname: string): GlobalChatConversation | null {
  const issueMatch = /^\/projects\/([^/]+)\/issues\/([^/]+)$/.exec(pathname);
  if (issueMatch?.[2]) {
    const issueConversation: GlobalIssueConversation = {
      key: `issue:${decodeURIComponent(issueMatch[2])}`,
      type: "issue",
      issueId: decodeURIComponent(issueMatch[2]),
      projectId: decodeURIComponent(issueMatch[1] || ""),
      title: "ISS-209",
      contextLabel: "Issue",
      subtitle: "Issue conversation",
      unreadCount: 0,
      updatedAt: new Date().toISOString(),
    };
    return issueConversation;
  }

  const issueAliasMatch = /^\/issue\/([^/]+)$/.exec(pathname);
  if (issueAliasMatch?.[1]) {
    const issueConversation: GlobalIssueConversation = {
      key: `issue:${decodeURIComponent(issueAliasMatch[1])}`,
      type: "issue",
      issueId: decodeURIComponent(issueAliasMatch[1]),
      title: "ISS-209",
      contextLabel: "Issue",
      subtitle: "Issue conversation",
      unreadCount: 0,
      updatedAt: new Date().toISOString(),
    };
    return issueConversation;
  }

  const projectMatch = /^\/projects\/([^/]+)$/.exec(pathname);
  if (projectMatch?.[1]) {
    const projectConversation: GlobalProjectConversation = {
      key: `project:${decodeURIComponent(projectMatch[1])}`,
      type: "project",
      projectId: decodeURIComponent(projectMatch[1]),
      title: "API Gateway",
      contextLabel: "Project",
      subtitle: "Team thread",
      unreadCount: 0,
      updatedAt: new Date().toISOString(),
    };
    return projectConversation;
  }

  return null;
}

export default function GlobalChatDock() {
  const { isOpen, totalUnread, selectedConversation, setDockOpen } = useGlobalChat();
  const location = useLocation();
  const [isFullscreen, setIsFullscreen] = useState(false);

  const routeFallbackConversation = useMemo(
    () => fallbackConversationFromRoute(location.pathname),
    [location.pathname],
  );

  const activeConversation = selectedConversation ?? routeFallbackConversation;
  const contextCue = getChatContextCue(activeConversation?.type ?? null);

  useEffect(() => {
    if (!isOpen) {
      setIsFullscreen(false);
      return;
    }

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") {
        return;
      }
      if (isFullscreen) {
        setIsFullscreen(false);
        return;
      }
      setDockOpen(false);
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [isFullscreen, isOpen, setDockOpen]);

  if (!isOpen) {
    return (
      <div className="fixed bottom-4 right-4 z-50">
        <button
          type="button"
          aria-label="Open global chat"
          onClick={() => setDockOpen(true)}
          className="inline-flex items-center gap-2 rounded-t-xl rounded-bl-xl border border-stone-700 bg-stone-900 px-4 py-2.5 text-sm font-medium text-stone-200 shadow-lg transition hover:border-amber-400/50"
        >
          <span>Chat</span>
          {totalUnread > 0 ? (
            <span className="inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-amber-500 px-1.5 text-[11px] font-semibold text-stone-950">
              {totalUnread > 99 ? "99+" : totalUnread}
            </span>
          ) : null}
        </button>
      </div>
    );
  }

  return (
    <div
      className={[
        "z-50",
        isFullscreen
          ? "fixed inset-4"
          : "fixed bottom-4 right-4 w-[calc(100vw-2rem)] max-w-[960px]",
      ].join(" ")}
    >
      <section className={`overflow-hidden rounded-2xl border border-stone-800 bg-stone-900 shadow-2xl ${isFullscreen ? "h-full" : ""}`}>
        <header className="flex items-center justify-between border-b border-stone-800 bg-stone-900/90 px-4 py-2.5">
          <div className="flex min-w-0 items-center gap-3">
            <h2 className="text-sm font-semibold text-stone-100">Global Chat</h2>
            <span
              className="rounded-full border border-stone-700 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.12em] text-stone-400"
              data-testid="global-chat-context-cue"
            >
              {contextCue}
            </span>
          </div>

          <div className="flex items-center gap-2">
            <span className="hidden text-[10px] font-mono uppercase tracking-[0.12em] text-lime-400 sm:inline-flex sm:items-center sm:gap-1.5">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-lime-400" />
              Online
            </span>
            <button
              type="button"
              aria-label={isFullscreen ? "Exit fullscreen chat" : "Fullscreen chat"}
              onClick={() => setIsFullscreen((current) => !current)}
              className="rounded-lg border border-stone-700 px-2.5 py-1 text-xs text-stone-400 transition hover:border-amber-400/40 hover:text-stone-200"
            >
              {isFullscreen ? "-" : "+"}
            </button>
            <button
              type="button"
              aria-label="Collapse global chat"
              onClick={() => setDockOpen(false)}
              className="rounded-lg border border-stone-700 px-2.5 py-1 text-xs text-stone-400 transition hover:border-amber-400/40 hover:text-stone-200"
            >
              Close
            </button>
          </div>
        </header>

        <div className={isFullscreen ? "h-[calc(100%-46px)]" : "h-[min(72vh,620px)] max-h-[calc(100vh-6rem)]"}>
          <GlobalChatSurface conversation={activeConversation} />
        </div>
      </section>
    </div>
  );
}
