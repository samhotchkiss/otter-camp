type ApiUrlEnv = {
  E2E_API_BASE_URL?: string;
  E2E_API_PORT?: string;
};

export function resolveApiBaseUrl(env: ApiUrlEnv = process.env): string {
  const baseUrl = env.E2E_API_BASE_URL?.trim();
  if (baseUrl) {
    return baseUrl.replace(/\/+$/, '');
  }

  const port = env.E2E_API_PORT?.trim() || '4200';
  return `http://127.0.0.1:${port}`;
}

export function resolveApiHealthUrl(env: ApiUrlEnv = process.env): string {
  return `${resolveApiBaseUrl(env)}/health`;
}
