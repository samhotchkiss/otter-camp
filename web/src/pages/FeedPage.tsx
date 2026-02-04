import ActivityPanel from "../components/ActivityPanel";

export default function FeedPage() {
  return (
    <div className="mx-auto max-w-4xl">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-slate-900 dark:text-white">
          Activity Feed
        </h1>
        <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
          Real-time updates from across your projects and agents
        </p>
      </div>
      <ActivityPanel className="min-h-[70vh]" />
    </div>
  );
}
