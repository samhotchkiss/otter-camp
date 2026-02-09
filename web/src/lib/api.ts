/**
 * API Client for Otter Camp
 * Connects to the Go backend at api.otter.camp
 */

import { isDemoMode } from './demo';

const browserOrigin = typeof window !== "undefined" ? window.location.origin : "";
export const API_URL = import.meta.env.VITE_API_URL || browserOrigin;

/**
 * Get query params for API calls that need org_id
 * Returns ?demo=true for demo mode, or ?org_id=<uuid> for authenticated users
 */
function getOrgQueryParam(): string {
  if (isDemoMode()) {
    return '?demo=true';
  }
  const orgId = localStorage.getItem('otter-camp-org-id');
  return orgId ? `?org_id=${orgId}` : '';
}

export interface ApiError extends Error {
  status: number;
}

export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const token = localStorage.getItem('otter_camp_token');
  const orgId = localStorage.getItem('otter-camp-org-id');

  let res: Response;
  try {
    res = await fetch(`${API_URL}${path}`, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        ...(token && { Authorization: `Bearer ${token}` }),
        ...(orgId && { 'X-Org-ID': orgId }),
        ...options?.headers,
      },
    });
  } catch (err) {
    if (err instanceof TypeError) {
      throw new Error('Connection failed');
    }
    throw err;
  }

  if (!res.ok) {
    let message = `API error: ${res.status}`;
    const contentType = res.headers.get('Content-Type') || '';
    if (contentType.includes('application/json')) {
      const body = await res.json().catch(() => null);
      if (body?.error) {
        message = body.error;
      } else if (body?.message) {
        message = body.message;
      }
    }

    const error = new Error(message) as ApiError;
    error.status = res.status;
    throw error;
  }

  return res.json();
}

// Type definitions matching backend models
export interface ActionItem {
  id: string;
  icon: string;
  project: string;
  time: string;
  agent: string;
  message: string;
  primaryAction: string;
  secondaryAction: string;
}

export interface FeedItem {
  id: string;
  avatar: string;
  avatarBg: string;
  title: string;
  text: string;
  meta: string;
  type: {
    label: string;
    className: string;
  } | null;
}

export interface Task {
  id: string;
  title: string;
  status: string;
  priority: string;
  agent?: string;
  project?: string;
}

export interface Project {
  id: string;
  name: string;
  description?: string | null;
  status?: string;
  taskCount?: number;
  completedCount?: number;
  labels?: Label[];
}

export interface Label {
  id: string;
  name: string;
  color: string;
}

export interface Approval {
  id: string;
  type: string;
  command?: string;
  agent: string;
  status: string;
  createdAt: string;
}

export interface InboxResponse {
  items: Approval[];
}

export interface HealthResponse {
  status: string;
  version?: string;
}

export interface FeedResponse {
  actionItems: ActionItem[];
  feedItems: FeedItem[];
}

export interface FeedApiItem {
  id: string;
  org_id: string;
  task_id?: string | null;
  agent_id?: string | null;
  type: string;
  metadata?: unknown;
  created_at: string;
  task_title?: string | null;
  agent_name?: string | null;
  summary?: string | null;
  score?: number | null;
  priority?: string | null;
}

export interface PaginatedFeedResponse {
  org_id: string;
  feed_mode?: string;
  types?: string[];
  from?: string | null;
  to?: string | null;
  limit?: number;
  offset?: number;
  total?: number;
  items: FeedApiItem[];
}

export type DashboardFeedResponse = FeedResponse | PaginatedFeedResponse;

export interface SyncAgentsResponse {
  last_sync?: string;
  last_sync_age_seconds?: number;
  bridge_status?: 'healthy' | 'degraded' | 'unhealthy';
  sync_healthy?: boolean;
}

export interface AdminConnectionsResponse {
  bridge?: {
    connected?: boolean;
    sync_healthy?: boolean;
    status?: 'healthy' | 'degraded' | 'unhealthy';
    last_sync?: string;
    last_sync_age_seconds?: number;
  };
}

export interface ApprovalResponse {
  success: boolean;
  message?: string;
}

export interface CreateTaskInput {
  title: string;
  description?: string;
  priority?: string;
  agent?: string;
  project?: string;
}

export interface CreateTaskResponse {
  id: string;
  title: string;
  status: string;
  priority?: string;
  createdAt: string;
}

// API methods
export const api = {
  health: () => apiFetch<HealthResponse>('/health'),
  // Pass org_id to get real data, or demo=true for demo mode
  feed: () => apiFetch<DashboardFeedResponse>(`/api/feed${getOrgQueryParam()}`),
  tasks: () => apiFetch<Task[]>(`/api/tasks${getOrgQueryParam()}`),
  inbox: () => apiFetch<InboxResponse>(`/api/inbox${getOrgQueryParam()}`),
  approvals: () => apiFetch<Approval[]>(`/api/approvals/exec${getOrgQueryParam()}`),
  projects: (labels: string[] = []) => {
    const params = new URLSearchParams(getOrgQueryParam().replace(/^\?/, ""));
    for (const labelID of labels) {
      const normalized = labelID.trim();
      if (!normalized) {
        continue;
      }
      params.append("label", normalized);
    }
    const query = params.toString();
    const path = query ? `/api/projects?${query}` : "/api/projects";
    return apiFetch<{ projects: Project[] }>(path);
  },
  syncAgents: () => apiFetch<SyncAgentsResponse>(`/api/sync/agents`),
  adminConnections: () => apiFetch<AdminConnectionsResponse>(`/api/admin/connections`),
  
  // Approval actions
  approveItem: (id: string) => apiFetch<ApprovalResponse>(`/api/approvals/exec/${id}/respond`, {
    method: 'POST',
    body: JSON.stringify({ action: 'approve' }),
  }),
  rejectItem: (id: string) => apiFetch<ApprovalResponse>(`/api/approvals/exec/${id}/respond`, {
    method: 'POST',
    body: JSON.stringify({ action: 'reject' }),
  }),
  
  // Task actions
  createTask: (input: CreateTaskInput) => apiFetch<CreateTaskResponse>('/api/tasks', {
    method: 'POST',
    body: JSON.stringify(input),
  }),
};

export default api;
