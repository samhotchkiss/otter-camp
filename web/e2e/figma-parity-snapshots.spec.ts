import { expect, test } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

const VIEWPORTS = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "tablet", width: 1024, height: 768 },
  { name: "mobile", width: 390, height: 844 },
] as const;

test.describe("Figma parity snapshots", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
    await page.addStyleTag({
      content:
        "*, *::before, *::after { animation: none !important; transition: none !important; caret-color: transparent !important; }",
    });
  });

  test("figma-parity-shell screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/projects");
      await expect(page.getByRole("heading", { name: "Projects", exact: true })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-shell-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });

  test("figma-parity-inbox screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/inbox");
      await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-inbox-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });

  test("figma-parity-projects screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/projects");
      await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-projects-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });

  test("figma-parity-chat screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/inbox");
      await expect(page.getByRole("heading", { name: "Inbox" })).toBeVisible();

      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-chat-closed-${viewport.name}.png`, {
        animations: "disabled",
      });

      const openButton = page.getByRole("button", { name: "Open global chat" });
      if (await openButton.isVisible().catch(() => false)) {
        await openButton.click();
      }

      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-chat-open-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });

  test("figma-parity-project-detail screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/projects/project-2");
      await expect(page.getByRole("heading", { name: "API Gateway" })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(
        `figma-parity-project-detail-${viewport.name}.png`,
        {
          animations: "disabled",
        },
      );
    }
  });

  test("figma-parity-secondary screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });

      await page.goto("/agents");
      await expect(page.getByRole("heading", { name: "Agent Status" })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-secondary-agents-${viewport.name}.png`, {
        animations: "disabled",
      });

      await page.goto("/knowledge");
      await expect(page.getByRole("heading", { name: "Memory System" })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-secondary-memory-${viewport.name}.png`, {
        animations: "disabled",
      });

      await page.goto("/connections");
      await expect(page.getByRole("heading", { name: "Operations" })).toBeVisible();
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-secondary-ops-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });
});
