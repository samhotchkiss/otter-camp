import ActivityPanel from "../components/ActivityPanel";

export default function FeedPage() {
  return (
    <div className="w-full">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold text-[var(--text)]">
          Activity Feed
        </h1>
        <p className="mt-1 text-sm text-[var(--text-muted)]">
          Real-time updates from across your projects and agents
        </p>
      </div>
      <ActivityPanel className="min-h-[70vh]" />
    </div>
  );
}
