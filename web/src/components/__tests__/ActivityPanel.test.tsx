import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ActivityPanel from "../ActivityPanel";
import { useWS } from "../../contexts/WebSocketContext";

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: () => ({
    getVirtualItems: () => [{ index: 0, size: 72, start: 0 }],
    getTotalSize: () => 72,
  }),
}));

vi.mock("../../contexts/WebSocketContext", () => ({
  useWS: vi.fn(),
}));

vi.mock("../approvals/ExecApprovalsFeed", () => ({
  default: () => null,
}));

describe("legacy ActivityPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("uses sender_login fallback for websocket activity actor names", async () => {
    vi.mocked(useWS).mockReturnValue({
      connected: true,
      lastMessage: {
        type: "TaskCreated",
        data: {
          sender_login: "samhotchkiss",
          title: "Wire feed actor names",
        },
      },
      sendMessage: vi.fn(),
    });

    render(<ActivityPanel />);

    expect(await screen.findByText("samhotchkiss")).toBeInTheDocument();
    expect(screen.getByText(/created task: Wire feed actor names/i)).toBeInTheDocument();
  });
});
