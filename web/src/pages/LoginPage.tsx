import { useState, type FormEvent } from "react";
import { useAuth } from "../contexts/AuthContext";
import { useToast } from "../contexts/ToastContext";

type LoginPageProps = {
  onLoginSuccess?: () => void;
};

export default function LoginPage({ onLoginSuccess }: LoginPageProps) {
  const { login } = useAuth();
  const toast = useToast();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);

    try {
      await login(email, password);
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
          <h1 className="mt-4 text-3xl font-bold text-otter-text dark:text-white">
            Otter Camp
          </h1>
          <p className="mt-2 text-otter-muted dark:text-otter-dark-muted">
            Sign in to your account
          </p>
        </div>

        {/* Login Form */}
        <div className="rounded-2xl border border-otter-border bg-white/80 p-8 shadow-xl backdrop-blur-sm dark:border-otter-dark-border dark:bg-otter-dark-bg/80">
          <form onSubmit={handleSubmit} className="space-y-6">
            {error && (
              <div className="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
                {error}
              </div>
            )}

            <div>
              <label
                htmlFor="email"
                className="block text-sm font-medium text-otter-text dark:text-otter-dark-muted"
              >
                Email
              </label>
              <input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="mt-2 block w-full rounded-xl border border-otter-border bg-white px-4 py-3 text-otter-text placeholder-slate-400 shadow-sm transition focus:border-otter-dark-accent focus:outline-none focus:ring-2 focus:ring-otter-dark-accent/20 dark:border-otter-dark-border dark:bg-otter-dark-surface dark:text-white dark:placeholder-slate-500 dark:focus:border-otter-dark-accent"
                placeholder="you@example.com"
              />
            </div>

            <div>
              <label
                htmlFor="password"
                className="block text-sm font-medium text-otter-text dark:text-otter-dark-muted"
              >
                Password
              </label>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="mt-2 block w-full rounded-xl border border-otter-border bg-white px-4 py-3 text-otter-text placeholder-slate-400 shadow-sm transition focus:border-otter-dark-accent focus:outline-none focus:ring-2 focus:ring-otter-dark-accent/20 dark:border-otter-dark-border dark:bg-otter-dark-surface dark:text-white dark:placeholder-slate-500 dark:focus:border-otter-dark-accent"
                placeholder="••••••••"
              />
            </div>

            <button
              type="submit"
              disabled={isSubmitting}
              className="flex w-full items-center justify-center gap-2 rounded-xl bg-gradient-to-r from-sky-500 to-emerald-500 px-4 py-3 font-semibold text-white shadow-lg transition hover:from-sky-600 hover:to-emerald-600 focus:outline-none focus:ring-2 focus:ring-otter-dark-accent/50 disabled:cursor-not-allowed disabled:opacity-60"
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
                  Signing in...
                </>
              ) : (
                "Sign in"
              )}
            </button>
          </form>

          <div className="mt-6 text-center text-sm text-otter-muted dark:text-otter-dark-muted">
            Don't have an account?{" "}
            <a
              href="#"
              className="font-medium text-sky-600 hover:text-sky-500 dark:text-sky-400"
            >
              Join the waitlist
            </a>
          </div>
        </div>

        {/* Footer */}
        <p className="mt-8 text-center text-xs text-otter-muted dark:text-otter-dark-muted">
          Cozy, collaborative, and ready for the next adventure.
        </p>
      </div>
    </div>
  );
}
