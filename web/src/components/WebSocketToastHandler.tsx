import { useEffect, useRef } from "react";
import { useWS } from "../contexts/WebSocketContext";
import { useToast } from "../contexts/ToastContext";

export default function WebSocketToastHandler() {
  const { connected, lastMessage } = useWS();
  const { success, error, info } = useToast();
  const wasConnected = useRef<boolean | null>(null);
  const hasShownInitialConnect = useRef(false);

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
      if (hasShownInitialConnect.current) {
        success("Reconnected", "Connection to server restored");
      } else {
        hasShownInitialConnect.current = true;
      }
    }

    // Connection lost
    if (!connected && wasConnected.current) {
      error("Connection lost", "Attempting to reconnect...");
    }

    wasConnected.current = connected;
  }, [connected, success, error]);

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
