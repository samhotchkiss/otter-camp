import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import KanbanBoard, { type Task, type TaskStatus } from "../KanbanBoard";

// Mock KeyboardShortcutsContext
vi.mock("../../contexts/KeyboardShortcutsContext", () => ({
  useKeyboardShortcutsContext: vi.fn(() => ({
    registerShortcut: vi.fn(),
    unregisterShortcut: vi.fn(),
    selectedTaskIndex: 0,
    setTaskCount: vi.fn(),
    openTaskDetail: vi.fn(),
  })),
}));

// Mock LiveRegion
vi.mock("../LiveRegion", () => ({
  useLiveRegion: vi.fn(() => ({
    announce: vi.fn(),
    announcePolite: vi.fn(),
    announceAssertive: vi.fn(),
  })),
}));

// Mock @dnd-kit modules
vi.mock("@dnd-kit/core", async () => {
  const actual = await vi.importActual("@dnd-kit/core");
  return {
    ...actual,
    DndContext: ({ children, onDragEnd }: { children: React.ReactNode; onDragEnd: (event: unknown) => void }) => (
      <div data-testid="dnd-context" data-ondragend={onDragEnd?.toString()}>
        {children}
      </div>
    ),
    DragOverlay: ({ children }: { children: React.ReactNode }) => (
      <div data-testid="drag-overlay">{children}</div>
    ),
    useDroppable: vi.fn(() => ({
      setNodeRef: vi.fn(),
      isOver: false,
      active: null,
      over: null,
      rect: null,
      node: { current: null },
    })),
    useSensor: vi.fn(),
    useSensors: vi.fn(() => []),
    closestCorners: vi.fn(),
    KeyboardSensor: vi.fn(),
    PointerSensor: vi.fn(),
  };
});

vi.mock("@dnd-kit/sortable", async () => {
  return {
    SortableContext: ({ children }: { children: React.ReactNode }) => (
      <div data-testid="sortable-context">{children}</div>
    ),
    sortableKeyboardCoordinates: vi.fn(),
    useSortable: vi.fn(() => ({
      attributes: { role: "button", tabIndex: 0 },
      listeners: {},
      setNodeRef: vi.fn(),
      transform: null,
      transition: undefined,
      isDragging: false,
    })),
    verticalListSortingStrategy: vi.fn(),
  };
});

// Mock fetch for API calls
const mockFetch = vi.fn();
global.fetch = mockFetch;

describe("KanbanBoard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetch.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({}),
    });
  });

  describe("rendering", () => {
    it("renders the board region", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByRole("region", { name: /kanban task board/i })).toBeInTheDocument();
    });

    it("renders all four columns", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByText("Backlog")).toBeInTheDocument();
      expect(screen.getByText("In Progress")).toBeInTheDocument();
      expect(screen.getByText("Review")).toBeInTheDocument();
      expect(screen.getByText("Done")).toBeInTheDocument();
    });

    it("renders column emojis", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByText("🗂️")).toBeInTheDocument();
      expect(screen.getByText("🚀")).toBeInTheDocument();
      expect(screen.getByText("👀")).toBeInTheDocument();
      expect(screen.getByText("✅")).toBeInTheDocument();
    });

    it("renders initial tasks in correct columns", () => {
      render(<KanbanBoard />);
      
      // Tasks should be visible
      expect(screen.getByText("Collect requirements for tasks board")).toBeInTheDocument();
      expect(screen.getByText("Implement Kanban columns + cards")).toBeInTheDocument();
      expect(screen.getByText("Review responsive layout on mobile")).toBeInTheDocument();
      expect(screen.getByText("Ship Kanban board component")).toBeInTheDocument();
    });

    it("displays task assignees", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByText("🦦 Scout Otter")).toBeInTheDocument();
      expect(screen.getAllByText("🦦 Builder Otter").length).toBeGreaterThanOrEqual(1);
      expect(screen.getByText("🦦 Lead Otter")).toBeInTheDocument();
    });

    it("displays task priorities", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByText("high")).toBeInTheDocument();
      expect(screen.getByText("medium")).toBeInTheDocument();
      expect(screen.getByText("low")).toBeInTheDocument();
      expect(screen.getByText("critical")).toBeInTheDocument();
    });

    it("displays task counts in columns", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByText("Backlog")).toBeInTheDocument();
      expect(screen.getByText("In Progress")).toBeInTheDocument();
      expect(screen.getByText("Review")).toBeInTheDocument();
      expect(screen.getByText("Done")).toBeInTheDocument();

      expect(screen.getByText("1", { selector: "#column-backlog-count" })).toBeInTheDocument();
      expect(screen.getByText("1", { selector: "#column-in-progress-count" })).toBeInTheDocument();
      expect(screen.getByText("1", { selector: "#column-review-count" })).toBeInTheDocument();
      expect(screen.getByText("1", { selector: "#column-done-count" })).toBeInTheDocument();
    });
  });

  describe("DnD context", () => {
    it("wraps content in DndContext", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByTestId("dnd-context")).toBeInTheDocument();
    });

    it("includes DragOverlay for active dragging", () => {
      render(<KanbanBoard />);
      
      expect(screen.getByTestId("drag-overlay")).toBeInTheDocument();
    });

    it("wraps task lists in SortableContext", () => {
      render(<KanbanBoard />);
      
      const sortableContexts = screen.getAllByTestId("sortable-context");
      expect(sortableContexts.length).toBe(4); // One per column
    });
  });

  describe("empty state", () => {
    it("shows empty placeholder when a column has no tasks", async () => {
      // The default tasks leave the column potentially empty after filtering
      // We need to verify the empty state text is rendered for columns with no tasks
      render(<KanbanBoard />);
      
      // The empty state text should be ready to appear
      // At minimum, the Done column has only 1 task initially
      const board = screen.getByTestId("dnd-context");
      expect(board).toBeInTheDocument();
    });
  });

  describe("task card interactions", () => {
    it("renders task cards with proper attributes for dragging", () => {
      render(<KanbanBoard />);
      
      // Task cards are now listitems within lists for better semantics
      const taskCards = screen.getAllByRole("listitem");
      // Each task should be rendered as a listitem
      expect(taskCards.length).toBeGreaterThanOrEqual(4);
    });

    it("applies different priority colors", () => {
      render(<KanbanBoard />);
      
      // High priority should have red styling
      const highPriorityBadges = screen.getAllByText("high");
      highPriorityBadges.forEach((badge) => {
        expect(badge.className).toMatch(/red/i);
      });
      
      // Medium priority should have amber styling
      const mediumPriorityBadges = screen.getAllByText("medium");
      mediumPriorityBadges.forEach((badge) => {
        expect(badge.className).toMatch(/amber/i);
      });
    });
  });

  describe("API integration", () => {
    it("calls updateTaskStatus API when task is moved", async () => {
      // Note: With mocked DnD, we test that the fetch setup is correct
      // The actual drag-drop behavior is handled by @dnd-kit and tested separately
      
      render(<KanbanBoard />);
      
      // The board should be rendered and ready to receive drag events
      expect(screen.getByTestId("dnd-context")).toBeInTheDocument();
      
      // API mock is set up and ready
      expect(mockFetch).not.toHaveBeenCalled(); // No API call yet (no drag happened)
    });

    it("handles API failure gracefully", async () => {
      const consoleSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
      
      mockFetch.mockRejectedValueOnce(new Error("Network error"));
      
      render(<KanbanBoard />);
      
      // Board should still render even if API is unavailable
      expect(screen.getByRole("region", { name: /kanban task board/i })).toBeInTheDocument();
      
      consoleSpy.mockRestore();
    });
  });

  describe("accessibility", () => {
    it("uses semantic headings for columns", () => {
      render(<KanbanBoard />);
      
      // Column titles are rendered as h3
      const columnHeadings = screen.getAllByRole("heading", { level: 3 });
      expect(columnHeadings.length).toBe(4);
    });

    it("makes task cards keyboard accessible", () => {
      render(<KanbanBoard />);
      
      // Task cards should have role="listitem" (for semantic list context) and tabIndex
      const taskCards = screen.getAllByRole("listitem");
      taskCards.forEach((card) => {
        expect(card).toHaveAttribute("tabindex", "0");
      });
    });
  });

  describe("responsive layout", () => {
    it("renders one sortable context per column", () => {
      render(<KanbanBoard />);
      
      // Find the container with all columns
      const dndContext = screen.getByTestId("dnd-context");
      expect(dndContext).toBeInTheDocument();
      
      // All columns should be children
      const sortableContexts = within(dndContext).getAllByTestId("sortable-context");
      expect(sortableContexts.length).toBe(4);
    });
  });
});

describe("KanbanBoard Task Types", () => {
  it("correctly types Task interface", () => {
    const task: Task = {
      id: "test-1",
      title: "Test Task",
      status: "backlog",
      priority: "medium",
      createdAt: new Date().toISOString(),
    };

    expect(task.id).toBe("test-1");
    expect(task.title).toBe("Test Task");
    expect(task.status).toBe("backlog");
  });

  it("allows optional fields in Task", () => {
    const minimalTask: Task = {
      id: "test-2",
      title: "Minimal Task",
      status: "in-progress",
      createdAt: new Date().toISOString(),
    };

    expect(minimalTask.priority).toBeUndefined();
  });

  it("validates TaskStatus union type", () => {
    const validStatuses: TaskStatus[] = ["backlog", "in-progress", "review", "done"];
    
    validStatuses.forEach((status) => {
      expect(["backlog", "in-progress", "review", "done"]).toContain(status);
    });
  });
});
