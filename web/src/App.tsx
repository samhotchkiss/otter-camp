import { RouterProvider } from "react-router-dom";
import { WebSocketProvider } from "./contexts/WebSocketContext";
import { router } from "./router";

export default function App() {
  return (
    <WebSocketProvider>
      <RouterProvider router={router} />
    </WebSocketProvider>
  );
}
