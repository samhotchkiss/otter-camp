import { useEffect, useRef } from "react";
import { useWS } from "../contexts/WebSocketContext";
import { useToast } from "../contexts/ToastContext";

const TRANSIENT_DISCONNECT_SUPPRESSION_MS = 3000;

export default function WebSocketToastHandler() {
  const { connected, lastMessage, reconnectReason } = useWS();
  const reconnectCause = reconnectReason ?? "initial";
  const { success, error, info } = useToast();
  const wasConnected = useRef<boolean | null>(null);
  const hasShownInitialConnect = useRef(false);
  const disconnectStartedAtRef = useRef<number | null>(null);
  const disconnectToastTimerRef = useRef<number | null>(null);
  const disconnectToastShownRef = useRef(false);

  const clearDisconnectToastTimer = () => {
    if (disconnectToastTimerRef.current !== null) {
      window.clearTimeout(disconnectToastTimerRef.current);
      disconnectToastTimerRef.current = null;
    }
  };

  // Handle connection state changes
  useEffect(() => {
    // Skip first render
    if (wasConnected.current === null) {
      wasConnected.current = connected;
      if (connected) {
        hasShownInitialConnect.current = true;
      }
      return;
    }

    // Connection restored
    if (connected && !wasConnected.current) {
      const downMs = disconnectStartedAtRef.current === null
        ? 0
        : Date.now() - disconnectStartedAtRef.current;
      clearDisconnectToastTimer();

      if (hasShownInitialConnect.current) {
        const shouldShowReconnected =
          disconnectToastShownRef.current ||
          (downMs >= TRANSIENT_DISCONNECT_SUPPRESSION_MS && reconnectCause !== "visibility");
        if (shouldShowReconnected) {
          success("Reconnected", "Connection to server restored");
        }
      } else {
        hasShownInitialConnect.current = true;
      }
      disconnectStartedAtRef.current = null;
      disconnectToastShownRef.current = false;
    }

    // Connection lost
    if (!connected && wasConnected.current) {
      disconnectStartedAtRef.current = Date.now();
      disconnectToastShownRef.current = false;
      clearDisconnectToastTimer();
      disconnectToastTimerRef.current = window.setTimeout(() => {
        disconnectToastShownRef.current = true;
        error("Connection lost", "Attempting to reconnect...");
      }, TRANSIENT_DISCONNECT_SUPPRESSION_MS);
    }

    wasConnected.current = connected;
    return clearDisconnectToastTimer;
  }, [connected, reconnectCause, success, error]);

  // Handle WebSocket messages for task events
  useEffect(() => {
    if (!lastMessage) return;

    switch (lastMessage.type) {
      case "TaskCreated": {
        const data = lastMessage.data as { title?: string } | undefined;
        info("Task created", data?.title || "A new task has been added");
        break;
      }
      case "TaskUpdated": {
        const data = lastMessage.data as { title?: string } | undefined;
        info("Task updated", data?.title || "A task has been modified");
        break;
      }
      case "TaskStatusChanged": {
        const data = lastMessage.data as {
          title?: string;
          status?: string;
        } | undefined;
        const status = data?.status || "updated";
        info("Task moved", `${data?.title || "Task"} → ${status}`);
        break;
      }
      default:
        // Ignore other message types
        break;
    }
  }, [lastMessage, info]);

  return null;
}
