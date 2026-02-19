/**
 * API Client for Otter Camp
 * Connects to the Go backend at api.otter.camp
 */

import { isDemoMode } from './demo';

const browserOrigin = typeof window !== "undefined" ? window.location.origin : "";
const isLocalhost = typeof window !== "undefined" &&
  (window.location.hostname === "localhost" || window.location.hostname === "127.0.0.1");
export const API_URL = isLocalhost ? browserOrigin : (import.meta.env.VITE_API_URL || browserOrigin);
const hostedBaseDomain = "otter.camp";
const hostedReservedSubdomains = new Set(["api", "www"]);
const hostedOrgSlugPattern = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

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

export function hostedOrgSlugFromHostname(hostname: string): string {
  const normalizedHost = hostname.trim().toLowerCase().replace(/\.$/, "");
  if (!normalizedHost || normalizedHost === hostedBaseDomain) {
    return "";
  }

  const suffix = `.${hostedBaseDomain}`;
  if (!normalizedHost.endsWith(suffix)) {
    return "";
  }

  const slug = normalizedHost.slice(0, -suffix.length);
  if (!slug || slug.includes(".") || hostedReservedSubdomains.has(slug)) {
    return "";
  }
  if (!hostedOrgSlugPattern.test(slug)) {
    return "";
  }

  return slug;
}

function hostedOrgSlugFromWindow(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return hostedOrgSlugFromHostname(window.location.hostname);
}

export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const token = localStorage.getItem('otter_camp_token');
  const orgId = localStorage.getItem('otter-camp-org-id');
  const hostedOrgSlug = hostedOrgSlugFromWindow();
  const shouldSendOrgHeader = !hostedOrgSlug;

  let res: Response;
  try {
    res = await fetch(`${API_URL}${path}`, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        ...(token && { Authorization: `Bearer ${token}` }),
        ...(shouldSendOrgHeader && orgId && { 'X-Org-ID': orgId }),
        ...(hostedOrgSlug && { 'X-Otter-Org': hostedOrgSlug }),
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

export interface IssueSummary {
  id: string;
  project_id: string;
  issue_number: number;
  title: string;
  state: string;
  origin: string;
  approval_state?: string;
  kind: string;
  owner_agent_id?: string | null;
  work_status?: string;
  priority?: string;
  last_activity_at?: string;
}

export interface IssueListResponse {
  items: IssueSummary[];
  total: number;
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

export interface RecentActivityApiItem {
  id: string;
  org_id: string;
  agent_id: string;
  session_key?: string;
  trigger: string;
  channel?: string;
  summary: string;
  detail?: string;
  project_id?: string;
  issue_id?: string;
  issue_number?: number;
  thread_id?: string;
  tokens_used?: number;
  model_used?: string;
  duration_ms?: number;
  status?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface RecentActivityResponse {
  items: RecentActivityApiItem[];
  total?: number;
  next_before?: string;
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

export interface AdminAgentSummary {
  id: string;
  workspace_agent_id: string;
  name: string;
  status: string;
  is_ephemeral: boolean;
  project_id?: string | null;
  model?: string;
  context_tokens?: number;
  total_tokens?: number;
  heartbeat_every?: string;
  channel?: string;
  session_key?: string;
  last_seen?: string;
}

export interface AdminAgentsResponse {
  agents: AdminAgentSummary[];
  total: number;
}

export interface AdminAgentDetailResponse {
  agent?: AdminAgentSummary;
  sync?: {
    current_task?: string;
    context_tokens?: number;
    total_tokens?: number;
    last_seen?: string;
    updated_at?: string;
  };
}

export interface MemoryEntry {
  id: string;
  agent_id: string;
  kind: string;
  title: string;
  content: string;
  metadata?: unknown;
  importance?: number;
  confidence?: number;
  sensitivity?: string;
  status?: string;
  occurred_at?: string;
  source_project?: string | null;
  source_issue?: string | null;
  created_at?: string;
  updated_at?: string;
  relevance?: number;
}

export interface MemoryEntriesResponse {
  items: MemoryEntry[];
  total: number;
}

export interface MemoryEvent {
  id: number;
  event_type: string;
  payload?: unknown;
  created_at: string;
}

export interface MemoryEventsResponse {
  items: MemoryEvent[];
  total: number;
}

export interface KnowledgeEntry {
  id: string;
  title: string;
  content: string;
  tags?: string[];
  created_by?: string;
  created_at?: string;
  updated_at?: string;
}

export interface KnowledgeResponse {
  items: KnowledgeEntry[];
  total: number;
}

export interface TaxonomyNode {
  id: string;
  org_id: string;
  parent_id?: string | null;
  slug: string;
  display_name: string;
  description?: string | null;
  depth: number;
}

export interface TaxonomyNodesResponse {
  nodes: TaxonomyNode[];
}

export interface TaxonomySubtreeMemory {
  memory_id: string;
  kind: string;
  title: string;
  content: string;
  source_conversation_id?: string | null;
  source_project_id?: string | null;
}

export interface TaxonomySubtreeMemoriesResponse {
  memories: TaxonomySubtreeMemory[];
}

export interface ProjectTreeEntry {
  name: string;
  type: string;
  path: string;
  size?: number;
}

export interface AgentMemoryFilesResponse {
  ref: string;
  path: string;
  entries: ProjectTreeEntry[];
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
  activityRecent: (limit = 30) => {
    const params = new URLSearchParams(getOrgQueryParam().replace(/^\?/, ""));
    params.set("limit", String(limit));
    const query = params.toString();
    const path = query ? `/api/activity/recent?${query}` : "/api/activity/recent";
    return apiFetch<RecentActivityResponse>(path);
  },
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
  project: (id: string) => {
    const params = new URLSearchParams(getOrgQueryParam().replace(/^\?/, ""));
    const query = params.toString();
    const path = query
      ? `/api/projects/${encodeURIComponent(id)}?${query}`
      : `/api/projects/${encodeURIComponent(id)}`;
    return apiFetch<Project>(path);
  },
  issues: (options: { projectID: string; state?: string; limit?: number }) => {
    const params = new URLSearchParams(getOrgQueryParam().replace(/^\?/, ""));
    params.set("project_id", options.projectID);
    if (options.state) {
      params.set("state", options.state);
    }
    if (typeof options.limit === "number" && Number.isFinite(options.limit)) {
      params.set("limit", String(Math.max(1, Math.floor(options.limit))));
    }
    const query = params.toString();
    const path = query ? `/api/issues?${query}` : "/api/issues";
    return apiFetch<IssueListResponse>(path);
  },
  syncAgents: () => apiFetch<SyncAgentsResponse>(`/api/sync/agents`),
  adminAgents: () => apiFetch<AdminAgentsResponse>(`/api/admin/agents`),
  adminAgent: (id: string) => apiFetch<AdminAgentDetailResponse>(`/api/admin/agents/${encodeURIComponent(id)}`),
  adminAgentMemoryFiles: (id: string) => apiFetch<AgentMemoryFilesResponse>(`/api/admin/agents/${encodeURIComponent(id)}/memory`),
  memoryEntries: (agentID: string, options: { kind?: string; limit?: number; offset?: number } = {}) => {
    const params = new URLSearchParams();
    params.set("agent_id", agentID);
    if (options.kind) {
      params.set("kind", options.kind);
    }
    if (typeof options.limit === "number" && Number.isFinite(options.limit)) {
      params.set("limit", String(Math.max(1, Math.floor(options.limit))));
    }
    if (typeof options.offset === "number" && Number.isFinite(options.offset)) {
      params.set("offset", String(Math.max(0, Math.floor(options.offset))));
    }
    return apiFetch<MemoryEntriesResponse>(`/api/memory/entries?${params.toString()}`);
  },
  memoryEvents: (limit = 100) => {
    const bounded = Math.max(1, Math.floor(limit));
    return apiFetch<MemoryEventsResponse>(`/api/memory/events?limit=${bounded}`);
  },
  knowledge: (limit = 200) => {
    const bounded = Math.max(1, Math.floor(limit));
    return apiFetch<KnowledgeResponse>(`/api/knowledge?limit=${bounded}`);
  },
  taxonomyNodes: (parentID?: string) => {
    const params = new URLSearchParams();
    if (parentID && parentID.trim()) {
      params.set("parent_id", parentID.trim());
    }
    const query = params.toString();
    const path = query ? `/api/taxonomy/nodes?${query}` : "/api/taxonomy/nodes";
    return apiFetch<TaxonomyNodesResponse>(path);
  },
  taxonomyNodeMemories: (id: string) => apiFetch<TaxonomySubtreeMemoriesResponse>(`/api/taxonomy/nodes/${encodeURIComponent(id)}/memories`),
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
