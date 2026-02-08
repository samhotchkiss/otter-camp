import type { Emission } from "../hooks/useEmissions";
import LiveTimestamp from "./LiveTimestamp";
import { selectEmissions, truncateEmissionSummary } from "./emissionUtils";

type EmissionTickerProps = {
  emissions: Emission[];
  projectId?: string;
  issueId?: string;
  sourceId?: string;
  limit?: number;
  emptyText?: string;
  className?: string;
};

export default function EmissionTicker({
  emissions,
  projectId,
  issueId,
  sourceId,
  limit = 5,
  emptyText = "No live emissions",
  className,
}: EmissionTickerProps) {
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
      <ul
        style={{
          display: "flex",
          gap: "0.5rem",
          overflowX: "auto",
          margin: 0,
          padding: 0,
          listStyle: "none",
        }}
      >
        {visible.map((emission) => (
          <li
            key={emission.id}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "0.35rem",
              whiteSpace: "nowrap",
              border: "1px solid rgba(148, 163, 184, 0.35)",
              borderRadius: "999px",
              padding: "0.25rem 0.6rem",
              fontSize: "0.85rem",
            }}
          >
            <strong>{emission.source_id}</strong>
            <span>{truncateEmissionSummary(emission.summary, 80)}</span>
            <span style={{ opacity: 0.8 }}>
              <LiveTimestamp timestamp={emission.timestamp} />
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
