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
      expect(screen.getByRole("region", { name: /camp tasks/i })).toBeInTheDocument();
    });

    it("renders all columns", () => {
      render(<KanbanBoard />);
      expect(screen.getByText("To Do")).toBeInTheDocument();
      expect(screen.getByText("In Progress")).toBeInTheDocument();
      expect(screen.getByText("Done")).toBeInTheDocument();
    });

    it("renders column emojis", () => {
      render(<KanbanBoard />);
      expect(screen.getByText("📋")).toBeInTheDocument();
      expect(screen.getByText("🚀")).toBeInTheDocument();
      expect(screen.getByText("✅")).toBeInTheDocument();
    });

    it("renders initial tasks", () => {
      render(<KanbanBoard />);
      expect(screen.getByText("Set up camp perimeter")).toBeInTheDocument();
      expect(screen.getByText("Gather firewood")).toBeInTheDocument();
      expect(screen.getByText("Check supplies")).toBeInTheDocument();
      expect(screen.getByText("Set up tents")).toBeInTheDocument();
    });

    it("displays task counts per column", () => {
      render(<KanbanBoard />);
      expect(screen.getByText("2", { selector: "#column-todo-count" })).toBeInTheDocument();
      expect(screen.getByText("1", { selector: "#column-in-progress-count" })).toBeInTheDocument();
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

    it("renders one SortableContext per column", () => {
      render(<KanbanBoard />);
      const sortableContexts = screen.getAllByTestId("sortable-context");
      expect(sortableContexts.length).toBe(3);
    });
  });

  describe("task cards", () => {
    it("renders task cards as keyboard-focusable listitems", () => {
      render(<KanbanBoard />);
      const taskCards = screen.getAllByRole("listitem");
      expect(taskCards.length).toBeGreaterThanOrEqual(4);
      taskCards.forEach((card) => {
        expect(card).toHaveAttribute("tabindex", "0");
      });
    });
  });

  describe("API integration", () => {
    it("does not call update API without a drag action", () => {
      render(<KanbanBoard />);
      expect(mockFetch).not.toHaveBeenCalled();
    });

    it("continues to render when API rejects", () => {
      const consoleSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      render(<KanbanBoard />);
      expect(screen.getByRole("region", { name: /camp tasks/i })).toBeInTheDocument();

      consoleSpy.mockRestore();
    });
  });

  describe("accessibility", () => {
    it("uses semantic headings for columns", () => {
      render(<KanbanBoard />);
      const columnHeadings = screen.getAllByRole("heading", { level: 3 });
      expect(columnHeadings.length).toBe(3);
    });
  });

  describe("layout", () => {
    it("contains sortable contexts inside the dnd wrapper", () => {
      render(<KanbanBoard />);
      const dndContext = screen.getByTestId("dnd-context");
      const sortableContexts = within(dndContext).getAllByTestId("sortable-context");
      expect(sortableContexts.length).toBe(3);
    });
  });
});

describe("KanbanBoard Task Types", () => {
  it("correctly types Task interface", () => {
    const task: Task = {
      id: "test-1",
      title: "Test Task",
      status: "todo",
      priority: "medium",
      createdAt: new Date().toISOString(),
    };

    expect(task.id).toBe("test-1");
    expect(task.title).toBe("Test Task");
    expect(task.status).toBe("todo");
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
    const validStatuses: TaskStatus[] = ["todo", "in-progress", "done"];

    validStatuses.forEach((status) => {
      expect(["todo", "in-progress", "done"]).toContain(status);
    });
  });
});
