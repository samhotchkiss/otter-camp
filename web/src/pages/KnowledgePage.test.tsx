import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import KnowledgePage from "./KnowledgePage";

const {
  adminAgentsMock,
  memoryEventsMock,
  knowledgeMock,
  taxonomyNodesMock,
  memoryEntriesMock,
  adminAgentMemoryFilesMock,
  taxonomyNodeMemoriesMock,
} = vi.hoisted(() => ({
  adminAgentsMock: vi.fn(),
  memoryEventsMock: vi.fn(),
  knowledgeMock: vi.fn(),
  taxonomyNodesMock: vi.fn(),
  memoryEntriesMock: vi.fn(),
  adminAgentMemoryFilesMock: vi.fn(),
  taxonomyNodeMemoriesMock: vi.fn(),
}));

vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return {
    ...actual,
    default: {
      ...actual.default,
      adminAgents: adminAgentsMock,
      memoryEvents: memoryEventsMock,
      knowledge: knowledgeMock,
      taxonomyNodes: taxonomyNodesMock,
      memoryEntries: memoryEntriesMock,
      adminAgentMemoryFiles: adminAgentMemoryFilesMock,
      taxonomyNodeMemories: taxonomyNodeMemoriesMock,
    },
  };
});

describe("KnowledgePage", () => {
  beforeEach(() => {
    adminAgentsMock.mockReset();
    memoryEventsMock.mockReset();
    knowledgeMock.mockReset();
    taxonomyNodesMock.mockReset();
    memoryEntriesMock.mockReset();
    adminAgentMemoryFilesMock.mockReset();
    taxonomyNodeMemoriesMock.mockReset();

    adminAgentsMock.mockResolvedValue({
      agents: [
        {
          id: "elephant",
          workspace_agent_id: "00000000-0000-0000-0000-000000000001",
          name: "Ellie",
          status: "online",
          is_ephemeral: false,
        },
      ],
      total: 1,
    });
    memoryEventsMock.mockResolvedValue({
      items: [
        {
          id: 1,
          event_type: "knowledge.shared",
          created_at: "2026-02-19T10:00:00Z",
          payload: {
            source_agent_id: "00000000-0000-0000-0000-000000000001",
            title: "Auth flow requires OTP fallback",
            quality_score: 0.93,
          },
        },
        {
          id: 2,
          event_type: "memory.created",
          created_at: "2026-02-19T09:40:00Z",
          payload: {
            agent_id: "00000000-0000-0000-0000-000000000001",
            title: "Captured deployment rollback lesson",
          },
        },
      ],
      total: 2,
    });
    knowledgeMock.mockResolvedValue({
      items: [
        {
          id: "k-1",
          title: "Customer Portal - Authentication Flow",
          content: "Use OTP fallback with rate-limited retries",
          tags: ["auth", "customer-portal"],
          created_by: "Ellie",
          updated_at: "2026-02-19T09:58:00Z",
          created_at: "2026-02-19T09:50:00Z",
        },
      ],
      total: 1,
    });
    taxonomyNodesMock.mockResolvedValue({
      nodes: [
        {
          id: "node-1",
          org_id: "org-1",
          parent_id: null,
          slug: "technical-preferences",
          display_name: "Technical Preferences",
          depth: 0,
        },
      ],
    });
    memoryEntriesMock.mockResolvedValue({
      items: [
        { id: "m-1", agent_id: "00000000-0000-0000-0000-000000000001", title: "One", content: "One", kind: "fact" },
        { id: "m-2", agent_id: "00000000-0000-0000-0000-000000000001", title: "Two", content: "Two", kind: "fact" },
      ],
      total: 2,
    });
    adminAgentMemoryFilesMock.mockResolvedValue({
      ref: "main",
      path: "/memory",
      entries: [
        { name: "2026-02-18.md", type: "file", path: "2026-02-18.md" },
      ],
    });
    taxonomyNodeMemoriesMock.mockResolvedValue({
      memories: [{ memory_id: "m-1", kind: "fact", title: "One", content: "One" }],
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders live memory system sections from API data", async () => {
    render(<KnowledgePage />);

    expect(screen.getByRole("heading", { name: "Memory System" })).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Search memory graph...")).toBeInTheDocument();

    expect(await screen.findByText("Customer Portal - Authentication Flow")).toBeInTheDocument();
    expect(screen.getByText("Technical Preferences")).toBeInTheDocument();
    expect(screen.getByText(/Auth flow requires OTP fallback/)).toBeInTheDocument();
    expect(screen.getByText("Vector Embeddings")).toBeInTheDocument();
    expect(screen.getByText("Entity Syntheses")).toBeInTheDocument();

    expect(screen.queryByText("Customer Portal requires mobile-first responsive design")).not.toBeInTheDocument();
  });

  it("shows endpoint failure and retries", async () => {
    adminAgentsMock
      .mockRejectedValueOnce(new Error("agents failed"))
      .mockResolvedValueOnce({
        agents: [
          {
            id: "elephant",
            workspace_agent_id: "00000000-0000-0000-0000-000000000001",
            name: "Ellie",
            status: "online",
            is_ephemeral: false,
          },
        ],
        total: 1,
      });
    memoryEventsMock
      .mockRejectedValueOnce(new Error("events failed"))
      .mockResolvedValueOnce({ items: [], total: 0 });
    knowledgeMock
      .mockRejectedValueOnce(new Error("knowledge failed"))
      .mockResolvedValueOnce({ items: [], total: 0 });
    taxonomyNodesMock
      .mockRejectedValueOnce(new Error("taxonomy failed"))
      .mockResolvedValueOnce({ nodes: [] });

    render(<KnowledgePage />);

    expect(await screen.findByText("Unable to load memory system endpoints")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Memory System")).toBeInTheDocument();
  });
});
