/**
 * Demo mode detection
 * 
 * When running on demo.otter.camp:
 * - Skip auth checks
 * - Always use ?demo=true on API calls
 * - Show demo banner
 */

const DEMO_HOSTNAMES = [
  'demo.otter.camp',
  'demo.localhost', // For local testing
];

/**
 * Check if the app is running in demo mode based on hostname
 */
export function isDemoMode(): boolean {
  if (typeof window === 'undefined') return false;
  return DEMO_HOSTNAMES.includes(window.location.hostname);
}

/**
 * Get the demo query param to append to API calls
 * Returns '?demo=true' or '&demo=true' depending on existing params
 */
export function getDemoParam(existingParams: boolean = false): string {
  if (!isDemoMode()) return '';
  return existingParams ? '&demo=true' : '?demo=true';
}

/**
 * Append demo param to a URL if in demo mode
 */
export function withDemoParam(url: string): string {
  if (!isDemoMode()) return url;
  const hasParams = url.includes('?');
  return url + (hasParams ? '&demo=true' : '?demo=true');
}
