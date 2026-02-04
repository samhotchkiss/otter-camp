import { lazy, Suspense } from "react";
import { createBrowserRouter, Outlet } from "react-router-dom";
import DashboardLayout from "./layouts/DashboardLayout";
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
    // Home page - Jeff's executive dashboard (no sidebar layout)
    path: "/",
    element: (
      <KeyboardShortcutsProvider>
        <Suspense fallback={<LoadingSpinner message="Loading..." size="lg" />}>
          <HomePage />
        </Suspense>
      </KeyboardShortcutsProvider>
    ),
  },
  {
    // Other pages use the sidebar layout
    path: "/",
    element: <DashboardRoot />,
    children: [
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
