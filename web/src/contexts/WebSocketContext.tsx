import { createContext, useContext, type ReactNode } from "react";
import useWebSocket, {
  type WebSocketMessage,
  type WebSocketSendMessage,
} from "../hooks/useWebSocket";

type WebSocketContextValue = {
  connected: boolean;
  lastMessage: WebSocketMessage | null;
  sendMessage: WebSocketSendMessage;
};

const WebSocketContext = createContext<WebSocketContextValue | undefined>(
  undefined,
);

export function WebSocketProvider({ children }: { children: ReactNode }) {
  const value = useWebSocket();
  return (
    <WebSocketContext.Provider value={value}>
      {children}
    </WebSocketContext.Provider>
  );
}

export function useWS(): WebSocketContextValue {
  const context = useContext(WebSocketContext);
  if (!context) {
    throw new Error("useWS must be used within a WebSocketProvider");
  }
  return context;
}
