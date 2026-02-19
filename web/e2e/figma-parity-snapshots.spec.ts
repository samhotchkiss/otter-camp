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
});
