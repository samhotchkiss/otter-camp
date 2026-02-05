import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import useWebSocket, { type WebSocketMessageType } from "../useWebSocket";

// Mock WebSocket
class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  url: string;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  sentMessages: string[] = [];

  constructor(url: string) {
    this.url = url;
    // Simulate async connection
    setTimeout(() => {
      this.readyState = MockWebSocket.OPEN;
      if (this.onopen) {
        this.onopen(new Event("open"));
      }
    }, 10);
  }

  send(data: string) {
    this.sentMessages.push(data);
  }

  close() {
    this.readyState = MockWebSocket.CLOSED;
    if (this.onclose) {
      this.onclose(new CloseEvent("close"));
    }
  }

  // Test helpers
  simulateMessage(data: string | Blob) {
    if (this.onmessage) {
      this.onmessage(new MessageEvent("message", { data }));
    }
  }

  simulateError() {
    if (this.onerror) {
      this.onerror(new Event("error"));
    }
    this.close();
  }
}

let mockWebSocketInstance: MockWebSocket | null = null;
const MockWebSocketConstructor = vi.fn((url: string) => {
  mockWebSocketInstance = new MockWebSocket(url);
  return mockWebSocketInstance;
});

// Setup global WebSocket mock
vi.stubGlobal("WebSocket", MockWebSocketConstructor);

// Mock window.location for URL construction
const originalLocation = window.location;

describe("useWebSocket", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    mockWebSocketInstance = null;

    // Mock window.location
    Object.defineProperty(window, "location", {
      value: {
        protocol: "https:",
        host: "localhost:3000",
      },
      writable: true,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    Object.defineProperty(window, "location", {
      value: originalLocation,
      writable: true,
    });
  });

  describe("connection", () => {
    it("connects to WebSocket on mount", async () => {
      const { result } = renderHook(() => useWebSocket());

      // Initially not connected
      expect(result.current.connected).toBe(false);

      // Fast-forward to connection
      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });
    });

    it("constructs correct WebSocket URL", async () => {
      renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      expect(MockWebSocketConstructor).toHaveBeenCalledWith("wss://localhost:3000/ws");
    });

    it("uses ws:// for http:// protocol", async () => {
      Object.defineProperty(window, "location", {
        value: {
          protocol: "http:",
          host: "localhost:3000",
        },
        writable: true,
      });

      renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      expect(MockWebSocketConstructor).toHaveBeenCalledWith("ws://localhost:3000/ws");
    });

    it("closes WebSocket on unmount", async () => {
      const { unmount } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      const closeSpy = vi.spyOn(mockWebSocketInstance!, "close");
      unmount();

      expect(closeSpy).toHaveBeenCalled();
    });
  });

  describe("reconnection", () => {
    it("reconnects after disconnect", async () => {
      const { result } = renderHook(() => useWebSocket());

      // Initial connection
      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const initialCallCount = MockWebSocketConstructor.mock.calls.length;

      // Simulate disconnect
      act(() => {
        mockWebSocketInstance?.close();
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(false);
      });

      // Fast-forward past reconnect delay (starts at 500ms)
      act(() => {
        vi.advanceTimersByTime(600);
      });

      // Should attempt to reconnect
      expect(MockWebSocketConstructor.mock.calls.length).toBe(initialCallCount + 1);
    });

    it("uses exponential backoff for reconnection", async () => {
      const { result } = renderHook(() => useWebSocket());

      // Initial connection
      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      // First disconnect and reconnect
      act(() => {
        mockWebSocketInstance?.close();
        vi.advanceTimersByTime(500); // First delay: 500ms
      });

      const firstReconnectCount = MockWebSocketConstructor.mock.calls.length;

      // Second disconnect (before successful reconnect)
      act(() => {
        mockWebSocketInstance?.close();
        vi.advanceTimersByTime(500); // Should need 1000ms now
      });

      // Should not have reconnected yet
      expect(MockWebSocketConstructor.mock.calls.length).toBe(firstReconnectCount);

      act(() => {
        vi.advanceTimersByTime(600); // Total 1100ms should trigger reconnect
      });

      expect(MockWebSocketConstructor.mock.calls.length).toBeGreaterThan(firstReconnectCount);
    });

    it("resets reconnect attempts on successful connection", async () => {
      const { result } = renderHook(() => useWebSocket());

      // Initial connection
      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      // Disconnect
      act(() => {
        mockWebSocketInstance?.close();
        vi.advanceTimersByTime(600); // Reconnect after first delay
      });

      // Wait for reconnection
      act(() => {
        vi.advanceTimersByTime(20);
      });

      // After successful reconnect, delay should reset to 500ms
      // Disconnect again
      act(() => {
        mockWebSocketInstance?.close();
      });

      const callsBeforeReconnect = MockWebSocketConstructor.mock.calls.length;

      // Should reconnect at 500ms (reset), not 1000ms
      act(() => {
        vi.advanceTimersByTime(600);
      });

      expect(MockWebSocketConstructor.mock.calls.length).toBeGreaterThan(callsBeforeReconnect);
    });
  });

  describe("message handling", () => {
    it("parses JSON messages correctly", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const testMessage = {
        type: "TaskCreated",
        data: { id: "task-1", title: "New Task" },
      };

      act(() => {
        mockWebSocketInstance?.simulateMessage(JSON.stringify(testMessage));
      });

      await waitFor(() => {
        expect(result.current.lastMessage).toEqual(testMessage);
      });
    });

    it("handles message with payload field", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const testMessage = {
        type: "TaskUpdated",
        payload: { id: "task-1", status: "done" },
      };

      act(() => {
        mockWebSocketInstance?.simulateMessage(JSON.stringify(testMessage));
      });

      await waitFor(() => {
        expect(result.current.lastMessage?.type).toBe("TaskUpdated");
        expect(result.current.lastMessage?.data).toEqual({ id: "task-1", status: "done" });
      });
    });

    it("handles message with messageType field", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const testMessage = {
        messageType: "CommentAdded",
        body: { taskId: "task-1", text: "Hello" },
      };

      act(() => {
        mockWebSocketInstance?.simulateMessage(JSON.stringify(testMessage));
      });

      await waitFor(() => {
        expect(result.current.lastMessage?.type).toBe("CommentAdded");
        expect(result.current.lastMessage?.data).toEqual({ taskId: "task-1", text: "Hello" });
      });
    });

    it("handles message with event field", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const testMessage = {
        event: "AgentStatusUpdated",
        message: { agentId: "agent-1", status: "online" },
      };

      act(() => {
        mockWebSocketInstance?.simulateMessage(JSON.stringify(testMessage));
      });

      await waitFor(() => {
        expect(result.current.lastMessage?.type).toBe("AgentStatusUpdated");
        expect(result.current.lastMessage?.data).toEqual({ agentId: "agent-1", status: "online" });
      });
    });

    it("returns Unknown type for unparseable messages", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      act(() => {
        mockWebSocketInstance?.simulateMessage("not valid json {{{");
      });

      await waitFor(() => {
        expect(result.current.lastMessage?.type).toBe("Unknown");
        expect(result.current.lastMessage?.data).toBe("not valid json {{{");
      });
    });

    it("returns Unknown type for messages without recognized type", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const testMessage = {
        unknownType: "SomeOtherEvent",
        data: { foo: "bar" },
      };

      act(() => {
        mockWebSocketInstance?.simulateMessage(JSON.stringify(testMessage));
      });

      await waitFor(() => {
        expect(result.current.lastMessage?.type).toBe("Unknown");
      });
    });

    it("handles Blob messages", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const testMessage = { type: "TaskCreated", data: { id: "task-1" } };
      const blob = new Blob([JSON.stringify(testMessage)], { type: "application/json" });

      act(() => {
        mockWebSocketInstance?.simulateMessage(blob);
      });

      await waitFor(() => {
        expect(result.current.lastMessage?.type).toBe("TaskCreated");
      });
    });
  });

  describe("sendMessage", () => {
    it("returns false when not connected", () => {
      const { result } = renderHook(() => useWebSocket());

      // Not connected yet
      expect(result.current.sendMessage("test")).toBe(false);
    });

    it("sends string messages when connected", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const success = result.current.sendMessage("test message");

      expect(success).toBe(true);
      expect(mockWebSocketInstance?.sentMessages).toContain("test message");
    });

    it("sends object messages as JSON", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const message = { type: "TaskUpdated", data: { id: "task-1" } };
      const success = result.current.sendMessage(message);

      expect(success).toBe(true);
      expect(mockWebSocketInstance?.sentMessages).toContain(JSON.stringify(message));
    });

    it("returns false when socket is closing", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      mockWebSocketInstance!.readyState = MockWebSocket.CLOSING;

      expect(result.current.sendMessage("test")).toBe(false);
    });
  });

  describe("error handling", () => {
    it("handles connection errors gracefully", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      act(() => {
        mockWebSocketInstance?.simulateError();
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(false);
      });
    });

    it("attempts reconnection after error", async () => {
      const { result } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const callCountBeforeError = MockWebSocketConstructor.mock.calls.length;

      act(() => {
        mockWebSocketInstance?.simulateError();
        vi.advanceTimersByTime(600);
      });

      expect(MockWebSocketConstructor.mock.calls.length).toBeGreaterThan(callCountBeforeError);
    });
  });

  describe("message types", () => {
    const validTypes: WebSocketMessageType[] = [
      "TaskCreated",
      "TaskUpdated",
      "TaskStatusChanged",
      "CommentAdded",
      "AgentStatusUpdated",
      "AgentStatusChanged",
      "FeedItemsAdded",
      "DMMessageReceived",
      "ExecApprovalRequested",
      "ExecApprovalResolved",
    ];

    validTypes.forEach((type) => {
      it(`recognizes ${type} as valid message type`, async () => {
        const { result } = renderHook(() => useWebSocket());

        act(() => {
          vi.advanceTimersByTime(20);
        });

        await waitFor(() => {
          expect(result.current.connected).toBe(true);
        });

        const testMessage = { type, data: {} };

        act(() => {
          mockWebSocketInstance?.simulateMessage(JSON.stringify(testMessage));
        });

        await waitFor(() => {
          expect(result.current.lastMessage?.type).toBe(type);
        });
      });
    });
  });

  describe("cleanup", () => {
    it("cleans up event handlers on unmount", async () => {
      const { unmount } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      unmount();

      expect(mockWebSocketInstance?.onopen).toBeNull();
      expect(mockWebSocketInstance?.onmessage).toBeNull();
      expect(mockWebSocketInstance?.onerror).toBeNull();
      expect(mockWebSocketInstance?.onclose).toBeNull();
    });

    it("clears pending reconnect timers on unmount", async () => {
      const { result, unmount } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      // Trigger disconnect to schedule reconnect
      act(() => {
        mockWebSocketInstance?.close();
      });

      const callCountAfterClose = MockWebSocketConstructor.mock.calls.length;

      // Unmount before reconnect timer fires
      unmount();

      // Advance time past reconnect delay
      act(() => {
        vi.advanceTimersByTime(1000);
      });

      // Should not have attempted to reconnect
      expect(MockWebSocketConstructor.mock.calls.length).toBe(callCountAfterClose);
    });

    it("does not update state after unmount", async () => {
      const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
      
      const { result, unmount } = renderHook(() => useWebSocket());

      act(() => {
        vi.advanceTimersByTime(20);
      });

      await waitFor(() => {
        expect(result.current.connected).toBe(true);
      });

      const ws = mockWebSocketInstance;
      unmount();

      // Try to trigger state updates after unmount
      // This should not cause React warnings
      act(() => {
        ws?.simulateMessage(JSON.stringify({ type: "TaskCreated", data: {} }));
      });

      // No React warning about updating unmounted component
      expect(consoleErrorSpy).not.toHaveBeenCalledWith(
        expect.stringContaining("Can't perform a React state update on an unmounted component")
      );

      consoleErrorSpy.mockRestore();
    });
  });
});
