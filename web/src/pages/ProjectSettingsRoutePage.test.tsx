import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import ProjectSettingsRoutePage from "./ProjectSettingsRoutePage";

vi.mock("./project/ProjectSettingsPage", () => ({
  default: (props: {
    projectID: string;
    selectedPrimaryAgentID: string;
    initialRequireHumanReview: boolean;
    generalError?: string | null;
    generalSuccess?: string | null;
    onPrimaryAgentChange: (value: string) => void;
    onSaveGeneralSettings: () => void;
  }) => (
    <div>
      <div data-testid="settings-project-id">{props.projectID}</div>
      <div data-testid="settings-primary">{props.selectedPrimaryAgentID}</div>
      <div data-testid="settings-review">{String(props.initialRequireHumanReview)}</div>
      <div data-testid="settings-error">{props.generalError ?? ""}</div>
      <div data-testid="settings-success">{props.generalSuccess ?? ""}</div>
      <button type="button" onClick={() => props.onPrimaryAgentChange("agent-2")}>
        Choose agent-2
      </button>
      <button type="button" onClick={props.onSaveGeneralSettings}>
        Save general
      </button>
    </div>
  ),
}));

function renderRoute(initialEntry = "/projects/project-1/settings") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route path="/projects/:id/settings" element={<ProjectSettingsRoutePage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ProjectSettingsRoutePage", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-1");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("loads project settings and saves primary agent selection", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1/settings") && init?.method === "PATCH") {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              id: "project-1",
              name: "Technonymous",
              primary_agent_id: "agent-2",
              require_human_review: true,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/api/projects/project-1")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              id: "project-1",
              name: "Technonymous",
              primary_agent_id: "agent-1",
              require_human_review: true,
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      if (url.includes("/api/agents")) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              agents: [
                { id: "agent-1", name: "Agent One" },
                { id: "agent-2", name: "Agent Two" },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return Promise.resolve(new Response(JSON.stringify({ error: "not found" }), { status: 404 }));
    });

    const user = userEvent.setup();
    renderRoute();

    expect(await screen.findByRole("heading", { name: "Project Settings" })).toBeInTheDocument();
    expect(screen.getByTestId("settings-project-id")).toHaveTextContent("project-1");
    expect(screen.getByTestId("settings-primary")).toHaveTextContent("agent-1");
    expect(screen.getByTestId("settings-review")).toHaveTextContent("true");

    await user.click(screen.getByRole("button", { name: "Choose agent-2" }));
    await user.click(screen.getByRole("button", { name: "Save general" }));

    await waitFor(() => {
      expect(screen.getByTestId("settings-success")).toHaveTextContent("Project settings saved.");
    });

    const patchCall = fetchMock.mock.calls.find((call) => {
      const url = String(call[0]);
      const method = (call[1] as RequestInit | undefined)?.method;
      return url.includes("/api/projects/project-1/settings") && method === "PATCH";
    });
    expect(patchCall).toBeDefined();
    expect((patchCall?.[1] as RequestInit)?.body).toBe(
      JSON.stringify({ primary_agent_id: "agent-2" }),
    );
  });
});
