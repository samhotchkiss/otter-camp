import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { API_URL } from "../lib/api";

type ArchivedChatRecord = {
  id: string;
  thread_type: "dm" | "project" | "issue";
  thread_key: string;
  title: string;
  last_message_preview: string;
  last_message_at: string;
  auto_archived_reason?: string;
};

type ArchivedChatsResponse = {
  chats?: ArchivedChatRecord[];
};

function getOrgID(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return (window.localStorage.getItem("otter-camp-org-id") ?? "").trim();
}

function getToken(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return (window.localStorage.getItem("otter_camp_token") ?? "").trim();
}

function formatTime(value: string): string {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) {
    return "";
  }
  const date = new Date(parsed);
  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }
  return date.toLocaleDateString([], { month: "short", day: "numeric" });
}

function toReasonLabel(reason: string | undefined): string {
  if (reason === "issue_closed") {
    return "Auto-archived: issue closed";
  }
  if (reason === "project_archived") {
    return "Auto-archived: project archived";
  }
  return "Archived manually";
}

function toTypeLabel(type: ArchivedChatRecord["thread_type"]): string {
  if (type === "project") {
    return "Project";
  }
  if (type === "issue") {
    return "Issue";
  }
  return "DM";
}

export default function ArchivedChatsPage() {
  const [searchQuery, setSearchQuery] = useState("");
  const [debouncedSearchQuery, setDebouncedSearchQuery] = useState("");
  const [archivedChats, setArchivedChats] = useState<ArchivedChatRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [unarchivingChatID, setUnarchivingChatID] = useState<string | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  const orgID = useMemo(() => getOrgID(), []);

  const loadArchivedChats = useCallback(async (query: string, signal?: AbortSignal) => {
    if (!orgID) {
      setArchivedChats([]);
      setError("Missing organization context");
      setLoading(false);
      return;
    }

    setLoading(true);
    setError(null);

    const token = getToken();
    const headers: Record<string, string> = {};
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }

    try {
      const url = new URL(`${API_URL}/api/chats`);
      url.searchParams.set("org_id", orgID);
      url.searchParams.set("archived", "true");
      const trimmedQuery = query.trim();
      if (trimmedQuery) {
        url.searchParams.set("q", trimmedQuery);
      }
      const response = await fetch(url.toString(), {
        headers,
        cache: "no-store",
        signal,
      });
      if (!response.ok) {
        throw new Error(`Failed to load archived chats (${response.status})`);
      }
      const payload = (await response.json()) as ArchivedChatsResponse;
      setArchivedChats(Array.isArray(payload.chats) ? payload.chats : []);
    } catch (fetchError) {
      if (fetchError instanceof DOMException && fetchError.name === "AbortError") {
        return;
      }
      setArchivedChats([]);
      setError(fetchError instanceof Error ? fetchError.message : "Failed to load archived chats");
    } finally {
      setLoading(false);
    }
  }, [orgID]);

  useEffect(() => {
    const timeoutID = window.setTimeout(() => {
      setDebouncedSearchQuery(searchQuery);
    }, 250);
    return () => window.clearTimeout(timeoutID);
  }, [searchQuery]);

  useEffect(() => {
    const controller = new AbortController();
    void loadArchivedChats(debouncedSearchQuery, controller.signal);
    return () => controller.abort();
  }, [debouncedSearchQuery, loadArchivedChats, reloadToken]);

  const handleUnarchive = useCallback(async (chatID: string) => {
    const trimmedChatID = chatID.trim();
    if (!trimmedChatID || !orgID) {
      return;
    }

    setUnarchivingChatID(trimmedChatID);
    setError(null);

    const token = getToken();
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }

    try {
      const url = new URL(`${API_URL}/api/chats/${encodeURIComponent(trimmedChatID)}/unarchive`);
      url.searchParams.set("org_id", orgID);
      const response = await fetch(url.toString(), {
        method: "POST",
        headers,
      });
      if (!response.ok) {
        throw new Error(`Failed to unarchive chat (${response.status})`);
      }

      setArchivedChats((prev) => prev.filter((chat) => chat.id !== trimmedChatID));
    } catch (unarchiveError) {
      setError(unarchiveError instanceof Error ? unarchiveError.message : "Failed to unarchive chat");
    } finally {
      setUnarchivingChatID(null);
    }
  }, [orgID]);

  return (
    <div className="w-full max-w-4xl">
      <header className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold text-[var(--text)]">Archived Chats</h1>
          <p className="mt-1 text-sm text-[var(--text-muted)]">
            Search archived conversations and restore any thread back to active chats.
          </p>
        </div>
        <Link
          to="/chats"
          className="rounded-lg border border-[var(--border)] px-3 py-2 text-sm font-medium text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)]"
        >
          Back to active chats
        </Link>
      </header>

      <div className="mb-4 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-3">
        <label htmlFor="archived-chat-search" className="mb-2 block text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">
          Search archived chats
        </label>
        <input
          id="archived-chat-search"
          type="search"
          role="searchbox"
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.target.value)}
          placeholder="Search by title or recent message..."
          className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] outline-none transition focus:border-[var(--accent)]"
        />
      </div>

      {error ? (
        <div className="mb-4 rounded-xl border border-[var(--red)]/40 bg-[var(--red)]/10 px-4 py-3">
          <p className="text-sm text-[var(--red)]">{error}</p>
          <button
            type="button"
            onClick={() => setReloadToken((current) => current + 1)}
            className="mt-2 rounded border border-[var(--red)]/40 px-2.5 py-1 text-xs text-[var(--red)] transition hover:border-[var(--red)]"
          >
            Retry
          </button>
        </div>
      ) : null}

      {loading ? (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6 text-sm text-[var(--text-muted)]">
          Loading archived chats...
        </div>
      ) : null}

      {!loading && archivedChats.length === 0 ? (
        <div className="rounded-xl border border-dashed border-[var(--border)] bg-[var(--surface)] p-6 text-sm text-[var(--text-muted)]">
          No archived chats found.
        </div>
      ) : null}

      {!loading && archivedChats.length > 0 ? (
        <ul className="space-y-3">
          {archivedChats.map((chat) => (
            <li key={chat.id} className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
              <div className="mb-2 flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="truncate text-sm font-semibold text-[var(--text)]">{chat.title || "Untitled chat"}</h2>
                  <div className="mt-1 flex items-center gap-2 text-[11px] text-[var(--text-muted)]">
                    <span className="rounded-full border border-[var(--border)] px-1.5 py-0.5 uppercase tracking-wide">
                      {toTypeLabel(chat.thread_type)}
                    </span>
                    <span>{toReasonLabel(chat.auto_archived_reason)}</span>
                  </div>
                </div>
                <span className="shrink-0 text-[11px] text-[var(--text-muted)]">{formatTime(chat.last_message_at)}</span>
              </div>
              <p className="truncate text-sm text-[var(--text-muted)]">
                {chat.last_message_preview || "No messages"}
              </p>
              <div className="mt-3 flex justify-end">
                <button
                  type="button"
                  onClick={() => {
                    void handleUnarchive(chat.id);
                  }}
                  disabled={unarchivingChatID === chat.id}
                  className="rounded border border-[var(--border)] px-2.5 py-1 text-xs text-[var(--text-muted)] transition hover:border-[var(--accent)] hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {unarchivingChatID === chat.id ? "Unarchiving..." : "Unarchive"}
                </button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
