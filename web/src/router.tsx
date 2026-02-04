import { createBrowserRouter, Outlet } from "react-router-dom";
import DashboardLayout from "./layouts/DashboardLayout";
import Dashboard from "./pages/Dashboard";
import AgentsPage from "./pages/AgentsPage";
import SettingsPage from "./pages/SettingsPage";
import FeedPage from "./pages/FeedPage";
import ProjectsPage from "./pages/ProjectsPage";
import NotFoundPage from "./pages/NotFoundPage";

function DashboardRoot() {
  return (
    <DashboardLayout>
      <Outlet />
    </DashboardLayout>
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
    ],
  },
  {
    path: "*",
    element: <NotFoundPage />,
  },
]);
