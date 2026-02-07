import { isRouteErrorResponse, useNavigate, useRouteError } from "react-router-dom";
import { isChunkLoadError } from "../lib/lazyRoute";

function getErrorMessage(error: unknown): string {
  if (isRouteErrorResponse(error)) {
    return error.statusText || error.data?.message || `Request failed (${error.status})`;
  }
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "Something went wrong while loading this page.";
}

export default function RouteErrorFallback() {
  const error = useRouteError();
  const navigate = useNavigate();
  const chunkError = isChunkLoadError(error);
  const message = getErrorMessage(error);

  return (
    <div className="flex min-h-[60vh] items-center justify-center px-6 py-10">
      <div className="w-full max-w-xl rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-lg">
        <h1 className="text-2xl font-semibold text-[var(--text)]">Page failed to load</h1>
        <p className="mt-2 text-sm text-[var(--text-muted)]">
          {chunkError
            ? "The app updated in the background and this page chunk is stale."
            : "An unexpected error occurred while rendering this route."}
        </p>
        <p className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text-muted)]">
          {message}
        </p>
        <div className="mt-5 flex flex-wrap gap-3">
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-lg bg-[#C9A86C] px-4 py-2 text-sm font-medium text-slate-900 hover:brightness-105"
          >
            Reload
          </button>
          <button
            type="button"
            onClick={() => navigate("/")}
            className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-4 py-2 text-sm text-[var(--text)] hover:bg-[var(--surface)]"
          >
            Go to dashboard
          </button>
        </div>
      </div>
    </div>
  );
}

