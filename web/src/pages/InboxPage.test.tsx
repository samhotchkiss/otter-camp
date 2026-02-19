import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import InboxPage from "./InboxPage";

const { inboxMock, approveItemMock, rejectItemMock } = vi.hoisted(() => ({
  inboxMock: vi.fn(),
  approveItemMock: vi.fn(),
  rejectItemMock: vi.fn(),
}));

vi.mock("../lib/api", () => ({
  default: {
    inbox: inboxMock,
    approveItem: approveItemMock,
    rejectItem: rejectItemMock,
  },
}));

const DEFAULT_PAYLOAD = {
  items: [
    {
      id: "approval-1",
      title: "Deploy frontend",
      command: "railway up --service frontend",
      agent: "Derek",
      status: "pending",
      createdAt: "2026-02-18T20:00:00Z",
    },
    {
      id: "approval-2",
      title: "Publish package",
      command: "npm publish",
      agent: "Ivy",
      status: "processing",
      createdAt: "2026-02-18T19:50:00Z",
    },
    {
      id: "approval-3",
      title: "Nightly sync complete",
      command: "sync complete",
      agent: "System",
      status: "approved",
      createdAt: "2026-02-18T18:00:00Z",
      issue_number: 209,
    },
  ],
};

function renderInbox() {
  render(
    <MemoryRouter initialEntries={["/inbox"]}>
      <InboxPage />
    </MemoryRouter>,
  );
}

describe("InboxPage", () => {
  beforeEach(() => {
    inboxMock.mockReset();
    approveItemMock.mockReset();
    rejectItemMock.mockReset();

    inboxMock.mockResolvedValue(DEFAULT_PAYLOAD);
    approveItemMock.mockResolvedValue({ success: true });
    rejectItemMock.mockResolvedValue({ success: true });
  });

  it("renders API-driven inbox rows and filter counts", async () => {
    renderInbox();

    expect(await screen.findByRole("heading", { name: "Inbox" })).toBeInTheDocument();
    expect(await screen.findByText("3 items waiting for your attention")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "All (3)" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Unread (2)" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Starred (0)" })).toBeInTheDocument();
    expect(screen.getByText("Deploy frontend")).toBeInTheDocument();
    expect(screen.getByText("from Derek")).toBeInTheDocument();
    expect(inboxMock).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "Toggle star for Deploy frontend" }));
    expect(screen.getByRole("tab", { name: "Starred (1)" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Starred (1)" }));
    expect(screen.getByText("Deploy frontend")).toBeInTheDocument();
  });

  it("shows loading state while inbox request is pending", () => {
    inboxMock.mockReturnValue(new Promise(() => {}));

    renderInbox();

    expect(screen.getByText("Loading inbox...")).toBeInTheDocument();
  });

  it("shows error state and retries fetching inbox data", async () => {
    inboxMock.mockRejectedValueOnce(new Error("inbox failed"));
    inboxMock.mockResolvedValueOnce({ items: [] });

    renderInbox();

    expect(await screen.findByText("inbox failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No inbox items found.")).toBeInTheDocument();
    expect(inboxMock).toHaveBeenCalledTimes(2);
  });

  it("wires approval actions and removes handled rows optimistically", async () => {
    renderInbox();

    expect(await screen.findByText("Deploy frontend")).toBeInTheDocument();
    expect(screen.getByText("Publish package")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Approve Deploy frontend" }));
    await waitFor(() => expect(approveItemMock).toHaveBeenCalledWith("approval-1"));
    await waitFor(() => expect(screen.queryByText("Deploy frontend")).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Approve Publish package" }));
    await waitFor(() => expect(approveItemMock).toHaveBeenCalledWith("approval-2"));
    await waitFor(() => expect(screen.queryByText("Publish package")).not.toBeInTheDocument());
  });

  it("wires reject action for approval rows", async () => {
    renderInbox();

    expect(await screen.findByText("Nightly sync complete")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Reject Nightly sync complete" }));

    await waitFor(() => expect(rejectItemMock).toHaveBeenCalledWith("approval-3"));
    await waitFor(() => expect(screen.queryByText("Nightly sync complete")).not.toBeInTheDocument());
  });

  it("restores optimistically removed row when approval action fails", async () => {
    approveItemMock.mockRejectedValueOnce(new Error("network error"));

    renderInbox();

    expect(await screen.findByText("Deploy frontend")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve Deploy frontend" }));

    await waitFor(() => expect(approveItemMock).toHaveBeenCalledWith("approval-1"));
    await waitFor(() => expect(screen.getByText("Deploy frontend")).toBeInTheDocument());
    expect(screen.getByText("network error")).toBeInTheDocument();
  });

  it("keeps issue-linked rows on issue routes when issue metadata is present", async () => {
    renderInbox();

    const issueRow = await screen.findByRole("link", { name: /Nightly sync complete/i });
    expect(issueRow).toHaveAttribute("href", "/issue/ISS-209");
  });

  it("applies responsive inbox guard classes for overflow stability", async () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/inbox"]}>
        <InboxPage />
      </MemoryRouter>,
    );

    await screen.findByText("Deploy frontend");

    const root = container.firstElementChild;
    expect(root).toHaveClass("min-w-0");

    const listSurface = screen.getByTestId("inbox-list-surface");
    expect(listSurface).toHaveClass("overflow-hidden");

    const linkedRows = screen.getAllByRole("link");
    expect(linkedRows[0]).toHaveClass("min-w-0");
  });
});
