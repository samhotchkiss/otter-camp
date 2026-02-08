import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ProjectDetailPage from "./ProjectDetailPage";

vi.mock("../components/project/ProjectFileBrowser", () => ({
  default: ({ projectId }: { projectId: string }) => (
    <div data-testid="project-file-browser-mock">File browser: {projectId}</div>
  ),
}));

vi.mock("../components/project/ProjectIssuesList", () => ({
  default: () => <div data-testid="project-issues-list-mock" />,
}));

vi.mock("../components/project/IssueThreadPanel", () => ({
  default: () => <div data-testid="issue-thread-panel-mock" />,
}));

const { upsertConversationMock, openConversationMock } = vi.hoisted(() => ({
  upsertConversationMock: vi.fn(),
  openConversationMock: vi.fn(),
}));

vi.mock("../contexts/GlobalChatContext", () => ({
  useGlobalChat: () => ({
    upsertConversation: upsertConversationMock,
    openConversation: openConversationMock,
  }),
}));

function mockJSONResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

function ProjectTaskRouteEcho() {
  const { id, taskId } = useParams<{ id: string; taskId: string }>();
  return <div data-testid="project-task-route">{id}:{taskId}</div>;
}

describe("ProjectDetailPage files tab", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    upsertConversationMock.mockReset();
    openConversationMock.mockReset();
    localStorage.clear();
    localStorage.setItem("otter-camp-org-id", "org-123");
  });

  it("shows Files tab, keeps chat as a header button, and renders ProjectFileBrowser when selected", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
          description: "Long-form writing",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ tasks: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Chat" })).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "+ New Task" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Files" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Code" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Files" }));
    expect(await screen.findByTestId("project-file-browser-mock")).toBeInTheDocument();
  });

  it("opens global chat when Chat button is clicked", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ tasks: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Chat" }));

    expect(openConversationMock).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "project",
        projectId: "project-1",
        title: "Technonymous",
      }),
      expect.objectContaining({ focus: true, openDock: true }),
    );
  });

  it("loads board data from project issues and computes issue-based header counts", async () => {
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          agents: [
            {
              id: "550e8400-e29b-41d4-a716-446655440111",
              name: "Derek",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "550e8400-e29b-41d4-a716-446655440211",
              issue_number: 11,
              title: "Stuck issue",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: "550e8400-e29b-41d4-a716-446655440111",
              work_status: "blocked",
              priority: "P1",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "550e8400-e29b-41d4-a716-446655440212",
              issue_number: 12,
              title: "Active issue",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: "550e8400-e29b-41d4-a716-446655440111",
              work_status: "in_progress",
              priority: "P2",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "550e8400-e29b-41d4-a716-446655440213",
              issue_number: 13,
              title: "Completed issue",
              state: "closed",
              origin: "local",
              kind: "issue",
              owner_agent_id: "550e8400-e29b-41d4-a716-446655440111",
              work_status: "done",
              priority: "P3",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();

    const fetchedURLs = fetchMock.mock.calls.map(([input]: [unknown]) => String(input));
    expect(fetchedURLs.some((url) => url.includes("/api/issues?"))).toBe(true);
    expect(fetchedURLs.some((url) => url.includes("/api/tasks?"))).toBe(false);
    expect(screen.getByText("1 item waiting on you")).toBeInTheDocument();
    expect(screen.getByText("2 active issues")).toBeInTheDocument();
  });

  it("submits New Issue request to project chat endpoint", async () => {
    const user = userEvent.setup();
    localStorage.setItem("otter-camp-user-name", "Sam");
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ tasks: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }))
      .mockResolvedValueOnce(
        mockJSONResponse({
          message: {
            id: "msg-1",
            project_id: "project-1",
            author: "Sam",
            body: "New issue request",
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          },
          delivery: { delivered: true },
        }),
      );

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.type(screen.getByLabelText("New issue"), "Add issue creation workflow");
    await user.click(screen.getByRole("button", { name: "New Issue" }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/projects/project-1/chat/messages?org_id=org-123"),
        expect.objectContaining({ method: "POST" }),
      );
    });

    const submitCall = fetchMock.mock.calls.find(
      (args: unknown[]) =>
        typeof args[0] === "string" &&
        (args[0] as string).includes("/api/projects/project-1/chat/messages?org_id=org-123"),
    );
    expect(submitCall).toBeTruthy();
    const submitBody = JSON.parse((submitCall?.[1] as RequestInit).body as string);
    expect(submitBody.author).toBe("Sam");
    expect(submitBody.body).toContain("New issue request");
    expect(submitBody.body).toContain("Title: Add issue creation workflow");

    expect(await screen.findByText("Issue request sent to the project agent.")).toBeInTheDocument();
    expect(openConversationMock).toHaveBeenCalledWith(
      expect.objectContaining({ type: "project", projectId: "project-1" }),
      expect.objectContaining({ focus: true, openDock: true }),
    );
  });

  it("navigates to project task detail when a board task card is clicked", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(
        mockJSONResponse({
          tasks: [
            {
              id: "550e8400-e29b-41d4-a716-446655440111",
              title: "Fix task detail routing",
              status: "queued",
              priority: "P1",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
          <Route path="/projects/:id/tasks/:taskId" element={<ProjectTaskRouteEcho />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByText("Fix task detail routing"));
    expect(await screen.findByTestId("project-task-route")).toHaveTextContent(
      "project-1:550e8400-e29b-41d4-a716-446655440111",
    );
  });

  it("navigates to project task detail when a list task row is clicked", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(
        mockJSONResponse({
          tasks: [
            {
              id: "550e8400-e29b-41d4-a716-446655440112",
              title: "Verify list row click route",
              status: "in_progress",
              priority: "P2",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
          <Route path="/projects/:id/tasks/:taskId" element={<ProjectTaskRouteEcho />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "List" }));
    await user.click(screen.getByText("Verify list row click route"));
    expect(await screen.findByTestId("project-task-route")).toHaveTextContent(
      "project-1:550e8400-e29b-41d4-a716-446655440112",
    );
  });

  it("shows task status badges in the list tab", async () => {
    const user = userEvent.setup();
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(
        mockJSONResponse({
          tasks: [
            {
              id: "task-1",
              title: "Ship status column",
              status: "in_progress",
              priority: "P1",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "List" }));

    expect(screen.getByText("Ship status column")).toBeInTheDocument();
    expect(screen.getByText("In Progress")).toBeInTheDocument();
  });
});
