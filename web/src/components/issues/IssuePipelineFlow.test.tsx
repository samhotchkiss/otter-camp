import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import IssuePipelineFlow from "./IssuePipelineFlow";
import { mapIssueStatusToPipeline } from "./pipelineStages";

describe("IssuePipelineFlow", () => {
  it("maps legacy and blocked statuses into canonical pipeline stages", () => {
    expect(mapIssueStatusToPipeline("ready_for_work")).toMatchObject({
      currentStage: "queued",
      blocked: false,
    });
    expect(mapIssueStatusToPipeline("planning")).toMatchObject({
      currentStage: "planning",
      blocked: false,
    });
    expect(mapIssueStatusToPipeline("blocked")).toMatchObject({
      currentStage: "in_progress",
      blocked: true,
    });
  });

  it("renders completed/current/future stage states for review", () => {
    render(<IssuePipelineFlow status="review" assigneeName="Sam" stageUpdatedAt="2026-02-08T20:00:00Z" />);

    expect(screen.getByTestId("pipeline-stage-queued")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("pipeline-stage-planning")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("pipeline-stage-in_progress")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("pipeline-stage-review")).toHaveAttribute("data-stage-state", "current");
    expect(screen.getByTestId("pipeline-stage-done")).toHaveAttribute("data-stage-state", "future");
    expect(screen.getByText("Sam")).toBeInTheDocument();
  });

  it("shows blocked detour badge when work status is blocked", () => {
    render(<IssuePipelineFlow status="blocked" />);
    expect(screen.getByTestId("pipeline-stage-in_progress")).toHaveAttribute("data-blocked", "true");
    expect(screen.getByText("Blocked")).toBeInTheDocument();
  });

  it("calls onStageSelect when user clicks a stage", async () => {
    const onStageSelect = vi.fn();
    const user = userEvent.setup();
    render(<IssuePipelineFlow status="queued" onStageSelect={onStageSelect} />);

    await user.click(screen.getByTestId("pipeline-stage-review"));
    expect(onStageSelect).toHaveBeenCalledWith("review");
  });
});
