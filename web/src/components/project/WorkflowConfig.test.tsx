import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import WorkflowConfig, { defaultWorkflowConfigState } from "./WorkflowConfig";

describe("WorkflowConfig", () => {
  it("updates schedule kind and workflow enabled toggle", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const value = defaultWorkflowConfigState();

    render(
      <WorkflowConfig
        value={value}
        onChange={onChange}
        agents={[
          { id: "agent-1", name: "Frank" },
          { id: "agent-2", name: "Derek" },
        ]}
      />,
    );

    await user.click(screen.getByLabelText("Workflow enabled"));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ enabled: true }));

    await user.selectOptions(screen.getByLabelText("Workflow schedule type"), "every");
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ scheduleKind: "every" }));
  });
});
