type CliCommand = {
  command: string;
  description: string;
  uses: number;
};

type ScheduledJob = {
  name: string;
  schedule: string;
  lastRun: string;
  status: string;
  nextRun: string;
};

type Migration = {
  project: string;
  status: "completed" | "in-progress";
  issues: number;
};

type ComplianceReview = {
  id: number;
  type: string;
  status: "passed" | "warning";
  reviewer: string;
};

const CLI_COMMANDS: CliCommand[] = [
  { command: "otter init", description: "Initialize new project repository", uses: 45 },
  { command: "otter sync", description: "Sync with GitHub", uses: 127 },
  { command: "otter agent spawn", description: "Spawn new chameleon agent", uses: 89 },
  { command: "otter memory export", description: "Export memory layers", uses: 23 },
];

const SCHEDULED_JOBS: ScheduledJob[] = [
  { name: "GitHub Sync", schedule: "Every 5m", lastRun: "2m ago", status: "success", nextRun: "3m" },
  { name: "Memory Consolidation", schedule: "Hourly", lastRun: "15m ago", status: "success", nextRun: "45m" },
  { name: "Agent Cleanup", schedule: "Daily 02:00", lastRun: "18h ago", status: "success", nextRun: "6h" },
  { name: "Compliance Review", schedule: "Weekly Mon", lastRun: "2d ago", status: "success", nextRun: "5d" },
];

const OPENCLAW_MIGRATIONS: Migration[] = [
  { project: "Legacy API", status: "completed", issues: 45 },
  { project: "Mobile App Backend", status: "completed", issues: 67 },
  { project: "Analytics Dashboard", status: "in-progress", issues: 23 },
];

const COMPLIANCE_REVIEWS: ComplianceReview[] = [
  { id: 1, type: "Security Audit", status: "passed", reviewer: "Agent-127" },
  { id: 2, type: "Code Quality", status: "passed", reviewer: "Memory System" },
  { id: 3, type: "Access Control", status: "warning", reviewer: "Orchestrator" },
];

export default function ConnectionsPage() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold text-stone-100">
            <span aria-hidden="true" className="text-amber-500">🖧</span>
            Operations
          </h1>
          <p className="text-sm text-stone-500">CLI • Integrations • Compliance</p>
        </div>
        <div className="flex gap-2">
          <button className="inline-flex items-center gap-2 rounded border border-stone-700 bg-stone-800 px-3 py-1.5 text-xs text-stone-300 transition hover:bg-stone-700" type="button">
            <span aria-hidden="true">☁</span>
            Multi-Tenant
          </button>
          <button className="inline-flex items-center gap-2 rounded border border-lime-500/20 bg-lime-600/10 px-3 py-1.5 text-xs text-lime-400 transition hover:bg-lime-600/20" type="button">
            <span aria-hidden="true">✓</span>
            System Healthy
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <section className="flex h-80 flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-950 font-mono text-sm shadow-2xl">
          <header className="flex items-center gap-2 border-b border-stone-800 bg-stone-900 px-4 py-2 text-xs text-stone-500">
            <div className="flex gap-1.5">
              <span className="h-2.5 w-2.5 rounded-full border border-red-500/50 bg-red-500/20" />
              <span className="h-2.5 w-2.5 rounded-full border border-amber-500/50 bg-amber-500/20" />
              <span className="h-2.5 w-2.5 rounded-full border border-lime-500/50 bg-lime-500/20" />
            </div>
            <span className="ml-2">otter-cli -- zsh</span>
          </header>
          <div className="flex-1 space-y-4 overflow-y-auto p-4 text-stone-300">
            <div>
              <div className="flex gap-2 text-stone-500">
                <span className="text-lime-500">-&gt;</span>
                <span className="text-amber-400">~</span>
                <span>otter status</span>
              </div>
              <div className="mt-1 pl-4 text-stone-400">
                <p>Otter Camp CLI v1.0.4</p>
                <p>Connected to <span className="text-lime-400">production</span> environment</p>
                <p>3 agents active, 12 projects synced</p>
              </div>
            </div>
            <div>
              <div className="flex gap-2 text-stone-500">
                <span className="text-lime-500">-&gt;</span>
                <span className="text-amber-400">~</span>
                <span>otter history --top 4</span>
              </div>
              <div className="mt-2 space-y-1 pl-4">
                {CLI_COMMANDS.map((cmd) => (
                  <div key={cmd.command} className="group flex cursor-pointer items-center justify-between rounded p-1 transition hover:bg-stone-900/50">
                    <span className="text-lime-300 transition group-hover:text-lime-200">{cmd.command}</span>
                    <span className="text-xs text-stone-600">Used {cmd.uses} times</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </section>

        <div className="space-y-6">
          <section className="rounded-lg border border-stone-800 bg-stone-900 p-5">
            <header className="mb-4 flex items-center justify-between">
              <h2 className="text-base font-semibold text-stone-200">OpenClaw Bridge</h2>
              <span className="inline-flex items-center gap-1.5 rounded border border-lime-500/20 bg-lime-500/10 px-2 py-0.5 text-[10px] text-lime-400">
                <span className="h-3 w-3 animate-pulse">◉</span>
                LIVE
              </span>
            </header>

            <div className="mb-4 grid grid-cols-3 gap-4">
              <div className="rounded border border-stone-800 bg-stone-950 p-3 text-center">
                <p className="text-2xl font-bold text-stone-200">3</p>
                <p className="text-[10px] uppercase tracking-wider text-stone-500">Connections</p>
              </div>
              <div className="rounded border border-stone-800 bg-stone-950 p-3 text-center">
                <p className="text-2xl font-bold text-stone-200">124MB</p>
                <p className="text-[10px] uppercase tracking-wider text-stone-500">Synced</p>
              </div>
              <div className="rounded border border-stone-800 bg-stone-950 p-3 text-center">
                <p className="text-2xl font-bold text-lime-400">99.9%</p>
                <p className="text-[10px] uppercase tracking-wider text-stone-500">Uptime</p>
              </div>
            </div>

            <div className="space-y-2">
              {OPENCLAW_MIGRATIONS.map((migration) => (
                <div key={migration.project} className="group flex items-center justify-between rounded p-2 text-xs transition hover:bg-stone-800">
                  <span className="font-medium text-stone-300">{migration.project}</span>
                  <div className="flex items-center gap-2">
                    <span className="text-stone-500">{migration.issues} issues</span>
                    <span
                      className={`h-1.5 w-1.5 rounded-full ${
                        migration.status === "completed" ? "bg-lime-500" : "animate-pulse bg-amber-500"
                      }`}
                    />
                  </div>
                </div>
              ))}
            </div>
          </section>

          <section className="rounded-lg border border-stone-800 bg-stone-900 p-5">
            <header className="mb-4">
              <h2 className="text-base font-semibold text-stone-200">Job Scheduler</h2>
            </header>
            <div className="space-y-3">
              {SCHEDULED_JOBS.map((job) => (
                <div key={job.name} className="flex items-center justify-between border-b border-stone-800/50 pb-2 text-xs last:border-0 last:pb-0">
                  <div>
                    <p className="font-medium text-stone-200">{job.name}</p>
                    <p className="mt-0.5 text-stone-500">Next run: {job.nextRun}</p>
                  </div>
                  <div className="text-right">
                    <span className="rounded bg-lime-500/10 px-1.5 py-0.5 font-mono text-[10px] text-lime-400">{job.status}</span>
                    <p className="mt-0.5 text-stone-600">{job.lastRun}</p>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>
      </div>

      <section className="overflow-hidden rounded-lg border border-stone-800 bg-stone-900">
        <header className="flex items-center justify-between border-b border-stone-800 bg-stone-950/30 px-5 py-3">
          <h2 className="text-sm font-semibold text-stone-200">Compliance Reviews</h2>
          <span className="text-xs text-stone-500">Last audit: Today</span>
        </header>
        <div className="grid grid-cols-1 divide-y divide-stone-800 md:grid-cols-3 md:divide-x md:divide-y-0">
          {COMPLIANCE_REVIEWS.map((review) => (
            <article key={review.id} className="group p-4 transition hover:bg-stone-800/20">
              <div className="mb-2 flex items-center justify-between">
                <span
                  className={`rounded border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${
                    review.status === "passed"
                      ? "border-lime-500/20 bg-lime-500/10 text-lime-400"
                      : "border-amber-500/20 bg-amber-500/10 text-amber-400"
                  }`}
                >
                  {review.status}
                </span>
                <span className="text-stone-600 transition group-hover:text-stone-400">🔒</span>
              </div>
              <h3 className="mb-1 text-sm font-medium text-stone-200">{review.type}</h3>
              <p className="text-xs text-stone-500">Reviewer: {review.reviewer}</p>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
