import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import InboxPage from "./InboxPage";

type MockApproval = {
  id: string;
  type: string;
  command?: string;
  agent: string;
  status: string;
  createdAt: string;
};

const {
  inboxMock,
  approveItemMock,
  rejectItemMock,
} = vi.hoisted(() => ({
  inboxMock: vi.fn<() => Promise<{ items: MockApproval[] }>>(),
  approveItemMock: vi.fn<(id: string) => Promise<unknown>>(),
  rejectItemMock: vi.fn<(id: string) => Promise<unknown>>(),
}));

vi.mock("../lib/api", () => ({
  default: {
    inbox: inboxMock,
    approveItem: approveItemMock,
    rejectItem: rejectItemMock,
  },
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("InboxPage", () => {
  beforeEach(() => {
    inboxMock.mockReset();
    approveItemMock.mockReset();
    rejectItemMock.mockReset();

    approveItemMock.mockResolvedValue({ success: true });
    rejectItemMock.mockResolvedValue({ success: true });
  });

  it("shows loading state before inbox response resolves", async () => {
    const pending = deferred<{ items: MockApproval[] }>();
    inboxMock.mockReturnValue(pending.promise);

    render(<InboxPage />);

    expect(screen.getByText("Loading...")).toBeInTheDocument();

    pending.resolve({ items: [] });
    expect(await screen.findByText("No pending items")).toBeInTheDocument();
  });

  it("renders empty state when inbox has no items", async () => {
    inboxMock.mockResolvedValue({ items: [] });

    render(<InboxPage />);

    expect(await screen.findByText("No pending items")).toBeInTheDocument();
  });

  it("renders inbox items with shared primitive class hooks", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-1",
          type: "Code review",
          command: "npm run lint",
          agent: "Agent-007",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
        {
          id: "approval-2",
          type: "Command",
          command: "npm run test",
          agent: "Agent-009",
          status: "pending",
          createdAt: "2026-02-18T19:00:00Z",
        },
      ],
    });

    render(<InboxPage />);

    const reviewHeading = await screen.findByRole("heading", { name: /Code review.*Agent-007/ });
    expect(reviewHeading).toBeInTheDocument();

    const card = reviewHeading.closest(".inbox-item");
    expect(card).toHaveClass("oc-card");
    expect(card).toHaveClass("oc-card-interactive");

    const typeBadge = screen.getByText("review");
    expect(typeBadge).toHaveClass("badge-type");
    expect(typeBadge).toHaveClass("badge-review");
    const approvalBadge = screen.getByText("approval");
    expect(approvalBadge).toHaveClass("badge-type");
    expect(approvalBadge).toHaveClass("badge-approval");

    expect(screen.getAllByRole("button", { name: "Approve" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("button", { name: "Reject" }).length).toBeGreaterThan(0);
  });

  it("renders filter tabs with counts and filters visible inbox items", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-10",
          type: "Code review",
          command: "npm run lint",
          agent: "Agent-007",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
        {
          id: "approval-11",
          type: "Deploy approval",
          command: "deploy service",
          agent: "Agent-127",
          status: "approved",
          createdAt: "2026-02-18T19:00:00Z",
        },
        {
          id: "approval-12",
          type: "Blocked release",
          command: "release train",
          agent: "Agent-300",
          status: "blocked",
          createdAt: "2026-02-18T18:00:00Z",
        },
      ],
    });

    render(<InboxPage />);

    expect(await screen.findByRole("heading", { name: /Code review.*Agent-007/ })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "All (3)" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Unread (1)" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Urgent (2)" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Unread (1)" }));
    expect(screen.getByRole("heading", { name: /Code review.*Agent-007/ })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /Deploy approval.*Agent-127/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /Blocked release.*Agent-300/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Urgent (2)" }));
    expect(screen.getByRole("heading", { name: /Code review.*Agent-007/ })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /Blocked release.*Agent-300/ })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /Deploy approval.*Agent-127/ })).not.toBeInTheDocument();
  });

  it("shows derived metadata from status for unread and urgency", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-20",
          type: "Code review",
          command: "npm run lint",
          agent: "Agent-007",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
        {
          id: "approval-21",
          type: "Deploy approval",
          command: "deploy service",
          agent: "Agent-127",
          status: "approved",
          createdAt: "2026-02-18T19:00:00Z",
        },
      ],
    });

    render(<InboxPage />);

    expect(await screen.findByRole("heading", { name: /Code review.*Agent-007/ })).toBeInTheDocument();

    const pendingCard = screen.getByRole("heading", { name: /Code review.*Agent-007/ }).closest(".inbox-item");
    const approvedCard = screen.getByRole("heading", { name: /Deploy approval.*Agent-127/ }).closest(".inbox-item");
    expect(pendingCard).toHaveClass("urgent");
    expect(approvedCard).not.toHaveClass("urgent");
    expect(screen.getByText("Unread")).toBeInTheDocument();
  });

  it("renders redesigned structure hooks for inbox header and row list", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-30",
          type: "Code review",
          command: "npm run lint",
          agent: "Agent-007",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
      ],
    });

    render(<InboxPage />);

    expect(await screen.findByRole("heading", { name: /Code review.*Agent-007/ })).toBeInTheDocument();
    expect(screen.getByTestId("inbox-header")).toBeInTheDocument();
    expect(screen.getByTestId("inbox-list-container")).toBeInTheDocument();
    expect(screen.getAllByTestId("inbox-row")).toHaveLength(1);
  });

  it("renders redesigned metadata lane with agent and timestamp context", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-31",
          type: "Code review",
          command: "npm run lint",
          agent: "Agent-007",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
      ],
    });

    render(<InboxPage />);

    expect(await screen.findByRole("heading", { name: /Code review.*Agent-007/ })).toBeInTheDocument();
    expect(screen.getByText("from Agent-007")).toBeInTheDocument();
    expect(screen.getByTestId("inbox-row-meta")).toBeInTheDocument();
  });

  it("applies inbox redesign class hooks for header, tabs, and rows", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-32",
          type: "Code review",
          command: "npm run lint",
          agent: "Agent-007",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
      ],
    });

    render(<InboxPage />);

    await screen.findByRole("heading", { name: /Code review.*Agent-007/ });
    expect(screen.getByTestId("inbox-header")).toHaveClass("inbox-header");
    expect(screen.getByRole("tablist", { name: "Inbox filters" })).toHaveClass("inbox-filter-tabs");
    expect(screen.getByTestId("inbox-list-container")).toHaveClass("inbox-list-container");
    expect(screen.getByTestId("inbox-row")).toHaveClass("inbox-row");
    expect(screen.getByTestId("inbox-row-meta")).toHaveClass("inbox-row-meta");
  });

  it("disables actions while approve is processing and removes item after success", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-2",
          type: "Deploy",
          command: "deploy service",
          agent: "Agent-127",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
      ],
    });

    const pendingApprove = deferred<{ success: boolean }>();
    approveItemMock.mockReturnValue(pendingApprove.promise);

    render(<InboxPage />);

    const approveButton = await screen.findByRole("button", { name: "Approve" });
    const rejectButton = screen.getByRole("button", { name: "Reject" });

    fireEvent.click(approveButton);

    const processingButtons = await screen.findAllByRole("button", { name: "Processing..." });
    expect(processingButtons).toHaveLength(2);
    expect(processingButtons[0]).toBeDisabled();
    expect(processingButtons[1]).toBeDisabled();
    expect(rejectButton).toBeDisabled();

    pendingApprove.resolve({ success: true });

    await waitFor(() => {
      expect(screen.queryByRole("heading", { name: /Deploy.*Agent-127/ })).not.toBeInTheDocument();
    });
    expect(approveItemMock).toHaveBeenCalledWith("approval-2");
  });

  it("disables actions while reject is processing and removes item after success", async () => {
    inboxMock.mockResolvedValue({
      items: [
        {
          id: "approval-3",
          type: "Deploy",
          command: "deploy service",
          agent: "Agent-128",
          status: "pending",
          createdAt: "2026-02-18T20:00:00Z",
        },
      ],
    });

    const pendingReject = deferred<{ success: boolean }>();
    rejectItemMock.mockReturnValue(pendingReject.promise);

    render(<InboxPage />);

    const rejectButton = await screen.findByRole("button", { name: "Reject" });
    const approveButton = screen.getByRole("button", { name: "Approve" });

    fireEvent.click(rejectButton);

    const processingButtons = await screen.findAllByRole("button", { name: "Processing..." });
    expect(processingButtons).toHaveLength(2);
    expect(processingButtons[0]).toBeDisabled();
    expect(processingButtons[1]).toBeDisabled();
    expect(approveButton).toBeDisabled();

    pendingReject.resolve({ success: true });

    await waitFor(() => {
      expect(screen.queryByRole("heading", { name: /Deploy.*Agent-128/ })).not.toBeInTheDocument();
    });
    expect(rejectItemMock).toHaveBeenCalledWith("approval-3");
  });

  it("renders error state when inbox request fails", async () => {
    inboxMock.mockRejectedValue(new Error("Failed to fetch approvals"));

    render(<InboxPage />);

    expect(await screen.findByText("Error: Failed to fetch approvals")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
