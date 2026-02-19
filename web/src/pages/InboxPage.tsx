import { useMemo, useState } from "react";
import { NavLink } from "react-router-dom";

type InboxFilter = "all" | "unread" | "starred";

type InboxItem = {
  id: string;
  issueId?: string;
  type: "approval" | "issue" | "notification" | "mention";
  title: string;
  description: string;
  project: string;
  from: string;
  timestamp: string;
  priority: "critical" | "high" | "medium" | "low";
  read: boolean;
  starred: boolean;
};

const INITIAL_ITEMS: InboxItem[] = [
  {
    id: "1",
    issueId: "ISS-234",
    type: "approval",
    title: "PR #234 awaiting approval",
    description: "Authentication flow refactor ready for review",
    project: "Customer Portal",
    from: "Agent-042",
    timestamp: "2 min ago",
    priority: "high",
    read: false,
    starred: false,
  },
  {
    id: "2",
    issueId: "ISS-209",
    type: "issue",
    title: "Critical: API rate limit exceeded",
    description: "Multiple requests hitting rate limit on /api/users endpoint",
    project: "API Gateway",
    from: "System",
    timestamp: "5 min ago",
    priority: "critical",
    read: false,
    starred: true,
  },
  {
    id: "3",
    issueId: "ISS-209",
    type: "mention",
    title: "You were mentioned in a discussion",
    description: "Agent-127 mentioned you in ISS-209 comments",
    project: "API Gateway",
    from: "Agent-127",
    timestamp: "12 min ago",
    priority: "medium",
    read: false,
    starred: false,
  },
  {
    id: "4",
    issueId: "ISS-198",
    type: "approval",
    title: "PR #231 approved and merged",
    description: "Database optimization changes have been deployed",
    project: "Internal Tools",
    from: "Agent-089",
    timestamp: "1 hour ago",
    priority: "low",
    read: true,
    starred: false,
  },
  {
    id: "5",
    type: "notification",
    title: "GitHub sync completed",
    description: "Successfully synced 15 commits from ottercamp/customer-portal",
    project: "Customer Portal",
    from: "GitHub Bot",
    timestamp: "2 hours ago",
    priority: "low",
    read: true,
    starred: false,
  },
  {
    id: "6",
    issueId: "ISS-311",
    type: "issue",
    title: "New issue: Update documentation",
    description: "README needs updates for new authentication flow",
    project: "Customer Portal",
    from: "Agent-042",
    timestamp: "3 hours ago",
    priority: "medium",
    read: true,
    starred: false,
  },
];

function typeGlyph(type: InboxItem["type"]): string {
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

function priorityClass(priority: InboxItem["priority"]): string {
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

function surfaceClass(type: InboxItem["type"]): string {
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

export default function InboxPage() {
  const [filter, setFilter] = useState<InboxFilter>("all");
  const [items, setItems] = useState<InboxItem[]>(INITIAL_ITEMS);

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

  return (
    <div className="space-y-4 md:space-y-6">
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

      <div className="divide-y divide-stone-800 rounded-lg border border-stone-800 bg-stone-900" data-testid="inbox-list-surface">
        {filteredItems.map((item) => {
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
                          markAsRead(item.id);
                        }}
                        className="rounded p-1.5 text-stone-400 transition-colors hover:bg-stone-700 hover:text-lime-400"
                        aria-label={`Mark ${item.title} as read`}
                      >
                        ✓
                      </button>
                      <button
                        type="button"
                        className="rounded p-1.5 text-stone-400 transition-colors hover:bg-stone-700 hover:text-stone-200"
                        aria-label={`Archive ${item.title}`}
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
              className={`group block p-4 transition-colors hover:bg-stone-800/50 md:p-6 ${item.read ? "" : "bg-stone-950/30"}`}
            >
              {content}
            </NavLink>
          );
        })}
      </div>
    </div>
  );
}
