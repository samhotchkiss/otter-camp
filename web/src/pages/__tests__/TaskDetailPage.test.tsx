import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import TaskDetailPage from "../TaskDetailPage";

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
});
