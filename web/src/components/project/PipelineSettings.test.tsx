import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PipelineSettings from "./PipelineSettings";

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    headers: {
      get: () => "application/json",
    },
    json: async () => body,
  } as Response;
}

describe("PipelineSettings", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads current role assignments and shows manual option", async () => {
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        planner: { agentId: "agent-1" },
        worker: { agentId: null },
        reviewer: { agentId: "agent-2" },
      }),
    );

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[
          { id: "agent-1", name: "Agent One" },
          { id: "agent-2", name: "Agent Two" },
        ]}
        initialRequireHumanReview={true}
      />,
    );

    expect(await screen.findByLabelText("Planner role")).toHaveValue("agent-1");
    expect(screen.getByLabelText("Worker role")).toHaveValue("");
    expect(screen.getByLabelText("Reviewer role")).toHaveValue("agent-2");
    expect(screen.getByRole("checkbox", { name: /require human approval before merge/i })).toBeChecked();
    expect(screen.getAllByRole("option", { name: "Manual (no agent)" }).length).toBeGreaterThan(0);
  });

  it("shows error when initial load fails", async () => {
    fetchMock.mockResolvedValueOnce(mockJSONResponse({ error: "pipeline load failed" }, false));

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[]}
        initialRequireHumanReview={false}
      />,
    );

    expect(await screen.findByText("pipeline load failed")).toBeInTheDocument();
  });

  it("submits role updates and human review toggle", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: "agent-2" },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          require_human_review: true,
        }),
      );

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[
          { id: "agent-1", name: "Agent One" },
          { id: "agent-2", name: "Agent Two" },
        ]}
        initialRequireHumanReview={false}
      />,
    );

    await screen.findByLabelText("Planner role");
    fireEvent.change(screen.getByLabelText("Planner role"), { target: { value: "agent-2" } });
    fireEvent.change(screen.getByLabelText("Worker role"), { target: { value: "" } });
    await user.click(screen.getByRole("checkbox", { name: /require human approval before merge/i }));
    await user.click(screen.getByRole("button", { name: "Save pipeline settings" }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(3);
    });

    const putCall = fetchMock.mock.calls[1]!;
    expect(String(putCall[0])).toContain("/api/projects/project-1/pipeline-roles");
    expect(putCall[1]).toMatchObject({ method: "PUT" });
    expect(JSON.parse(String(putCall[1]?.body))).toEqual({
      planner: { agentId: "agent-2" },
      worker: { agentId: null },
      reviewer: { agentId: null },
    });

    const patchCall = fetchMock.mock.calls[2]!;
    expect(String(patchCall[0])).toContain("/api/projects/project-1");
    expect(patchCall[1]).toMatchObject({ method: "PATCH" });
    expect(JSON.parse(String(patchCall[1]?.body))).toEqual({ require_human_review: true });

    expect(await screen.findByText("Pipeline settings saved.")).toBeInTheDocument();
  });

  it("calls onRequireHumanReviewSaved after successful save", async () => {
    const user = userEvent.setup();
    const onRequireHumanReviewSaved = vi.fn();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          require_human_review: true,
        }),
      );

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[]}
        initialRequireHumanReview={false}
        onRequireHumanReviewSaved={onRequireHumanReviewSaved}
      />,
    );

    await screen.findByLabelText("Planner role");
    await user.click(screen.getByRole("checkbox", { name: /require human approval before merge/i }));
    await user.click(screen.getByRole("button", { name: "Save pipeline settings" }));

    await waitFor(() => {
      expect(onRequireHumanReviewSaved).toHaveBeenCalledWith(true);
    });
  });

  it("shows API error when save fails", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ error: "bad role payload" }, false));

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[{ id: "agent-1", name: "Agent One" }]}
        initialRequireHumanReview={false}
      />,
    );

    await screen.findByLabelText("Planner role");
    await user.click(screen.getByRole("button", { name: "Save pipeline settings" }));

    expect(await screen.findByText("bad role payload")).toBeInTheDocument();
  });

  it("shows partial save error when roles save succeeds but human review update fails", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ error: "toggle failed" }, false));

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[{ id: "agent-1", name: "Agent One" }]}
        initialRequireHumanReview={false}
      />,
    );

    await screen.findByLabelText("Planner role");
    await user.click(screen.getByRole("button", { name: "Save pipeline settings" }));

    expect(
      await screen.findByText("Roles saved, but failed to update human review setting."),
    ).toBeInTheDocument();
  });

  it("clears success message when form values change after save", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          planner: { agentId: null },
          worker: { agentId: null },
          reviewer: { agentId: null },
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          require_human_review: false,
        }),
      );

    render(
      <PipelineSettings
        projectId="project-1"
        agents={[{ id: "agent-1", name: "Agent One" }]}
        initialRequireHumanReview={false}
      />,
    );

    await screen.findByLabelText("Planner role");
    await user.click(screen.getByRole("button", { name: "Save pipeline settings" }));
    expect(await screen.findByText("Pipeline settings saved.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Planner role"), { target: { value: "agent-1" } });
    expect(screen.queryByText("Pipeline settings saved.")).not.toBeInTheDocument();
  });

  it("URL-encodes project id in API paths", async () => {
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        planner: { agentId: null },
        worker: { agentId: null },
        reviewer: { agentId: null },
      }),
    );

    render(
      <PipelineSettings
        projectId="project /settings"
        agents={[]}
        initialRequireHumanReview={false}
      />,
    );

    await screen.findByLabelText("Planner role");
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain("/api/projects/project%20%2Fsettings/pipeline-roles");
  });
});
