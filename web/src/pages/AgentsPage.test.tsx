import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentsPage from "./AgentsPage";
import useEmissions from "../hooks/useEmissions";

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: () => ({
    getVirtualItems: () => [{ index: 0, key: "row-0", size: 236, start: 0 }],
    getTotalSize: () => 236,
  }),
}));

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => ({
    connected: true,
    lastMessage: null,
    sendMessage: vi.fn(),
  }),
}));

vi.mock("../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => ({
    openConversation: vi.fn(),
  }),
}));

vi.mock("../hooks/useEmissions", () => ({
  default: vi.fn(),
}));

describe("AgentsPage", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      json: async () => ({
        agents: [
          {
            id: "agent-1",
            name: "Agent One",
            status: "online",
            role: "Worker",
          },
        ],
      }),
    } as Response);

    vi.mocked(useEmissions).mockReturnValue({
      emissions: [],
      latestBySource: new Map([
        [
          "agent-1",
          {
            id: "em-1",
            source_type: "agent",
            source_id: "agent-1",
            kind: "progress",
            summary: "Completed 3/7 tasks",
            timestamp: new Date().toISOString(),
          },
        ],
      ]),
      loading: false,
      error: null,
      refresh: vi.fn(),
    });
  });

  it("renders latest emission snippet and working indicator on agent cards", async () => {
    render(<AgentsPage apiEndpoint="/api/test-agents" />);

    expect(await screen.findByText("Agent One")).toBeInTheDocument();
    expect(screen.getByText("Completed 3/7 tasks")).toBeInTheDocument();
    expect(screen.getByText(/agent is working/i)).toBeInTheDocument();
  });
});
