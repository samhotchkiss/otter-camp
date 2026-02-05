/**
 * API Client for Otter Camp
 * Connects to the Go backend at api.otter.camp
 */

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

export interface ApiError extends Error {
  status: number;
}

export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const token = localStorage.getItem('token');
  const res = await fetch(`${API_URL}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(token && { Authorization: `Bearer ${token}` }),
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

// API methods
export const api = {
  health: () => apiFetch<HealthResponse>('/health'),
  feed: () => apiFetch<FeedResponse>('/feed'),
  tasks: () => apiFetch<Task[]>('/tasks'),
  approvals: () => apiFetch<Approval[]>('/approvals/exec'),
};

export default api;
