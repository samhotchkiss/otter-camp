import { RouterProvider } from "react-router-dom";
import { ToastProvider } from "./contexts/ToastContext";
import { WebSocketProvider } from "./contexts/WebSocketContext";
import ToastContainer from "./components/ToastContainer";
import WebSocketToastHandler from "./components/WebSocketToastHandler";
import { router } from "./router";

export default function App() {
  return (
    <ToastProvider>
      <WebSocketProvider>
        <RouterProvider router={router} />
        <WebSocketToastHandler />
        <ToastContainer />
      </WebSocketProvider>
    </ToastProvider>
  );
}
