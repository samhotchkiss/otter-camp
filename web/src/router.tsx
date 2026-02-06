import { lazy, Suspense } from "react";
import { createBrowserRouter, Outlet } from "react-router-dom";
import DashboardLayout from "./layouts/DashboardLayout";
import LoadingSpinner from "./components/LoadingSpinner";
import { KeyboardShortcutsProvider } from "./contexts/KeyboardShortcutsContext";

// Lazy load all page components for code splitting
const Dashboard = lazy(() => import("./pages/Dashboard"));
const AgentsPage = lazy(() => import("./pages/AgentsPage"));
const SettingsPage = lazy(() => import("./pages/SettingsPage"));
const FeedPage = lazy(() => import("./pages/FeedPage"));
const ProjectsPage = lazy(() => import("./pages/ProjectsPage"));
const NotificationsPage = lazy(() => import("./pages/NotificationsPage"));
const TaskDetailPage = lazy(() => import("./pages/TaskDetailPage"));
const NotFoundPage = lazy(() => import("./pages/NotFoundPage"));
const InboxPage = lazy(() => import("./pages/InboxPage"));
const ProjectDetailPage = lazy(() => import("./pages/ProjectDetailPage"));
const WorkflowsPage = lazy(() => import("./pages/WorkflowsPage"));
const KnowledgePage = lazy(() => import("./pages/KnowledgePage"));

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
    element: (
      <SuspenseRoute>
        <NotFoundPage />
      </SuspenseRoute>
    ),
  },
]);
