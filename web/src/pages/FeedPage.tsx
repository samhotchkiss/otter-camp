import { useMemo, useState } from "react";
import ActivityPanel from "../components/ActivityPanel";
import EmissionStream from "../components/EmissionStream";
import useEmissions from "../hooks/useEmissions";

export default function FeedPage() {
  const { emissions } = useEmissions({ limit: 100 });
  const [kindFilter, setKindFilter] = useState<string>("all");
  const [sourceFilter, setSourceFilter] = useState<string>("all");

  const sourceOptions = useMemo(() => {
    const values = new Set<string>();
    for (const emission of emissions) {
      if (emission.source_id) {
        values.add(emission.source_id);
      }
    }
    return [...values].sort((a, b) => a.localeCompare(b));
  }, [emissions]);

  const filteredEmissions = useMemo(() => {
    return emissions.filter((emission) => {
      if (kindFilter !== "all" && emission.kind !== kindFilter) {
        return false;
      }
      if (sourceFilter !== "all" && emission.source_id !== sourceFilter) {
        return false;
      }
      return true;
    });
  }, [emissions, kindFilter, sourceFilter]);

  return (
    <div className="w-full space-y-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-[var(--text)]">
          Activity Feed
        </h1>
        <p className="mt-1 text-sm text-[var(--text-muted)]">
          Real-time updates from across your projects and agents
        </p>
      </div>

      <section className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <div className="mb-3 flex flex-wrap items-end gap-3">
          <div>
            <label className="block text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]" htmlFor="feed-kind-filter">
              Emission Kind
            </label>
            <select
              id="feed-kind-filter"
              className="mt-1 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-2 py-1 text-sm"
              value={kindFilter}
              onChange={(event) => setKindFilter(event.target.value)}
            >
              <option value="all">All</option>
              <option value="status">Status</option>
              <option value="progress">Progress</option>
              <option value="log">Log</option>
              <option value="milestone">Milestone</option>
              <option value="error">Error</option>
            </select>
          </div>
          <div>
            <label className="block text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]" htmlFor="feed-source-filter">
              Source
            </label>
            <select
              id="feed-source-filter"
              className="mt-1 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-2 py-1 text-sm"
              value={sourceFilter}
              onChange={(event) => setSourceFilter(event.target.value)}
            >
              <option value="all">All</option>
              {sourceOptions.map((sourceID) => (
                <option key={sourceID} value={sourceID}>
                  {sourceID}
                </option>
              ))}
            </select>
          </div>
        </div>
        <EmissionStream emissions={filteredEmissions} limit={20} emptyText="No live emissions in this filter set" />
      </section>

      <ActivityPanel className="min-h-[70vh]" />
    </div>
  );
}
