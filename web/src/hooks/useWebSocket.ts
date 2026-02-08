import { useCallback, useEffect, useRef, useState } from "react";

const RECONNECT_BASE_MS = 500;
const RECONNECT_MAX_MS = 30000;

export type WebSocketMessageType =
  | "TaskCreated"
  | "TaskUpdated"
  | "TaskStatusChanged"
  | "CommentAdded"
  | "AgentStatusUpdated"
  | "AgentStatusChanged"
  | "FeedItemsAdded"
  | "DMMessageReceived"
  | "ExecApprovalRequested"
  | "ExecApprovalResolved"
  | "ProjectChatMessageCreated"
  | "IssueCommentCreated"
  | "IssueReviewSaved"
  | "IssueReviewAddressed"
  | "EmissionReceived";

export type WebSocketMessage =
  | {
      type: WebSocketMessageType;
      data: unknown;
    }
  | {
      type: "Unknown";
      data: unknown;
    };

export type WebSocketSendMessage = (
  message: string | Record<string, unknown>,
) => boolean;

export type WebSocketReconnectReason = "initial" | "backoff" | "visibility";

type UseWebSocketResult = {
  connected: boolean;
  lastMessage: WebSocketMessage | null;
  sendMessage: WebSocketSendMessage;
  reconnectReason: WebSocketReconnectReason;
};

const MESSAGE_TYPES: WebSocketMessageType[] = [
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
  "EmissionReceived",
];

const messageTypeSet = new Set<WebSocketMessageType>(MESSAGE_TYPES);

const resolveApiUrl = (): string => {
  const configured = (import.meta.env.VITE_API_URL as string | undefined)?.trim();
  if (configured) {
    return configured;
  }
  if (typeof window !== "undefined" && window.location?.origin) {
    return window.location.origin;
  }
  return "";
};

const API_URL = resolveApiUrl();

const toWebSocketUrl = (path: string) => {
  if (typeof window === "undefined") {
    return path;
  }

  // Connect to API server for WebSocket (may be different from frontend host).
  const apiBase = API_URL || window.location.origin;
  const apiHost = apiBase.replace(/^https?:\/\//, "");
  const protocol = apiBase.startsWith("https") ? "wss:" : "ws:";
  return `${protocol}//${apiHost}${path}`;
};

const parseMessage = (raw: string): WebSocketMessage => {
  let parsed: unknown = raw;

  try {
    parsed = JSON.parse(raw);
  } catch {
    return { type: "Unknown", data: raw };
  }

  if (parsed && typeof parsed === "object") {
    const record = parsed as Record<string, unknown>;
    const type =
      (record.type as WebSocketMessageType | undefined) ??
      (record.messageType as WebSocketMessageType | undefined) ??
      (record.event as WebSocketMessageType | undefined);

    if (type && messageTypeSet.has(type)) {
      const data =
        record.emission ??
        record.payload ??
        record.data ??
        record.body ??
        record.message ??
        record;
      return { type, data };
    }
  }

  return { type: "Unknown", data: parsed };
};

export default function useWebSocket(): UseWebSocketResult {
  const [connected, setConnected] = useState(false);
  const [lastMessage, setLastMessage] = useState<WebSocketMessage | null>(null);
  const [reconnectReason, setReconnectReason] = useState<WebSocketReconnectReason>("initial");
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptRef = useRef(0);
  const shouldReconnectRef = useRef(true);
  const connectInFlightRef = useRef(false);

  const sendMessage = useCallback<WebSocketSendMessage>((message) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return false;
    }

    const payload =
      typeof message === "string" ? message : JSON.stringify(message);
    socket.send(payload);
    return true;
  }, []);

  useEffect(() => {
    let isMounted = true;
    shouldReconnectRef.current = true;

    const clearReconnectTimer = () => {
      if (reconnectTimerRef.current !== null) {
        window.clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
    };

    const scheduleReconnect = () => {
      if (!shouldReconnectRef.current) {
        return;
      }

      const attempt = reconnectAttemptRef.current;
      const delay = Math.min(
        RECONNECT_MAX_MS,
        RECONNECT_BASE_MS * Math.pow(2, attempt),
      );
      reconnectAttemptRef.current += 1;

      clearReconnectTimer();
      reconnectTimerRef.current = window.setTimeout(() => {
        void connect("backoff");
      }, delay);
    };

    const connect = (reason: WebSocketReconnectReason) => {
      if (!shouldReconnectRef.current) {
        return;
      }
      if (connectInFlightRef.current) {
        return;
      }
      if (socketRef.current) {
        const state = socketRef.current.readyState;
        if (state === WebSocket.OPEN || state === WebSocket.CONNECTING) {
          return;
        }
      }
      connectInFlightRef.current = true;

      const socket = new WebSocket(toWebSocketUrl("/ws"));
      socketRef.current = socket;

      socket.onopen = () => {
        if (!isMounted) {
          return;
        }
        reconnectAttemptRef.current = 0;
        connectInFlightRef.current = false;
        setReconnectReason(reason);
        setConnected(true);
      };

      socket.onmessage = async (event) => {
        if (!isMounted) {
          return;
        }

        let text: string;
        if (typeof event.data === "string") {
          text = event.data;
        } else if (event.data instanceof Blob) {
          text = await event.data.text();
        } else {
          setLastMessage({ type: "Unknown", data: event.data });
          return;
        }

        setLastMessage(parseMessage(text));
      };

      socket.onerror = () => {
        connectInFlightRef.current = false;
        socket.close();
      };

      socket.onclose = () => {
        if (!isMounted) {
          return;
        }
        connectInFlightRef.current = false;
        setConnected(false);
        socketRef.current = null;
        scheduleReconnect();
      };
    };

    const handleVisibilityChange = () => {
      if (!shouldReconnectRef.current) {
        return;
      }
      if (document.visibilityState !== "visible") {
        return;
      }
      const socket = socketRef.current;
      if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
        return;
      }
      reconnectAttemptRef.current = 0;
      clearReconnectTimer();
      void connect("visibility");
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    connect("initial");

    return () => {
      isMounted = false;
      shouldReconnectRef.current = false;
      connectInFlightRef.current = false;
      clearReconnectTimer();
      document.removeEventListener("visibilitychange", handleVisibilityChange);

      if (socketRef.current) {
        socketRef.current.onopen = null;
        socketRef.current.onmessage = null;
        socketRef.current.onerror = null;
        socketRef.current.onclose = null;
        socketRef.current.close();
        socketRef.current = null;
      }
    };
  }, []);

  return { connected, lastMessage, sendMessage, reconnectReason };
}
