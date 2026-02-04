import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";

// Sample data - would come from API
const NEEDS_YOU = [
  {
    id: "1",
    icon: "🚀",
    project: "ItsAlive",
    agent: "Ivy",
    text: "is waiting on your approval to deploy v2.1.0 with the new onboarding flow.",
    time: "5 min ago",
    primaryAction: "Approve Deploy",
    secondaryAction: "View Details",
  },
  {
    id: "2",
    icon: "✍️",
    project: "Content",
    agent: "Stone",
    text: 'finished a blog post for you to review: "Why I Run 12 AI Agents"',
    time: "1 hour ago",
    primaryAction: "Review Post",
    secondaryAction: "Later",
  },
];

const FEED_ITEMS = [
  {
    id: "progress",
    type: "progress",
    avatar: "✓",
    avatarBg: "bg-otter-green",
    title: "4 projects active",
    text: "Derek pushed 4 commits to Pearl, Jeff G finished mockups, Nova scheduled tweets",
    meta: "Last 6 hours • 14 updates total",
  },
  {
    id: "email",
    type: "insight",
    avatar: "P",
    avatarBg: "bg-otter-blue",
    title: "Important email",
    text: 'from investor@example.com — "Follow up on our conversation"',
    meta: "Penny • Email",
    time: "30 min ago",
  },
  {
    id: "markets",
    type: "progress",
    avatar: "B",
    avatarBg: "bg-otter-orange",
    title: "Market Summary",
    text: "S&P up 0.8%, your watchlist +1.2%. No alerts triggered.",
    meta: "Beau H • Markets",
    time: "2 hours ago",
  },
  {
    id: "twitter",
    type: "insight",
    avatar: "N",
    avatarBg: "bg-pink-500",
    title: "@samhotchkiss",
    text: "got 3 replies worth reading. One potential lead.",
    meta: "Nova • Twitter",
    time: "45 min ago",
  },
];

const PROJECTS = [
  { id: "1", name: "ItsAlive", desc: "Waiting on deploy approval", status: "blocked", time: "5m" },
  { id: "2", name: "Pearl", desc: "Derek pushing commits", status: "working", time: "2m" },
  { id: "3", name: "Otter Camp", desc: "Design + architecture in progress", status: "working", time: "now" },
  { id: "4", name: "Three Stones", desc: "Presentation shipped", status: "idle", time: "3h" },
  { id: "5", name: "Content", desc: "Tweets scheduled", status: "idle", time: "1h" },
];

type ProjectStatus = "blocked" | "working" | "idle";

const STATUS_COLORS: Record<ProjectStatus, string> = {
  blocked: "bg-otter-red",
  working: "bg-otter-green",
  idle: "bg-otter-muted",
};

export default function HomePage() {
  const { openCommandPalette } = useKeyboardShortcutsContext();

  return (
    <div className="flex flex-col gap-6">
      <section className="mx-auto grid w-full max-w-6xl grid-cols-1 gap-6 lg:grid-cols-[1fr_360px]">
        {/* Primary Column */}
        <div className="flex flex-col gap-6">
          {/* Needs You Section */}
          <section>
            <div className="mb-4 flex items-center justify-between">
              <span className="text-xs font-bold uppercase tracking-wider text-otter-dark-muted">
                ⚡ Needs You
              </span>
              <span className="rounded-full bg-otter-red px-2.5 py-0.5 text-xs font-bold text-white">
                {NEEDS_YOU.length}
              </span>
            </div>

            <div className="space-y-3">
              {NEEDS_YOU.map((item) => (
                <div
                  key={item.id}
                  className="rounded-xl border-2 border-otter-orange bg-otter-dark-surface p-6 shadow-lg shadow-otter-orange/10 transition hover:-translate-y-0.5 hover:shadow-xl hover:shadow-otter-orange/20"
                >
                  <div className="mb-3 flex items-center gap-3">
                    <span className="text-2xl">{item.icon}</span>
                    <span className="text-lg font-bold text-otter-dark-text">{item.project}</span>
                    <span className="ml-auto text-xs text-otter-dark-muted">{item.time}</span>
                  </div>
                  <p className="mb-4 text-otter-dark-muted">
                    <strong className="text-otter-dark-text">{item.agent}</strong> {item.text}
                  </p>
                  <div className="flex gap-3">
                    <button className="rounded-lg bg-otter-dark-accent px-5 py-2.5 text-sm font-semibold text-otter-dark-bg transition hover:bg-otter-dark-accent-hover">
                      {item.primaryAction}
                    </button>
                    <button className="rounded-lg border border-otter-dark-border bg-otter-dark-surface-alt px-5 py-2.5 text-sm font-semibold text-otter-dark-text transition hover:bg-otter-dark-surface">
                      {item.secondaryAction}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* Your Feed Section */}
          <section>
            <div className="mb-4 flex items-center justify-between">
              <span className="text-xs font-bold uppercase tracking-wider text-otter-dark-muted">
                📡 Your Feed
              </span>
              <span className="rounded-full bg-otter-dark-muted px-2.5 py-0.5 text-xs font-bold text-white">
                {FEED_ITEMS.length}
              </span>
            </div>

            {/* Progress Card */}
            <div className="overflow-hidden rounded-xl border border-otter-dark-border bg-otter-dark-surface">
              <div className="border-b border-otter-dark-border bg-otter-dark-surface-alt px-5 py-4">
                <span className="text-sm font-bold text-otter-dark-text">📊 Project Progress</span>
              </div>
              <div
                className="flex cursor-pointer gap-3.5 p-4 transition hover:bg-otter-dark-surface-alt"
              >
                <div className={`flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full ${FEED_ITEMS[0].avatarBg} text-sm font-semibold text-white`}>
                  {FEED_ITEMS[0].avatar}
                </div>
                <div className="flex-1">
                  <p className="text-sm text-otter-dark-text">
                    <strong>{FEED_ITEMS[0].title}</strong> • {FEED_ITEMS[0].text}
                  </p>
                  <p className="mt-1 text-xs text-otter-dark-muted">{FEED_ITEMS[0].meta}</p>
                </div>
              </div>
            </div>

            {/* Other Feed Items */}
            <div className="mt-3 overflow-hidden rounded-xl border border-otter-dark-border bg-otter-dark-surface">
              {FEED_ITEMS.slice(1).map((item, i) => (
                <div
                  key={item.id}
                  className={`flex cursor-pointer gap-3.5 p-4 transition hover:bg-otter-dark-surface-alt ${
                    i < FEED_ITEMS.length - 2 ? "border-b border-otter-dark-border" : ""
                  }`}
                >
                  <div className={`flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full ${item.avatarBg} text-sm font-semibold text-white`}>
                    {item.avatar}
                  </div>
                  <div className="flex-1">
                    <p className="text-sm text-otter-dark-text">
                      <strong>{item.title}</strong> {item.text}
                    </p>
                    <p className="mt-1 text-xs text-otter-dark-muted">
                      <span className={`mr-2 rounded px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide ${
                        item.type === "insight" 
                          ? "bg-otter-blue/15 text-otter-blue" 
                          : "bg-otter-green/15 text-otter-green"
                      }`}>
                        {item.meta}
                      </span>
                      • {item.time}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>

        {/* Secondary Column */}
        <div className="flex flex-col gap-6">
          {/* Otter Illustration */}
          <div className="overflow-hidden rounded-2xl border border-otter-dark-border bg-otter-dark-surface p-5 text-center">
            <img
              src="/images/otters-sailing.png"
              alt="Otters sailing together"
              className="mx-auto w-full max-w-[280px] rounded-lg opacity-90 transition hover:opacity-100 hover:scale-[1.02]"
              style={{ filter: "sepia(20%) contrast(1.1)" }}
              onError={(e) => {
                // Fallback to emoji if image not found
                e.currentTarget.style.display = 'none';
                e.currentTarget.nextElementSibling?.classList.remove('hidden');
              }}
            />
            <div className="hidden text-8xl py-8">🦦🚣</div>
            <p className="mt-3 text-sm italic text-otter-dark-muted">Your otters, working together</p>
          </div>

          {/* Drop a Thought Button */}
          <button
            onClick={openCommandPalette}
            className="group rounded-xl border-2 border-dashed border-otter-dark-border bg-otter-dark-surface p-6 text-center transition hover:border-solid hover:border-otter-dark-accent hover:-translate-y-0.5 hover:shadow-lg"
          >
            <div className="text-5xl transition group-hover:scale-110">🦦💭</div>
            <div className="mt-2 text-sm font-semibold text-otter-dark-muted">Drop a thought</div>
            <div className="mt-1 text-xs text-otter-dark-muted/70">Press / to open command bar</div>
          </button>

          {/* Projects List */}
          <div className="overflow-hidden rounded-xl border border-otter-dark-border bg-otter-dark-surface">
            <div className="border-b border-otter-dark-border bg-otter-dark-surface-alt px-5 py-3.5 text-sm font-bold text-otter-dark-text">
              Projects
            </div>
            {PROJECTS.map((project, i) => (
              <div
                key={project.id}
                className={`flex cursor-pointer items-center gap-3 px-5 py-3.5 transition hover:bg-otter-dark-surface-alt hover:translate-x-1 ${
                  i < PROJECTS.length - 1 ? "border-b border-otter-dark-border" : ""
                }`}
              >
                <div className={`h-2.5 w-2.5 flex-shrink-0 rounded-full ${STATUS_COLORS[project.status as ProjectStatus]}`} />
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-semibold text-otter-dark-text">{project.name}</div>
                  <div className="truncate text-xs text-otter-dark-muted">{project.desc}</div>
                </div>
                <div className="text-[11px] text-otter-dark-muted">{project.time}</div>
              </div>
            ))}
          </div>
        </div>
      </section>
    </div>
  );
}
