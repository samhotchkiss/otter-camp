import type { Emission } from "../hooks/useEmissions";
import LiveTimestamp from "./LiveTimestamp";
import { selectEmissions } from "./emissionUtils";

type EmissionStreamProps = {
  emissions: Emission[];
  projectId?: string;
  issueId?: string;
  sourceId?: string;
  limit?: number;
  emptyText?: string;
  className?: string;
};

const kindToLabel = (kind: string): string => {
  switch (kind) {
    case "status":
      return "Status";
    case "progress":
      return "Progress";
    case "milestone":
      return "Milestone";
    case "error":
      return "Error";
    case "log":
      return "Log";
    default:
      return "Update";
  }
};

const progressLabel = (emission: Emission): string | null => {
  if (!emission.progress) {
    return null;
  }
  const base = `${emission.progress.current}/${emission.progress.total}`;
  if (emission.progress.unit) {
    return `${base} ${emission.progress.unit}`;
  }
  return base;
};

export default function EmissionStream({
  emissions,
  projectId,
  issueId,
  sourceId,
  limit = 15,
  emptyText = "No emissions yet",
  className,
}: EmissionStreamProps) {
  const visible = selectEmissions(
    emissions,
    { projectId, issueId, sourceId },
    limit,
  );

  if (visible.length === 0) {
    return (
      <div className={className}>
        <p>{emptyText}</p>
      </div>
    );
  }

  return (
    <div className={className}>
      <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: "0.5rem" }}>
        {visible.map((emission) => {
          const progress = progressLabel(emission);
          return (
            <li
              key={emission.id}
              style={{
                border: "1px solid rgba(148, 163, 184, 0.35)",
                borderRadius: "0.5rem",
                padding: "0.6rem 0.75rem",
              }}
            >
              <div style={{ display: "flex", justifyContent: "space-between", gap: "0.75rem" }}>
                <strong>{kindToLabel(emission.kind)}</strong>
                <span style={{ opacity: 0.85 }}>
                  <LiveTimestamp timestamp={emission.timestamp} />
                </span>
              </div>
              <div style={{ marginTop: "0.25rem" }}>{emission.summary}</div>
              <div style={{ marginTop: "0.3rem", fontSize: "0.85rem", opacity: 0.8 }}>
                <span>{emission.source_id}</span>
                {progress ? <span> · {progress}</span> : null}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
