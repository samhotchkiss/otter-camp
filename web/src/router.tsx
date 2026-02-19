import { lazy, Suspense } from "react";
import { createBrowserRouter, Navigate, Outlet, useParams } from "react-router-dom";
import DashboardLayout from "./layouts/DashboardLayout";
import LoadingSpinner from "./components/LoadingSpinner";
import RouteErrorFallback from "./components/RouteErrorFallback";
import { KeyboardShortcutsProvider } from "./contexts/KeyboardShortcutsContext";
import { lazyWithChunkRetry } from "./lib/lazyRoute";

// Lazy load all page components for code splitting
const Dashboard = lazy(() => lazyWithChunkRetry(() => import("./pages/Dashboard")));
const AgentsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/AgentsPage")));
const AgentsNewPage = lazy(() => lazyWithChunkRetry(() => import("./pages/AgentsNewPage")));
const AgentDetailPage = lazy(() => lazyWithChunkRetry(() => import("./pages/AgentDetailPage")));
const SettingsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/SettingsPage")));
const FeedPage = lazy(() => lazyWithChunkRetry(() => import("./pages/FeedPage")));
const ProjectsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ProjectsPage")));
const NotificationsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/NotificationsPage")));
const TaskDetailPage = lazy(() => lazyWithChunkRetry(() => import("./pages/TaskDetailPage")));
const NotFoundPage = lazy(() => lazyWithChunkRetry(() => import("./pages/NotFoundPage")));
const InboxPage = lazy(() => lazyWithChunkRetry(() => import("./pages/InboxPage")));
const ProjectDetailPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ProjectDetailPage")));
const IssueDetailPage = lazy(() => lazyWithChunkRetry(() => import("./pages/IssueDetailPage")));
const WorkflowsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/WorkflowsPage")));
const KnowledgePage = lazy(() => lazyWithChunkRetry(() => import("./pages/KnowledgePage")));
const MemoryEvaluationPage = lazy(() => lazyWithChunkRetry(() => import("./pages/MemoryEvaluationPage")));
const EllieIngestionCoveragePage = lazy(() => lazyWithChunkRetry(() => import("./pages/EllieIngestionCoveragePage")));
const ConnectionsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ConnectionsPage")));
const ArchivedChatsPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ArchivedChatsPage")));
const ContentReviewPage = lazy(() => lazyWithChunkRetry(() => import("./pages/ContentReviewPage")));

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

function ProjectAliasAdapter() {
  const { projectId } = useParams<{ projectId?: string }>();
  if (!projectId) {
    return <Navigate to="/projects" replace />;
  }
  return <Navigate to={`/projects/${encodeURIComponent(projectId)}`} replace />;
}

export const router = createBrowserRouter([
  {
    path: "/",
    element: <DashboardRoot />,
    errorElement: <RouteErrorFallback />,
    children: [
      {
        index: true,
        element: <Navigate to="/inbox" replace />,
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
        path: "chats",
        element: <Dashboard />,
      },
      {
        path: "chats/:chatId",
        element: <Dashboard />,
      },
      {
        path: "chats/archived",
        element: <ArchivedChatsPage />,
      },
      {
        path: "agents",
        element: <AgentsPage />,
      },
      {
        path: "agents/new",
        element: <AgentsNewPage />,
      },
      {
        path: "agents/:id",
        element: <AgentDetailPage />,
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
        path: "projects/:id/tasks/:taskId",
        element: <TaskDetailPage />,
      },
      {
        path: "project/:projectId",
        element: <ProjectAliasAdapter />,
      },
      {
        path: "issue/:issueId",
        element: <IssueDetailPage />,
      },
      {
        path: "projects/:id/issues/:issueId",
        element: <IssueDetailPage />,
      },
      {
        path: "review/:documentId",
        element: <ContentReviewPage />,
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
      {
        path: "knowledge/evaluation",
        element: <MemoryEvaluationPage />,
      },
      {
        path: "knowledge/ingestion",
        element: <EllieIngestionCoveragePage />,
      },
      {
        path: "connections",
        element: <ConnectionsPage />,
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
