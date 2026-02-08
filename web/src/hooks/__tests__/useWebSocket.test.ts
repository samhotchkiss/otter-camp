import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

import useWebSocket, { type WebSocketMessageType } from "../useWebSocket";

class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  url: string;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void | Promise<void>) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  sentMessages: string[] = [];

  constructor(url: string) {
    this.url = url;
  }

  send(data: string) {
    this.sentMessages.push(data);
  }

  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.(new CloseEvent("close"));
  }

  simulateOpen() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }

  async simulateMessage(data: string | Blob) {
    if (!this.onmessage) {
      return;
    }
    await this.onmessage(new MessageEvent("message", { data }));
  }

  simulateError() {
    this.onerror?.(new Event("error"));
  }
}

type MockWebSocketCtor = {
  new (url: string): MockWebSocket;
  CONNECTING: number;
  OPEN: number;
  CLOSING: number;
  CLOSED: number;
};

const sockets: MockWebSocket[] = [];
const MockWebSocketConstructor = vi.fn((url: string) => {
  const socket = new MockWebSocket(url);
  sockets.push(socket);
  return socket;
}) as unknown as MockWebSocketCtor;

MockWebSocketConstructor.CONNECTING = MockWebSocket.CONNECTING;
MockWebSocketConstructor.OPEN = MockWebSocket.OPEN;
MockWebSocketConstructor.CLOSING = MockWebSocket.CLOSING;
MockWebSocketConstructor.CLOSED = MockWebSocket.CLOSED;

vi.stubGlobal("WebSocket", MockWebSocketConstructor);

const latestSocket = () => {
  const socket = sockets[sockets.length - 1];
  expect(socket).toBeDefined();
  return socket as MockWebSocket;
};

const expectedSocketUrl = () => {
  const configured = (import.meta.env.VITE_API_URL as string | undefined)?.trim();
  const base = configured || window.location.origin;
  const host = base.replace(/^https?:\/\//, "");
  const protocol = base.startsWith("https") ? "wss:" : "ws:";
  return `${protocol}//${host}/ws`;
};

const setVisibilityState = (value: "visible" | "hidden") => {
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value,
  });
};

describe("useWebSocket", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    sockets.length = 0;
    setVisibilityState("visible");
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("connects on mount and tracks connected state", () => {
    const { result } = renderHook(() => useWebSocket());

    expect(result.current.connected).toBe(false);
    expect(result.current.reconnectReason).toBe("initial");

    act(() => {
      latestSocket().simulateOpen();
    });

    expect(result.current.connected).toBe(true);
  });

  it("uses the configured websocket endpoint", () => {
    renderHook(() => useWebSocket());
    expect(MockWebSocketConstructor).toHaveBeenCalledWith(expectedSocketUrl());
  });

  it("closes websocket and removes handlers on unmount", () => {
    const { unmount } = renderHook(() => useWebSocket());
    const socket = latestSocket();
    const closeSpy = vi.spyOn(socket, "close");

    unmount();

    expect(closeSpy).toHaveBeenCalled();
    expect(socket.onopen).toBeNull();
    expect(socket.onmessage).toBeNull();
    expect(socket.onerror).toBeNull();
    expect(socket.onclose).toBeNull();
  });

  it("reconnects after close using backoff and resets after successful connect", () => {
    const { result } = renderHook(() => useWebSocket());

    const first = latestSocket();
    act(() => {
      first.simulateOpen();
    });
    expect(result.current.connected).toBe(true);

    act(() => {
      first.close();
    });
    expect(result.current.connected).toBe(false);

    act(() => {
      vi.advanceTimersByTime(499);
    });
    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(1);

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(2);

    const second = latestSocket();
    act(() => {
      second.simulateOpen();
    });
    expect(result.current.reconnectReason).toBe("backoff");

    act(() => {
      second.close();
      vi.advanceTimersByTime(500);
    });
    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(3);
  });

  it("reconnects immediately when tab becomes visible while disconnected", () => {
    const { result } = renderHook(() => useWebSocket());
    const first = latestSocket();

    act(() => {
      first.simulateOpen();
    });
    expect(result.current.connected).toBe(true);

    act(() => {
      first.close();
    });
    expect(result.current.connected).toBe(false);

    setVisibilityState("visible");
    act(() => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(2);

    const second = latestSocket();
    act(() => {
      second.simulateOpen();
    });
    expect(result.current.reconnectReason).toBe("visibility");
  });

  it("clears pending reconnect timers on unmount", () => {
    const { unmount } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.close();
    });

    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(1);

    unmount();

    act(() => {
      vi.advanceTimersByTime(3000);
    });

    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(1);
  });

  it("parses type/data messages", async () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    await act(async () => {
      await socket.simulateMessage(
        JSON.stringify({ type: "TaskCreated", data: { id: "task-1" } }),
      );
    });

    expect(result.current.lastMessage).toEqual({
      type: "TaskCreated",
      data: { id: "task-1" },
    });
  });

  it("parses messageType/body fallback payloads", async () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    await act(async () => {
      await socket.simulateMessage(
        JSON.stringify({ messageType: "CommentAdded", body: { text: "hi" } }),
      );
    });

    expect(result.current.lastMessage).toEqual({
      type: "CommentAdded",
      data: { text: "hi" },
    });
  });

  it("parses event/message fallback payloads", async () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    await act(async () => {
      await socket.simulateMessage(
        JSON.stringify({ event: "AgentStatusUpdated", message: { status: "online" } }),
      );
    });

    expect(result.current.lastMessage).toEqual({
      type: "AgentStatusUpdated",
      data: { status: "online" },
    });
  });

  it("normalizes websocket message type tokens with separators", async () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    await act(async () => {
      await socket.simulateMessage(
        JSON.stringify({ event_type: "project.chat.message.created", data: { ok: true } }),
      );
    });

    expect(result.current.lastMessage).toEqual({
      type: "ProjectChatMessageCreated",
      data: { ok: true },
    });
  });

  it("handles blob messages", async () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    const blobPayload = JSON.stringify({ type: "TaskUpdated", data: { id: "a" } });
    const blob = new Blob([blobPayload], {
      type: "application/json",
    }) as Blob & { text?: () => Promise<string> };

    if (typeof blob.text !== "function") {
      Object.defineProperty(blob, "text", {
        value: async () => blobPayload,
      });
    }

    await act(async () => {
      await socket.simulateMessage(blob);
    });

    expect(result.current.lastMessage).toEqual({
      type: "TaskUpdated",
      data: { id: "a" },
    });
  });

  it("returns Unknown for invalid or unsupported message shapes", async () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    await act(async () => {
      await socket.simulateMessage("not-json");
    });
    expect(result.current.lastMessage).toEqual({ type: "Unknown", data: "not-json" });

    await act(async () => {
      await socket.simulateMessage(JSON.stringify({ foo: "bar" }));
    });
    expect(result.current.lastMessage).toEqual({ type: "Unknown", data: { foo: "bar" } });
  });

  it("sendMessage gates on open state and serializes objects", () => {
    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    expect(result.current.sendMessage("hello")).toBe(false);

    act(() => {
      socket.simulateOpen();
    });

    expect(result.current.sendMessage("hello")).toBe(true);
    expect(result.current.sendMessage({ type: "TaskUpdated", data: { id: "1" } })).toBe(
      true,
    );
    expect(socket.sentMessages).toEqual([
      "hello",
      JSON.stringify({ type: "TaskUpdated", data: { id: "1" } }),
    ]);

    socket.readyState = MockWebSocket.CLOSING;
    expect(result.current.sendMessage("later")).toBe(false);
  });

  it("closes socket on error and schedules reconnect", () => {
    renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
      socket.simulateError();
    });

    expect(socket.readyState).toBe(MockWebSocket.CLOSED);

    act(() => {
      vi.advanceTimersByTime(500);
    });

    expect(MockWebSocketConstructor).toHaveBeenCalledTimes(2);
  });

  it("accepts all supported websocket message types", async () => {
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
      "ProjectChatMessageCreated",
      "IssueCommentCreated",
      "IssueReviewSaved",
      "IssueReviewAddressed",
      "ActivityEventReceived",
    ];

    const { result } = renderHook(() => useWebSocket());
    const socket = latestSocket();

    act(() => {
      socket.simulateOpen();
    });

    for (const type of validTypes) {
      await act(async () => {
        await socket.simulateMessage(JSON.stringify({ type, data: { ok: true } }));
      });
      expect(result.current.lastMessage?.type).toBe(type);
    }
  });
});
