import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Emission } from "../hooks/useEmissions";
import EmissionTicker from "./EmissionTicker";

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

describe("EmissionTicker", () => {
  it("renders newest emissions first and applies the limit", () => {
    render(
      <EmissionTicker
        limit={2}
        emissions={[
          makeEmission("old", "2026-02-08T12:00:00Z", "old"),
          makeEmission("newest", "2026-02-08T12:02:00Z", "newest"),
          makeEmission("middle", "2026-02-08T12:01:00Z", "middle"),
        ]}
      />,
    );

    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("newest");
    expect(items[1]).toHaveTextContent("middle");
    expect(screen.queryByText("old")).not.toBeInTheDocument();
  });

  it("applies optional scope filters", () => {
    render(
      <EmissionTicker
        projectId="project-a"
        emissions={[
          makeEmission("match", "2026-02-08T12:02:00Z", "keep", {
            scope: { project_id: "project-a" },
          }),
          makeEmission("drop", "2026-02-08T12:03:00Z", "drop", {
            scope: { project_id: "project-b" },
          }),
        ]}
      />,
    );

    expect(screen.getByText("keep")).toBeInTheDocument();
    expect(screen.queryByText("drop")).not.toBeInTheDocument();
  });

  it("renders empty fallback text", () => {
    render(<EmissionTicker emissions={[]} emptyText="No live emissions yet" />);
    expect(screen.getByText("No live emissions yet")).toBeInTheDocument();
  });
});
