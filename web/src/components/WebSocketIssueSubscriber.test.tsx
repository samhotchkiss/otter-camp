import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import WebSocketIssueSubscriber from "./WebSocketIssueSubscriber";

const sendMessage = vi.fn(() => true);
const wsState = {
  connected: false,
  lastMessage: null as unknown,
  sendMessage,
};

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => wsState,
}));

describe("WebSocketIssueSubscriber", () => {
  beforeEach(() => {
    wsState.connected = false;
    sendMessage.mockReset();
    sendMessage.mockReturnValue(true);
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("subscribes to the active issue channel when connected", () => {
    wsState.connected = true;
    localStorage.setItem("otter-camp-org-id", "org-1");

    render(<WebSocketIssueSubscriber issueID="issue-1" />);

    expect(sendMessage).toHaveBeenCalledWith({
      type: "subscribe",
      org_id: "org-1",
      channel: "issue:issue-1",
    });
  });

  it("unsubscribes previous issue channel before subscribing to next one", () => {
    wsState.connected = true;
    localStorage.setItem("otter-camp-org-id", "org-1");

    const { rerender } = render(<WebSocketIssueSubscriber issueID="issue-a" />);
    sendMessage.mockClear();

    rerender(<WebSocketIssueSubscriber issueID="issue-b" />);

    expect(sendMessage).toHaveBeenNthCalledWith(1, {
      type: "unsubscribe",
      org_id: "org-1",
      channel: "issue:issue-a",
    });
    expect(sendMessage).toHaveBeenNthCalledWith(2, {
      type: "subscribe",
      org_id: "org-1",
      channel: "issue:issue-b",
    });
  });

  it("does not subscribe without org id or when disconnected", () => {
    wsState.connected = false;
    render(<WebSocketIssueSubscriber issueID="issue-a" />);
    expect(sendMessage).not.toHaveBeenCalled();

    wsState.connected = true;
    const { rerender } = render(<WebSocketIssueSubscriber issueID="issue-a" />);
    rerender(<WebSocketIssueSubscriber issueID="issue-a" />);
    expect(sendMessage).not.toHaveBeenCalled();
  });
});
