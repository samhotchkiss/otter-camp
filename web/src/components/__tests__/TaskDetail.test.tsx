import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import TaskDetail, {
  type TaskDetailData,
  StatusBadge,
  PriorityBadge,
} from "../TaskDetail";

const wsState: { lastMessage: unknown } = { lastMessage: null };

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(() => ({
    connected: true,
    lastMessage: wsState.lastMessage,
    sendMessage: vi.fn(),
  })),
}));

vi.mock("../TaskThread", () => ({
  default: ({ taskId }: { taskId: string }) => (
    <div data-testid="task-thread">TaskThread for {taskId}</div>
  ),
}));

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
  ],
  createdAt: "2026-02-01T09:00:00.000Z",
  updatedAt: "2026-02-01T10:30:00.000Z",
};

function mockFetchTask(task: TaskDetailData = mockTask) {
  mockFetch.mockResolvedValueOnce({
    ok: true,
    json: () => Promise.resolve({ task }),
  });
}

describe("TaskDetail", () => {
  const mockOnClose = vi.fn();
  const mockOnTaskUpdated = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    wsState.lastMessage = null;
  });

  it("does not render when closed", () => {
    render(<TaskDetail taskId="task-123" isOpen={false} onClose={mockOnClose} />);
    expect(screen.queryByText("Task Details")).not.toBeInTheDocument();
  });

  it("loads and renders task details", async () => {
    mockFetchTask();

    render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        apiEndpoint="/api/tasks"
      />,
    );

    expect(screen.getByText("Loading task...")).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText("Test Task")).toBeInTheDocument();
    });

    expect(screen.getByText("John Doe")).toBeInTheDocument();
    expect(screen.getByText("Bug")).toBeInTheDocument();
    expect(screen.getByTestId("task-thread")).toHaveTextContent("task-123");
  });

  it("uses local origin for the default task API endpoint", async () => {
    mockFetchTask();

    render(<TaskDetail taskId="task-123" isOpen={true} onClose={mockOnClose} />);

    await screen.findByText("Test Task");

    expect(mockFetch).toHaveBeenCalledWith(`${window.location.origin}/api/tasks/task-123`);
  });

  it("closes when close button is clicked", async () => {
    mockFetchTask();
    const user = userEvent.setup();

    render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        apiEndpoint="/api/tasks"
      />,
    );

    await screen.findByText("Test Task");
    await user.click(screen.getByRole("button", { name: /close task details/i }));
    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });

  it("enters edit mode and saves changes", async () => {
    mockFetchTask();
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ task: { ...mockTask, title: "Updated Title" } }),
    });

    const user = userEvent.setup();

    render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        onTaskUpdated={mockOnTaskUpdated}
        apiEndpoint="/api/tasks"
      />,
    );

    await screen.findByText("Test Task");
    await user.click(screen.getByRole("button", { name: /edit task/i }));

    const titleInput = screen.getByPlaceholderText("Task title");
    await user.clear(titleInput);
    await user.type(titleInput, "Updated Title");
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        "/api/tasks/task-123",
        expect.objectContaining({
          method: "PATCH",
          body: expect.stringContaining("Updated Title"),
        }),
      );
    });

    expect(mockOnTaskUpdated).toHaveBeenCalled();
  });

  it("updates status with optimistic PATCH call", async () => {
    mockFetchTask();
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ task: { ...mockTask, status: "done" } }),
    });

    render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        apiEndpoint="/api/tasks"
      />,
    );

    await screen.findByText("Test Task");

    const [statusSelect] = screen.getAllByRole("combobox");
    fireEvent.change(statusSelect, { target: { value: "done" } });

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        "/api/tasks/task-123",
        expect.objectContaining({
          method: "PATCH",
          body: expect.stringContaining('"status":"done"'),
        }),
      );
    });
  });

  it("shows dismissible error when task fetch fails", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, statusText: "Not Found" });
    const user = userEvent.setup();

    render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        apiEndpoint="/api/tasks"
      />,
    );

    expect(await screen.findByText("Failed to fetch task details")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() => {
      expect(screen.queryByText("Failed to fetch task details")).not.toBeInTheDocument();
    });
  });

  it("applies TaskUpdated websocket payloads for the active task", async () => {
    mockFetchTask();

    const { rerender } = render(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        apiEndpoint="/api/tasks"
      />,
    );

    await screen.findByText("Test Task");

    wsState.lastMessage = {
      type: "TaskUpdated",
      data: {
        id: "task-123",
        title: "Task Updated via WebSocket",
      },
    };

    rerender(
      <TaskDetail
        taskId="task-123"
        isOpen={true}
        onClose={mockOnClose}
        apiEndpoint="/api/tasks"
      />,
    );

    expect(await screen.findByText("Task Updated via WebSocket")).toBeInTheDocument();
  });
});

describe("TaskDetail badges", () => {
  it("renders StatusBadge", () => {
    render(<StatusBadge status="todo" />);
    expect(screen.getByText("To Do")).toBeInTheDocument();
  });

  it("renders PriorityBadge", () => {
    render(<PriorityBadge priority="high" />);
    expect(screen.getByText("High")).toBeInTheDocument();
  });
});
