import { render, screen, waitFor, within } from "@testing-library/react";
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

function ProjectIssueRouteEcho() {
  const { id, issueId } = useParams<{ id: string; issueId: string }>();
  return <div data-testid="project-issue-route">{id}:{issueId}</div>;
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
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    expect(screen.getByTestId("project-detail-shell")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Chat" })).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "+ New Task" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Files" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Code" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "Files" }));
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
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }))
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

  it("groups issue work status values into board columns and removes legacy task controls", async () => {
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
          items: [
            {
              id: "issue-queued",
              issue_number: 101,
              title: "Queued issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "ready",
              priority: "P2",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-planning",
              issue_number: 102,
              title: "Planning issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "planning",
              priority: "P2",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-ready-for-work",
              issue_number: 103,
              title: "Ready for work issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "ready_for_work",
              priority: "P1",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-in-progress",
              issue_number: 104,
              title: "In progress issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "in_progress",
              priority: "P1",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-review",
              issue_number: 105,
              title: "Review issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "review",
              priority: "P1",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-blocked",
              issue_number: 106,
              title: "Blocked issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "blocked",
              priority: "P0",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-flagged",
              issue_number: 107,
              title: "Flagged issue",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "flagged",
              priority: "P0",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-done",
              issue_number: 108,
              title: "Done issue",
              state: "closed",
              origin: "local",
              kind: "issue",
              work_status: "done",
              priority: "P3",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
            {
              id: "issue-cancelled",
              issue_number: 109,
              title: "Cancelled issue",
              state: "closed",
              origin: "local",
              kind: "issue",
              work_status: "cancelled",
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

    const planningColumn = screen.getByTestId("board-column-planning");
    const queueColumn = screen.getByTestId("board-column-queue");
    const inProgressColumn = screen.getByTestId("board-column-in_progress");
    const reviewColumn = screen.getByTestId("board-column-review");
    const doneColumn = screen.getByTestId("board-column-done");

    expect(within(planningColumn).getByText("Planning issue")).toBeInTheDocument();
    expect(within(queueColumn).getByText("Queued issue")).toBeInTheDocument();
    expect(within(queueColumn).getByText("Ready for work issue")).toBeInTheDocument();
    expect(within(inProgressColumn).getByText("In progress issue")).toBeInTheDocument();
    expect(within(inProgressColumn).getByTestId("project-board-mini-issue-in-progress")).toBeInTheDocument();
    expect(within(reviewColumn).getByText("Review issue")).toBeInTheDocument();
    expect(within(reviewColumn).getByText("Blocked issue")).toBeInTheDocument();
    expect(within(reviewColumn).getByText("Flagged issue")).toBeInTheDocument();
    expect(within(doneColumn).getByText("Done issue")).toBeInTheDocument();
    expect(within(doneColumn).getByText("Cancelled issue")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /\+ Add Task/i })).not.toBeInTheDocument();
    expect(screen.queryByText("No tasks")).not.toBeInTheDocument();
  });

  it("applies responsive overflow classes for tabs and list layout", async () => {
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
          items: [
            {
              id: "issue-responsive-1",
              issue_number: 42,
              title: "A very long issue title that should preserve layout stability in narrow widths",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "in_progress",
              priority: "P1",
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
    expect(screen.getByTestId("project-detail-shell")).toHaveClass("min-w-0");

    const tabsContainer = screen.getByRole("tab", { name: "Board" }).closest("div");
    expect(tabsContainer).toHaveClass("overflow-x-auto");
    expect(tabsContainer).toHaveClass("whitespace-nowrap");

    await user.click(screen.getByRole("tab", { name: "List" }));

    const listHeader = screen.getByText("Issue").parentElement;
    expect(listHeader).toHaveClass("min-w-[720px]");

    const listRow = screen.getByRole("button", { name: /A very long issue title/i });
    expect(listRow).toHaveClass("min-w-[720px]");
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
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }))
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

  it("exposes project sections with tablist semantics", async () => {
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }))
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    const tablist = screen.getByRole("tablist", { name: "Project detail sections" });
    expect(tablist).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Board" })).toHaveAttribute("aria-selected", "true");
  });

  it("navigates to project issue detail when a board issue card is clicked", async () => {
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
          items: [
            {
              id: "550e8400-e29b-41d4-a716-446655440111",
              issue_number: 40,
              title: "Fix task detail routing",
              work_status: "queued",
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
          <Route path="/projects/:id/issues/:issueId" element={<ProjectIssueRouteEcho />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByText("Fix task detail routing"));
    expect(await screen.findByTestId("project-issue-route")).toHaveTextContent(
      "project-1:550e8400-e29b-41d4-a716-446655440111",
    );
  });

  it("navigates to project issue detail when a list issue row is clicked", async () => {
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
          items: [
            {
              id: "550e8400-e29b-41d4-a716-446655440112",
              issue_number: 41,
              title: "Verify list row click route",
              work_status: "in_progress",
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
          <Route path="/projects/:id/issues/:issueId" element={<ProjectIssueRouteEcho />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "List" }));
    await user.click(screen.getByText(/Verify list row click route/i));
    expect(await screen.findByTestId("project-issue-route")).toHaveTextContent(
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
          items: [
            {
              id: "issue-status-1",
              issue_number: 301,
              title: "Ship status column",
              state: "open",
              origin: "local",
              kind: "issue",
              work_status: "in_progress",
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
    await user.click(screen.getByRole("tab", { name: "List" }));

    expect(screen.getByText(/Ship status column/i)).toBeInTheDocument();
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByTestId("project-list-mini-issue-status-1")).toBeInTheDocument();
  });

  it("shows issue list columns for status, assignee, and priority", async () => {
    const user = userEvent.setup();
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
              id: "550e8400-e29b-41d4-a716-446655440222",
              name: "Ivy",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "issue-501",
              issue_number: 501,
              title: "List columns should render",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: "550e8400-e29b-41d4-a716-446655440222",
              work_status: "in_progress",
              priority: "P1",
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
    await user.click(screen.getByRole("tab", { name: "List" }));

    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("Assignee")).toBeInTheDocument();
    expect(screen.getByText("Priority")).toBeInTheDocument();
    expect(screen.getByText(/List columns should render/i)).toBeInTheDocument();
    expect(screen.getByText("Ivy")).toBeInTheDocument();
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByText("P1")).toBeInTheDocument();
  });

  it("uses issue-centric empty copy in list tab when no active issues remain", async () => {
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
          items: [
            {
              id: "issue-closed",
              issue_number: 520,
              title: "Closed issue",
              state: "closed",
              origin: "local",
              kind: "issue",
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
    await user.click(screen.getByRole("tab", { name: "List" }));

    expect(screen.getByText("No active issues")).toBeInTheDocument();
    expect(screen.queryByText("No active tasks")).not.toBeInTheDocument();
  });

  it("does not leak agent name mappings across project remounts", async () => {
    const ownerID = "550e8400-e29b-41d4-a716-446655440abc";
    fetchMock
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-1",
          name: "Project One",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          agents: [{ id: ownerID, name: "Ivy" }],
        }),
      )
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "issue-1",
              issue_number: 101,
              title: "Project one issue",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: ownerID,
              work_status: "in_progress",
              priority: "P1",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }))
      .mockResolvedValueOnce(
        mockJSONResponse({
          id: "project-2",
          name: "Project Two",
          status: "active",
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ agents: [] }))
      .mockResolvedValueOnce(
        mockJSONResponse({
          items: [
            {
              id: "issue-2",
              issue_number: 202,
              title: "Project two issue",
              state: "open",
              origin: "local",
              kind: "issue",
              owner_agent_id: ownerID,
              work_status: "in_progress",
              priority: "P1",
              last_activity_at: "2026-02-08T12:00:00Z",
            },
          ],
        }),
      )
      .mockResolvedValueOnce(mockJSONResponse({ items: [] }));

    const firstRender = render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Project One" })).toBeInTheDocument();
    expect(screen.getByText("Ivy")).toBeInTheDocument();
    firstRender.unmount();

    render(
      <MemoryRouter initialEntries={["/projects/project-2"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Project Two" })).toBeInTheDocument();
    expect(screen.getByText("Project two issue")).toBeInTheDocument();
    expect(screen.getByText("Unassigned")).toBeInTheDocument();
    expect(screen.queryByText("Ivy")).not.toBeInTheDocument();
  });

  it("saves workflow config through project patch endpoint from settings tab", async () => {
    const user = userEvent.setup();
    const requests: Array<{ url: string; method: string; body?: string }> = [];
    const workflowAgentID = "550e8400-e29b-41d4-a716-446655440222";

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method || "GET";
      const body = typeof init?.body === "string" ? init.body : undefined;
      requests.push({ url, method, body });

      if (url.includes("/api/projects/project-1") && method === "GET") {
        return mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
          primary_agent_id: workflowAgentID,
          workflow_enabled: true,
          workflow_schedule: { kind: "cron", expr: "0 6 * * *", tz: "America/Denver" },
          workflow_template: {
            title_pattern: "Morning Briefing — {{date}}",
            body: "Generate briefing",
            priority: "P2",
            labels: ["automated"],
            auto_close: true,
            pipeline: "none",
          },
          workflow_agent_id: workflowAgentID,
        });
      }
      if (url.includes("/api/agents?") && method === "GET") {
        return mockJSONResponse({
          agents: [{ id: workflowAgentID, name: "Frank" }],
        });
      }
      if (url.includes("/api/issues?") && method === "GET") {
        return mockJSONResponse({ items: [] });
      }
      if (url.includes("/api/activity/recent?") && method === "GET") {
        return mockJSONResponse({ items: [] });
      }
      if (url.includes("/api/projects/project-1/settings") && method === "PATCH") {
        return mockJSONResponse({
          id: "project-1",
          primary_agent_id: workflowAgentID,
        });
      }
      if (url.includes("/api/projects/project-1?") && method === "PATCH") {
        return mockJSONResponse({
          id: "project-1",
          workflow_enabled: true,
          workflow_schedule: { kind: "every", everyMs: 600000 },
          workflow_template: {
            title_pattern: "Every run {{run_number}}",
            body: "Run body",
            priority: "P1",
            labels: ["automated", "briefing"],
            auto_close: true,
            pipeline: "auto_close",
          },
          workflow_agent_id: workflowAgentID,
        });
      }

      throw new Error(`Unexpected request: ${url} ${method}`);
    });

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Settings" }));

    await user.selectOptions(screen.getByLabelText("Workflow schedule type"), "every");
    await user.clear(screen.getByLabelText("Workflow every milliseconds"));
    await user.type(screen.getByLabelText("Workflow every milliseconds"), "600000");
    await user.clear(screen.getByLabelText("Workflow issue title pattern"));
    await user.type(screen.getByLabelText("Workflow issue title pattern"), "Every run pattern");
    await user.clear(screen.getByLabelText("Workflow issue body"));
    await user.type(screen.getByLabelText("Workflow issue body"), "Run body");
    await user.selectOptions(screen.getByLabelText("Workflow issue priority"), "P1");
    await user.clear(screen.getByLabelText("Workflow issue labels"));
    await user.type(screen.getByLabelText("Workflow issue labels"), "automated, briefing");
    await user.selectOptions(screen.getByLabelText("Workflow pipeline"), "auto_close");
    await user.click(screen.getByRole("button", { name: "Save settings" }));

    expect(await screen.findByText("Project settings saved.")).toBeInTheDocument();

    const workflowPatch = requests.find(
      (request) => request.method === "PATCH" && request.url.includes("/api/projects/project-1?"),
    );
    expect(workflowPatch).toBeDefined();
    const payload = JSON.parse(workflowPatch?.body || "{}");
    expect(payload.workflow_enabled).toBe(true);
    expect(payload.workflow_schedule).toEqual({ kind: "every", everyMs: 600000 });
    expect(payload.workflow_template).toEqual({
      title_pattern: "Every run pattern",
      body: "Run body",
      priority: "P1",
      labels: ["automated", "briefing"],
      auto_close: true,
      pipeline: "auto_close",
    });
    expect(payload.workflow_agent_id).toBe(workflowAgentID);
  });

  it("re-syncs project settings when workflow save fails after primary settings patch", async () => {
    const user = userEvent.setup();
    const requests: Array<{ url: string; method: string; body?: string }> = [];
    const workflowAgentID = "550e8400-e29b-41d4-a716-446655440222";
    let projectGetCount = 0;

    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method || "GET";
      const body = typeof init?.body === "string" ? init.body : undefined;
      requests.push({ url, method, body });

      if (url.includes("/api/projects/project-1") && method === "GET") {
        projectGetCount += 1;
        if (projectGetCount === 1) {
          return mockJSONResponse({
            id: "project-1",
            name: "Technonymous",
            status: "active",
            primary_agent_id: workflowAgentID,
            workflow_enabled: true,
            workflow_schedule: { kind: "cron", expr: "0 6 * * *", tz: "America/Denver" },
            workflow_template: {
              title_pattern: "Morning Briefing — {{date}}",
              body: "Generate briefing",
              priority: "P2",
              labels: ["automated"],
              auto_close: true,
              pipeline: "none",
            },
            workflow_agent_id: workflowAgentID,
          });
        }
        return mockJSONResponse({
          id: "project-1",
          name: "Technonymous",
          status: "active",
          primary_agent_id: workflowAgentID,
          workflow_enabled: true,
          workflow_schedule: { kind: "cron", expr: "0 6 * * *", tz: "America/Denver" },
          workflow_template: {
            title_pattern: "Morning Briefing — {{date}}",
            body: "Generate briefing",
            priority: "P2",
            labels: ["automated"],
            auto_close: true,
            pipeline: "none",
          },
          workflow_agent_id: workflowAgentID,
        });
      }
      if (url.includes("/api/agents?") && method === "GET") {
        return mockJSONResponse({
          agents: [{ id: workflowAgentID, name: "Frank" }],
        });
      }
      if (url.includes("/api/issues?") && method === "GET") {
        return mockJSONResponse({ items: [] });
      }
      if (url.includes("/api/activity/recent?") && method === "GET") {
        return mockJSONResponse({ items: [] });
      }
      if (url.includes("/api/projects/project-1/settings") && method === "PATCH") {
        return mockJSONResponse({
          id: "project-1",
          primary_agent_id: workflowAgentID,
        });
      }
      if (url.includes("/api/projects/project-1?") && method === "PATCH") {
        return {
          ok: false,
          json: async () => ({ error: "Failed to save workflow settings" }),
        } as Response;
      }

      throw new Error(`Unexpected request: ${url} ${method}`);
    });

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Settings" }));

    expect(screen.getByLabelText("Workflow schedule type")).toHaveValue("cron");
    await user.selectOptions(screen.getByLabelText("Workflow schedule type"), "every");
    expect(screen.getByLabelText("Workflow schedule type")).toHaveValue("every");
    const projectGetCountBeforeSave = projectGetCount;

    await user.click(screen.getByRole("button", { name: "Save settings" }));

    expect(await screen.findByText("Failed to save workflow settings")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByLabelText("Workflow schedule type")).toHaveValue("cron");
    });
    expect(projectGetCount > projectGetCountBeforeSave).toBe(true);
    expect(
      requests.some((request) => request.method === "PATCH" && request.url.includes("/api/projects/project-1/settings")),
    ).toBe(true);
    expect(
      requests.some((request) => request.method === "PATCH" && request.url.includes("/api/projects/project-1?")),
    ).toBe(true);
  });

  it("loads activity with project org_id when local org storage is empty", async () => {
    localStorage.clear();

    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/projects/project-1")) {
        return Promise.resolve(
          mockJSONResponse({
            id: "project-1",
            org_id: "org-from-project",
            name: "Technonymous",
            status: "active",
          }),
        );
      }
      if (url.includes("/api/agents")) {
        return Promise.resolve(mockJSONResponse({ agents: [] }));
      }
      if (url.includes("/api/issues")) {
        return Promise.resolve(mockJSONResponse({ items: [] }));
      }
      if (url.includes("/api/activity/recent")) {
        return Promise.resolve(mockJSONResponse({ items: [] }));
      }
      return Promise.resolve(mockJSONResponse({}, false));
    });

    render(
      <MemoryRouter initialEntries={["/projects/project-1"]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Technonymous" })).toBeInTheDocument();

    await waitFor(() => {
      const fetchedURLs = fetchMock.mock.calls.map(([input]: [unknown]) => String(input));
      const activityURL = fetchedURLs.find((url) => url.includes("/api/activity/recent?"));
      expect(activityURL).toBeDefined();
      expect(activityURL).toContain("org_id=org-from-project");
      expect(activityURL).toContain("project_id=project-1");
    });
  });
});
