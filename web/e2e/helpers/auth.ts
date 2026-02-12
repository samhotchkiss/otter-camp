import type { Page } from "@playwright/test";

type AuthUser = {
  id: string;
  email: string;
  name: string;
};

type AuthOptions = {
  token?: string;
  orgId?: string;
  user?: AuthUser;
  expiresAt?: string;
  mockShellApis?: boolean;
};

const DEFAULT_TOKEN = "oc_sess_e2e_token";
const DEFAULT_ORG_ID = "org-e2e";
const ONBOARDING_COMPLETE_KEY = "otter_camp_onboarding_complete";
const DEFAULT_USER: AuthUser = {
  id: "user-e2e",
  email: "test@example.com",
  name: "Test User",
};

export async function bootstrapAuthenticatedSession(
  page: Page,
  options: AuthOptions = {},
): Promise<void> {
  const token = (options.token ?? DEFAULT_TOKEN).trim();
  const orgId = (options.orgId ?? DEFAULT_ORG_ID).trim();
  const user = options.user ?? DEFAULT_USER;
  const expiresAt =
    options.expiresAt ?? new Date(Date.now() + 60 * 60 * 1000).toISOString();
  const mockShellApis = options.mockShellApis ?? true;

  await page.route("**/api/auth/validate**", async (route) => {
    const requestURL = new URL(route.request().url());
    const candidateToken = (requestURL.searchParams.get("token") ?? "").trim();
    if (!candidateToken) {
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "missing token" }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        user_id: user.id,
        email: user.email,
        name: user.name,
        org_id: orgId,
        session_token: candidateToken,
        expires_at: expiresAt,
      }),
    });
  });

  await page.addInitScript(
    ({ seedToken, seedUser, seedOrgId, seedExpiresAt, onboardingCompleteKey }) => {
      localStorage.setItem("otter_camp_token", seedToken);
      localStorage.setItem("otter_camp_user", JSON.stringify(seedUser));
      localStorage.setItem("otter_camp_token_expires_at", seedExpiresAt);
      localStorage.setItem("otter-camp-org-id", seedOrgId);
      localStorage.setItem(onboardingCompleteKey, "true");
    },
    {
      seedToken: token,
      seedUser: user,
      seedOrgId: orgId,
      seedExpiresAt: expiresAt,
      onboardingCompleteKey: ONBOARDING_COMPLETE_KEY,
    },
  );

  if (!mockShellApis) {
    return;
  }

  // Keep app shell network calls deterministic for route-level E2E tests.
  await page.route("**/api/inbox**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    });
  });

  await page.route("**/api/admin/connections", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        bridge: { connected: true, sync_healthy: true, status: "healthy" },
      }),
    });
  });

  await page.route("**/api/feed**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ actionItems: [], feedItems: [] }),
    });
  });

  await page.route("**/api/activity/recent**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    });
  });

  await page.route("**/api/emissions/recent**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [] }),
    });
  });

  await page.route("**/api/projects**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ projects: [] }),
    });
  });

  await page.route("**/api/chats**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ chats: [] }),
    });
  });

  await page.route("**/api/sync/agents**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ bridge_status: "healthy", sync_healthy: true }),
    });
  });

  await page.route("**/api/agents**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [] }),
    });
  });

  await page.route("**/api/notifications**", async (route) => {
    if (route.request().method() !== "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([]),
    });
  });
}
