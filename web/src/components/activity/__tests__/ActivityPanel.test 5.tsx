import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ActivityPanel from "../ActivityPanel";
import { useWS } from "../../../contexts/WebSocketContext";

vi.mock("../../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(),
}));

const mockFetch = vi.fn();
global.fetch = mockFetch;

const ORG_ID = "00000000-0000-0000-0000-000000000000";

describe("ActivityPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("renders recent activity and filters by type, agent, and date", async () => {
    const wsState = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(),
    };
    vi.mocked(useWS).mockImplementation(() => wsState);

    localStorage.setItem("otter-camp-org-id", ORG_ID);

    const now = new Date("2026-02-03T12:00:00.000Z");
    const items = [
      {
        id: "1",
        org_id: ORG_ID,
        type: "commit",
        created_at: new Date(now.getTime() - 5 * 60 * 1000).toISOString(),
        agent_name: "Frank the Agent",
        metadata: { repo: "otter-camp", message: "Fix activity panel", sha: "abc123" },
        summary: "Frank the Agent committed to otter-camp: \"Fix activity panel\"",
      },
      {
        id: "2",
        org_id: ORG_ID,
        type: "comment",
        created_at: new Date(now.getTime() - 2 * 24 * 60 * 60 * 1000).toISOString(),
        agent_name: "Jane Smith",
        task_title: "Wire up real-time feed updates",
        metadata: { text: "Looks good!" },
        summary: "Jane Smith commented on \"Wire up real-time feed updates\"",
      },
      {
        id: "3",
        org_id: ORG_ID,
        type: "task_status_changed",
        created_at: new Date(now.getTime() - 9 * 24 * 60 * 60 * 1000).toISOString(),
        agent_name: "System",
        task_title: "Ship v0.1",
        metadata: { previous_status: "in_progress", new_status: "done" },
        summary: "System changed task \"Ship v0.1\" to done",
      },
    ];

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ org_id: ORG_ID, items }),
    });

    render(<ActivityPanel />);

    await waitFor(() => {
      expect(screen.getByText(/committed to otter-camp/i)).toBeInTheDocument();
    });

    // Filter by type
    await userEvent.selectOptions(screen.getByLabelText("Type"), "commit");
    expect(screen.getByText(/committed to otter-camp/i)).toBeInTheDocument();
    expect(screen.queryByText(/commented on/i)).not.toBeInTheDocument();

    // Filter by agent
    await userEvent.selectOptions(screen.getByLabelText("Type"), "all");
    await userEvent.selectOptions(screen.getByLabelText("Agent"), "Jane Smith");
    expect(screen.queryByText(/committed to otter-camp/i)).not.toBeInTheDocument();
    expect(screen.getByText(/commented on/i)).toBeInTheDocument();

    // Filter by date (exclude Jane's comment, include only the recent commit)
    await userEvent.selectOptions(screen.getByLabelText("Agent"), "all");
    await userEvent.selectOptions(screen.getByLabelText("Type"), "all");
    await userEvent.clear(screen.getByLabelText("From"));
    await userEvent.type(screen.getByLabelText("From"), "2026-02-03");
    expect(screen.getByText(/committed to otter-camp/i)).toBeInTheDocument();
    expect(screen.queryByText(/commented on/i)).not.toBeInTheDocument();
  });

  it("expands activity items to show metadata", async () => {
    const wsState = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(),
    };
    vi.mocked(useWS).mockImplementation(() => wsState);

    localStorage.setItem("otter-camp-org-id", ORG_ID);

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          org_id: ORG_ID,
          items: [
            {
              id: "1",
              org_id: ORG_ID,
              type: "commit",
              created_at: "2026-02-03T12:00:00.000Z",
              agent_name: "Frank the Agent",
              metadata: { repo: "otter-camp", message: "Add filters", sha: "deadbeef" },
              summary: "Frank the Agent committed to otter-camp: \"Add filters\"",
            },
          ],
        }),
    });

    render(<ActivityPanel />);

    const rowText = await screen.findByText(/committed to otter-camp/i);
    const rowButton = rowText.closest("button");
    expect(rowButton).toBeTruthy();

    await userEvent.click(rowButton!);
    expect(await screen.findByText("Metadata")).toBeInTheDocument();
    expect(screen.getAllByText(/deadbeef/).length).toBeGreaterThanOrEqual(1);
  });

  it("merges real-time FeedItemsAdded updates into the list", async () => {
    const wsState: {
      connected: boolean;
      lastMessage: unknown;
      sendMessage: ReturnType<typeof vi.fn>;
    } = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(),
    };
    vi.mocked(useWS).mockImplementation(() => wsState as any);

    localStorage.setItem("otter-camp-org-id", ORG_ID);

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
          org_id: ORG_ID,
          items: [
            {
              id: "1",
              org_id: ORG_ID,
              type: "commit",
              created_at: "2026-02-03T12:00:00.000Z",
              agent_id: "agent-1",
              agent_name: "Frank the Agent",
              metadata: { repo: "otter-camp", message: "Initial", sha: "111" },
            },
          ],
        }),
    });

    const { rerender } = render(<ActivityPanel className="ws-seed-a" />);

    await screen.findByText(/committed to otter-camp/i);

    wsState.lastMessage = {
      type: "FeedItemsAdded",
      data: {
        type: "FeedItemsAdded",
        items: [
          {
            id: "2",
            org_id: ORG_ID,
            agent_id: "agent-1",
            type: "commit",
            created_at: "2026-02-03T12:01:00.000Z",
            metadata: { repo: "otter-camp", message: "Live update", sha: "222" },
          },
        ],
      },
    };

    rerender(<ActivityPanel className="ws-seed-b" />);

    expect(await screen.findByText(/live update/i)).toBeInTheDocument();
  });

  it("shows an empty state when there is no activity", async () => {
    const wsState = {
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(),
    };
    vi.mocked(useWS).mockImplementation(() => wsState);

    localStorage.setItem("otter-camp-org-id", ORG_ID);

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ org_id: ORG_ID, items: [] }),
    });

    render(<ActivityPanel />);

    expect(await screen.findByText(/no activity yet/i)).toBeInTheDocument();
  });
});
