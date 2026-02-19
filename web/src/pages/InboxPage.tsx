import { useEffect, useMemo, useState } from "react";
import { NavLink } from "react-router-dom";

import api from "../lib/api";
import { mapInboxPayloadToCoreItems, type CoreInboxItem } from "../lib/coreDataAdapters";

type InboxFilter = "all" | "unread" | "starred";

function typeGlyph(type: CoreInboxItem["type"]): string {
  if (type === "approval") {
    return "PR";
  }
  if (type === "issue") {
    return "!";
  }
  if (type === "mention") {
    return "@";
  }
  return "OK";
}

function priorityClass(priority: CoreInboxItem["priority"]): string {
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

function surfaceClass(type: CoreInboxItem["type"]): string {
  if (type === "approval") {
    return "bg-amber-500/10 text-amber-400 border-amber-500/20";
  }
  if (type === "issue") {
    return "bg-rose-500/10 text-rose-400 border-rose-500/20";
  }
  if (type === "mention") {
    return "bg-lime-500/10 text-lime-400 border-lime-500/20";
  }
  return "bg-stone-700/50 text-stone-400 border-stone-600/50";
}

function normalizeErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "Failed to load inbox items";
}

function isApprovalRow(item: CoreInboxItem): boolean {
  return item.type === "approval";
}

export default function InboxPage() {
  const [filter, setFilter] = useState<InboxFilter>("all");
  const [items, setItems] = useState<CoreInboxItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [actingItemID, setActingItemID] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError(null);

    void api
      .inbox()
      .then((payload) => {
        if (cancelled) {
          return;
        }
        setItems(mapInboxPayloadToCoreItems(payload));
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        setItems([]);
        setLoadError(normalizeErrorMessage(error));
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const unreadCount = useMemo(() => items.filter((item) => !item.read).length, [items]);
  const starredCount = useMemo(() => items.filter((item) => item.starred).length, [items]);
  const filteredItems = useMemo(
    () =>
      items.filter((item) => {
        if (filter === "unread") return !item.read;
        if (filter === "starred") return item.starred;
        return true;
      }),
    [filter, items],
  );

  const markAsRead = (id: string) => {
    setItems((current) => current.map((item) => (item.id === id ? { ...item, read: true } : item)));
  };

  const toggleStar = (id: string) => {
    setItems((current) => current.map((item) => (item.id === id ? { ...item, starred: !item.starred } : item)));
  };

  const resolveApproval = async (id: string, decision: "approve" | "reject") => {
    const previousItems = items;
    setActingItemID(id);
    setLoadError(null);
    setItems((current) => current.filter((item) => item.id !== id));

    try {
      if (decision === "approve") {
        await api.approveItem(id);
      } else {
        await api.rejectItem(id);
      }
    } catch (error: unknown) {
      setItems(previousItems);
      setLoadError(
        error instanceof Error && error.message.trim()
          ? error.message
          : decision === "approve"
            ? "Failed to approve inbox item"
            : "Failed to reject inbox item",
      );
    } finally {
      setActingItemID(null);
    }
  };

  return (
    <div className="min-w-0 space-y-4 md:space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="mb-1 text-2xl font-bold text-stone-100 md:text-3xl">Inbox</h1>
          <p className="text-sm text-stone-400">{filteredItems.length} items waiting for your attention</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="rounded-md p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200"
            aria-label="Filter inbox"
          >
            FLT
          </button>
          <button
            type="button"
            className="rounded-md p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200"
            aria-label="Archive inbox"
          >
            ARC
          </button>
        </div>
      </div>

      <div className="flex items-center gap-2 overflow-x-auto border-b border-stone-800" role="tablist" aria-label="Inbox filters">
        <button
          type="button"
          role="tab"
          aria-selected={filter === "all"}
          onClick={() => setFilter("all")}
          className={`whitespace-nowrap px-4 py-2 text-sm font-medium transition-colors ${
            filter === "all" ? "border-b-2 border-amber-400 text-amber-400" : "text-stone-400 hover:text-stone-200"
          }`}
        >
          All ({items.length})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={filter === "unread"}
          onClick={() => setFilter("unread")}
          className={`whitespace-nowrap px-4 py-2 text-sm font-medium transition-colors ${
            filter === "unread" ? "border-b-2 border-amber-400 text-amber-400" : "text-stone-400 hover:text-stone-200"
          }`}
        >
          Unread ({unreadCount})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={filter === "starred"}
          onClick={() => setFilter("starred")}
          className={`whitespace-nowrap px-4 py-2 text-sm font-medium transition-colors ${
            filter === "starred" ? "border-b-2 border-amber-400 text-amber-400" : "text-stone-400 hover:text-stone-200"
          }`}
        >
          Starred ({starredCount})
        </button>
      </div>

      <div className="divide-y divide-stone-800 overflow-hidden rounded-lg border border-stone-800 bg-stone-900" data-testid="inbox-list-surface">
        {loading ? (
          <div className="p-4 text-sm text-stone-400 md:p-6">Loading inbox...</div>
        ) : null}

        {!loading && loadError ? (
          <div className="space-y-3 p-4 md:p-6">
            <p className="text-sm text-rose-400">{loadError}</p>
            <button
              type="button"
              onClick={() => setRefreshKey((current) => current + 1)}
              className="rounded border border-rose-500/40 bg-rose-500/10 px-3 py-1.5 text-xs font-semibold text-rose-300 transition-colors hover:bg-rose-500/20"
            >
              Retry
            </button>
          </div>
        ) : null}

        {!loading && !loadError && filteredItems.length === 0 ? (
          <div className="p-4 text-sm text-stone-400 md:p-6">No inbox items found.</div>
        ) : null}

        {!loading && !loadError
          ? filteredItems.map((item) => {
              const content = (
                <>
                  <div className="flex gap-3 md:gap-4">
                    <div className={`mt-2 h-2 w-2 shrink-0 rounded-full ${priorityClass(item.priority)}`}></div>

                    <div className="min-w-0 flex-1">
                      <div className="mb-2 flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
                        <div className="min-w-0 flex-1">
                          <div className="mb-1 flex flex-wrap items-center gap-2">
                            <span className={`inline-flex h-5 min-w-6 items-center justify-center rounded border px-1 text-[10px] font-semibold ${surfaceClass(item.type)}`}>
                              {typeGlyph(item.type)}
                            </span>
                            <h3 className={`text-sm font-medium md:text-base ${item.read ? "text-stone-300" : "text-stone-100"}`}>{item.title}</h3>
                          </div>
                          <p className="text-xs text-stone-400 md:text-sm">{item.description}</p>
                        </div>
                        <span className="whitespace-nowrap text-xs text-stone-600">{item.timestamp}</span>
                      </div>

                      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                        <div className="flex flex-wrap items-center gap-3 text-xs text-stone-500">
                          <span>{item.project}</span>
                          <span className="hidden sm:inline">•</span>
                          <span>from {item.from}</span>
                          {item.issueId ? (
                            <>
                              <span className="hidden sm:inline">•</span>
                              <span className="font-mono text-amber-400">{item.issueId}</span>
                            </>
                          ) : null}
                        </div>

                        <div className="flex items-center gap-1 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100">
                          <button
                            type="button"
                            onClick={(event) => {
                              event.preventDefault();
                              event.stopPropagation();
                              toggleStar(item.id);
                            }}
                            className="rounded p-1.5 text-stone-400 transition-colors hover:bg-stone-700 hover:text-amber-400"
                            aria-label={`Toggle star for ${item.title}`}
                          >
                            {item.starred ? "★" : "☆"}
                          </button>
                          <button
                            type="button"
                            onClick={(event) => {
                              event.preventDefault();
                              event.stopPropagation();
                              if (isApprovalRow(item)) {
                                void resolveApproval(item.id, "approve");
                                return;
                              }
                              markAsRead(item.id);
                            }}
                            disabled={actingItemID === item.id}
                            className="rounded p-1.5 text-stone-400 transition-colors hover:bg-stone-700 hover:text-lime-400 disabled:cursor-not-allowed disabled:opacity-60"
                            aria-label={isApprovalRow(item) ? `Approve ${item.title}` : `Mark ${item.title} as read`}
                          >
                            ✓
                          </button>
                          <button
                            type="button"
                            onClick={(event) => {
                              event.preventDefault();
                              event.stopPropagation();
                              if (!isApprovalRow(item)) {
                                return;
                              }
                              void resolveApproval(item.id, "reject");
                            }}
                            disabled={actingItemID === item.id}
                            className="rounded p-1.5 text-stone-400 transition-colors hover:bg-stone-700 hover:text-rose-400 disabled:cursor-not-allowed disabled:opacity-60"
                            aria-label={isApprovalRow(item) ? `Reject ${item.title}` : `Archive ${item.title}`}
                          >
                            ▣
                          </button>
                        </div>
                      </div>
                    </div>
                  </div>
                </>
              );

              if (!item.issueId) {
                return (
                  <div
                    key={item.id}
                    className={`group p-4 transition-colors hover:bg-stone-800/50 md:p-6 ${item.read ? "" : "bg-stone-950/30"}`}
                  >
                    {content}
                  </div>
                );
              }

              return (
                <NavLink
                  key={item.id}
                  to={`/issue/${encodeURIComponent(item.issueId)}`}
                  className={`group block min-w-0 p-4 transition-colors hover:bg-stone-800/50 md:p-6 ${item.read ? "" : "bg-stone-950/30"}`}
                >
                  {content}
                </NavLink>
              );
            })
          : null}
      </div>
    </div>
  );
}
