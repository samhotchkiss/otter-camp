import { RouterProvider } from "react-router-dom";
import { ToastProvider } from "./contexts/ToastContext";
import { WebSocketProvider } from "./contexts/WebSocketContext";
import { NotificationProvider } from "./contexts/NotificationContext";
import ToastContainer from "./components/ToastContainer";
import WebSocketToastHandler from "./components/WebSocketToastHandler";
import WebSocketOrgSubscriber from "./components/WebSocketOrgSubscriber";
import SkipLink from "./components/SkipLink";
import { LiveRegionProvider } from "./components/LiveRegion";
import { router } from "./router";

export default function App() {
  return (
    <LiveRegionProvider>
      <ToastProvider>
        <WebSocketProvider>
          <WebSocketOrgSubscriber />
          <NotificationProvider>
            <SkipLink targetId="main-content" />
            <RouterProvider router={router} />
            <WebSocketToastHandler />
            <ToastContainer />
          </NotificationProvider>
        </WebSocketProvider>
      </ToastProvider>
    </LiveRegionProvider>
  );
}
