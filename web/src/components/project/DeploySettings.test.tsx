import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DeploySettings from "./DeploySettings";

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

describe("DeploySettings", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("loads deploy config and renders github fields", async () => {
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        deployMethod: "github_push",
        githubRepoUrl: "https://github.com/acme/otter",
        githubBranch: "release",
        cliCommand: null,
      }),
    );

    render(<DeploySettings projectId="project-1" />);

    expect(await screen.findByLabelText("Deploy method")).toHaveValue("github_push");
    expect(screen.getByLabelText("GitHub repo URL")).toHaveValue("https://github.com/acme/otter");
    expect(screen.getByLabelText("GitHub branch")).toHaveValue("release");
  });

  it("submits github push payload with default branch", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          deployMethod: "none",
          githubRepoUrl: null,
          githubBranch: "main",
          cliCommand: null,
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          deployMethod: "github_push",
          githubRepoUrl: "https://github.com/acme/otter",
          githubBranch: "main",
          cliCommand: null,
        }),
      );

    render(<DeploySettings projectId="project-1" />);
    await screen.findByLabelText("Deploy method");

    fireEvent.change(screen.getByLabelText("Deploy method"), { target: { value: "github_push" } });
    fireEvent.change(screen.getByLabelText("GitHub repo URL"), {
      target: { value: "https://github.com/acme/otter" },
    });
    fireEvent.change(screen.getByLabelText("GitHub branch"), { target: { value: "" } });

    await user.click(screen.getByRole("button", { name: "Save deployment settings" }));

    const putCall = fetchMock.mock.calls[1]!;
    expect(String(putCall[0])).toContain("/api/projects/project-1/deploy-config");
    expect(putCall[1]).toMatchObject({ method: "PUT" });
    expect(JSON.parse(String(putCall[1]?.body))).toEqual({
      deployMethod: "github_push",
      githubRepoUrl: "https://github.com/acme/otter",
      githubBranch: "main",
      cliCommand: null,
    });

    expect(await screen.findByText("Deployment settings saved.")).toBeInTheDocument();
  });

  it("validates cli command requirement for cli_command method", async () => {
    const user = userEvent.setup();
    fetchMock.mockResolvedValueOnce(
      mockJSONResponse({
        deployMethod: "none",
        githubRepoUrl: null,
        githubBranch: "main",
        cliCommand: null,
      }),
    );

    render(<DeploySettings projectId="project-1" />);
    await screen.findByLabelText("Deploy method");

    fireEvent.change(screen.getByLabelText("Deploy method"), { target: { value: "cli_command" } });
    await user.click(screen.getByRole("button", { name: "Save deployment settings" }));

    expect(await screen.findByText("CLI command is required for CLI command deploy mode.")).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  });
});
