import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import FeedPage from "./FeedPage";
import useEmissions from "../hooks/useEmissions";

vi.mock("../hooks/useEmissions", () => ({
  default: vi.fn(),
}));

vi.mock("../components/ActivityPanel", () => ({
  default: () => <div data-testid="activity-panel-mock" />,
}));

describe("FeedPage", () => {
  beforeEach(() => {
    vi.mocked(useEmissions).mockReturnValue({
      emissions: [
        {
          id: "em-1",
          source_type: "agent",
          source_id: "agent-a",
          kind: "progress",
          summary: "Agent A progressed task",
          timestamp: "2026-02-08T12:00:00Z",
        },
        {
          id: "em-2",
          source_type: "agent",
          source_id: "agent-b",
          kind: "error",
          summary: "Agent B hit an error",
          timestamp: "2026-02-08T11:59:00Z",
        },
      ],
      latestBySource: new Map(),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });
  });

  it("renders emission stream and filters by kind/source", async () => {
    const user = userEvent.setup();

    render(<FeedPage />);

    expect(screen.getByText("Agent A progressed task")).toBeInTheDocument();
    expect(screen.getByText("Agent B hit an error")).toBeInTheDocument();
    expect(screen.getByTestId("activity-panel-mock")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Emission Kind"), "progress");
    expect(screen.getByText("Agent A progressed task")).toBeInTheDocument();
    expect(screen.queryByText("Agent B hit an error")).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Source"), "agent-b");
    expect(screen.getByText("No live emissions in this filter set")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Emission Kind"), "all");
    expect(screen.getByText("Agent B hit an error")).toBeInTheDocument();
    expect(screen.queryByText("Agent A progressed task")).not.toBeInTheDocument();
  });
});
