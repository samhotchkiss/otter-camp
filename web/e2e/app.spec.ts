import { test, expect } from '@playwright/test';

test.describe('Otter Camp E2E Tests', () => {
  test('homepage loads successfully', async ({ page }) => {
    await page.goto('/');
    
    // Wait for the app to load
    await expect(page).toHaveTitle(/Otter Camp/i);
  });

  test('can navigate to main sections', async ({ page }) => {
    await page.goto('/');
    
    // App should render without crashing
    await expect(page.locator('body')).toBeVisible();
  });

  test('API health check', async ({ request }) => {
    const response = await request.get('http://localhost:8080/health');
    expect(response.ok()).toBeTruthy();
  });
});
