import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import TaskDetailPage from "../TaskDetailPage";
import IssueDetailPage from "../IssueDetailPage";

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: () => ({
    connected: false,
    lastMessage: null,
    sendMessage: vi.fn(() => true),
  }),
  useOptionalWS: () => null,
}));

const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: vi.fn((key: string) => store[key] || null),
    setItem: vi.fn((key: string, value: string) => {
      store[key] = value;
    }),
    removeItem: vi.fn((key: string) => {
      delete store[key];
    }),
    clear: vi.fn(() => {
      store = {};
    }),
  };
})();

Object.defineProperty(window, "localStorage", {
  value: localStorageMock,
});

function renderTaskDetail(pathname: string) {
  const router = createMemoryRouter(
    [
      {
        path: "/tasks/:taskId",
        element: <TaskDetailPage />,
      },
      {
        path: "/projects/:id/tasks/:taskId",
        element: <TaskDetailPage />,
      },
    ],
    { initialEntries: [pathname] }
  );

  return render(<RouterProvider router={router} />);
}

function renderIssueDetail(pathname: string) {
  const router = createMemoryRouter(
    [
      {
        path: "/projects/:id/issues/:issueId",
        element: <IssueDetailPage />,
      },
      {
        path: "/issue/:issueId",
        element: <IssueDetailPage />,
      },
    ],
    { initialEntries: [pathname] }
  );

  return render(<RouterProvider router={router} />);
}

describe("TaskDetailPage", () => {
  beforeEach(() => {
    localStorageMock.clear();
    global.fetch = vi.fn().mockRejectedValue(new Error("Network error")) as unknown as typeof fetch;
  });

  it("renders task details and supports editing, subtasks, comments, and activity", async () => {
    const user = userEvent.setup();
    renderTaskDetail("/tasks/task-1");

    // Loads sample task data (API failures are ignored).
    expect(await screen.findByRole("heading", { name: "Set up camp perimeter" })).toBeInTheDocument();

    expect(screen.getByLabelText("Status")).toHaveValue("todo");
    expect(screen.getByLabelText("Priority")).toHaveValue("high");
    expect(screen.getByLabelText("Assignee")).toHaveValue("derek");

    // Markdown description renders.
    expect(screen.getByText("Checklist")).toBeInTheDocument();
    expect(screen.getByText("Confirm trail access points")).toBeInTheDocument();

    // Edit title + description.
    await user.click(screen.getByRole("button", { name: /edit/i }));
    const titleInput = screen.getByLabelText("Task title");
    await user.clear(titleInput);
    await user.type(titleInput, "Updated perimeter plan");
    const descInput = screen.getByPlaceholderText("Write a task description…");
    await user.type(descInput, "\n\nNew line");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("heading", { name: "Updated perimeter plan" })).toBeInTheDocument();

    // Add + complete a subtask.
    await user.type(screen.getByLabelText("New subtask title"), "Test subtask");
    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByText("Test subtask")).toBeInTheDocument();
    const subtaskCheckbox = screen.getByLabelText(/Mark subtask Test subtask as complete/i);
    await user.click(subtaskCheckbox);
    expect(subtaskCheckbox).toBeChecked();

    // Add a comment.
    const commentEventsBefore = screen.getAllByText(/added a comment/i).length;
    await user.type(screen.getByLabelText("New comment"), "Hello **world**");
    await user.click(screen.getByRole("button", { name: /post comment/i }));
    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByText("world")).toBeInTheDocument();

    // Activity timeline updates.
    expect(screen.getByText('added subtask “Test subtask”')).toBeInTheDocument();
    expect(screen.getAllByText(/added a comment/i).length).toBe(commentEventsBefore + 1);
  });

  it("loads project-scoped task details and renders a project breadcrumb", async () => {
    localStorageMock.setItem("otter-camp-org-id", "550e8400-e29b-41d4-a716-446655440000");
    global.fetch = vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          id: "550e8400-e29b-41d4-a716-446655440101",
          title: "Fix task detail page routing",
          description: "Task description",
          status: "in_progress",
          priority: "P1",
          assigned_agent_id: "550e8400-e29b-41d4-a716-446655440102",
          assignee_name: "Derek",
          project_name: "Otter Camp",
          created_at: "2026-02-07T12:00:00Z",
          updated_at: "2026-02-08T12:00:00Z",
        }),
      } as Response) as unknown as typeof fetch;

    renderTaskDetail("/projects/550e8400-e29b-41d4-a716-446655440010/tasks/550e8400-e29b-41d4-a716-446655440101");

    expect(await screen.findByRole("heading", { name: "Fix task detail page routing" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Projects" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Otter Camp" })).toHaveAttribute(
      "href",
      "/projects/550e8400-e29b-41d4-a716-446655440010",
    );
    expect(global.fetch).toHaveBeenCalledWith(
      "/api/projects/550e8400-e29b-41d4-a716-446655440010/tasks/550e8400-e29b-41d4-a716-446655440101?org_id=550e8400-e29b-41d4-a716-446655440000",
    );
  });

  it("shows a project-scoped 404 with Back to Project link and no demo copy", async () => {
    global.fetch = vi.fn().mockResolvedValue({ ok: false } as Response) as unknown as typeof fetch;

    renderTaskDetail("/projects/550e8400-e29b-41d4-a716-446655440010/tasks/550e8400-e29b-41d4-a716-446655440103");

    expect(await screen.findByRole("heading", { name: "Task not found" })).toBeInTheDocument();
    expect(screen.queryByText(/demo/i)).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to Project" })).toHaveAttribute(
      "href",
      "/projects/550e8400-e29b-41d4-a716-446655440010",
    );
    expect(screen.queryByRole("link", { name: "Back to Dashboard" })).not.toBeInTheDocument();
  });

  it("renders dedicated issue-detail shell on project issue route", async () => {
    renderIssueDetail("/projects/550e8400-e29b-41d4-a716-446655440010/issues/issue-123");
    expect(screen.getByTestId("issue-detail-shell")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request Changes" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve Solution" })).toBeInTheDocument();
  });

  it("renders dedicated issue-detail shell on issue alias route", async () => {
    renderIssueDetail("/issue/issue-123");
    expect(screen.getByTestId("issue-detail-shell")).toBeInTheDocument();
  });

  it("keeps static issue-detail baseline stable when alias route param trims empty", async () => {
    renderIssueDetail("/issue/%20");
    expect(screen.getByTestId("issue-detail-shell")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Fix API rate limiting" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve Solution" })).toBeInTheDocument();
  });

  it("keeps static approval controls non-interactive without alert regressions", async () => {
    const user = userEvent.setup();
    localStorageMock.setItem("otter-camp-org-id", "550e8400-e29b-41d4-a716-446655440000");
    global.fetch = vi.fn().mockRejectedValue(new Error("Network error")) as unknown as typeof fetch;

    renderIssueDetail("/issue/issue-123");
    await user.click(screen.getByRole("button", { name: "Approve Solution" }));

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByTestId("issue-detail-shell")).toBeInTheDocument();
  });
});
