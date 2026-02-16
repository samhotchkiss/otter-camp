import { expect, test } from '@playwright/test';

import { resolveApiBaseUrl } from './api-base-url';

test.describe('resolveApiBaseUrl', () => {
  test('defaults to IPv4 localhost with port 4200', () => {
    expect(resolveApiBaseUrl({})).toBe('http://127.0.0.1:4200');
  });

  test('uses explicit E2E_API_BASE_URL override', () => {
    expect(resolveApiBaseUrl({ E2E_API_BASE_URL: 'http://localhost:9999/' })).toBe('http://localhost:9999');
  });

  test('uses E2E_API_PORT when base URL is not set', () => {
    expect(resolveApiBaseUrl({ E2E_API_PORT: '4300' })).toBe('http://127.0.0.1:4300');
  });
});
