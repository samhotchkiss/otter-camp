import { NavLink, useParams } from "react-router-dom";

type IssueStatus = "approval-needed" | "in-progress" | "blocked" | "review" | "open";
type IssuePriority = "critical" | "high" | "medium" | "low";

type RelatedIssue = {
  id: string;
  title: string;
  relation: "blocks" | "related";
};

type TimelineItem = {
  event: string;
  user: string;
  time: string;
};

type ProposedChange = {
  file: string;
  type: "added" | "modified" | "deleted";
  lines: string;
};

const ISSUE = {
  id: "ISS-209",
  title: "Fix API rate limiting",
  description:
    "The current rate limiting implementation allows burst requests that exceed the configured threshold. We need a token bucket approach with stronger enforcement.",
  status: "approval-needed" as IssueStatus,
  priority: "critical" as IssuePriority,
  project: "API Gateway",
  projectID: "project-2",
  assignee: "Agent-007",
  reporter: "You",
  created: "2 hours ago",
  updated: "15 minutes ago",
  labels: ["bug", "performance", "infrastructure"],
};

const PROPOSED_SOLUTION = {
  agent: "Agent-127",
  timestamp: "45 minutes ago",
  summary: "Implement Redis-backed token bucket rate limiting",
  details:
    "Replace the in-memory limiter with a Redis-backed token bucket algorithm so all gateway instances enforce shared limits consistently.",
  estimatedEffort: "4-6 hours",
  risks: ["Requires Redis dependency", "Potential latency increase of ~2ms per request"],
  changes: [
    { file: "src/middleware/rateLimit.ts", type: "modified", lines: "+145, -67" },
    { file: "src/config/redis.ts", type: "added", lines: "+89" },
    { file: "tests/rateLimit.test.ts", type: "modified", lines: "+234, -42" },
  ] as ProposedChange[],
};

const COMMENTS = [
  {
    id: "c1",
    author: "You",
    authorType: "user" as const,
    timestamp: "1 hour ago",
    content: "This is blocking production deployment. Can we prioritize this?",
  },
  {
    id: "c2",
    author: "Agent-007",
    authorType: "agent" as const,
    timestamp: "50 minutes ago",
    content: "Acknowledged. Assigned to Agent-127 for a distributed-systems solution proposal.",
  },
  {
    id: "c3",
    author: "Agent-127",
    authorType: "agent" as const,
    timestamp: "45 minutes ago",
    content: "Analysis complete. Redis token bucket plan is ready for approval.",
  },
];

const TIMELINE: TimelineItem[] = [
  { event: "Issue created", user: "You", time: "2 hours ago" },
  { event: "Assigned to Agent-007", user: "System", time: "2 hours ago" },
  { event: "Priority set to Critical", user: "You", time: "1 hour ago" },
  { event: "Agent-127 proposed solution", user: "Agent-127", time: "45 minutes ago" },
  { event: "Status changed to Approval Needed", user: "Agent-007", time: "45 minutes ago" },
];

const RELATED_ISSUES: RelatedIssue[] = [
  { id: "ISS-198", title: "Optimize database queries", relation: "blocks" },
  { id: "ISS-234", title: "Auth flow refactor", relation: "related" },
];

function statusClass(status: IssueStatus): string {
  if (status === "approval-needed") return "text-rose-400 bg-rose-500/10 border-rose-500/20";
  if (status === "in-progress") return "text-amber-400 bg-amber-500/10 border-amber-500/20";
  if (status === "blocked") return "text-orange-400 bg-orange-500/10 border-orange-500/20";
  if (status === "review") return "text-lime-400 bg-lime-500/10 border-lime-500/20";
  return "text-stone-400 bg-stone-500/10 border-stone-500/20";
}

function priorityDotClass(priority: IssuePriority): string {
  if (priority === "critical") return "bg-rose-500";
  if (priority === "high") return "bg-orange-500";
  if (priority === "medium") return "bg-amber-500";
  return "bg-stone-600";
}

function changeTypeClass(type: ProposedChange["type"]): string {
  if (type === "added") return "bg-lime-500/10 text-lime-400";
  if (type === "modified") return "bg-amber-500/10 text-amber-400";
  return "bg-rose-500/10 text-rose-400";
}

export default function IssueDetailPage() {
  const { id: projectIDParam } = useParams<{ id?: string; issueId?: string }>();
  const projectLink = projectIDParam ? `/projects/${encodeURIComponent(projectIDParam)}` : `/projects/${ISSUE.projectID}`;

  return (
    <div data-testid="issue-detail-shell" className="min-w-0 space-y-4 md:space-y-6">
      <section className="rounded-lg border border-stone-800 bg-stone-900 p-4 md:p-6">
        <div className="mb-4 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex-1">
            <div className="mb-3 flex flex-wrap items-center gap-2 md:gap-3">
              <span className="text-xs font-mono text-stone-500">{ISSUE.id}</span>
              <span className={`rounded-full border px-2 py-1 text-[10px] md:text-xs ${statusClass(ISSUE.status)}`}>
                {ISSUE.status.replace("-", " ")}
              </span>
              <div className="flex items-center gap-2">
                <div className={`h-2 w-2 rounded-full ${priorityDotClass(ISSUE.priority)}`} />
                <span className="text-xs uppercase tracking-wider text-stone-400">{ISSUE.priority}</span>
              </div>
            </div>
            <h1 className="mb-2 text-xl font-bold text-stone-100 md:text-2xl">{ISSUE.title}</h1>
            <p className="text-sm leading-relaxed text-stone-400 md:text-base">{ISSUE.description}</p>
          </div>
          <button
            type="button"
            aria-label="Issue settings"
            className="self-end rounded-md p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200 sm:self-auto"
          >
            ⚙
          </button>
        </div>

        <div className="flex flex-col gap-2 text-xs md:text-sm sm:flex-row sm:flex-wrap sm:items-center sm:gap-4">
          <div className="flex items-center gap-2">
            <span className="text-stone-500">Project:</span>
            <NavLink to={projectLink} className="font-medium text-amber-400 hover:text-amber-300">
              {ISSUE.project}
            </NavLink>
          </div>
          <span className="hidden text-stone-700 sm:inline">•</span>
          <div className="flex items-center gap-2">
            <span className="text-stone-500">Assignee:</span>
            <span className="font-medium text-stone-300">{ISSUE.assignee}</span>
          </div>
          <span className="hidden text-stone-700 sm:inline">•</span>
          <div className="flex items-center gap-2">
            <span className="text-stone-500">Reporter:</span>
            <span className="font-medium text-stone-300">{ISSUE.reporter}</span>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap items-center gap-2">
          {ISSUE.labels.map((label) => (
            <span key={label} className="rounded border border-stone-700 bg-stone-800 px-2 py-1 text-xs text-stone-400">
              {label}
            </span>
          ))}
        </div>
      </section>

      <section className="rounded-lg border-2 border-amber-600/30 bg-amber-950/20 p-4 md:p-6">
        <div className="mb-4">
          <div className="mb-2 flex items-center gap-2">
            <span className="text-amber-400">!</span>
            <h2 className="text-lg font-semibold text-amber-200">Proposed Solution Awaiting Approval</h2>
          </div>
          <p className="text-sm text-amber-300/80">
            {PROPOSED_SOLUTION.agent} proposed this approach <span className="ml-1 text-amber-500">{PROPOSED_SOLUTION.timestamp}</span>
          </p>
        </div>

        <div className="mb-4 rounded-lg border border-stone-800 bg-stone-900/50 p-4">
          <h3 className="mb-2 font-semibold text-stone-100">{PROPOSED_SOLUTION.summary}</h3>
          <p className="mb-4 text-sm text-stone-300">{PROPOSED_SOLUTION.details}</p>

          <div className="mb-4 grid grid-cols-1 gap-4 md:grid-cols-2">
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-stone-500">Estimated Effort</p>
              <p className="text-sm font-medium text-stone-300">{PROPOSED_SOLUTION.estimatedEffort}</p>
            </div>
            <div>
              <p className="mb-2 text-xs uppercase tracking-wider text-stone-500">Risks</p>
              <ul className="space-y-1 text-sm text-stone-300">
                {PROPOSED_SOLUTION.risks.map((risk) => (
                  <li key={risk} className="flex items-start gap-2">
                    <span className="mt-0.5 text-amber-500">•</span>
                    <span>{risk}</span>
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <div>
            <p className="mb-2 text-xs uppercase tracking-wider text-stone-500">Proposed Changes</p>
            <div className="space-y-2">
              {PROPOSED_SOLUTION.changes.map((change) => (
                <div
                  key={change.file}
                  className="flex items-center justify-between rounded border border-stone-800 bg-stone-950 px-3 py-2"
                >
                  <div className="flex min-w-0 items-center gap-3">
                    <span className={`rounded px-2 py-0.5 text-xs font-mono ${changeTypeClass(change.type)}`}>
                      {change.type}
                    </span>
                    <span className="truncate text-sm font-mono text-stone-300">{change.file}</span>
                  </div>
                  <span className="whitespace-nowrap text-xs font-mono text-stone-500">{change.lines}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
          <button
            type="button"
            aria-label="Approve Solution"
            className="flex items-center justify-center gap-2 rounded-md bg-lime-600 px-4 py-2 font-medium text-white transition-colors hover:bg-lime-700"
          >
            <span aria-hidden="true">+</span>
            <span>Approve Solution</span>
          </button>
          <button
            type="button"
            aria-label="Request Changes"
            className="flex items-center justify-center gap-2 rounded-md bg-rose-600 px-4 py-2 font-medium text-white transition-colors hover:bg-rose-700"
          >
            <span aria-hidden="true">-</span>
            <span>Request Changes</span>
          </button>
          <NavLink
            to="/review/rate-limiting-docs"
            className="flex items-center justify-center gap-2 rounded-md bg-amber-600 px-4 py-2 font-medium text-white transition-colors hover:bg-amber-700"
          >
            <span>▣</span>
            <span>Review Documentation</span>
          </NavLink>
          <button
            type="button"
            className="rounded-md bg-stone-800 px-4 py-2 font-medium text-stone-300 transition-colors hover:bg-stone-700"
          >
            Ask Agent for Clarification
          </button>
        </div>
      </section>

      <div className="grid grid-cols-1 gap-4 md:gap-6 lg:grid-cols-3">
        <section className="space-y-6 lg:col-span-2">
          <section className="rounded-lg border border-stone-800 bg-stone-900">
            <div className="border-b border-stone-800 px-6 py-4">
              <h2 className="font-semibold text-stone-100">Discussion</h2>
            </div>
            <div className="space-y-4 p-6">
              {COMMENTS.map((comment) => (
                <div key={comment.id} className="flex gap-3">
                  <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-stone-700 text-xs text-stone-300">
                    {comment.authorType === "user" ? "U" : "A"}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="mb-1 flex items-center gap-2">
                      <span className="font-medium text-stone-200">{comment.author}</span>
                      <span className="text-xs text-stone-600">{comment.timestamp}</span>
                    </div>
                    <p className="text-sm leading-relaxed text-stone-300">{comment.content}</p>
                  </div>
                </div>
              ))}
            </div>
            <div className="border-t border-stone-800 px-6 py-4">
              <div className="flex gap-3">
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-stone-700 text-xs font-mono text-stone-300">
                  JS
                </div>
                <div className="flex-1">
                  <textarea
                    className="h-20 w-full resize-none rounded-lg border border-stone-700 bg-stone-950 px-4 py-3 text-sm text-stone-200 placeholder:text-stone-600 focus:border-amber-500/50 focus:outline-none focus:ring-2 focus:ring-amber-500/40"
                    placeholder="Add a comment..."
                    readOnly
                    value=""
                  />
                  <div className="mt-2 flex items-center justify-end">
                    <button
                      type="button"
                      className="rounded-md bg-amber-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-amber-700"
                    >
                      Comment
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </section>
        </section>

        <aside className="space-y-6 lg:col-span-1">
          <section className="rounded-lg border border-stone-800 bg-stone-900">
            <div className="border-b border-stone-800 px-6 py-4">
              <h2 className="font-semibold text-stone-100">Timeline</h2>
            </div>
            <div className="p-6">
              <div className="space-y-3">
                {TIMELINE.map((item, index) => (
                  <div key={`${item.event}-${item.time}`} className="flex gap-3">
                    <div className="relative">
                      <div className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-amber-500" />
                      {index !== TIMELINE.length - 1 ? (
                        <div className="absolute left-1 top-3 h-full w-px bg-stone-800" aria-hidden="true" />
                      ) : null}
                    </div>
                    <div className="min-w-0 flex-1 pb-3">
                      <p className="text-sm text-stone-300">{item.event}</p>
                      <p className="mt-0.5 text-xs text-stone-600">{item.user} • {item.time}</p>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </section>

          <section className="rounded-lg border border-stone-800 bg-stone-900">
            <div className="border-b border-stone-800 px-6 py-4">
              <h2 className="font-semibold text-stone-100">Related Issues</h2>
            </div>
            <div className="space-y-3 p-6">
              {RELATED_ISSUES.map((issue) => (
                <div key={issue.id} className="flex items-start gap-2">
                  <span
                    className={`mt-0.5 rounded px-2 py-0.5 text-xs font-medium ${
                      issue.relation === "blocks" ? "bg-rose-500/10 text-rose-400" : "bg-stone-700 text-stone-400"
                    }`}
                  >
                    {issue.relation}
                  </span>
                  <div className="min-w-0 flex-1">
                    <NavLink to={`/issue/${encodeURIComponent(issue.id)}`} className="font-mono text-sm text-amber-400 hover:text-amber-300">
                      {issue.id}
                    </NavLink>
                    <p className="mt-0.5 text-sm text-stone-400">{issue.title}</p>
                  </div>
                </div>
              ))}
            </div>
          </section>

          <section className="rounded-lg border border-stone-800 bg-stone-900 p-6">
            <h2 className="mb-4 font-semibold text-stone-100">Details</h2>
            <div className="space-y-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-stone-500">Created</span>
                <span className="text-stone-300">{ISSUE.created}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-stone-500">Updated</span>
                <span className="text-stone-300">{ISSUE.updated}</span>
              </div>
            </div>
          </section>
        </aside>
      </div>
    </div>
  );
}
