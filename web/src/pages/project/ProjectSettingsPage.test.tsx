import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ProjectSettingsPage from "./ProjectSettingsPage";

vi.mock("../../components/project/PipelineSettings", () => ({
  default: ({ projectId }: { projectId: string }) => (
    <div data-testid="pipeline-settings-mock">Pipeline settings for {projectId}</div>
  ),
}));

vi.mock("../../components/project/DeploySettings", () => ({
  default: ({ projectId }: { projectId: string }) => (
    <div data-testid="deploy-settings-mock">Deploy settings for {projectId}</div>
  ),
}));

describe("ProjectSettingsPage", () => {
  it("renders general settings controls and pipeline section", () => {
    const onPrimaryAgentChange = vi.fn();
    const onSaveGeneralSettings = vi.fn();

    render(
      <ProjectSettingsPage
        projectID="project-1"
        availableAgents={[
          { id: "agent-1", name: "Agent One" },
          { id: "agent-2", name: "Agent Two" },
        ]}
        selectedPrimaryAgentID="agent-1"
        onPrimaryAgentChange={onPrimaryAgentChange}
        onSaveGeneralSettings={onSaveGeneralSettings}
        isSavingGeneralSettings={false}
        initialRequireHumanReview={true}
      />,
    );

    expect(screen.getByText("General")).toBeInTheDocument();
    expect(screen.getByLabelText("Primary Agent")).toHaveValue("agent-1");
    expect(screen.getByTestId("pipeline-settings-mock")).toHaveTextContent(
      "Pipeline settings for project-1",
    );
    expect(screen.getByTestId("deploy-settings-mock")).toHaveTextContent(
      "Deploy settings for project-1",
    );

    fireEvent.change(screen.getByLabelText("Primary Agent"), { target: { value: "agent-2" } });
    expect(onPrimaryAgentChange).toHaveBeenCalledWith("agent-2");

    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(onSaveGeneralSettings).toHaveBeenCalledTimes(1);
  });

  it("shows settings feedback and saving state", () => {
    render(
      <ProjectSettingsPage
        projectID="project-1"
        availableAgents={[]}
        selectedPrimaryAgentID=""
        onPrimaryAgentChange={() => {}}
        onSaveGeneralSettings={() => {}}
        isSavingGeneralSettings={true}
        generalError="general save failed"
        generalSuccess="general save succeeded"
        initialRequireHumanReview={false}
      />,
    );

    expect(screen.getByText("general save failed")).toBeInTheDocument();
    expect(screen.getByText("general save succeeded")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Saving..." })).toBeDisabled();
  });
});
