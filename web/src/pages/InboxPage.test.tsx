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

    expect(await screen.findByText(/Code review/)).toBeInTheDocument();

    const card = screen.getByText(/Code review/).closest(".inbox-item");
    expect(card).toHaveClass("inbox-item");

    const typeBadge = screen.getByText("review");
    expect(typeBadge).toHaveClass("badge-type");
    expect(typeBadge).toHaveClass("badge-review");
    const approvalBadge = screen.getByText("approval");
    expect(approvalBadge).toHaveClass("badge-type");
    expect(approvalBadge).toHaveClass("badge-approval");

    expect(screen.getAllByRole("button", { name: "Approve" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("button", { name: "Reject" }).length).toBeGreaterThan(0);
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
      expect(screen.queryByText(/Deploy/)).not.toBeInTheDocument();
    });
    expect(approveItemMock).toHaveBeenCalledWith("approval-2");
  });

  it("renders error state when inbox request fails", async () => {
    inboxMock.mockRejectedValue(new Error("Failed to fetch approvals"));

    render(<InboxPage />);

    expect(await screen.findByText("Error: Failed to fetch approvals")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
