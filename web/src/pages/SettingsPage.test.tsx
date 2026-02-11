import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import SettingsPage from "./SettingsPage";

vi.mock("../components/DataManagement", () => ({
  default: () => <div data-testid="data-management-mock" />,
}));

vi.mock("./settings/GitHubSettings", () => ({
  default: () => <div data-testid="github-settings-mock" />,
}));

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("SettingsPage label management", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  });

  it("loads seeded labels and supports create/edit/delete workflows", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({
          labels: [{ id: "label-bug", name: "bug", color: "#ef4444" }],
        });
      }
      if (url.includes("/api/labels?org_id=org-123") && init?.method === "POST") {
        return jsonResponse({ id: "label-feature", name: "feature", color: "#22c55e" }, 201);
      }
      if (url.includes("/api/labels/label-bug?org_id=org-123") && init?.method === "PATCH") {
        return jsonResponse({ id: "label-bug", name: "defect", color: "#dc2626" });
      }
      if (url.includes("/api/labels/label-feature?org_id=org-123") && init?.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);

    render(<SettingsPage />);

    expect(await screen.findByRole("heading", { name: "Label Management" })).toBeInTheDocument();
    expect(screen.getByTestId("label-name-label-bug")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("New label name"), {
      target: { value: "feature" },
    });
    fireEvent.change(screen.getByLabelText("New label color"), {
      target: { value: "#22c55e" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create Label" }));

    expect(await screen.findByTestId("label-row-label-feature")).toBeInTheDocument();

    fireEvent.change(screen.getByTestId("label-name-label-bug"), {
      target: { value: "defect" },
    });
    fireEvent.change(screen.getByTestId("label-color-label-bug"), {
      target: { value: "#dc2626" },
    });
    fireEvent.click(within(screen.getByTestId("label-row-label-bug")).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect((screen.getByTestId("label-name-label-bug") as HTMLInputElement).value).toBe("defect");
    });

    fireEvent.click(within(screen.getByTestId("label-row-label-feature")).getByRole("button", { name: "Delete" }));
    await waitFor(() => {
      expect(screen.queryByTestId("label-row-label-feature")).not.toBeInTheDocument();
    });

    expect(confirmSpy).toHaveBeenCalledWith(
      'Delete label "feature"? This will remove it from linked projects and issues.',
    );
    expect(
      fetchMock.mock.calls.some(([input]) =>
        String(input).includes("/api/labels?org_id=org-123&seed=true"),
      ),
    ).toBe(true);
    expect(
      fetchMock.mock.calls.some(([input, requestInit]) =>
        String(input).includes("/api/labels/label-bug?org_id=org-123") &&
        (requestInit as RequestInit | undefined)?.method === "PATCH",
      ),
    ).toBe(true);
    expect(
      fetchMock.mock.calls.some(([input, requestInit]) =>
        String(input).includes("/api/labels/label-feature?org_id=org-123") &&
        (requestInit as RequestInit | undefined)?.method === "DELETE",
      ),
    ).toBe(true);
  });

  it("loads git access tokens from API", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/git/tokens")) {
        return jsonResponse({
          tokens: [
            {
              id: "token-1",
              name: "Bootstrap token",
              token_prefix: "oc_git_abcd",
              created_at: "2026-02-11T18:00:00Z",
              projects: [],
            },
          ],
        });
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({ labels: [] });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);

    expect(await screen.findByRole("heading", { name: "Git Access Tokens" })).toBeInTheDocument();
    expect(screen.getByText("Bootstrap token")).toBeInTheDocument();
    expect(screen.getByText(/oc_git_abcd/)).toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).includes("/api/git/tokens")),
    ).toBe(true);
  });

  it("creates git token", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/git/tokens") && (!init?.method || init.method === "GET")) {
        return jsonResponse({ tokens: [] });
      }
      if (url.includes("/api/projects")) {
        return jsonResponse({
          projects: [{ id: "project-1", name: "Getting Started" }],
        });
      }
      if (url.includes("/api/git/tokens") && init?.method === "POST") {
        return jsonResponse({
          id: "token-1",
          name: "Git Token 1",
          token_prefix: "oc_git_new1",
          token: "oc_git_supersecret",
          projects: [{ project_id: "project-1", permission: "write" }],
          created_at: "2026-02-11T18:10:00Z",
        });
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({ labels: [] });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Generate Git Token" }));

    expect(await screen.findByText("Git Token 1")).toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(([input, requestInit]) =>
        String(input).includes("/api/git/tokens") &&
        (requestInit as RequestInit | undefined)?.method === "POST",
      ),
    ).toBe(true);
  });

  it("shows generated git token once", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/git/tokens") && (!init?.method || init.method === "GET")) {
        return jsonResponse({ tokens: [] });
      }
      if (url.includes("/api/projects")) {
        return jsonResponse({
          projects: [{ id: "project-1", name: "Getting Started" }],
        });
      }
      if (url.includes("/api/git/tokens") && init?.method === "POST") {
        return jsonResponse({
          id: "token-1",
          name: "Git Token 1",
          token_prefix: "oc_git_new1",
          token: "oc_git_supersecret",
          projects: [{ project_id: "project-1", permission: "write" }],
          created_at: "2026-02-11T18:10:00Z",
        });
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({ labels: [] });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Generate Git Token" }));

    expect(await screen.findByText("Copy this token now. It will only be shown once.")).toBeInTheDocument();
    expect(screen.getByText("oc_git_supersecret")).toBeInTheDocument();
  });

  it("shows project required message when no projects exist", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/git/tokens") && (!init?.method || init.method === "GET")) {
        return jsonResponse({ tokens: [] });
      }
      if (url.includes("/api/projects")) {
        return jsonResponse({ projects: [] });
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({ labels: [] });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Generate Git Token" }));

    expect(await screen.findByText("Create a project before generating a git token.")).toBeInTheDocument();
  });

  it("uses theme token classes for section shell and shared controls", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({ labels: [] });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);

    const profileCardHeading = await screen.findByRole("heading", { name: "Profile" });
    const profileCard = profileCardHeading.closest("section");
    expect(profileCard).not.toBeNull();
    expect(profileCard?.className).toContain("bg-[var(--surface)]");
    expect(profileCard?.className).toContain("border-[var(--border)]");

    const displayNameInput = screen.getByLabelText("Display Name");
    expect(displayNameInput.className).toContain("bg-[var(--surface)]");
    expect(displayNameInput.className).toContain("border-[var(--border)]");

    const inviteMemberButton = screen.getByRole("button", { name: "Invite Member" });
    expect(inviteMemberButton.className).toContain("bg-[var(--surface)]");
    expect(inviteMemberButton.className).toContain("border-[var(--border)]");
  });

  it("uses theme token classes for section-level row and appearance option containers", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/settings/")) {
        return jsonResponse({}, 500);
      }
      if (url.includes("/api/labels?org_id=org-123&seed=true")) {
        return jsonResponse({
          labels: [{ id: "label-bug", name: "bug", color: "#ef4444" }],
        });
      }
      return jsonResponse({ error: "unexpected request" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<SettingsPage />);

    expect(await screen.findByTestId("label-row-label-bug")).toBeInTheDocument();

    const labelNameInput = screen.getByTestId("label-name-label-bug");
    expect(labelNameInput.className).toContain("bg-[var(--surface)]");
    expect(labelNameInput.className).toContain("border-[var(--border)]");

    const lightThemeButton = screen.getByRole("button", { name: /Light/i });
    expect(lightThemeButton.className).toContain("bg-[var(--surface)]");
    expect(lightThemeButton.className).toContain("border-[var(--border)]");
  });
});
