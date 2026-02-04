import { lazy, Suspense } from "react";
import { createBrowserRouter, Outlet } from "react-router-dom";
import TopbarLayout from "./layouts/TopbarLayout";
import LoadingSpinner from "./components/LoadingSpinner";
import { KeyboardShortcutsProvider } from "./contexts/KeyboardShortcutsContext";

// Lazy load all page components for code splitting
const HomePage = lazy(() => import("./pages/HomePage"));
const Dashboard = lazy(() => import("./pages/Dashboard"));
const AgentsPage = lazy(() => import("./pages/AgentsPage"));
const SettingsPage = lazy(() => import("./pages/SettingsPage"));
const FeedPage = lazy(() => import("./pages/FeedPage"));
const ProjectsPage = lazy(() => import("./pages/ProjectsPage"));
const NotificationsPage = lazy(() => import("./pages/NotificationsPage"));
const ChatPage = lazy(() => import("./pages/ChatPage"));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage"));

/**
 * Suspense wrapper for lazy-loaded routes with loading fallback.
 */
function SuspenseRoute({ children }: { children: React.ReactNode }) {
  return (
    <Suspense fallback={<LoadingSpinner message="Loading page..." size="lg" />}>
      {children}
    </Suspense>
  );
}

function AppRoot() {
  return (
    <KeyboardShortcutsProvider>
      <TopbarLayout>
        <Suspense fallback={<LoadingSpinner message="Loading page..." size="lg" />}>
          <Outlet />
        </Suspense>
      </TopbarLayout>
    </KeyboardShortcutsProvider>
  );
}

export const router = createBrowserRouter([
  {
    path: "/",
    element: <AppRoot />,
    children: [
      {
        index: true,
        element: <HomePage />,
      },
      {
        path: "tasks",
        element: <Dashboard />,
      },
      {
        path: "agents",
        element: <AgentsPage />,
      },
      {
        path: "settings",
        element: <SettingsPage />,
      },
      {
        path: "feed",
        element: <FeedPage />,
      },
      {
        path: "projects",
        element: <ProjectsPage />,
      },
      {
        path: "notifications",
        element: <NotificationsPage />,
      },
      {
        path: "chat",
        element: <ChatPage />,
      },
      {
        path: "chat/:agentId",
        element: <ChatPage />,
      },
    ],
  },
  {
    path: "*",
    element: (
      <SuspenseRoute>
        <NotFoundPage />
      </SuspenseRoute>
    ),
  },
]);
