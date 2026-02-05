/**
 * API Client for Otter Camp
 * Connects to the Go backend at api.otter.camp
 */

import { isDemoMode } from './demo';

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

/**
 * Get demo query param based on hostname or explicit flag
 */
function getDemoQueryParam(): string {
  return isDemoMode() ? '?demo=true' : '';
}

export interface ApiError extends Error {
  status: number;
}

export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const token = localStorage.getItem('otter_camp_token');
  const orgId = localStorage.getItem('otter-camp-org-id');
  const res = await fetch(`${API_URL}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(token && { Authorization: `Bearer ${token}` }),
      ...(orgId && { 'X-Org-ID': orgId }),
      ...options?.headers,
    },
  });
  
  if (!res.ok) {
    const error = new Error(`API error: ${res.status}`) as ApiError;
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

export interface Approval {
  id: string;
  type: string;
  command?: string;
  agent: string;
  status: string;
  createdAt: string;
}

export interface HealthResponse {
  status: string;
  version?: string;
}

export interface FeedResponse {
  actionItems: ActionItem[];
  feedItems: FeedItem[];
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
  // Use demo mode based on hostname (demo.otter.camp) or for MVP testing
  feed: () => apiFetch<FeedResponse>(`/api/feed${getDemoQueryParam()}`),
  tasks: () => apiFetch<Task[]>(`/api/tasks${getDemoQueryParam()}`),
  approvals: () => apiFetch<Approval[]>(`/api/approvals/exec${getDemoQueryParam()}`),
  
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
