import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import PipelineMiniProgress from "./PipelineMiniProgress";

describe("PipelineMiniProgress", () => {
  it("renders stage progress for done issues", () => {
    render(<PipelineMiniProgress status="done" />);

    expect(screen.getByTestId("mini-stage-queued")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("mini-stage-planning")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("mini-stage-in_progress")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("mini-stage-review")).toHaveAttribute("data-stage-state", "completed");
    expect(screen.getByTestId("mini-stage-done")).toHaveAttribute("data-stage-state", "current");
  });

  it("maps blocked status to in-progress stage and shows warning marker", () => {
    render(<PipelineMiniProgress status="blocked" />);

    expect(screen.getByTestId("mini-stage-in_progress")).toHaveAttribute("data-stage-state", "current");
    expect(screen.getByTestId("mini-stage-in_progress")).toHaveAttribute("data-blocked", "true");
  });
});
