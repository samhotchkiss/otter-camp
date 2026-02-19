import { describe, expect, it } from "vitest";

import {
  mapInboxPayloadToCoreItems,
  mapProjectIssuesPayloadToCoreDetailIssues,
  mapProjectPayloadToCoreDetailProject,
  mapProjectsPayloadToCoreCards,
} from "./coreDataAdapters";

describe("mapInboxPayloadToCoreItems", () => {
  it("normalizes inbox payload rows with stable fallbacks", () => {
    const now = new Date("2026-02-18T20:05:00Z");
    const items = mapInboxPayloadToCoreItems(
      {
        items: [
          {
            id: "approval-1",
            command: "npm publish",
            agent: "Ivy",
            status: "pending",
            createdAt: "2026-02-18T20:00:00Z",
          },
        ],
      },
      now,
    );

    expect(items).toEqual([
      {
        id: "approval-1",
        type: "approval",
        title: "Approval request",
        description: "Run: npm publish",
        project: "Exec approvals",
        from: "Ivy",
        timestamp: "5m ago",
        priority: "high",
        read: false,
        starred: false,
        status: "pending",
      },
    ]);
  });

  it("supports exec approvals list response shape and unknown-date fallbacks", () => {
    const items = mapInboxPayloadToCoreItems({
      requests: [
        {
          id: "request-1",
          status: "processing",
          command: "",
          created_at: "not-a-date",
        },
      ],
    });

    expect(items[0]?.from).toBe("Unknown");
    expect(items[0]?.timestamp).toBe("Unknown");
    expect(items[0]?.description).toBe("Awaiting response");
    expect(items[0]?.priority).toBe("medium");
  });

  it("filters out malformed inbox rows that cannot map to records", () => {
    const items = mapInboxPayloadToCoreItems({
      items: [
        null,
        {
          id: "approval-2",
          title: "Ship release branch",
          status: "pending",
        },
      ],
    });

    expect(items).toHaveLength(1);
    expect(items[0]?.id).toBe("approval-2");
  });
});

describe("mapProjectsPayloadToCoreCards", () => {
  it("maps project cards and computes open issue fallback metrics", () => {
    const cards = mapProjectsPayloadToCoreCards({
      projects: [
        {
          id: "project-1",
          name: "Customer Portal",
          repo_url: "https://github.com/ottercamp/customer-portal.git",
          taskCount: 12,
          completedCount: 5,
          in_progress: 4,
          needs_approval: 2,
          tech_stack: ["React", "Tailwind"],
        },
        {
          id: "project-2",
          name: "",
          repo_url: "",
          require_human_review: true,
        },
      ],
    });

    expect(cards[0]).toEqual({
      id: "project-1",
      name: "Customer Portal",
      repo: "github.com/ottercamp/customer-portal",
      status: "active",
      openIssues: 7,
      inProgress: 4,
      needsApproval: 2,
      githubSync: true,
      techStack: ["React", "Tailwind"],
    });

    expect(cards[1]?.name).toBe("Untitled Project");
    expect(cards[1]?.repo).toBe("local/project-2");
    expect(cards[1]?.needsApproval).toBe(1);
  });
});

describe("mapProjectPayloadToCoreDetailProject", () => {
  it("maps project detail metadata with issue-count fallback", () => {
    const detail = mapProjectPayloadToCoreDetailProject(
      {
        id: "project-9",
        name: "API Gateway",
        description: "",
        repo_url: "",
        updated_at: "2026-02-18T19:05:00Z",
      },
      4,
      new Date("2026-02-18T20:05:00Z"),
    );

    expect(detail.id).toBe("project-9");
    expect(detail.name).toBe("API Gateway");
    expect(detail.description).toBe("No description provided.");
    expect(detail.repo).toBe("local/project-9");
    expect(detail.lastSync).toBe("1h ago");
    expect(detail.stats.openIssues).toBe(4);
  });
});

describe("mapProjectIssuesPayloadToCoreDetailIssues", () => {
  it("maps issue summaries into project-detail baseline issue rows", () => {
    const issues = mapProjectIssuesPayloadToCoreDetailIssues(
      {
        items: [
          {
            issue_number: 209,
            title: "Fix API rate limiting",
            work_status: "in_progress",
            priority: "P1",
            owner_agent_id: "Agent-007",
            last_activity_at: "2026-02-18T20:00:00Z",
          },
          {
            id: "8f8f8f8f-8f8f-4f8f-8f8f-8f8f8f8f8f8f",
            title: "",
            approval_state: "ready_for_review",
            priority: "",
            created_at: "",
          },
        ],
      },
      new Date("2026-02-18T20:05:00Z"),
    );

    expect(issues[0]).toEqual({
      id: "ISS-209",
      title: "Fix API rate limiting",
      status: "in-progress",
      priority: "high",
      assignee: "Agent-007",
      created: "5m ago",
    });

    expect(issues[1]).toEqual({
      id: "ISS-2",
      title: "Untitled issue",
      status: "approval-needed",
      priority: "low",
      assignee: null,
      created: "Unknown",
    });
  });
});
