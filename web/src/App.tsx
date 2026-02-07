import { WebSocketProvider } from "./contexts/WebSocketContext";

export default function App() {
  return (
    <WebSocketProvider>
      <main className="min-h-screen bg-gradient-to-br from-sky-100 via-white to-emerald-100 px-6 py-16 dark:from-slate-900 dark:via-slate-950 dark:to-slate-900">
        <div className="mx-auto max-w-3xl">
          <div className="inline-flex items-center gap-3 rounded-full border border-slate-200 bg-white/80 px-5 py-2 text-sm font-medium text-slate-600 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/60 dark:text-slate-300">
            Camp basecamp is live
          </div>
          <h1 className="mt-6 text-5xl font-semibold tracking-tight text-slate-900 dark:text-white sm:text-6xl">
            🦦 Otter Camp
          </h1>
          <p className="mt-4 text-lg text-slate-600 dark:text-slate-300">
            Cozy, collaborative, and ready for the next adventure.
          </p>
          <div className="mt-10 flex flex-wrap gap-3">
            <span className="rounded-full bg-sky-500/10 px-4 py-2 text-sm font-medium text-sky-700 dark:bg-sky-500/20 dark:text-sky-200">
              Vite + React + TypeScript
            </span>
            <span className="rounded-full bg-emerald-500/10 px-4 py-2 text-sm font-medium text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-200">
              Tailwind Ready
            </span>
            <span className="rounded-full bg-slate-900/10 px-4 py-2 text-sm font-medium text-slate-700 dark:bg-white/10 dark:text-slate-200">
              TanStack Query
            </span>
          </div>
        </div>
      </main>
    </WebSocketProvider>
  );
}
