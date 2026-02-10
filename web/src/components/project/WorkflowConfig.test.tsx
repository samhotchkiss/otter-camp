import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import WorkflowConfig, { defaultWorkflowConfigState } from "./WorkflowConfig";

function renderWorkflowConfig(onChange = vi.fn()) {
  function Harness() {
    const [value, setValue] = useState(defaultWorkflowConfigState());
    return (
      <WorkflowConfig
        value={value}
        onChange={(next) => {
          onChange(next);
          setValue(next);
        }}
        agents={[
          { id: "agent-1", name: "Frank" },
          { id: "agent-2", name: "Derek" },
        ]}
      />
    );
  }

  render(<Harness />);
}

describe("WorkflowConfig", () => {
  it("updates workflow enabled toggle", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWorkflowConfig(onChange);

    await user.click(screen.getByLabelText("Workflow enabled"));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ enabled: true }));
  });

  it("renders cron schedule inputs by default", () => {
    renderWorkflowConfig();
    expect(screen.getByLabelText("Workflow cron expression")).toBeInTheDocument();
    expect(screen.getByLabelText("Workflow timezone")).toBeInTheDocument();
    expect(screen.queryByLabelText("Workflow every milliseconds")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Workflow run at")).not.toBeInTheDocument();
  });

  it("shows everyMs input when schedule type is every", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWorkflowConfig(onChange);

    await user.selectOptions(screen.getByLabelText("Workflow schedule type"), "every");

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ scheduleKind: "every" }));
    expect(screen.getByLabelText("Workflow every milliseconds")).toBeInTheDocument();
    expect(screen.queryByLabelText("Workflow cron expression")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Workflow timezone")).not.toBeInTheDocument();
  });

  it("shows run-at input when schedule type is at", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWorkflowConfig(onChange);

    await user.selectOptions(screen.getByLabelText("Workflow schedule type"), "at");

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ scheduleKind: "at" }));
    expect(screen.getByLabelText("Workflow run at")).toBeInTheDocument();
    expect(screen.queryByLabelText("Workflow cron expression")).not.toBeInTheDocument();
  });

  it("updates selected workflow agent", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWorkflowConfig(onChange);

    await user.selectOptions(screen.getByLabelText("Workflow agent"), "agent-2");

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ workflowAgentID: "agent-2" }));
  });

  it("updates issue template text fields", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWorkflowConfig(onChange);

    await user.clear(screen.getByLabelText("Workflow issue title pattern"));
    await user.type(screen.getByLabelText("Workflow issue title pattern"), "Morning Briefing - {{date}}");
    await user.clear(screen.getByLabelText("Workflow issue body"));
    await user.type(screen.getByLabelText("Workflow issue body"), "Summarize inbox and calendar");
    await user.clear(screen.getByLabelText("Workflow issue labels"));
    await user.type(screen.getByLabelText("Workflow issue labels"), "automated,briefing");

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ titlePattern: expect.stringContaining("Morning Briefing") }),
    );
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ body: "Summarize inbox and calendar" }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ labels: "automated,briefing" }));
  });

  it("updates priority, pipeline, and auto-close template controls", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWorkflowConfig(onChange);

    await user.selectOptions(screen.getByLabelText("Workflow issue priority"), "P0");
    await user.selectOptions(screen.getByLabelText("Workflow pipeline"), "standard");
    await user.click(screen.getByLabelText("Workflow auto close"));

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ priority: "P0" }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ pipeline: "standard" }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ autoClose: false }));
  });
});
