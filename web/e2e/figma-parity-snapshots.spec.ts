import { expect, test, type Page } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";
import { installCoreDataApiMocks } from "./helpers/coreDataRoutes";

const VIEWPORTS = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "tablet", width: 1024, height: 768 },
  { name: "mobile", width: 390, height: 844 },
] as const;

async function installDeterministicWebSocket(page: Page): Promise<void> {
  await page.addInitScript(() => {
    class DeterministicWebSocket {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      readonly url: string;
      readyState = DeterministicWebSocket.CONNECTING;
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent<string>) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      onclose: ((event: CloseEvent) => void) | null = null;

      constructor(url: string | URL) {
        this.url = String(url);
      }

      send(): void {}

      close(): void {
        if (this.readyState === DeterministicWebSocket.CLOSED) {
          return;
        }
        this.readyState = DeterministicWebSocket.CLOSED;
        this.onclose?.(new CloseEvent("close"));
      }

      addEventListener(): void {}

      removeEventListener(): void {}

      dispatchEvent(): boolean {
        return true;
      }
    }

    Object.defineProperty(window, "WebSocket", {
      value: DeterministicWebSocket,
      configurable: true,
      writable: true,
    });
  });
}

async function normalizeEditorViewport(page: Page): Promise<void> {
  await page.evaluate(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
    const main = document.getElementById("main-content");
    if (main instanceof HTMLElement) {
      main.scrollTop = 0;
    }
    const source = document.querySelector('[data-testid="source-textarea"]');
    if (source instanceof HTMLElement) {
      source.blur();
      source.scrollTop = 0;
    }
  });
}

test.describe("Figma parity snapshots", () => {
  test.beforeEach(async ({ page }) => {
    await installDeterministicWebSocket(page);
    await bootstrapAuthenticatedSession(page);
    await installCoreDataApiMocks(page);
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

  test("figma-parity-issue screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/issue/ISS-209");
      await expect(page.getByRole("heading", { name: "Fix API rate limiting" })).toBeVisible();
      await normalizeEditorViewport(page);
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-issue-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });

  test("figma-parity-review screenshot baselines", async ({ page }) => {
    for (const viewport of VIEWPORTS) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto("/review/docs%2Frate-limiting-implementation.md");
      await expect(page.getByRole("heading", { name: "Content Review" })).toBeVisible();
      await normalizeEditorViewport(page);
      await expect(page.getByTestId("shell-layout")).toHaveScreenshot(`figma-parity-review-${viewport.name}.png`, {
        animations: "disabled",
      });
    }
  });
});
