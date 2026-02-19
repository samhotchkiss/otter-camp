import { fireEvent, render, screen } from "@testing-library/react";
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
  });

  it("renders static figma baseline scaffold without api dependency", () => {
    renderInbox();

    expect(screen.getByRole("heading", { name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByText("6 items waiting for your attention")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "All (6)" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Unread (3)" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Starred (1)" })).toBeInTheDocument();
    expect(screen.getByText("PR #234 awaiting approval")).toBeInTheDocument();
    expect(screen.getAllByText("from Agent-042").length).toBeGreaterThan(0);
    expect(inboxMock).not.toHaveBeenCalled();
  });

  it("filters unread and starred items from local baseline state", () => {
    renderInbox();

    fireEvent.click(screen.getByRole("tab", { name: "Unread (3)" }));
    expect(screen.getByText("PR #234 awaiting approval")).toBeInTheDocument();
    expect(screen.queryByText("PR #231 approved and merged")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Starred (1)" }));
    expect(screen.getByText("Critical: API rate limit exceeded")).toBeInTheDocument();
    expect(screen.queryByText("PR #234 awaiting approval")).not.toBeInTheDocument();
  });

  it("supports local star and read interactions", () => {
    renderInbox();

    fireEvent.click(screen.getByRole("button", { name: "Toggle star for PR #234 awaiting approval" }));
    fireEvent.click(screen.getByRole("tab", { name: "Starred (2)" }));
    expect(screen.getByText("PR #234 awaiting approval")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Unread (3)" }));
    fireEvent.click(screen.getByRole("button", { name: "Mark PR #234 awaiting approval as read" }));
    expect(screen.getByRole("tab", { name: "Unread (2)" })).toBeInTheDocument();
  });

  it("keeps issue-linked rows on issue routes", () => {
    renderInbox();

    const issueRow = screen.getByRole("link", { name: /Critical: API rate limit exceeded/i });
    expect(issueRow).toHaveAttribute("href", "/issue/ISS-209");
  });

  it("applies responsive inbox guard classes for overflow stability", () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/inbox"]}>
        <InboxPage />
      </MemoryRouter>,
    );

    const root = container.firstElementChild;
    expect(root).toHaveClass("min-w-0");

    const listSurface = screen.getByTestId("inbox-list-surface");
    expect(listSurface).toHaveClass("overflow-hidden");

    const linkedRows = screen.getAllByRole("link");
    expect(linkedRows[0]).toHaveClass("min-w-0");
  });
});
