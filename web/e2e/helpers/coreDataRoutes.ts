import type { Page } from "@playwright/test";

type ProjectIssue = {
  id: string;
  issue_number: number;
  title: string;
  approval_state: string;
  priority: string;
  owner_agent_id: string;
  last_activity_at: string;
  state: string;
  origin: string;
  kind: string;
  project_id: string;
};

function buildCoreDataFixtures() {
  const now = Date.now();
  const minutesAgo = (minutes: number) => new Date(now - minutes * 60_000).toISOString();

  const inbox = {
    items: [
      {
        id: "approval-1",
        title: "Deploy frontend",
        command: "railway up --service frontend",
        agent: "Derek",
        status: "pending",
        createdAt: minutesAgo(2),
      },
      {
        id: "approval-2",
        title: "Publish package",
        command: "npm publish",
        agent: "Ivy",
        status: "processing",
        createdAt: minutesAgo(7),
      },
      {
        id: "approval-3",
        title: "Nightly sync complete",
        command: "sync complete",
        agent: "System",
        status: "approved",
        createdAt: minutesAgo(60),
        issue_number: 209,
      },
    ],
  };

  const projects = [
    {
      id: "project-1",
      name: "Customer Portal",
      repo_url: "https://github.com/ottercamp/customer-portal.git",
      taskCount: 12,
      completedCount: 5,
      in_progress: 5,
      needs_approval: 2,
      tech_stack: ["React", "Tailwind", "Supabase"],
    },
    {
      id: "project-2",
      name: "API Gateway",
      repo_url: "https://github.com/company/api-gateway",
      taskCount: 8,
      completedCount: 3,
      in_progress: 3,
      needs_approval: 1,
      tech_stack: ["Node.js", "Redis", "Docker"],
      branches: 12,
      commits: 247,
      contributors: 3,
      description:
        "Core API gateway handling authentication, rate limiting, and request routing for all services",
      updated_at: minutesAgo(2),
    },
    {
      id: "project-3",
      name: "Internal Tools",
      repo_url: "",
      taskCount: 15,
      completedCount: 0,
      in_progress: 7,
      needs_approval: 0,
      tech_stack: ["Python", "Click", "Postgres"],
    },
  ];

  const projectByID = new Map<string, Record<string, unknown>>();
  for (const project of projects) {
    projectByID.set(project.id, {
      ...project,
      description:
        (typeof project.description === "string" && project.description) ||
        "Project detail overview",
      updated_at:
        (typeof project.updated_at === "string" && project.updated_at) || minutesAgo(10),
    });
  }

  const issuesByProjectID = new Map<string, ProjectIssue[]>();
  issuesByProjectID.set("project-1", [
    {
      id: "issue-104",
      issue_number: 104,
      title: "Add user authentication flow",
      approval_state: "ready_for_review",
      priority: "P1",
      owner_agent_id: "Agent-042",
      last_activity_at: minutesAgo(15),
      state: "open",
      origin: "local",
      kind: "issue",
      project_id: "project-1",
    },
    {
      id: "issue-311",
      issue_number: 311,
      title: "Update documentation",
      approval_state: "draft",
      priority: "P3",
      owner_agent_id: "Agent-127",
      last_activity_at: minutesAgo(90),
      state: "open",
      origin: "local",
      kind: "issue",
      project_id: "project-1",
    },
  ]);

  issuesByProjectID.set("project-2", [
    {
      id: "issue-209",
      issue_number: 209,
      title: "Fix API rate limiting",
      approval_state: "ready_for_review",
      priority: "P1",
      owner_agent_id: "Agent-007",
      last_activity_at: minutesAgo(5),
      state: "open",
      origin: "local",
      kind: "issue",
      project_id: "project-2",
    },
    {
      id: "issue-234",
      issue_number: 234,
      title: "Auth flow refactor",
      approval_state: "draft",
      priority: "P1",
      owner_agent_id: "Agent-127",
      last_activity_at: minutesAgo(70),
      state: "open",
      origin: "local",
      kind: "issue",
      project_id: "project-2",
    },
  ]);

  issuesByProjectID.set("project-3", []);

  return {
    inbox,
    projects,
    projectByID,
    issuesByProjectID,
  };
}

export async function installCoreDataApiMocks(page: Page): Promise<void> {
  const fixtures = buildCoreDataFixtures();

  await page.route("**/api/inbox**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(fixtures.inbox),
    });
  });

  await page.route("**/api/approvals/exec/*/respond**", async (route) => {
    const requestURL = new URL(route.request().url());
    const pathParts = requestURL.pathname.split("/");
    const approvalID = decodeURIComponent(pathParts[pathParts.length - 2] || "");
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        id: approvalID,
      }),
    });
  });

  await page.route("**/api/projects**", async (route) => {
    const requestURL = new URL(route.request().url());
    if (requestURL.pathname === "/api/projects") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          projects: fixtures.projects,
          total: fixtures.projects.length,
        }),
      });
      return;
    }

    await route.fallback();
  });

  await page.route("**/api/projects/**", async (route) => {
    const requestURL = new URL(route.request().url());
    const parts = requestURL.pathname.split("/").filter(Boolean);
    const projectID = decodeURIComponent(parts[2] || "");
    if (!projectID) {
      await route.fallback();
      return;
    }

    const payload = fixtures.projectByID.get(projectID);
    if (!payload) {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "project not found" }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(payload),
    });
  });

  await page.route("**/api/issues**", async (route) => {
    const requestURL = new URL(route.request().url());
    if (requestURL.pathname !== "/api/issues") {
      await route.fallback();
      return;
    }
    const projectID = requestURL.searchParams.get("project_id") || "";
    const items = fixtures.issuesByProjectID.get(projectID) ?? [];
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items,
        total: items.length,
      }),
    });
  });
}
