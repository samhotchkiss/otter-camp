import { RouterProvider } from "react-router-dom";
import { ToastProvider } from "./contexts/ToastContext";
import { WebSocketProvider } from "./contexts/WebSocketContext";
import { NotificationProvider } from "./contexts/NotificationContext";
import { AuthProvider } from "./contexts/AuthContext";
import ToastContainer from "./components/ToastContainer";
import WebSocketToastHandler from "./components/WebSocketToastHandler";
import WebSocketOrgSubscriber from "./components/WebSocketOrgSubscriber";
import SkipLink from "./components/SkipLink";
import { LiveRegionProvider } from "./components/LiveRegion";
import ProtectedRoute from "./components/ProtectedRoute";
import DemoBanner from "./components/DemoBanner";
import { isDemoMode } from "./lib/demo";
import { router } from "./router";

export default function App() {
  // In demo mode, skip auth entirely
  if (isDemoMode()) {
    return (
      <LiveRegionProvider>
        <DemoBanner />
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

  // Normal mode: require auth
  return (
    <LiveRegionProvider>
      <ToastProvider>
        <AuthProvider>
          <ProtectedRoute>
            <WebSocketProvider>
              <WebSocketOrgSubscriber />
              <NotificationProvider>
                <SkipLink targetId="main-content" />
                <RouterProvider router={router} />
                <WebSocketToastHandler />
                <ToastContainer />
              </NotificationProvider>
            </WebSocketProvider>
          </ProtectedRoute>
        </AuthProvider>
      </ToastProvider>
    </LiveRegionProvider>
  );
}
