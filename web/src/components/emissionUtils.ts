import type { Emission } from "../hooks/useEmissions";

type EmissionFilter = {
  projectId?: string;
  issueId?: string;
  sourceId?: string;
};

const toTimestamp = (value: string): number => {
  const parsed = new Date(value).getTime();
  if (Number.isNaN(parsed)) {
    return 0;
  }
  return parsed;
};

export function selectEmissions(
  emissions: Emission[],
  filter: EmissionFilter,
  limit: number,
): Emission[] {
  const projectID = (filter.projectId ?? "").trim();
  const issueID = (filter.issueId ?? "").trim();
  const sourceID = (filter.sourceId ?? "").trim();
  const normalizedLimit = Number.isFinite(limit) && limit > 0 ? Math.trunc(limit) : 5;

  return emissions
    .filter((emission) => {
      if (sourceID && emission.source_id !== sourceID) {
        return false;
      }
      if (projectID && emission.scope?.project_id !== projectID) {
        return false;
      }
      if (issueID && emission.scope?.issue_id !== issueID) {
        return false;
      }
      return true;
    })
    .sort((a, b) => toTimestamp(b.timestamp) - toTimestamp(a.timestamp))
    .slice(0, normalizedLimit);
}

export function truncateEmissionSummary(summary: string, maxLength: number): string {
  if (summary.length <= maxLength) {
    return summary;
  }
  if (maxLength <= 1) {
    return summary.slice(0, maxLength);
  }
  return `${summary.slice(0, maxLength - 1)}…`;
}
