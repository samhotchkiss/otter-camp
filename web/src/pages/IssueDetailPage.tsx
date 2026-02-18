import { Link, useParams } from "react-router-dom";
import IssueThreadPanel from "../components/project/IssueThreadPanel";

export default function IssueDetailPage() {
  const { id: projectId, issueId } = useParams<{ id?: string; issueId?: string }>();
  const resolvedIssueId = (issueId ?? "").trim();
  const resolvedProjectId = (projectId ?? "").trim();

  if (!resolvedIssueId) {
    return (
      <div data-testid="issue-detail-shell" className="mx-auto w-full max-w-[1280px]">
        <div className="rounded-2xl border border-red-500/40 bg-red-500/10 p-6 text-sm text-red-300">
          Missing issue id.
        </div>
      </div>
    );
  }

  return (
    <div data-testid="issue-detail-shell" className="mx-auto w-full max-w-[1280px] space-y-4">
      <nav className="flex items-center gap-2 text-sm text-[var(--text-muted)]">
        <Link to="/projects" className="hover:text-[var(--text)]">
          Projects
        </Link>
        {resolvedProjectId ? (
          <>
            <span>›</span>
            <Link to={`/projects/${encodeURIComponent(resolvedProjectId)}`} className="hover:text-[var(--text)]">
              Project
            </Link>
          </>
        ) : null}
        <span>›</span>
        <span className="font-medium text-[var(--text)]">Issue detail</span>
      </nav>

      <header className="rounded-3xl border border-[var(--border)] bg-[var(--surface)]/75 p-6 shadow-sm">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)]">Issue #{resolvedIssueId}</h1>
            <p className="mt-1 text-sm text-[var(--text-muted)]">
              Dedicated issue workflow surface with approval, discussion, and review context.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            {resolvedProjectId ? (
              <Link
                to={`/projects/${encodeURIComponent(resolvedProjectId)}`}
                className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--text)] hover:bg-[var(--surface-alt)]"
              >
                Back to Project
              </Link>
            ) : null}
            <Link
              to="/projects"
              className="rounded-lg border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 text-xs font-medium text-[var(--text)] hover:bg-[var(--surface-alt)]"
            >
              All Projects
            </Link>
          </div>
        </div>
      </header>

      <div className="grid gap-4 xl:grid-cols-[minmax(260px,320px)_1fr]">
        <aside className="rounded-2xl border border-[var(--border)] bg-[var(--surface)]/70 p-4">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-[var(--text-muted)]">Issue context</h2>
          <dl className="mt-3 space-y-2 text-sm">
            <div>
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">Issue ID</dt>
              <dd className="text-[var(--text)]">{resolvedIssueId}</dd>
            </div>
            <div>
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">Project scope</dt>
              <dd className="text-[var(--text)]">{resolvedProjectId || "Global alias route"}</dd>
            </div>
            <div>
              <dt className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">Controls</dt>
              <dd className="text-[var(--text-muted)]">Approval and review controls are available in the thread panel.</dd>
            </div>
          </dl>
        </aside>
        <IssueThreadPanel issueID={resolvedIssueId} projectID={resolvedProjectId || undefined} />
      </div>
    </div>
  );
}
