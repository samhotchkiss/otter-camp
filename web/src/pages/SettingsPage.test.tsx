import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import SettingsPage from "./SettingsPage";

vi.mock("../contexts/AuthContext", () => ({
  useAuth: () => ({
    user: {
      id: "user-1",
      name: "Sam",
      email: "sam@otter.camp",
    },
  }),
}));

vi.mock("../components/DataManagement", () => ({
  default: () => <div data-testid="data-management" />,
}));

vi.mock("./settings/GitHubSettings", () => ({
  default: () => <div data-testid="github-settings" />,
}));

describe("SettingsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads profile/workspace/integrations from authenticated context + APIs", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/orgs")) {
        return new Response(
          JSON.stringify({
            orgs: [
              { id: "org-123", name: "Otter Camp HQ", slug: "otter-camp-hq" },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/api/agents")) {
        return new Response(
          JSON.stringify({
            agents: [
              { id: "agent-1", name: "Frank", role: "Chief of Staff", avatar: "🤖" },
              { id: "agent-2", name: "Ivy", role: "Product", avatar: "🤖" },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      return new Response(JSON.stringify({ error: "unexpected endpoint" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);

    await waitFor(() => {
      expect(screen.getByDisplayValue("Sam")).toBeInTheDocument();
    });

    expect(screen.getByDisplayValue("sam@otter.camp")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Otter Camp HQ")).toBeInTheDocument();
    expect(screen.getByText("Members (3)")).toBeInTheDocument();

    const workspaceSection = screen.getByText("Workspace").closest("section");
    expect(workspaceSection).not.toBeNull();
    if (workspaceSection) {
      const section = within(workspaceSection);
      expect(section.getByText("Sam")).toBeInTheDocument();
      expect(section.getByText("Frank")).toBeInTheDocument();
      expect(section.getByText("Ivy")).toBeInTheDocument();
    }

    expect(
      screen.getByDisplayValue("https://api.otter.camp/api/webhooks/openclaw")
    ).toBeInTheDocument();

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/orgs"),
      expect.anything()
    );
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/agents"),
      expect.anything()
    );
    expect(fetchMock).not.toHaveBeenCalledWith(
      expect.stringContaining("/api/settings/"),
      expect.anything()
    );
  });
});
