import { test, expect } from '@playwright/test';

import { resolveApiHealthUrl } from './api-base-url';

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
    try {
      const response = await request.get(resolveApiHealthUrl());
      expect(response.ok()).toBeTruthy();
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      test.skip(message.includes('ECONNREFUSED'), 'API server not running for frontend-only e2e run');
      throw error;
    }
  });
});
