import { type ReactNode } from "react";
import { useAuth } from "../contexts/AuthContext";
import LoginPage from "../pages/LoginPage";
import OrgPickerPage from "../pages/OrgPickerPage";

type ProtectedRouteProps = {
  children: ReactNode;
};

export default function ProtectedRoute({ children }: ProtectedRouteProps) {
  const { isAuthenticated, isLoading } = useAuth();

  if (isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gradient-to-br from-sky-100 via-white to-emerald-100 dark:from-slate-900 dark:via-slate-950 dark:to-slate-900">
        <div className="flex flex-col items-center gap-4">
          <span className="text-5xl animate-bounce">🦦</span>
          <div className="text-lg font-medium text-slate-600 dark:text-slate-300">
            Loading...
          </div>
        </div>
      </div>
    );
  }

  if (!isAuthenticated) {
    return <LoginPage />;
  }

  const orgId = localStorage.getItem('otter-camp-org-id');
  if (!orgId) {
    return <OrgPickerPage />;
  }

  return <>{children}</>;
}
