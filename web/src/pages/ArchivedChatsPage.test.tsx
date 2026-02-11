import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ArchivedChatsPage from "./ArchivedChatsPage";

type ChatPayload = {
  id: string;
  thread_type: "dm" | "project" | "issue";
  thread_key: string;
  title: string;
  last_message_preview: string;
  last_message_at: string;
  auto_archived_reason?: string;
};

function makeResponse(chats: ChatPayload[]) {
  return {
    ok: true,
    json: async () => ({ chats }),
  };
}

function renderArchivedChatsPage() {
  render(
    <MemoryRouter initialEntries={["/chats/archived"]}>
      <ArchivedChatsPage />
    </MemoryRouter>,
  );
}

describe("ArchivedChatsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    window.localStorage.setItem("otter-camp-org-id", "org-1");
    window.localStorage.setItem("otter_camp_token", "test-token");
  });

  it("loads archived chats on mount", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/chats?")) {
        return makeResponse([
          {
            id: "chat-1",
            thread_type: "issue",
            thread_key: "issue:issue-1",
            title: "Closed issue thread",
            last_message_preview: "Looks good",
            last_message_at: "2026-02-10T10:00:00Z",
            auto_archived_reason: "issue_closed",
          },
        ]);
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    renderArchivedChatsPage();

    expect(await screen.findByText("Closed issue thread")).toBeInTheDocument();

    await waitFor(() => {
      const firstRequest = String(fetchMock.mock.calls[0]?.[0] ?? "");
      expect(firstRequest).toContain("/api/chats?");
      expect(firstRequest).toContain("archived=true");
      expect(firstRequest).toContain("org_id=org-1");
    });
  });

  it("passes query text to archived chat search", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const requestURL = new URL(String(input));
      if (requestURL.pathname.endsWith("/api/chats")) {
        const query = (requestURL.searchParams.get("q") ?? "").trim().toLowerCase();
        if (query === "ops") {
          return makeResponse([
            {
              id: "chat-2",
              thread_type: "project",
              thread_key: "project:project-2",
              title: "Ops incident recap",
              last_message_preview: "Postmortem done",
              last_message_at: "2026-02-10T11:00:00Z",
            },
          ]);
        }
        return makeResponse([
          {
            id: "chat-1",
            thread_type: "project",
            thread_key: "project:project-1",
            title: "Design standup",
            last_message_preview: "Need follow-up",
            last_message_at: "2026-02-10T10:00:00Z",
          },
        ]);
      }
      throw new Error(`unexpected url: ${String(input)}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    renderArchivedChatsPage();

    expect(await screen.findByText("Design standup")).toBeInTheDocument();

    await user.type(screen.getByRole("searchbox", { name: "Search archived chats" }), "ops");

    await waitFor(() => {
      expect(screen.getByText("Ops incident recap")).toBeInTheDocument();
    });
    expect(screen.queryByText("Design standup")).not.toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(([input]) => String(input).includes("q=ops")),
    ).toBe(true);
  });

  it("unarchives a chat and removes it from the archived list", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/api/chats?")) {
        return makeResponse([
          {
            id: "chat-archive-1",
            thread_type: "project",
            thread_key: "project:project-1",
            title: "Archived planning thread",
            last_message_preview: "Final notes",
            last_message_at: "2026-02-10T10:00:00Z",
          },
        ]);
      }
      if (url.includes("/api/chats/chat-archive-1/unarchive")) {
        return {
          ok: init?.method === "POST",
          json: async () => ({
            chat: {
              id: "chat-archive-1",
            },
          }),
        };
      }
      throw new Error(`unexpected url: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    renderArchivedChatsPage();

    expect(await screen.findByText("Archived planning thread")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Unarchive" }));

    await waitFor(() => {
      expect(screen.getByText("No archived chats found.")).toBeInTheDocument();
    });

    expect(
      fetchMock.mock.calls.some(
        ([input, init]) =>
          String(input).includes("/api/chats/chat-archive-1/unarchive") &&
          (init as RequestInit | undefined)?.method === "POST",
      ),
    ).toBe(true);
  });
});
