import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Emission } from "../hooks/useEmissions";
import EmissionStream from "./EmissionStream";

const makeEmission = (
  id: string,
  timestamp: string,
  summary: string,
  overrides: Partial<Emission> = {},
): Emission => ({
  id,
  source_type: "agent",
  source_id: "agent-1",
  kind: "status",
  summary,
  timestamp,
  ...overrides,
});

describe("EmissionStream", () => {
  it("renders a deterministic empty state", () => {
    render(<EmissionStream emissions={[]} emptyText="No activity" />);
    expect(screen.getByText("No activity")).toBeInTheDocument();
  });

  it("filters by issue/source and applies limit", () => {
    render(
      <EmissionStream
        issueId="issue-1"
        sourceId="agent-1"
        limit={1}
        emissions={[
          makeEmission("a", "2026-02-08T12:00:00Z", "drop issue", {
            scope: { issue_id: "issue-2" },
          }),
          makeEmission("b", "2026-02-08T12:02:00Z", "keep newest", {
            scope: { issue_id: "issue-1" },
          }),
          makeEmission("c", "2026-02-08T12:01:00Z", "drop by limit", {
            scope: { issue_id: "issue-1" },
          }),
        ]}
      />,
    );

    const rows = screen.getAllByRole("listitem");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveTextContent("keep newest");
  });

  it("shows progress metadata when available", () => {
    render(
      <EmissionStream
        emissions={[
          makeEmission("progress", "2026-02-08T12:00:00Z", "working", {
            kind: "progress",
            progress: { current: 3, total: 7, unit: "sub-issues" },
          }),
        ]}
      />,
    );

    expect(screen.getByText(/3\/7 sub-issues/i)).toBeInTheDocument();
  });
});
