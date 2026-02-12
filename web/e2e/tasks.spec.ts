import { test, expect } from "@playwright/test";
import { bootstrapAuthenticatedSession } from "./helpers/auth";

test.describe("Task Detail Route", () => {
  test.beforeEach(async ({ page }) => {
    await bootstrapAuthenticatedSession(page);
  });

  test("loads sample task detail for known task id", async ({ page }) => {
    await page.goto("/tasks/task-1");

    await expect(page.getByRole("heading", { name: /set up camp perimeter/i })).toBeVisible();
    await expect(page.getByText(/mark boundaries and secure the area/i)).toBeVisible();
    await expect(page.getByRole("button", { name: /go back/i })).toBeVisible();
  });

  test("shows not-found state for unknown task id", async ({ page }) => {
    await page.goto("/tasks/task-does-not-exist");

    await expect(page.getByRole("heading", { name: /task not found/i })).toBeVisible();
    await expect(page.getByRole("link", { name: /back to projects/i })).toBeVisible();
  });

  test("supports inline task title editing", async ({ page }) => {
    await page.goto("/tasks/task-1");

    await page.getByRole("button", { name: /edit/i }).click();
    const titleInput = page.getByRole("textbox", { name: /^Task title$/i });

    await titleInput.fill("Set up camp perimeter - updated");
    await page.getByRole("button", { name: /^save$/i }).click();

    await expect(page.getByRole("heading", { name: /set up camp perimeter - updated/i })).toBeVisible();
  });
});
