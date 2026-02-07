import { lazy, Suspense } from "react";
import { createBrowserRouter, Outlet } from "react-router-dom";
import DashboardLayout from "./layouts/DashboardLayout";
import LoadingSpinner from "./components/LoadingSpinner";
import RouteErrorFallback from "./components/RouteErrorFallback";
import { KeyboardShortcutsProvider } from "./contexts/KeyboardShortcutsContext";
import { lazyWithChunkRetry } from "./lib/lazyRoute";

// Lazy load all page components for code splitting
const Dashboard = lazy(() => lazyWithChunkRetry(() => import("./pages/Dashboard")));
const AgentsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/AgentsPage")));
const SettingsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/SettingsPage")));
const FeedPage = lazy(() => lazyWithChunkRetry(() => import("./pages/FeedPage")));
const ProjectsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ProjectsPage")));
const NotificationsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/NotificationsPage")));
const TaskDetailPage = lazy(() => lazyWithChunkRetry(() => import("./pages/TaskDetailPage")));
const NotFoundPage = lazy(() => lazyWithChunkRetry(() => import("./pages/NotFoundPage")));
const InboxPage = lazy(() => lazyWithChunkRetry(() => import("./pages/InboxPage")));
const ProjectDetailPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ProjectDetailPage")));
const WorkflowsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/WorkflowsPage")));
const KnowledgePage = lazy(() => lazyWithChunkRetry(() => import("./pages/KnowledgePage")));

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

function DashboardRoot() {
  return (
    <KeyboardShortcutsProvider>
      <DashboardLayout>
        <Suspense fallback={<LoadingSpinner message="Loading page..." size="lg" />}>
          <Outlet />
        </Suspense>
      </DashboardLayout>
    </KeyboardShortcutsProvider>
  );
}

export const router = createBrowserRouter([
  {
    path: "/",
    element: <DashboardRoot />,
    errorElement: <RouteErrorFallback />,
    children: [
      {
        index: true,
        element: <Dashboard />,
      },
      {
        path: "tasks",
        element: <Dashboard />,
      },
      {
        path: "tasks/:taskId",
        element: <TaskDetailPage />,
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
        path: "projects/:id",
        element: <ProjectDetailPage />,
      },
      {
        path: "projects/:id/issues/:issueId",
        element: <ProjectDetailPage />,
      },
      {
        path: "notifications",
        element: <NotificationsPage />,
      },
      {
        path: "inbox",
        element: <InboxPage />,
      },
      {
        path: "workflows",
        element: <WorkflowsPage />,
      },
      {
        path: "knowledge",
        element: <KnowledgePage />,
      },
    ],
  },
  {
    path: "*",
    errorElement: <RouteErrorFallback />,
    element: (
      <SuspenseRoute>
        <NotFoundPage />
      </SuspenseRoute>
    ),
  },
]);
