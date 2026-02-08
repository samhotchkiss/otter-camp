import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

function mockJSONResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function stubFetchForDataTests() {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/api/orgs")) {
      return mockJSONResponse({
        orgs: [
          { id: "org-123", name: "Otter Camp HQ", slug: "otter-camp-hq" },
        ],
      });
    }
    if (url.includes("/api/agents")) {
      return mockJSONResponse({
        agents: [
          { id: "agent-1", name: "Frank", role: "Chief of Staff", avatar: "🤖" },
          { id: "agent-2", name: "Ivy", role: "Product", avatar: "🤖" },
        ],
      });
    }
    return mockJSONResponse({ error: "unexpected endpoint" }, 500);
  });
  vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);
  return fetchMock;
}

function stubFetchForThemeTests() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => mockJSONResponse({})),
  );
}

describe("SettingsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    document.documentElement.classList.remove("dark", "light");
  });

  it("loads profile/workspace/integrations from authenticated context + APIs", async () => {
    const fetchMock = stubFetchForDataTests();

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

describe("SettingsPage theme behavior", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
    document.documentElement.classList.remove("dark", "light");
    stubFetchForDataTests();

    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: vi.fn().mockImplementation(() => ({
        matches: false,
        media: "(prefers-color-scheme: dark)",
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  it("respects system preference (light when matchMedia prefers light)", async () => {
    render(<SettingsPage />);

    // matchMedia.matches is false (prefers-color-scheme: dark = false => light)
    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(false);
    });
  });

  it("toggles dark class when switching between dark and light appearance", async () => {
    const user = userEvent.setup();
    render(<SettingsPage />);

    // System defaults to light (matchMedia.matches = false)
    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(false);
    });

    await user.click(await screen.findByRole("button", { name: /dark/i }));
    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(true);
    });

    await user.click(screen.getByRole("button", { name: /light/i }));
    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(false);
    });
  });
});
