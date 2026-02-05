import { normalizeMetadata } from "./activityFormat";

export type ActivityFeedItem = {
  id: string;
  orgId: string;
  taskId?: string;
  agentId?: string;
  type: string;
  createdAt: Date;
  actorName: string;
  taskTitle?: string;
  summary?: string;
  metadata: Record<string, unknown>;
  priority?: string;
};

const now = Date.now();

export const SAMPLE_ACTIVITY_ITEMS: ActivityFeedItem[] = [
  {
    id: "demo-commit-1",
    orgId: "demo",
    type: "commit",
    createdAt: new Date(now - 12 * 60 * 1000),
    actorName: "Frank the Agent",
    summary: "Frank the Agent committed to otter-camp: \"Implement activity panel\"",
    metadata: normalizeMetadata({
      repo: "otter-camp",
      message: "Implement activity panel",
      sha: "c0ffee1",
      branch: "main",
    }),
  },
  {
    id: "demo-comment-1",
    orgId: "demo",
    type: "comment",
    createdAt: new Date(now - 48 * 60 * 1000),
    actorName: "Jane Smith",
    taskTitle: "Wire up real-time feed updates",
    summary: "Jane Smith commented on \"Wire up real-time feed updates\"",
    metadata: normalizeMetadata({
      text: "Added a quick debounce so the feed doesn’t spam refreshes.",
    }),
  },
  {
    id: "demo-status-1",
    orgId: "demo",
    type: "task_status_changed",
    createdAt: new Date(now - 3 * 60 * 60 * 1000),
    actorName: "System",
    taskTitle: "Ship v0.1",
    summary: "System changed task \"Ship v0.1\" to done",
    metadata: normalizeMetadata({
      previous_status: "in_progress",
      new_status: "done",
    }),
    priority: "high",
  },
];

