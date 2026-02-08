import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsPage from "./SettingsPage";

vi.mock("./settings/GitHubSettings", () => ({
  default: () => <div data-testid="github-settings-mock">GitHub settings</div>,
}));

vi.mock("../components/DataManagement", () => ({
  default: () => <div data-testid="data-management-mock">Data management</div>,
}));

function mockJSONResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("SettingsPage theme behavior", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove("dark", "light");

    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/api/settings/profile")) {
          return mockJSONResponse({
            name: "Sam",
            email: "sam@example.com",
            avatarUrl: null,
          });
        }
        if (url.includes("/api/settings/notifications")) {
          return mockJSONResponse({
            taskAssigned: { email: true, push: true, inApp: true },
            taskCompleted: { email: false, push: true, inApp: true },
            mentions: { email: true, push: true, inApp: true },
            comments: { email: false, push: false, inApp: true },
            agentUpdates: { email: false, push: true, inApp: true },
            weeklyDigest: { email: true, push: false, inApp: false },
          });
        }
        if (url.includes("/api/settings/workspace")) {
          return mockJSONResponse({
            name: "Otter Camp",
            members: [],
          });
        }
        if (url.includes("/api/settings/integrations")) {
          return mockJSONResponse({
            openclawWebhookUrl: "",
            apiKeys: [],
          });
        }
        return mockJSONResponse({});
      }),
    );

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

  it("applies dark class by default for system theme", async () => {
    render(<SettingsPage />);

    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(true);
    });
  });

  it("toggles dark class when switching between light and dark appearance", async () => {
    const user = userEvent.setup();
    render(<SettingsPage />);

    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(true);
    });

    await user.click(await screen.findByRole("button", { name: /light/i }));
    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(false);
    });

    await user.click(screen.getByRole("button", { name: /dark/i }));
    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(true);
    });
  });
});
