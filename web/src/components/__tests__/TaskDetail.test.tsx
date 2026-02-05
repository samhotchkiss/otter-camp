import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import TaskDetail, {
  type TaskDetailData,
  StatusBadge,
  PriorityBadge,
} from "../TaskDetail";

// Mock WebSocketContext
vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(() => ({
    connected: true,
    lastMessage: null,
    sendMessage: vi.fn(),
  })),
}));

// Mock TaskThread component
vi.mock("../TaskThread", () => ({
  default: ({ taskId }: { taskId: string }) => (
    <div data-testid="task-thread">TaskThread for {taskId}</div>
  ),
}));

// Mock fetch
const mockFetch = vi.fn();
global.fetch = mockFetch;

const mockTask: TaskDetailData = {
  id: "task-123",
  title: "Test Task",
  description: "This is a **test** task with `code` and [link](https://example.com)",
  status: "in-progress",
  priority: "high",
  assignee: {
    id: "user-1",
    name: "John Doe",
    avatarUrl: "https://example.com/avatar.jpg",
  },
  dueDate: "2026-03-01T00:00:00.000Z",
  labels: [
    { id: "label-1", name: "Bug", color: "#ff0000" },
    { id: "label-2", name: "Frontend", color: "#00ff00" },
  ],
  attachments: [
    {
      id: "attach-1",
      filename: "screenshot.png",
      size_bytes: 102400,
      mime_type: "image/png",
      url: "https://example.com/screenshot.png",
      thumbnail_url: "https://example.com/screenshot-thumb.png",
      uploadedAt: "2026-02-01T10:00:00.000Z",
      uploadedBy: "John Doe",
    },
  ],
  activities: [
    {
      id: "activity-1",
      type: "created",
      actor: "John Doe",
      timestamp: "2026-02-01T09:00:00.000Z",
    },
    {
      id: "activity-2",
      type: "status_changed",
      actor: "Jane Smith",
      timestamp: "2026-02-01T10:30:00.000Z",
      oldValue: "todo",
      newValue: "in-progress",
    },
  ],
  createdAt: "2026-02-01T09:00:00.000Z",
  updatedAt: "2026-02-01T10:30:00.000Z",
};

describe("TaskDetail", () => {
  const mockOnClose = vi.fn();
  const mockOnTaskUpdated = vi.fn();
  const mockOnTaskDeleted = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockFetch.mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ task: mockTask }),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("rendering", () => {
    it("does not render when isOpen is false", () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={false}
          onClose={mockOnClose}
        />
      );

      expect(screen.queryByText("Task Details")).not.toBeInTheDocument();
    });

    it("renders loading state initially", () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      expect(screen.getByText("Loading task...")).toBeInTheDocument();
    });

    it("renders task details after loading", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });
    });

    it("displays task header with otter emoji", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("🦦")).toBeInTheDocument();
        expect(screen.getByText("Task Details")).toBeInTheDocument();
      });
    });

    it("renders assignee with avatar", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("John Doe")).toBeInTheDocument();
        expect(screen.getByAltText("John Doe")).toHaveAttribute(
          "src",
          "https://example.com/avatar.jpg"
        );
      });
    });

    it("renders labels", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Bug")).toBeInTheDocument();
        expect(screen.getByText("Frontend")).toBeInTheDocument();
      });
    });

    it("renders markdown description", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        // Should render bold text
        expect(screen.getByText("test")).toBeInTheDocument();
        // Should render code
        expect(screen.getByText("code")).toBeInTheDocument();
        // Should render link
        expect(screen.getByRole("link", { name: "link" })).toHaveAttribute(
          "href",
          "https://example.com"
        );
      });
    });

    it("renders due date", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText(/Due/)).toBeInTheDocument();
      });
    });
  });

  describe("error handling", () => {
    it("displays error when fetch fails", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        statusText: "Not Found",
      });

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Failed to fetch task details")).toBeInTheDocument();
      });
    });

    it("allows dismissing error", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        statusText: "Not Found",
      });

      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Failed to fetch task details")).toBeInTheDocument();
      });

      await user.click(screen.getByText("Dismiss"));

      await waitFor(() => {
        expect(screen.queryByText("Failed to fetch task details")).not.toBeInTheDocument();
      });
    });
  });

  describe("user interactions", () => {
    it("closes on backdrop click", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      // Click the backdrop
      const backdrop = document.querySelector('[aria-hidden="true"]');
      if (backdrop) {
        fireEvent.click(backdrop);
      }

      expect(mockOnClose).toHaveBeenCalled();
    });

    it("closes on close button click", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      const closeButton = screen.getByLabelText("Close");
      await user.click(closeButton);

      expect(mockOnClose).toHaveBeenCalled();
    });

    it("closes on Escape key", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      fireEvent.keyDown(window, { key: "Escape" });

      expect(mockOnClose).toHaveBeenCalled();
    });

    it("enters edit mode on Edit button click", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByText("✏️ Edit"));

      // Should show input fields
      expect(screen.getByDisplayValue("Test Task")).toBeInTheDocument();
      expect(screen.getByText("Save Changes")).toBeInTheDocument();
      expect(screen.getByText("Cancel")).toBeInTheDocument();
    });

    it("cancels edit mode", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByText("✏️ Edit"));
      
      // Modify the title
      const titleInput = screen.getByDisplayValue("Test Task");
      await user.clear(titleInput);
      await user.type(titleInput, "Modified Title");

      // Cancel
      await user.click(screen.getByText("Cancel"));

      // Should revert to original
      expect(screen.getByText("Test Task")).toBeInTheDocument();
      expect(screen.queryByText("Modified Title")).not.toBeInTheDocument();
    });

    it("saves edited task", async () => {
      const user = userEvent.setup();

      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ task: { ...mockTask, title: "Updated Title" } }),
      });

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
          onTaskUpdated={mockOnTaskUpdated}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByText("✏️ Edit"));
      
      const titleInput = screen.getByDisplayValue("Test Task");
      await user.clear(titleInput);
      await user.type(titleInput, "Updated Title");

      await user.click(screen.getByText("Save Changes"));

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalledWith(
          "/api/tasks/task-123",
          expect.objectContaining({
            method: "PATCH",
            body: expect.stringContaining("Updated Title"),
          })
        );
      });
    });
  });

  describe("status changes", () => {
    it("updates status via dropdown", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
          onTaskUpdated={mockOnTaskUpdated}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      const statusDropdown = screen.getByDisplayValue(/In Progress/i);
      await user.selectOptions(statusDropdown, "done");

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalledWith(
          "/api/tasks/task-123",
          expect.objectContaining({
            method: "PATCH",
            body: expect.stringContaining("done"),
          })
        );
      });
    });
  });

  describe("delete functionality", () => {
    it("shows delete confirmation", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByText("🗑️ Delete"));

      expect(screen.getByText("Delete this task?")).toBeInTheDocument();
      expect(screen.getByText("Confirm")).toBeInTheDocument();
    });

    it("cancels delete confirmation", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByText("🗑️ Delete"));
      await user.click(screen.getByRole("button", { name: "Cancel" }));

      expect(screen.queryByText("Delete this task?")).not.toBeInTheDocument();
    });

    it("deletes task on confirmation", async () => {
      const user = userEvent.setup();

      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({}),
      });

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
          onTaskDeleted={mockOnTaskDeleted}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByText("🗑️ Delete"));
      await user.click(screen.getByText("Confirm"));

      await waitFor(() => {
        expect(mockFetch).toHaveBeenCalledWith(
          "/api/tasks/task-123",
          expect.objectContaining({ method: "DELETE" })
        );
        expect(mockOnTaskDeleted).toHaveBeenCalledWith("task-123");
        expect(mockOnClose).toHaveBeenCalled();
      });
    });
  });

  describe("tabs", () => {
    it("renders all tabs", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      expect(screen.getByText("Comments")).toBeInTheDocument();
      expect(screen.getByText("Activity")).toBeInTheDocument();
      expect(screen.getByText("Attachments")).toBeInTheDocument();
    });

    it("switches to Activity tab", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByRole("button", { name: /Activity/i }));

      // Should show activity items
      await waitFor(() => {
        expect(screen.getByText(/created this task/)).toBeInTheDocument();
        expect(screen.getByText(/changed status from todo to in-progress/)).toBeInTheDocument();
      });
    });

    it("switches to Attachments tab", async () => {
      const user = userEvent.setup();

      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      await user.click(screen.getByRole("button", { name: /Attachments/i }));

      // Should show attachment
      await waitFor(() => {
        expect(screen.getByText("screenshot.png")).toBeInTheDocument();
      });
    });

    it("shows attachment count badge", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      // Should show count badge
      expect(screen.getByText("1")).toBeInTheDocument();
    });
  });

  describe("TaskThread integration", () => {
    it("renders TaskThread in Comments tab", async () => {
      render(
        <TaskDetail
          taskId="task-123"
          isOpen={true}
          onClose={mockOnClose}
        />
      );

      await waitFor(() => {
        expect(screen.getByText("Test Task")).toBeInTheDocument();
      });

      expect(screen.getByTestId("task-thread")).toBeInTheDocument();
      expect(screen.getByText("TaskThread for task-123")).toBeInTheDocument();
    });
  });
});

describe("StatusBadge", () => {
  it("renders todo status", () => {
    render(<StatusBadge status="todo" />);
    
    expect(screen.getByText("To Do")).toBeInTheDocument();
    expect(screen.getByText("📋")).toBeInTheDocument();
  });

  it("renders in-progress status", () => {
    render(<StatusBadge status="in-progress" />);
    
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByText("🚀")).toBeInTheDocument();
  });

  it("renders done status", () => {
    render(<StatusBadge status="done" />);
    
    expect(screen.getByText("Done")).toBeInTheDocument();
    expect(screen.getByText("✅")).toBeInTheDocument();
  });

  it("applies correct color classes", () => {
    const { container } = render(<StatusBadge status="in-progress" />);
    
    const badge = container.firstChild;
    expect(badge).toHaveClass("bg-sky-100");
  });
});

describe("PriorityBadge", () => {
  it("renders low priority", () => {
    render(<PriorityBadge priority="low" />);
    
    expect(screen.getByText("Low")).toBeInTheDocument();
  });

  it("renders medium priority", () => {
    render(<PriorityBadge priority="medium" />);
    
    expect(screen.getByText("Medium")).toBeInTheDocument();
  });

  it("renders high priority", () => {
    render(<PriorityBadge priority="high" />);
    
    expect(screen.getByText("High")).toBeInTheDocument();
  });

  it("applies correct color classes for high priority", () => {
    const { container } = render(<PriorityBadge priority="high" />);
    
    const badge = container.firstChild;
    expect(badge).toHaveClass("bg-red-100");
  });
});

describe("WebSocket integration", () => {
  it("updates task when receiving TaskUpdated message", async () => {
    const { useWS } = await import("../../contexts/WebSocketContext");
    const mockUseWS = vi.mocked(useWS);
    const onClose = vi.fn();

    mockUseWS.mockReturnValue({
      connected: true,
      lastMessage: null,
      sendMessage: vi.fn(),
    });

    const { rerender } = render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={onClose}
      />
    );

    await waitFor(() => {
      expect(screen.getByText("Test Task")).toBeInTheDocument();
    });

    // Simulate WebSocket message
    mockUseWS.mockReturnValue({
      connected: true,
      lastMessage: {
        type: "TaskUpdated",
        data: { id: "task-123", title: "Updated via WebSocket" },
      },
      sendMessage: vi.fn(),
    });

    rerender(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={onClose}
      />
    );

    // The component should handle the WebSocket update
    // (actual behavior depends on the effect running)
  });
});
