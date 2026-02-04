import ActivityPanel from "../components/ActivityPanel";

export default function FeedPage() {
  return (
    <div className="mx-auto max-w-4xl">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-otter-text dark:text-white">
          Activity Feed
        </h1>
        <p className="mt-1 text-sm text-otter-muted dark:text-otter-dark-muted">
          Real-time updates from across your projects and agents
        </p>
      </div>
      <ActivityPanel className="min-h-[70vh]" />
    </div>
  );
}
