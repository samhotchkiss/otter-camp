import { useState, type FormEvent } from "react";
import { useAuth } from "../contexts/AuthContext";
import { useToast } from "../contexts/ToastContext";

type LoginPageProps = {
  onLoginSuccess?: () => void;
};

export default function LoginPage({ onLoginSuccess }: LoginPageProps) {
  const { requestLogin, exchangeToken } = useAuth();
  const toast = useToast();
  const [orgId, setOrgId] = useState("");
  const [authRequest, setAuthRequest] = useState<{
    request_id: string;
    state: string;
    expires_at: string;
    exchange_url: string;
    openclaw_request: {
      request_id: string;
      state: string;
      org_id: string;
      callback_url: string;
      expires_at: string;
    };
  } | null>(null);
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const handleRequest = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);

    try {
      const request = await requestLogin(orgId);
      setAuthRequest(request);
      toast.success("Auth request created", "OpenClaw will prompt you to approve the login");
    } catch (err) {
      const message = err instanceof Error ? err.message : "Login failed";
      setError(message);
      toast.error("Request failed", message);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleExchange = async (e: FormEvent) => {
    e.preventDefault();
    if (!authRequest) return;
    setError(null);
    setIsSubmitting(true);

    try {
      await exchangeToken(authRequest.request_id, token);
      toast.success("Welcome back!", "You have successfully signed in");
      onLoginSuccess?.();
    } catch (err) {
      const message = err instanceof Error ? err.message : "Login failed";
      setError(message);
      toast.error("Sign in failed", message);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-to-br from-sky-100 via-white to-emerald-100 px-4 dark:from-slate-900 dark:via-slate-950 dark:to-slate-900">
      <div className="w-full max-w-md">
        {/* Logo */}
        <div className="mb-8 text-center">
          <span className="text-6xl">🦦</span>
          <h1 className="mt-4 text-3xl font-bold text-slate-900 dark:text-white">
            Otter Camp
          </h1>
          <p className="mt-2 text-slate-600 dark:text-slate-400">
            Sign in to your account
          </p>
        </div>

        {/* Login Form */}
        <div className="rounded-2xl border border-slate-200 bg-white/80 p-8 shadow-xl backdrop-blur-sm dark:border-slate-800 dark:bg-slate-900/80">
          <form onSubmit={handleRequest} className="space-y-6">
            {error && (
              <div className="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
                {error}
              </div>
            )}

            <div>
              <label
                htmlFor="org-id"
                className="block text-sm font-medium text-slate-700 dark:text-slate-300"
              >
                Organization ID
              </label>
              <input
                id="org-id"
                type="text"
                autoComplete="organization"
                required
                value={orgId}
                onChange={(e) => setOrgId(e.target.value)}
                className="mt-2 block w-full rounded-xl border border-slate-300 bg-white px-4 py-3 text-slate-900 placeholder-slate-400 shadow-sm transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-white dark:placeholder-slate-500 dark:focus:border-sky-400"
                placeholder="00000000-0000-0000-0000-000000000000"
              />
            </div>

            <button
              type="submit"
              disabled={isSubmitting}
              className="flex w-full items-center justify-center gap-2 rounded-xl bg-gradient-to-r from-sky-500 to-emerald-500 px-4 py-3 font-semibold text-white shadow-lg transition hover:from-sky-600 hover:to-emerald-600 focus:outline-none focus:ring-2 focus:ring-sky-500/50 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isSubmitting ? (
                <>
                  <svg
                    className="h-5 w-5 animate-spin"
                    fill="none"
                    viewBox="0 0 24 24"
                  >
                    <circle
                      className="opacity-25"
                      cx="12"
                      cy="12"
                      r="10"
                      stroke="currentColor"
                      strokeWidth="4"
                    />
                    <path
                      className="opacity-75"
                      fill="currentColor"
                      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                    />
                  </svg>
                  Requesting...
                </>
              ) : (
                "Request Login"
              )}
            </button>
          </form>

          {authRequest && (
            <div className="mt-8 space-y-4 rounded-xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/60 dark:text-slate-300">
              <div>
                <div className="text-xs uppercase tracking-wide text-slate-500">OpenClaw Request</div>
                <div className="mt-1 break-all font-mono text-xs">
                  {JSON.stringify(authRequest.openclaw_request)}
                </div>
              </div>

              <form onSubmit={handleExchange} className="space-y-3">
                <div>
                  <label
                    htmlFor="token"
                    className="block text-sm font-medium text-slate-700 dark:text-slate-300"
                  >
                    OpenClaw Token
                  </label>
                  <input
                    id="token"
                    type="text"
                    required
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    className="mt-2 block w-full rounded-xl border border-slate-300 bg-white px-4 py-3 text-slate-900 placeholder-slate-400 shadow-sm transition focus:border-emerald-500 focus:outline-none focus:ring-2 focus:ring-emerald-500/20 dark:border-slate-700 dark:bg-slate-800 dark:text-white dark:placeholder-slate-500 dark:focus:border-emerald-400"
                    placeholder="oc_auth_..."
                  />
                </div>

                <button
                  type="submit"
                  disabled={isSubmitting}
                  className="flex w-full items-center justify-center gap-2 rounded-xl bg-slate-900 px-4 py-3 font-semibold text-white shadow-lg transition hover:bg-slate-800 focus:outline-none focus:ring-2 focus:ring-slate-500/50 disabled:cursor-not-allowed disabled:opacity-60 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-white"
                >
                  {isSubmitting ? "Signing in..." : "Exchange Token"}
                </button>
              </form>
            </div>
          )}
        </div>

        {/* Footer */}
        <p className="mt-8 text-center text-xs text-slate-500 dark:text-slate-400">
          Cozy, collaborative, and ready for the next adventure.
        </p>
      </div>
    </div>
  );
}
