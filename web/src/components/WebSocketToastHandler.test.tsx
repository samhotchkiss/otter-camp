import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import WebSocketToastHandler from "./WebSocketToastHandler";

const wsState = {
  connected: false,
  lastMessage: null as unknown,
  reconnectReason: "initial" as "initial" | "backoff" | "visibility",
  sendMessage: vi.fn(),
};

const success = vi.fn();
const error = vi.fn();
const info = vi.fn();

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => wsState,
}));

vi.mock("../contexts/ToastContext", () => ({
  useToast: () => ({
    success,
    error,
    info,
  }),
}));

describe("WebSocketToastHandler", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    wsState.connected = false;
    wsState.lastMessage = null;
    wsState.reconnectReason = "initial";
  });

  it("suppresses toast spam for short disconnects", () => {
    const { rerender } = render(<WebSocketToastHandler />);

    wsState.connected = true;
    rerender(<WebSocketToastHandler />);

    wsState.connected = false;
    rerender(<WebSocketToastHandler />);

    vi.advanceTimersByTime(2000);
    expect(error).not.toHaveBeenCalled();

    wsState.connected = true;
    wsState.reconnectReason = "visibility";
    rerender(<WebSocketToastHandler />);

    expect(error).not.toHaveBeenCalled();
    expect(success).not.toHaveBeenCalled();
  });

  it("shows disconnect and reconnect toasts for prolonged outages", () => {
    const { rerender } = render(<WebSocketToastHandler />);

    wsState.connected = true;
    rerender(<WebSocketToastHandler />);

    wsState.connected = false;
    rerender(<WebSocketToastHandler />);

    vi.advanceTimersByTime(3000);
    expect(error).toHaveBeenCalledWith("Connection lost", "Attempting to reconnect...");

    wsState.connected = true;
    wsState.reconnectReason = "backoff";
    rerender(<WebSocketToastHandler />);

    expect(success).toHaveBeenCalledWith("Reconnected", "Connection to server restored");
  });

  it("cleans up pending disconnect timer on unmount", () => {
    const { rerender, unmount } = render(<WebSocketToastHandler />);

    wsState.connected = true;
    rerender(<WebSocketToastHandler />);

    wsState.connected = false;
    rerender(<WebSocketToastHandler />);

    unmount();
    vi.advanceTimersByTime(5000);

    expect(error).not.toHaveBeenCalled();
  });
});
