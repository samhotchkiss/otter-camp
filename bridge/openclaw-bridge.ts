#!/usr/bin/env npx tsx
/**
 * OpenClaw <-> Otter Camp Bridge
 *
 * Responsibilities:
 * 1) Pull sessions from OpenClaw and push sync snapshots to Otter Camp.
 * 2) Keep /ws/openclaw connected so Otter Camp can dispatch dm.message events.
 * 3) Forward dm.message events to OpenClaw via chat.send.
 * 4) Persist OpenClaw assistant replies back into Otter Camp DM threads.
 *
 * Usage:
 *   OPENCLAW_TOKEN=... OTTERCAMP_URL=https://api.otter.camp npx tsx bridge/openclaw-bridge.ts
 *   OPENCLAW_TOKEN=... OPENCLAW_WS_SECRET=... npx tsx bridge/openclaw-bridge.ts --continuous
 */

import WebSocket from 'ws';
import crypto from 'node:crypto';
import { execFile } from 'node:child_process';
import http from 'node:http';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

const OPENCLAW_HOST = process.env.OPENCLAW_HOST || '127.0.0.1';
const OPENCLAW_PORT = process.env.OPENCLAW_PORT || '18789';
const OPENCLAW_TOKEN = process.env.OPENCLAW_TOKEN || '';
const OTTERCAMP_URL = process.env.OTTERCAMP_URL || 'https://api.otter.camp';
const OTTERCAMP_TOKEN = process.env.OTTERCAMP_TOKEN || '';
export function resolveOtterCampWSSecret(env: NodeJS.ProcessEnv = process.env): string {
  const openClawSecret = (env.OPENCLAW_WS_SECRET || '').trim();
  if (openClawSecret) {
    return openClawSecret;
  }
  return (env.OTTERCAMP_WS_SECRET || '').trim();
}
const OTTERCAMP_WS_SECRET = resolveOtterCampWSSecret(process.env);
const OTTER_PROGRESS_LOG_PATH = (process.env.OTTER_PROGRESS_LOG_PATH || '').trim();
const FETCH_RETRY_DELAYS_MS = [300, 900, 2000];
const COMPACTION_RECOVERY_RETRY_DELAYS_MS = [200, 600, 1500];
const COMPACTION_RECOVERY_DEDUP_WINDOW_MS = 5 * 60 * 1000;
const MAX_TRACKED_COMPACTION_RECOVERY_KEYS = 500;
const MAX_TRACKED_RUN_IDS = 2000;
const MAX_TRACKED_PROGRESS_LOG_HASHES = 4000;
const SYNC_INTERVAL_MS = (() => {
  const raw = Number.parseInt((process.env.OTTER_SYNC_INTERVAL_MS || '').trim(), 10);
  if (!Number.isFinite(raw) || raw < 1000) {
    return 10000;
  }
  return raw;
})();
const PROGRESS_LOG_MAX_LINES_PER_SYNC = (() => {
  const raw = Number.parseInt((process.env.OTTER_PROGRESS_LOG_MAX_LINES || '').trim(), 10);
  if (!Number.isFinite(raw) || raw <= 0) {
    return 50;
  }
  return raw;
})();
const DISPATCH_QUEUE_POLL_INTERVAL_MS = 5000;
const ACTIVITY_EVENT_FLUSH_INTERVAL_MS = 5000;
const ACTIVITY_EVENTS_BATCH_SIZE = 200;
const MAX_TRACKED_ACTIVITY_EVENT_IDS = 5000;
const RECONNECT_MAX_DELAY_MS = 30000;
const RECONNECT_JITTER_SPREAD = 0.2;
const RECONNECT_WARNING_THRESHOLD = 5;
const RECONNECT_ALERT_THRESHOLD = 30;
const RECONNECT_RESTART_THRESHOLD = 60;
const RESTART_FAILURE_EXIT_THRESHOLD = 2;
const HEARTBEAT_INTERVAL_MS = 10000;
const HEARTBEAT_PONG_TIMEOUT_MS = 5000;
const HEARTBEAT_MISS_THRESHOLD = 2;
const DISPATCH_REPLAY_MAX_ITEMS = 1000;
const DISPATCH_REPLAY_MAX_BYTES = 10 * 1024 * 1024;
const MAX_TRACKED_DISPATCH_REPLAY_IDS = 5000;
const BRIDGE_HEALTH_PORT = (() => {
  const raw = Number.parseInt((process.env.OTTER_BRIDGE_HEALTH_PORT || '').trim(), 10);
  if (!Number.isFinite(raw) || raw <= 0) {
    return 8787;
  }
  return raw;
})();
const MAX_TRACKED_SESSION_CONTEXTS = (() => {
  const raw = Number.parseInt((process.env.OTTER_SESSION_CONTEXT_MAX || '').trim(), 10);
  if (!Number.isFinite(raw) || raw <= 0) {
    return 5000;
  }
  return raw;
})();
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');
const PROJECT_ID_PATTERN = /(?:^|:)project:([0-9a-f-]{36})(?:$|:)/i;
const ISSUE_ID_PATTERN = /(?:^|:)issue:([0-9a-f-]{36})(?:$|:)/i;
const COMPLETION_PROGRESS_LINE_PATTERN = /\bIssue\s+#(\d+)\s+\|\s+Commit\s+([0-9a-f]{7,40})\s+\|\s+([^|]+)\|/i;
const CHAMELEON_SESSION_KEY_PATTERN =
  /^agent:chameleon:oc:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i;
const SUPPORTED_DISPATCH_EVENT_TYPES = new Set([
  'dm.message',
  'project.chat.message',
  'issue.comment.message',
  'admin.command',
]);
const IGNORED_OTTERCAMP_SOCKET_EVENT_TYPES = new Set([
  'connected',
]);
const SAFE_FALLBACK_AGENT_ID_PATTERN =
  /^(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9][a-z0-9_-]{0,63})$/i;
const SAFE_AGENT_SLOT_PATTERN = /^[a-z0-9][a-z0-9_-]{0,63}$/i;
const SAFE_SESSION_FILENAME_PATTERN = /^[a-z0-9][a-z0-9._-]{7,127}$/i;
const HEARTBEAT_PATTERN = /\bheartbeat\b/i;
const CHAT_CHANNELS = new Set(['slack', 'telegram', 'tui', 'discord']);
const OTTERCAMP_ORG_ID = (process.env.OTTERCAMP_ORG_ID || '').trim();
let otterCampOrgIDForTestOverride: string | null = null;
const COMPACT_WHOAMI_MIN_SUMMARY_CHARS = 60;
const IDENTITY_BLOCK_MAX_CHARS = 1200;
const SESSION_TASK_SUMMARY_MAX_CHARS = 96;
const AUTO_RECALL_MAX_RESULTS = (() => {
  const raw = Number.parseInt((process.env.OTTER_MEMORY_RECALL_MAX_RESULTS || '').trim(), 10);
  if (!Number.isFinite(raw) || raw <= 0) {
    return 3;
  }
  return Math.min(raw, 10);
})();
const AUTO_RECALL_MIN_RELEVANCE = (() => {
  const raw = Number.parseFloat((process.env.OTTER_MEMORY_RECALL_MIN_RELEVANCE || '').trim());
  if (!Number.isFinite(raw) || raw < 0 || raw > 1) {
    return 0.7;
  }
  return raw;
})();
const AUTO_RECALL_MAX_CHARS = (() => {
  const raw = Number.parseInt((process.env.OTTER_MEMORY_RECALL_MAX_CHARS || '').trim(), 10);
  if (!Number.isFinite(raw) || raw <= 0) {
    return 2000;
  }
  return Math.min(raw, 8000);
})();

export interface OpenClawSession {
  key: string;
  kind: string;
  channel: string;
  displayName?: string;
  deliveryContext?: Record<string, unknown>;
  updatedAt: number;
  sessionId: string;
  model: string;
  contextTokens: number;
  totalTokens: number;
  systemSent: boolean;
  abortedLastRun?: boolean;
  lastChannel?: string;
  lastTo?: string;
  lastAccountId?: string;
  transcriptPath?: string;
}

export type AgentActivityScope = {
  project_id?: string;
  issue_id?: string;
  issue_number?: number;
  thread_id?: string;
};

export type BridgeAgentActivityEvent = {
  id: string;
  agent_id: string;
  session_key: string;
  trigger: string;
  channel?: string;
  summary: string;
  detail?: string;
  scope?: AgentActivityScope;
  tokens_used: number;
  model_used?: string;
  commit_sha?: string;
  commit_branch?: string;
  commit_remote?: string;
  push_status?: 'succeeded' | 'failed' | 'unknown';
  duration_ms: number;
  status: 'started' | 'completed' | 'failed' | 'timeout';
  started_at: string;
  completed_at?: string;
};

interface OpenClawCronJobSnapshot {
  id: string;
  name?: string;
  schedule?: string;
  session_target?: string;
  payload_type?: string;
  last_run_at?: string;
  last_status?: string;
  next_run_at?: string;
  enabled: boolean;
}

interface BridgeWorkflowProjectSnapshot {
  id: string;
  name: string;
  workflow_enabled?: boolean;
  workflow_schedule?: unknown;
  workflow_template?: unknown;
  workflow_agent_id?: string;
}

interface OpenClawProcessSnapshot {
  id: string;
  command?: string;
  pid?: number;
  status?: string;
  duration_seconds?: number;
  agent_id?: string;
  session_key?: string;
  started_at?: string;
}

interface SessionsListResponse {
  count: number;
  sessions: OpenClawSession[];
}

type PendingRequest = {
  resolve: (value: unknown) => void;
  reject: (reason?: unknown) => void;
};

type DMDispatchEvent = {
  type?: string;
  org_id?: string;
  data?: {
    thread_id?: string;
    session_key?: string;
    content?: string;
    message_id?: string;
    agent_id?: string;
    sender_id?: string;
    sender_type?: string;
    sender_name?: string;
  };
};

type ProjectChatDispatchEvent = {
  type?: string;
  org_id?: string;
  data?: {
    message_id?: string;
    project_id?: string;
    agent_id?: string;
    agent_name?: string;
    session_key?: string;
    content?: string;
    author?: string;
    questionnaire?: unknown;
  };
};

type IssueCommentDispatchEvent = {
  type?: string;
  org_id?: string;
  data?: {
    message_id?: string;
    issue_id?: string;
    project_id?: string;
    issue_number?: number;
    issue_title?: string;
    document_path?: string;
    agent_id?: string;
    agent_name?: string;
    responder_agent_id?: string;
    author_agent_id?: string;
    sender_type?: string;
    session_key?: string;
    content?: string;
    questionnaire?: unknown;
  };
};

export type QuestionnaireQuestion = {
  id: string;
  text: string;
  type: 'text' | 'textarea' | 'boolean' | 'select' | 'multiselect' | 'number' | 'date';
  required?: boolean;
  options?: string[];
};

export type QuestionnairePayload = {
  id: string;
  contextType?: string;
  contextID?: string;
  author?: string;
  title?: string;
  questions: QuestionnaireQuestion[];
  responses?: Record<string, unknown>;
};

type AdminCommandDispatchEvent = {
  type?: string;
  org_id?: string;
  data?: {
    command_id?: string;
    action?: string;
    agent_id?: string;
    session_key?: string;
    job_id?: string;
    process_id?: string;
    enabled?: boolean;
    config_patch?: unknown;
    config_full?: unknown;
    config_hash?: string;
    confirm?: boolean;
    dry_run?: boolean;
  };
};

type DispatchQueueJob = {
  id: number;
  event_type?: string;
  payload?: Record<string, unknown>;
  claim_token?: string;
  attempts?: number;
};

type SessionContext = {
  kind?: 'dm' | 'project_chat' | 'issue_comment';
  orgID?: string;
  threadID?: string;
  agentID?: string;
  agentName?: string;
  projectID?: string;
  issueID?: string;
  issueNumber?: number;
  issueTitle?: string;
  documentPath?: string;
  responderAgentID?: string;
  pendingQuestionnaire?: QuestionnairePayload;
  identityMetadata?: SessionIdentityMetadata;
  displayLabel?: string;
  executionMode?: ExecutionMode;
  projectRoot?: string;
};

type ExecutionMode = 'conversation' | 'project';

type WhoAmITaskPointer = {
  project?: string;
  issue?: string;
  title?: string;
  status?: string;
};

type SessionIdentityMetadata = {
  profile: 'compact' | 'full';
  preamble: string;
  displayLabel?: string;
};

type DeviceIdentity = {
  deviceId: string;
  publicKeyPem: string;
  privateKeyPem: string;
};

export type BridgeConnectionState =
  | 'connecting'
  | 'connected'
  | 'degraded'
  | 'disconnected'
  | 'reconnecting';

export type BridgeConnectionTransitionTrigger =
  | 'connect_attempt'
  | 'socket_open'
  | 'socket_message'
  | 'health_warning'
  | 'heartbeat_missed'
  | 'socket_closed'
  | 'reconnect_scheduled'
  | 'reconnect_timer_fired';

type BridgeSocketRole = 'openclaw' | 'ottercamp';

type SocketReconnectController = {
  timer: ReturnType<typeof setTimeout> | null;
  consecutiveFailures: number;
  totalReconnectAttempts: number;
  firstMessageReceived: boolean;
  lastConnectedAt: number;
  disconnectedSince: number;
  alertEmittedForOutage: boolean;
  restartFailures: number;
};

type SocketHeartbeatController = {
  intervalTimer: ReturnType<typeof setInterval> | null;
  pongTimeoutTimer: ReturnType<typeof setTimeout> | null;
  missedPongs: number;
  lastPingAt: number;
  lastPongAt: number;
  lastMessageAt: number;
};

type DispatchReplayQueueItem = {
  id: string;
  eventType: string;
  payload: Record<string, unknown>;
  sizeBytes: number;
  queuedAtMs: number;
};

export type BridgeConnectionHealthInput = {
  uptimeSeconds: number;
  queueDepth: number;
  lastSuccessfulSyncAtMs: number;
  openclaw: {
    connected: boolean;
    lastConnectedAtMs: number;
    disconnectedSinceMs: number;
    consecutiveFailures: number;
    totalReconnectAttempts: number;
  };
  ottercamp: {
    connected: boolean;
    lastConnectedAtMs: number;
    disconnectedSinceMs: number;
    consecutiveFailures: number;
    totalReconnectAttempts: number;
  };
};

type BridgeHealthPayload = {
  status: 'healthy' | 'degraded' | 'disconnected';
  openclaw: {
    connected: boolean;
    lastConnectedAt: string | null;
    disconnectedSince: string | null;
    consecutiveFailures: number;
    totalReconnectAttempts: number;
  };
  ottercamp: {
    connected: boolean;
    lastConnectedAt: string | null;
    disconnectedSince: string | null;
    consecutiveFailures: number;
    totalReconnectAttempts: number;
  };
  uptime: string;
  lastSuccessfulSync: string | null;
  queueDepth: number;
};

let openClawWS: WebSocket | null = null;
let otterCampWS: WebSocket | null = null;
let healthServer: http.Server | null = null;
let continuousModeEnabled = false;
const processStartedAtMs = Date.now();
const reconnectByRole: Record<BridgeSocketRole, SocketReconnectController> = {
  openclaw: {
    timer: null,
    consecutiveFailures: 0,
    totalReconnectAttempts: 0,
    firstMessageReceived: false,
    lastConnectedAt: 0,
    disconnectedSince: 0,
    alertEmittedForOutage: false,
    restartFailures: 0,
  },
  ottercamp: {
    timer: null,
    consecutiveFailures: 0,
    totalReconnectAttempts: 0,
    firstMessageReceived: false,
    lastConnectedAt: 0,
    disconnectedSince: 0,
    alertEmittedForOutage: false,
    restartFailures: 0,
  },
};
const heartbeatByRole: Record<BridgeSocketRole, SocketHeartbeatController> = {
  openclaw: {
    intervalTimer: null,
    pongTimeoutTimer: null,
    missedPongs: 0,
    lastPingAt: 0,
    lastPongAt: 0,
    lastMessageAt: 0,
  },
  ottercamp: {
    intervalTimer: null,
    pongTimeoutTimer: null,
    missedPongs: 0,
    lastPingAt: 0,
    lastPongAt: 0,
    lastMessageAt: 0,
  },
};
const connectionStateByRole: Record<BridgeSocketRole, BridgeConnectionState> = {
  openclaw: 'disconnected',
  ottercamp: 'disconnected',
};
let isDispatchQueuePolling = false;
let isDispatchReplayFlushing = false;
let isPeriodicSyncRunning = false;
let requestId = 0;
const pendingRequests = new Map<string, PendingRequest>();
const sessionContexts = new Map<string, SessionContext>();
const contextPrimedSessions = new Set<string>();
const deliveredRunIDs = new Set<string>();
const deliveredRunIDOrder: string[] = [];
const progressLogLineHashes = new Set<string>();
const progressLogLineHashOrder: string[] = [];
let progressLogByteOffset = 0;
let progressLogOffsetInitialized = false;
let previousSessionsByKey = new Map<string, OpenClawSession>();
const previousCronLastRunByID = new Map<string, string>();
const lastPatchedWorkflowConfigByCronID = new Map<string, string>();
let cronRunDetectionInitialized = false;
let workflowSyncInProgress = false;
const queuedActivityEventsByOrg = new Map<string, BridgeAgentActivityEvent[]>();
const queuedActivityEventIDs = new Set<string>();
const deliveredActivityEventIDs = new Set<string>();
const deliveredActivityEventIDOrder: string[] = [];
const dispatchReplayQueue: DispatchReplayQueueItem[] = [];
const queuedDispatchReplayIDs = new Set<string>();
const deliveredDispatchReplayIDs = new Set<string>();
const deliveredDispatchReplayIDOrder: string[] = [];
let dispatchReplayQueueBytes = 0;
const recentCompactionRecoveryByKey = new Map<string, number>();
let lastSuccessfulSyncAtMs = 0;
let gitCompletionDefaultsResolved = false;
let gitCompletionBranch = '';
let gitCompletionRemote = '';

const genId = () => `req-${++requestId}`;
const defaultExecFileAsync = promisify(execFile);
let execFileAsync = defaultExecFileAsync;
const defaultProcessExit = (code: number): never => process.exit(code);
let processExitFn: (code: number) => never = defaultProcessExit;

export function setExecFileForTest(
  fn: ((file: string, args: readonly string[], options: { timeout?: number; maxBuffer?: number }, callback: (error: Error | null, stdout?: string, stderr?: string) => void) => void) | null,
): void {
  if (!fn) {
    execFileAsync = defaultExecFileAsync;
    return;
  }
  execFileAsync = promisify(fn);
}

export function setProcessExitForTest(fn: ((code: number) => never) | null): void {
  processExitFn = fn || defaultProcessExit;
}

function resolveConfiguredOtterCampOrgID(): string {
  if (otterCampOrgIDForTestOverride !== null) {
    return otterCampOrgIDForTestOverride;
  }
  return OTTERCAMP_ORG_ID;
}

export function setOtterCampOrgIDForTest(orgID: string | null): void {
  if (orgID === null) {
    otterCampOrgIDForTestOverride = null;
    return;
  }
  otterCampOrgIDForTestOverride = getTrimmedString(orgID);
}

export function computeReconnectDelayMs(attempt: number, randomFn: () => number = Math.random): number {
  const safeAttempt = Number.isFinite(attempt) && attempt >= 0 ? Math.floor(attempt) : 0;
  const baseDelay = Math.min(RECONNECT_MAX_DELAY_MS, 1000 * Math.pow(2, safeAttempt));
  const jitterRandom = Math.min(1, Math.max(0, randomFn()));
  const jitterMultiplier = 1 + ((jitterRandom * 2 - 1) * RECONNECT_JITTER_SPREAD);
  return Math.min(RECONNECT_MAX_DELAY_MS, Math.max(100, Math.round(baseDelay * jitterMultiplier)));
}

export type ReconnectEscalationTier = 'none' | 'warn' | 'alert' | 'restart';

export function reconnectEscalationTierForFailures(consecutiveFailures: number): ReconnectEscalationTier {
  const safeFailures = Number.isFinite(consecutiveFailures) && consecutiveFailures > 0
    ? Math.floor(consecutiveFailures)
    : 0;
  if (safeFailures >= RECONNECT_RESTART_THRESHOLD) {
    return 'restart';
  }
  if (safeFailures >= RECONNECT_ALERT_THRESHOLD) {
    return 'alert';
  }
  if (safeFailures >= RECONNECT_WARNING_THRESHOLD) {
    return 'warn';
  }
  return 'none';
}

export function shouldExitAfterReconnectFailures(consecutiveFailures: number): boolean {
  return reconnectEscalationTierForFailures(consecutiveFailures) === 'restart';
}

export function nextMissedPongCount(currentMissedPongs: number, receivedPong: boolean): number {
  if (receivedPong) {
    return 0;
  }
  const safeCurrent = Number.isFinite(currentMissedPongs) && currentMissedPongs > 0
    ? Math.floor(currentMissedPongs)
    : 0;
  return safeCurrent + 1;
}

export function shouldForceReconnectFromHeartbeat(missedPongs: number): boolean {
  return Number.isFinite(missedPongs) && missedPongs >= HEARTBEAT_MISS_THRESHOLD;
}

function toOptionalISO(timestampMs: number): string | null {
  if (!Number.isFinite(timestampMs) || timestampMs <= 0) {
    return null;
  }
  return new Date(timestampMs).toISOString();
}

function formatUptime(uptimeSeconds: number): string {
  const safeSeconds = Number.isFinite(uptimeSeconds) && uptimeSeconds > 0
    ? Math.floor(uptimeSeconds)
    : 0;
  const hours = Math.floor(safeSeconds / 3600);
  const minutes = Math.floor((safeSeconds % 3600) / 60);
  const seconds = safeSeconds % 60;
  if (hours > 0) {
    return minutes > 0 ? `${hours}h${minutes}m` : `${hours}h`;
  }
  if (minutes > 0) {
    return seconds > 0 ? `${minutes}m${seconds}s` : `${minutes}m`;
  }
  return `${seconds}s`;
}

function classifyBridgeHealthStatus(input: BridgeConnectionHealthInput): 'healthy' | 'degraded' | 'disconnected' {
  if (input.openclaw.connected && input.ottercamp.connected) {
    return 'healthy';
  }
  if (input.openclaw.connected || input.ottercamp.connected) {
    return 'degraded';
  }
  return 'disconnected';
}

export function buildHealthPayload(input: BridgeConnectionHealthInput): BridgeHealthPayload {
  return {
    status: classifyBridgeHealthStatus(input),
    openclaw: {
      connected: input.openclaw.connected,
      lastConnectedAt: toOptionalISO(input.openclaw.lastConnectedAtMs),
      disconnectedSince: toOptionalISO(input.openclaw.disconnectedSinceMs),
      consecutiveFailures: input.openclaw.consecutiveFailures,
      totalReconnectAttempts: input.openclaw.totalReconnectAttempts,
    },
    ottercamp: {
      connected: input.ottercamp.connected,
      lastConnectedAt: toOptionalISO(input.ottercamp.lastConnectedAtMs),
      disconnectedSince: toOptionalISO(input.ottercamp.disconnectedSinceMs),
      consecutiveFailures: input.ottercamp.consecutiveFailures,
      totalReconnectAttempts: input.ottercamp.totalReconnectAttempts,
    },
    uptime: formatUptime(input.uptimeSeconds),
    lastSuccessfulSync: toOptionalISO(input.lastSuccessfulSyncAtMs),
    queueDepth: input.queueDepth,
  };
}

function getDispatchQueueDepthForHealth(): number {
  return dispatchReplayQueue.length;
}

function isConnectedState(state: BridgeConnectionState): boolean {
  return state === 'connected' || state === 'degraded';
}

function buildRuntimeHealthInput(): BridgeConnectionHealthInput {
  const openclawState = connectionStateByRole.openclaw;
  const ottercampState = connectionStateByRole.ottercamp;
  return {
    uptimeSeconds: Math.max(0, Math.floor((Date.now() - processStartedAtMs) / 1000)),
    queueDepth: getDispatchQueueDepthForHealth(),
    lastSuccessfulSyncAtMs,
    openclaw: {
      connected: isConnectedState(openclawState),
      lastConnectedAtMs: reconnectByRole.openclaw.lastConnectedAt,
      disconnectedSinceMs: reconnectByRole.openclaw.disconnectedSince,
      consecutiveFailures: reconnectByRole.openclaw.consecutiveFailures,
      totalReconnectAttempts: reconnectByRole.openclaw.totalReconnectAttempts,
    },
    ottercamp: {
      connected: isConnectedState(ottercampState),
      lastConnectedAtMs: reconnectByRole.ottercamp.lastConnectedAt,
      disconnectedSinceMs: reconnectByRole.ottercamp.disconnectedSince,
      consecutiveFailures: reconnectByRole.ottercamp.consecutiveFailures,
      totalReconnectAttempts: reconnectByRole.ottercamp.totalReconnectAttempts,
    },
  };
}

function startHealthEndpoint(): void {
  if (healthServer) {
    return;
  }

  healthServer = http.createServer((req, res) => {
    const url = req.url || '/';
    if (req.method !== 'GET' || !url.startsWith('/health')) {
      res.statusCode = 404;
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ error: 'not found' }));
      return;
    }

    const payload = buildHealthPayload(buildRuntimeHealthInput());
    res.statusCode = 200;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(payload));
  });

  healthServer.listen(BRIDGE_HEALTH_PORT, () => {
    console.log(`[bridge] health endpoint listening on :${BRIDGE_HEALTH_PORT}`);
  });
}

export function transitionConnectionState(
  currentState: BridgeConnectionState,
  trigger: BridgeConnectionTransitionTrigger,
): BridgeConnectionState {
  switch (trigger) {
    case 'connect_attempt':
      return 'connecting';
    case 'socket_open':
    case 'socket_message':
      return 'connected';
    case 'health_warning':
      return currentState === 'connected' ? 'degraded' : currentState;
    case 'heartbeat_missed':
    case 'socket_closed':
      return 'disconnected';
    case 'reconnect_scheduled':
    case 'reconnect_timer_fired':
      return 'reconnecting';
    default:
      return currentState;
  }
}

function applyConnectionTransition(
  role: BridgeSocketRole,
  trigger: BridgeConnectionTransitionTrigger,
  extra: Record<string, unknown> = {},
): void {
  const previous = connectionStateByRole[role];
  const next = transitionConnectionState(previous, trigger);
  connectionStateByRole[role] = next;
  if (!isConnectedState(previous) && isConnectedState(next)) {
    reconnectByRole[role].lastConnectedAt = Date.now();
    reconnectByRole[role].disconnectedSince = 0;
  }
  if (isConnectedState(previous) && !isConnectedState(next)) {
    if (!Number.isFinite(reconnectByRole[role].disconnectedSince) || reconnectByRole[role].disconnectedSince <= 0) {
      reconnectByRole[role].disconnectedSince = Date.now();
    }
  }
  if (previous === next) {
    return;
  }
  const transitionTimestamp = new Date().toISOString();
  console.log(
    `[bridge] connection.transition role=${role} from=${previous} to=${next} trigger=${trigger} at=${transitionTimestamp} ${JSON.stringify(extra)}`,
  );
}

function clearReconnectTimer(role: BridgeSocketRole): void {
  const controller = reconnectByRole[role];
  if (controller.timer) {
    clearTimeout(controller.timer);
    controller.timer = null;
  }
}

export function resetReconnectStateForTest(role: BridgeSocketRole): void {
  clearReconnectTimer(role);
  reconnectByRole[role].consecutiveFailures = 0;
  reconnectByRole[role].totalReconnectAttempts = 0;
  reconnectByRole[role].firstMessageReceived = false;
  reconnectByRole[role].lastConnectedAt = 0;
  reconnectByRole[role].disconnectedSince = 0;
  reconnectByRole[role].alertEmittedForOutage = false;
  reconnectByRole[role].restartFailures = 0;
}

export function setConnectionStateForTest(role: BridgeSocketRole, state: BridgeConnectionState): void {
  connectionStateByRole[role] = state;
}

export function getReconnectStateForTest(role: BridgeSocketRole): {
  consecutiveFailures: number;
  totalReconnectAttempts: number;
  disconnectedSince: number;
  alertEmittedForOutage: boolean;
  restartFailures: number;
  hasReconnectTimer: boolean;
} {
  const controller = reconnectByRole[role];
  return {
    consecutiveFailures: controller.consecutiveFailures,
    totalReconnectAttempts: controller.totalReconnectAttempts,
    disconnectedSince: controller.disconnectedSince,
    alertEmittedForOutage: controller.alertEmittedForOutage,
    restartFailures: controller.restartFailures,
    hasReconnectTimer: controller.timer !== null,
  };
}

function resetReconnectBackoffAfterFirstMessage(role: BridgeSocketRole): void {
  const controller = reconnectByRole[role];
  controller.consecutiveFailures = 0;
  controller.alertEmittedForOutage = false;
  controller.restartFailures = 0;
}

function markSocketConnectAttempt(role: BridgeSocketRole): void {
  reconnectByRole[role].firstMessageReceived = false;
  applyConnectionTransition(role, 'connect_attempt');
}

function markSocketOpen(role: BridgeSocketRole): void {
  applyConnectionTransition(role, 'socket_open');
}

function markSocketMessage(role: BridgeSocketRole): void {
  markHeartbeatTraffic(role);
  applyConnectionTransition(role, 'socket_message');
  const controller = reconnectByRole[role];
  if (!controller.firstMessageReceived) {
    controller.firstMessageReceived = true;
    resetReconnectBackoffAfterFirstMessage(role);
    clearReconnectTimer(role);
  }
}

function queueReconnectEscalationAlert(role: BridgeSocketRole, controller: SocketReconnectController): void {
  if (controller.alertEmittedForOutage) {
    return;
  }
  const orgID = getTrimmedString(resolveConfiguredOtterCampOrgID());
  if (!orgID) {
    console.warn(`[bridge] ${role} reconnect alert skipped: OTTERCAMP_ORG_ID is not configured`);
    return;
  }
  if (!isConnectedState(connectionStateByRole.ottercamp)) {
    console.warn(`[bridge] ${role} reconnect alert skipped: OtterCamp connection is unavailable`);
    return;
  }

  const nowISO = new Date().toISOString();
  const disconnectedForSeconds = controller.disconnectedSince > 0
    ? Math.max(0, Math.floor((Date.now() - controller.disconnectedSince) / 1000))
    : 0;
  const event: BridgeAgentActivityEvent = {
    id: `bridge_reconnect_alert_${role}_${controller.disconnectedSince || Date.now()}`,
    agent_id: 'bridge',
    session_key: `bridge:${role}:reconnect`,
    trigger: 'bridge.reconnect.alert',
    channel: 'system',
    summary: `Bridge reconnect alert: ${role} disconnected`,
    detail: `${role} reconnect has failed ${controller.consecutiveFailures} times (${disconnectedForSeconds}s disconnected).`,
    tokens_used: 0,
    duration_ms: disconnectedForSeconds * 1000,
    status: 'failed',
    started_at: nowISO,
    completed_at: nowISO,
  };
  const queued = queueActivityEventsForOrg(orgID, [event]);
  if (queued <= 0) {
    return;
  }
  controller.alertEmittedForOutage = true;
  void flushBufferedActivityEvents('reconnect-escalation-alert').catch((err) => {
    console.error('[bridge] reconnect escalation alert flush failed:', err);
  });
}

function requestSupervisorRestart(role: BridgeSocketRole, controller: SocketReconnectController): boolean {
  const disconnectedForSeconds = controller.disconnectedSince > 0
    ? Math.max(0, Math.floor((Date.now() - controller.disconnectedSince) / 1000))
    : 0;
  const reason =
    `${role} reconnect failed ${controller.consecutiveFailures} times (${disconnectedForSeconds}s disconnected); requesting supervisor restart`;
  console.error(`[bridge] ${reason}`);
  try {
    processExitFn(1);
    return true;
  } catch (err) {
    controller.restartFailures += 1;
    console.error(
      `[bridge] supervisor restart request failed (${controller.restartFailures}/${RESTART_FAILURE_EXIT_THRESHOLD}):`,
      err,
    );
    if (controller.restartFailures >= RESTART_FAILURE_EXIT_THRESHOLD) {
      console.error('[bridge] restart request failed twice; forcing process exit');
      process.exit(1);
    }
    return false;
  }
}

function scheduleReconnect(role: BridgeSocketRole, reconnectFn: () => void): void {
  const controller = reconnectByRole[role];
  controller.consecutiveFailures += 1;
  controller.totalReconnectAttempts += 1;
  const escalationTier = reconnectEscalationTierForFailures(controller.consecutiveFailures);
  if (controller.consecutiveFailures === RECONNECT_WARNING_THRESHOLD) {
    console.warn(
      `[bridge] ${role} reconnect warning: ${controller.consecutiveFailures} consecutive failures`,
    );
  }
  if (
    controller.consecutiveFailures >= RECONNECT_ALERT_THRESHOLD &&
    escalationTier === 'alert'
  ) {
    queueReconnectEscalationAlert(role, controller);
  }
  if (escalationTier === 'restart') {
    const restartRequested = requestSupervisorRestart(role, controller);
    if (restartRequested || controller.restartFailures >= RESTART_FAILURE_EXIT_THRESHOLD) {
      return;
    }
  }

  const delayMs = computeReconnectDelayMs(controller.consecutiveFailures - 1);
  applyConnectionTransition(role, 'reconnect_scheduled', {
    attempt: controller.consecutiveFailures,
    delay_ms: delayMs,
  });
  clearReconnectTimer(role);
  controller.timer = setTimeout(() => {
    controller.timer = null;
    applyConnectionTransition(role, 'reconnect_timer_fired', { attempt: controller.consecutiveFailures });
    reconnectFn();
  }, delayMs);
}

function clearHeartbeatTimers(role: BridgeSocketRole): void {
  const heartbeat = heartbeatByRole[role];
  if (heartbeat.intervalTimer) {
    clearInterval(heartbeat.intervalTimer);
    heartbeat.intervalTimer = null;
  }
  if (heartbeat.pongTimeoutTimer) {
    clearTimeout(heartbeat.pongTimeoutTimer);
    heartbeat.pongTimeoutTimer = null;
  }
}

function handleOpenClawSocketClosed(
  code: number,
  reason: string,
  onClose: (err: Error) => void,
  reconnectFn: () => void,
): void {
  console.warn(`[bridge] OpenClaw socket closed (${code}) ${reason}`);
  applyConnectionTransition('openclaw', 'socket_closed', { code, reason });
  clearHeartbeatTimers('openclaw');
  openClawWS = null;
  for (const [pendingID, pending] of Array.from(pendingRequests.entries())) {
    pendingRequests.delete(pendingID);
    pending.reject(new Error('OpenClaw socket closed'));
  }
  onClose(new Error(`OpenClaw socket closed (${code})`));
  if (continuousModeEnabled) {
    scheduleReconnect('openclaw', reconnectFn);
  }
}

export function triggerOpenClawCloseForTest(
  code: number,
  reason: string,
  reconnectFn: () => void,
): void {
  handleOpenClawSocketClosed(code, reason, () => {}, reconnectFn);
}

function handleOtterCampSocketClosed(
  code: number,
  reason: string,
  reconnectFn: () => void,
): void {
  console.warn(`[bridge] OtterCamp websocket closed (${code}) ${reason}`);
  applyConnectionTransition('ottercamp', 'socket_closed', { code, reason });
  clearHeartbeatTimers('ottercamp');
  otterCampWS = null;
  if (continuousModeEnabled) {
    scheduleReconnect('ottercamp', reconnectFn);
  }
}

export function triggerOtterCampCloseForTest(
  code: number,
  reason: string,
  reconnectFn: () => void,
): void {
  handleOtterCampSocketClosed(code, reason, reconnectFn);
}

export function triggerSocketMessageForTest(role: BridgeSocketRole): void {
  markSocketMessage(role);
}

export function setContinuousModeEnabledForTest(enabled: boolean): void {
  continuousModeEnabled = enabled;
}

function markHeartbeatTraffic(role: BridgeSocketRole): void {
  heartbeatByRole[role].lastMessageAt = Date.now();
}

function markHeartbeatPong(role: BridgeSocketRole): void {
  const heartbeat = heartbeatByRole[role];
  heartbeat.lastPongAt = Date.now();
  heartbeat.missedPongs = nextMissedPongCount(heartbeat.missedPongs, true);
  if (heartbeat.pongTimeoutTimer) {
    clearTimeout(heartbeat.pongTimeoutTimer);
    heartbeat.pongTimeoutTimer = null;
  }
}

function startHeartbeatLoop(
  role: BridgeSocketRole,
  socket: WebSocket,
  onForceReconnect: () => void,
): void {
  clearHeartbeatTimers(role);
  const heartbeat = heartbeatByRole[role];
  heartbeat.missedPongs = 0;
  heartbeat.lastPingAt = 0;
  heartbeat.lastPongAt = 0;

  const triggerHeartbeat = () => {
    if (socket.readyState !== WebSocket.OPEN) {
      return;
    }
    heartbeat.lastPingAt = Date.now();
    try {
      socket.ping();
    } catch (err) {
      console.error(`[bridge] ${role} heartbeat ping failed:`, err);
      return;
    }

    if (heartbeat.pongTimeoutTimer) {
      clearTimeout(heartbeat.pongTimeoutTimer);
    }
    heartbeat.pongTimeoutTimer = setTimeout(() => {
      heartbeat.pongTimeoutTimer = null;
      heartbeat.missedPongs = nextMissedPongCount(heartbeat.missedPongs, false);
      applyConnectionTransition(role, 'health_warning', {
        missed_pongs: heartbeat.missedPongs,
        timeout_ms: HEARTBEAT_PONG_TIMEOUT_MS,
      });
      if (shouldForceReconnectFromHeartbeat(heartbeat.missedPongs)) {
        applyConnectionTransition(role, 'heartbeat_missed', { missed_pongs: heartbeat.missedPongs });
        onForceReconnect();
      }
    }, HEARTBEAT_PONG_TIMEOUT_MS);
  };

  heartbeat.intervalTimer = setInterval(triggerHeartbeat, HEARTBEAT_INTERVAL_MS);
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

function getTrimmedString(value: unknown): string {
  if (typeof value !== 'string') {
    return '';
  }
  return value.trim();
}

function estimateDispatchReplayPayloadSize(eventType: string, payload: Record<string, unknown>, dedupeID: string): number {
  return Buffer.byteLength(
    JSON.stringify({
      id: dedupeID,
      event_type: eventType,
      payload,
    }),
    'utf8',
  );
}

function rememberDeliveredDispatchReplayID(eventID: string): void {
  if (!eventID) {
    return;
  }
  if (deliveredDispatchReplayIDs.has(eventID)) {
    return;
  }
  deliveredDispatchReplayIDs.add(eventID);
  deliveredDispatchReplayIDOrder.push(eventID);
  while (deliveredDispatchReplayIDOrder.length > MAX_TRACKED_DISPATCH_REPLAY_IDS) {
    const oldest = deliveredDispatchReplayIDOrder.shift();
    if (oldest) {
      deliveredDispatchReplayIDs.delete(oldest);
    }
  }
}

function dropOldestDispatchReplayItem(): void {
  const oldest = dispatchReplayQueue.shift();
  if (!oldest) {
    return;
  }
  queuedDispatchReplayIDs.delete(oldest.id);
  dispatchReplayQueueBytes = Math.max(0, dispatchReplayQueueBytes - oldest.sizeBytes);
}

function deriveDispatchReplayID(eventType: string, payload: Record<string, unknown>): string {
  const eventRecord = payload as {
    data?: {
      message_id?: string;
      command_id?: string;
      thread_id?: string;
      session_key?: string;
    };
  };
  const messageID = getTrimmedString(eventRecord.data?.message_id);
  if (messageID) {
    return `${eventType}:${messageID}`;
  }
  const commandID = getTrimmedString(eventRecord.data?.command_id);
  if (commandID) {
    return `${eventType}:${commandID}`;
  }
  const sessionKey = getTrimmedString(eventRecord.data?.session_key);
  const threadID = getTrimmedString(eventRecord.data?.thread_id);
  const fallback = `${eventType}:${sessionKey}:${threadID}:${JSON.stringify(payload).slice(0, 240)}`;
  return crypto.createHash('sha1').update(fallback).digest('hex');
}

export function queueDispatchEventForReplay(
  eventType: string,
  payload: Record<string, unknown>,
  dedupeID?: string,
  options?: { maxItems?: number; maxBytes?: number },
): boolean {
  const normalizedEventType = getTrimmedString(eventType);
  if (!normalizedEventType || !payload || typeof payload !== 'object' || Array.isArray(payload)) {
    return false;
  }

  const id = getTrimmedString(dedupeID) || deriveDispatchReplayID(normalizedEventType, payload);
  if (!id || queuedDispatchReplayIDs.has(id) || deliveredDispatchReplayIDs.has(id)) {
    return false;
  }

  const sizeBytes = estimateDispatchReplayPayloadSize(normalizedEventType, payload, id);
  const maxItems = options?.maxItems && options.maxItems > 0 ? options.maxItems : DISPATCH_REPLAY_MAX_ITEMS;
  const maxBytes = options?.maxBytes && options.maxBytes > 0 ? options.maxBytes : DISPATCH_REPLAY_MAX_BYTES;
  const item: DispatchReplayQueueItem = {
    id,
    eventType: normalizedEventType,
    payload,
    sizeBytes,
    queuedAtMs: Date.now(),
  };
  dispatchReplayQueue.push(item);
  queuedDispatchReplayIDs.add(id);
  dispatchReplayQueueBytes += sizeBytes;

  while (dispatchReplayQueue.length > maxItems || dispatchReplayQueueBytes > maxBytes) {
    dropOldestDispatchReplayItem();
  }
  return queuedDispatchReplayIDs.has(id);
}

export function getDispatchReplayQueueStateForTest(): {
  depth: number;
  totalBytes: number;
  ids: string[];
} {
  return {
    depth: dispatchReplayQueue.length,
    totalBytes: dispatchReplayQueueBytes,
    ids: dispatchReplayQueue.map((item) => item.id),
  };
}

export function resetDispatchReplayQueueForTest(): void {
  dispatchReplayQueue.length = 0;
  queuedDispatchReplayIDs.clear();
  deliveredDispatchReplayIDs.clear();
  deliveredDispatchReplayIDOrder.length = 0;
  dispatchReplayQueueBytes = 0;
}

export async function replayQueuedDispatchEventsForTest(
  dispatcher: (eventType: string, payload: Record<string, unknown>) => Promise<void>,
): Promise<string[]> {
  const flushedIDs: string[] = [];
  while (dispatchReplayQueue.length > 0) {
    const current = dispatchReplayQueue[0];
    if (!current) {
      break;
    }
    await dispatcher(current.eventType, current.payload);
    dispatchReplayQueue.shift();
    queuedDispatchReplayIDs.delete(current.id);
    dispatchReplayQueueBytes = Math.max(0, dispatchReplayQueueBytes - current.sizeBytes);
    rememberDeliveredDispatchReplayID(current.id);
    flushedIDs.push(current.id);
  }
  return flushedIDs;
}

function setSessionContext(sessionKey: string, context: SessionContext): void {
  const normalized = getTrimmedString(sessionKey);
  if (!normalized) {
    return;
  }
  if (sessionContexts.has(normalized)) {
    sessionContexts.delete(normalized);
  }
  sessionContexts.set(normalized, context);
  while (sessionContexts.size > MAX_TRACKED_SESSION_CONTEXTS) {
    const oldest = sessionContexts.keys().next();
    if (oldest.done) {
      break;
    }
    sessionContexts.delete(oldest.value);
    contextPrimedSessions.delete(oldest.value);
  }
}

export function parseChameleonSessionKey(sessionKey: string): string | null {
  const match = CHAMELEON_SESSION_KEY_PATTERN.exec(getTrimmedString(sessionKey));
  if (!match || !match[1]) {
    return null;
  }
  return match[1].toLowerCase();
}

export function isCanonicalChameleonSessionKey(sessionKey: string): boolean {
  return parseChameleonSessionKey(sessionKey) !== null;
}

function parseAgentIDFromSessionKey(sessionKey: string): string {
  const chameleonAgentID = parseChameleonSessionKey(sessionKey);
  if (chameleonAgentID) {
    return chameleonAgentID;
  }
  const match = /^agent:([^:]+):/i.exec(sessionKey.trim());
  if (!match || !match[1]) {
    return '';
  }
  const candidate = match[1].trim();
  if (!SAFE_FALLBACK_AGENT_ID_PATTERN.test(candidate)) {
    return '';
  }
  return candidate.toLowerCase();
}

export function parseAgentIDFromSessionKeyForTest(sessionKey: string): string {
  return parseAgentIDFromSessionKey(sessionKey);
}

function parseAgentSlotFromSessionKey(sessionKey: string): string {
  const match = /^agent:([^:]+):/i.exec(getTrimmedString(sessionKey));
  if (!match || !match[1]) {
    return '';
  }
  const candidate = match[1].trim().toLowerCase();
  if (!SAFE_AGENT_SLOT_PATTERN.test(candidate)) {
    return '';
  }
  return candidate;
}

function normalizeUpdatedAt(value: number): number {
  if (!Number.isFinite(value) || value <= 0) {
    return Date.now();
  }
  if (value > 1_000_000_000_000) {
    return Math.floor(value);
  }
  return Math.floor(value * 1000);
}

function deriveAgentID(session: OpenClawSession): string {
  const fromSessionKey = parseAgentIDFromSessionKey(session.key);
  if (fromSessionKey) {
    return fromSessionKey;
  }
  const context = sessionContexts.get(session.key);
  const fromContext = getTrimmedString(context?.agentID);
  if (fromContext) {
    return fromContext;
  }
  const deliveryContext = asRecord(session.deliveryContext);
  const fromDelivery =
    getTrimmedString(deliveryContext?.agent_id) ||
    getTrimmedString(deliveryContext?.agentId) ||
    getTrimmedString(session.lastTo);
  if (fromDelivery) {
    return fromDelivery;
  }
  return 'system';
}

function deriveActivityScope(session: OpenClawSession): AgentActivityScope | undefined {
  const scope: AgentActivityScope = {};
  const sessionKey = getTrimmedString(session.key);
  if (!sessionKey) {
    return undefined;
  }

  const context = sessionContexts.get(sessionKey);
  const deliveryContext = asRecord(session.deliveryContext);

  const projectFromSession = PROJECT_ID_PATTERN.exec(sessionKey)?.[1];
  const issueFromSession = ISSUE_ID_PATTERN.exec(sessionKey)?.[1];
  const projectID =
    projectFromSession ||
    getTrimmedString(context?.projectID) ||
    getTrimmedString(deliveryContext?.project_id) ||
    getTrimmedString(deliveryContext?.projectId);
  const issueID =
    issueFromSession ||
    getTrimmedString(context?.issueID) ||
    getTrimmedString(deliveryContext?.issue_id) ||
    getTrimmedString(deliveryContext?.issueId);
  const threadID =
    getTrimmedString(context?.threadID) ||
    getTrimmedString(deliveryContext?.thread_id) ||
    getTrimmedString(deliveryContext?.threadId);
  const issueNumber =
    context?.issueNumber ??
    getNumeric(deliveryContext?.issue_number) ??
    getNumeric(deliveryContext?.issueNumber);

  if (projectID) {
    scope.project_id = projectID;
  }
  if (issueID) {
    scope.issue_id = issueID;
  }
  if (Number.isFinite(issueNumber)) {
    scope.issue_number = Number(issueNumber);
  }
  if (threadID) {
    scope.thread_id = threadID;
  }
  if (!scope.project_id && !scope.issue_id && !scope.issue_number && !scope.thread_id) {
    return undefined;
  }
  return scope;
}

export function inferActivityTrigger(
  session: OpenClawSession,
  previous?: OpenClawSession,
): { trigger: string; channel: string } {
  const sessionKey = getTrimmedString(session.key).toLowerCase();
  const displayName = getTrimmedString(session.displayName).toLowerCase();
  const channel =
    getTrimmedString(session.channel).toLowerCase() ||
    getTrimmedString(session.lastChannel).toLowerCase() ||
    getTrimmedString(previous?.channel).toLowerCase();

  if (sessionKey.startsWith('cron:') || sessionKey.includes(':cron:')) {
    return { trigger: 'cron.scheduled', channel: 'cron' };
  }
  if (HEARTBEAT_PATTERN.test(sessionKey) || HEARTBEAT_PATTERN.test(displayName)) {
    return { trigger: 'heartbeat', channel: channel || 'system' };
  }
  if (sessionKey.startsWith('spawn:') || session.kind === 'sub') {
    return { trigger: 'spawn.sub_agent', channel: channel || 'system' };
  }
  if (session.kind === 'isolated') {
    return { trigger: 'spawn.isolated', channel: channel || 'system' };
  }
  if (CHAT_CHANNELS.has(channel)) {
    return { trigger: `chat.${channel}`, channel };
  }
  if (session.kind === 'main') {
    const chatChannel = channel || 'tui';
    return { trigger: `chat.${chatChannel}`, channel: chatChannel };
  }
  return { trigger: 'system.event', channel: channel || 'system' };
}

function shouldEmitActivityDelta(current: OpenClawSession, previous?: OpenClawSession): boolean {
  if (!previous) {
    return true;
  }
  const currentUpdatedAt = normalizeUpdatedAt(current.updatedAt);
  const previousUpdatedAt = normalizeUpdatedAt(previous.updatedAt);
  if (currentUpdatedAt > previousUpdatedAt) {
    return true;
  }
  if (getTrimmedString(current.displayName) !== getTrimmedString(previous.displayName)) {
    return true;
  }
  if ((current.totalTokens || 0) !== (previous.totalTokens || 0)) {
    return true;
  }
  if (Boolean(current.abortedLastRun) !== Boolean(previous.abortedLastRun)) {
    return true;
  }
  return false;
}

function buildActivitySummary(
  session: OpenClawSession,
  previous: OpenClawSession | undefined,
  trigger: string,
): string {
  const displayName = getTrimmedString(session.displayName);
  const previousDisplayName = getTrimmedString(previous?.displayName);
  if (!previous) {
    if (displayName) {
      return `Started ${displayName}`;
    }
    return `Started ${trigger}`;
  }
  if (displayName && displayName !== previousDisplayName) {
    return `Updated task: ${displayName}`;
  }
  if (session.abortedLastRun) {
    return displayName ? `Failed while working on ${displayName}` : 'Session run failed';
  }
  return displayName ? `Worked on ${displayName}` : `Session activity (${session.key})`;
}

function buildActivityDetail(session: OpenClawSession, previous?: OpenClawSession): string {
  const parts: string[] = [];
  if (previous && getTrimmedString(previous.displayName) !== getTrimmedString(session.displayName)) {
    const from = getTrimmedString(previous.displayName) || 'unknown';
    const to = getTrimmedString(session.displayName) || 'unknown';
    parts.push(`task: ${from} -> ${to}`);
  }
  if (session.model) {
    parts.push(`model=${session.model}`);
  }
  parts.push(`session=${session.key}`);
  return parts.join(' | ');
}

function buildActivityEventID(session: OpenClawSession, previous?: OpenClawSession): string {
  const seed = [
    getTrimmedString(session.key),
    String(normalizeUpdatedAt(session.updatedAt)),
    String(session.totalTokens || 0),
    String(previous?.totalTokens || 0),
    getTrimmedString(session.displayName),
  ].join('|');
  return `act_${crypto.createHash('sha1').update(seed).digest('hex').slice(0, 24)}`;
}

function mapSessionDeltaToActivityEvent(
  session: OpenClawSession,
  previous?: OpenClawSession,
): BridgeAgentActivityEvent | null {
  if (!shouldEmitActivityDelta(session, previous)) {
    return null;
  }

  const triggerInfo = inferActivityTrigger(session, previous);
  const updatedAtMs = normalizeUpdatedAt(session.updatedAt);
  const previousUpdatedAtMs = previous ? normalizeUpdatedAt(previous.updatedAt) : updatedAtMs;
  const durationMs = Math.max(0, updatedAtMs - previousUpdatedAtMs);
  const totalTokens = Number.isFinite(session.totalTokens) ? Math.max(0, session.totalTokens) : 0;
  const previousTokens = previous && Number.isFinite(previous.totalTokens) ? Math.max(0, previous.totalTokens) : 0;
  const tokensUsed = previous ? Math.max(0, totalTokens - previousTokens) : totalTokens;

  let status: BridgeAgentActivityEvent['status'] = 'completed';
  if (!previous) {
    status = 'started';
  } else if (session.abortedLastRun) {
    status = 'failed';
  }

  const startedAtISO = new Date(updatedAtMs).toISOString();
  const completedAt = status === 'started' ? undefined : startedAtISO;
  const event: BridgeAgentActivityEvent = {
    id: buildActivityEventID(session, previous),
    agent_id: deriveAgentID(session),
    session_key: getTrimmedString(session.key),
    trigger: triggerInfo.trigger,
    channel: triggerInfo.channel || undefined,
    summary: buildActivitySummary(session, previous, triggerInfo.trigger),
    detail: buildActivityDetail(session, previous),
    tokens_used: tokensUsed,
    model_used: getTrimmedString(session.model) || undefined,
    duration_ms: durationMs,
    status,
    started_at: startedAtISO,
    completed_at: completedAt,
  };

  const scope = deriveActivityScope(session);
  if (scope) {
    event.scope = scope;
  }

  return event;
}

export function buildActivityEventsFromSessionDeltas(params: {
  previousByKey: Map<string, OpenClawSession>;
  currentSessions: OpenClawSession[];
}): BridgeAgentActivityEvent[] {
  const events: BridgeAgentActivityEvent[] = [];
  for (const session of params.currentSessions) {
    const key = getTrimmedString(session.key);
    if (!key) {
      continue;
    }
    const previous = params.previousByKey.get(key);
    const event = mapSessionDeltaToActivityEvent(session, previous);
    if (!event) {
      continue;
    }
    events.push(event);
  }
  events.sort((a, b) => a.started_at.localeCompare(b.started_at));
  return events;
}

function sessionsByKey(sessions: OpenClawSession[]): Map<string, OpenClawSession> {
  const next = new Map<string, OpenClawSession>();
  for (const session of sessions) {
    const key = getTrimmedString(session.key);
    if (!key) {
      continue;
    }
    next.set(key, session);
  }
  return next;
}

function getNumeric(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === 'string') {
    const parsed = Number(value.trim());
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return undefined;
}

function getBoolean(value: unknown): boolean | undefined {
  if (typeof value === 'boolean') {
    return value;
  }
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase();
    if (normalized === 'true') return true;
    if (normalized === 'false') return false;
  }
  return undefined;
}

function normalizeTimeString(value: unknown): string | undefined {
  const raw = getTrimmedString(value);
  if (!raw) {
    return undefined;
  }
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) {
    return undefined;
  }
  return parsed.toISOString();
}

function parseJSONValue(raw: string): unknown {
  const trimmed = raw.trim();
  if (!trimmed) {
    return null;
  }
  return JSON.parse(trimmed);
}

function base64UrlEncode(buf: Buffer): string {
  return buf.toString('base64').replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/g, '');
}

function derivePublicKeyRaw(publicKeyPem: string): Buffer {
  const spki = crypto.createPublicKey(publicKeyPem).export({
    type: 'spki',
    format: 'der',
  }) as Buffer;
  if (
    spki.length === ED25519_SPKI_PREFIX.length + 32 &&
    spki.subarray(0, ED25519_SPKI_PREFIX.length).equals(ED25519_SPKI_PREFIX)
  ) {
    return spki.subarray(ED25519_SPKI_PREFIX.length);
  }
  return spki;
}

function resolveOpenClawStateDir(): string {
  const envDir = getTrimmedString(process.env.OPENCLAW_STATE_DIR);
  if (envDir) {
    return envDir;
  }
  return path.join(os.homedir(), '.openclaw');
}

function resolveOpenClawConfigPath(): string {
  const envPath = getTrimmedString(process.env.OPENCLAW_CONFIG_PATH);
  if (envPath) {
    return envPath;
  }
  return path.join(resolveOpenClawStateDir(), 'openclaw.json');
}

function resolveOpenClawIdentityPath(fileName: string): string {
  return path.join(resolveOpenClawStateDir(), 'identity', fileName);
}

type LocalSessionResetResult = {
  attempted: boolean;
  cleared: boolean;
  storePath: string;
  transcriptDeleted: boolean;
  sessionID?: string;
  reason?: string;
};

function isPathWithinRoot(rootDir: string, targetPath: string): boolean {
  const root = path.resolve(rootDir);
  const target = path.resolve(targetPath);
  const relative = path.relative(root, target);
  return relative === '' || (!relative.startsWith('..') && !path.isAbsolute(relative));
}

function resetSessionFromLocalStore(
  sessionKey: string,
  stateDir: string = resolveOpenClawStateDir(),
): LocalSessionResetResult {
  const normalizedSessionKey = getTrimmedString(sessionKey);
  const agentSlot = parseAgentSlotFromSessionKey(normalizedSessionKey);
  const storePath = path.join(stateDir, 'agents', agentSlot, 'sessions', 'sessions.json');
  if (!normalizedSessionKey || !agentSlot) {
    return {
      attempted: false,
      cleared: false,
      storePath,
      transcriptDeleted: false,
      reason: 'invalid session key',
    };
  }
  if (!fs.existsSync(storePath)) {
    return {
      attempted: true,
      cleared: false,
      storePath,
      transcriptDeleted: false,
      reason: 'store not found',
    };
  }

  const tempPath = `${storePath}.tmp.${process.pid}.${Date.now()}.${Math.random().toString(16).slice(2, 10)}`;
  try {
    const raw = fs.readFileSync(storePath, 'utf8');
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const keys = Object.keys(parsed || {});
    const targetKey = normalizedSessionKey.toLowerCase();
    const matchedKey = keys.find((key) => key.trim().toLowerCase() === targetKey);
    if (!matchedKey) {
      return {
        attempted: true,
        cleared: false,
        storePath,
        transcriptDeleted: false,
        reason: 'session not found',
      };
    }

    const existingEntry = asRecord(parsed[matchedKey]);
    const sessionID = getTrimmedString(existingEntry?.sessionId);
    const sessionFilePath = getTrimmedString(existingEntry?.sessionFile);
    delete parsed[matchedKey];

    const serialized = `${JSON.stringify(parsed, null, 2)}\n`;
    fs.writeFileSync(tempPath, serialized, 'utf8');
    fs.renameSync(tempPath, storePath);

    let transcriptDeleted = false;
    const sessionsDir = path.dirname(storePath);
    const candidateTranscriptPaths: string[] = [];
    if (sessionFilePath) {
      candidateTranscriptPaths.push(sessionFilePath);
    }
    if (sessionID && SAFE_SESSION_FILENAME_PATTERN.test(sessionID)) {
      candidateTranscriptPaths.push(path.join(sessionsDir, `${sessionID}.jsonl`));
    }
    for (const candidatePath of candidateTranscriptPaths) {
      const resolvedPath = path.resolve(candidatePath);
      if (!isPathWithinRoot(sessionsDir, resolvedPath)) {
        continue;
      }
      if (!fs.existsSync(resolvedPath)) {
        continue;
      }
      const stat = fs.lstatSync(resolvedPath);
      if (!stat.isFile()) {
        continue;
      }
      fs.unlinkSync(resolvedPath);
      transcriptDeleted = true;
    }

    return {
      attempted: true,
      cleared: true,
      storePath,
      transcriptDeleted,
      sessionID: sessionID || undefined,
    };
  } catch (err) {
    try {
      if (fs.existsSync(tempPath)) {
        fs.unlinkSync(tempPath);
      }
    } catch {
      // Best-effort cleanup.
    }
    return {
      attempted: true,
      cleared: false,
      storePath,
      transcriptDeleted: false,
      reason: err instanceof Error ? err.message : String(err),
    };
  }
}

export function resetSessionFromLocalStoreForTest(
  sessionKey: string,
  stateDir: string,
): LocalSessionResetResult {
  return resetSessionFromLocalStore(sessionKey, stateDir);
}

function loadDeviceIdentity(): DeviceIdentity | null {
  try {
    const raw = fs.readFileSync(resolveOpenClawIdentityPath('device.json'), 'utf8');
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const deviceId = getTrimmedString(parsed.deviceId);
    const publicKeyPem = getTrimmedString(parsed.publicKeyPem);
    const privateKeyPem = getTrimmedString(parsed.privateKeyPem);
    if (!deviceId || !publicKeyPem || !privateKeyPem) {
      return null;
    }
    return { deviceId, publicKeyPem, privateKeyPem };
  } catch {
    return null;
  }
}

function loadDeviceRoleToken(deviceId: string, role: string): string {
  try {
    const raw = fs.readFileSync(resolveOpenClawIdentityPath('device-auth.json'), 'utf8');
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const storedDeviceID = getTrimmedString(parsed.deviceId);
    if (!storedDeviceID || storedDeviceID !== deviceId) {
      return '';
    }
    const tokens = asRecord(parsed.tokens);
    const roleEntry = asRecord(tokens?.[role]);
    return getTrimmedString(roleEntry?.token);
  } catch {
    return '';
  }
}

function buildDeviceAuthPayload(params: {
  deviceId: string;
  clientId: string;
  clientMode: string;
  role: string;
  scopes: string[];
  signedAtMs: number;
  token: string;
  nonce?: string;
}): string {
  const version = params.nonce ? 'v2' : 'v1';
  const base = [
    version,
    params.deviceId,
    params.clientId,
    params.clientMode,
    params.role,
    params.scopes.join(','),
    String(params.signedAtMs),
    params.token,
  ];
  if (params.nonce) {
    base.push(params.nonce);
  }
  return base.join('|');
}

function signDevicePayload(privateKeyPem: string, payload: string): string {
  const signature = crypto.sign(null, Buffer.from(payload, 'utf8'), crypto.createPrivateKey(privateKeyPem));
  return base64UrlEncode(signature);
}

function buildContextEnvelope(context: SessionContext): string {
  const lines: string[] = ['You are responding inside OtterCamp.'];
  if (context.kind === 'dm') {
    lines.push('Surface: direct message chat.');
    if (context.threadID) {
      lines.push(`Thread ID: ${context.threadID}.`);
    }
    if (context.agentID) {
      lines.push(`Target agent identity: ${context.agentID}.`);
    }
    lines.push('Default meaning: "project" refers to an OtterCamp project record unless the user explicitly asks for local code scaffolding.');
    lines.push('If asked to create a project and a name is provided, create it in OtterCamp with sensible defaults and confirm the result.');
  } else if (context.kind === 'project_chat') {
    lines.push('Surface: project chat.');
    if (context.projectID) {
      lines.push(`Project ID: ${context.projectID}.`);
    }
    if (context.agentName || context.agentID) {
      lines.push(`Primary project agent: ${context.agentName || context.agentID}.`);
    }
  } else if (context.kind === 'issue_comment') {
    lines.push('Surface: issue thread comment.');
    if (context.projectID) {
      lines.push(`Project ID: ${context.projectID}.`);
    }
    if (context.issueID) {
      const issueLabel =
        typeof context.issueNumber === 'number' && Number.isFinite(context.issueNumber)
          ? `#${context.issueNumber} (${context.issueID})`
          : context.issueID;
      lines.push(`Issue: ${issueLabel}.`);
    }
    if (context.issueTitle) {
      lines.push(`Issue title: ${context.issueTitle}.`);
    }
    if (context.documentPath) {
      lines.push(`Linked issue document: ${context.documentPath}.`);
    }
    if (context.agentName || context.agentID) {
      lines.push(`Issue owner agent: ${context.agentName || context.agentID}.`);
    }
  }
  lines.push('Load relevant AGENTS.md and project docs before taking action.');
  return `[OTTERCAMP_CONTEXT]\n${lines.map((line) => `- ${line}`).join('\n')}\n[/OTTERCAMP_CONTEXT]`;
}

function buildSurfaceActionDefaults(context: SessionContext): string {
  if (context.kind !== 'dm') {
    return '';
  }
  return [
    '[OTTERCAMP_ACTION_DEFAULTS]',
    '- In this DM, "project", "task", "issue", and "agent" refer to OtterCamp entities unless the user says otherwise.',
    '- "Create a project" means create an OtterCamp project (status=active, description optional), not a local folder/repo scaffold.',
    '- If a project name is provided, create it directly and confirm; ask at most one concise follow-up only when required.',
    '[/OTTERCAMP_ACTION_DEFAULTS]',
  ].join('\n');
}

function buildContextReminder(context: SessionContext): string {
  if (context.kind === 'project_chat') {
    return `Project chat (${context.projectID || 'unknown project'})`;
  }
  if (context.kind === 'issue_comment') {
    const issueLabel =
      typeof context.issueNumber === 'number' && Number.isFinite(context.issueNumber)
        ? `#${context.issueNumber}`
        : context.issueID || 'unknown issue';
    return `Issue thread ${issueLabel} (${context.projectID || 'unknown project'})`;
  }
  return `DM thread (${context.threadID || 'unknown thread'})`;
}

function resolveProjectWorktreeBaseDir(): string {
  const override = getTrimmedString(process.env.OTTER_PROJECT_WORKTREE_ROOT);
  if (override) {
    return override;
  }
  return path.join(os.homedir(), '.otter', 'projects');
}

function safeProjectPathSegment(projectID: string): string {
  const normalized = getTrimmedString(projectID).toLowerCase();
  if (/^[0-9a-f-]{8,64}$/.test(normalized)) {
    return normalized;
  }
  return crypto.createHash('sha1').update(normalized || 'project').digest('hex').slice(0, 16);
}

function hashSessionKeyForWorktree(sessionKey: string): string {
  return crypto.createHash('sha1').update(getTrimmedString(sessionKey)).digest('hex').slice(0, 16);
}

export function resolveProjectWorktreeRoot(projectID: string, sessionKey: string): string {
  return path.join(
    resolveProjectWorktreeBaseDir(),
    safeProjectPathSegment(projectID),
    'worktrees',
    hashSessionKeyForWorktree(sessionKey),
  );
}

async function pathHasSymlinkSegments(fromRoot: string, targetPath: string): Promise<boolean> {
  // NOTE: This is a point-in-time check and is therefore vulnerable to TOCTOU swaps.
  // TODO(spec-110-hardening): Re-check path segments at each file-write interception hook.
  const root = path.resolve(fromRoot);
  const target = path.resolve(targetPath);
  const relative = path.relative(root, target);
  if (!relative || relative === '.') {
    return false;
  }
  if (relative.startsWith('..') || path.isAbsolute(relative)) {
    return true;
  }

  const segments = relative.split(path.sep).filter(Boolean);
  let cursor = root;
  for (const segment of segments) {
    cursor = path.join(cursor, segment);
    try {
      const stat = await fs.promises.lstat(cursor);
      if (stat.isSymbolicLink()) {
        return true;
      }
    } catch (err) {
      const code = (err as NodeJS.ErrnoException).code;
      if (code === 'ENOENT') {
        continue;
      }
      throw err;
    }
  }
  return false;
}

export async function isPathWithinProjectRoot(projectRoot: string, targetPath: string): Promise<boolean> {
  const root = path.resolve(projectRoot);
  try {
    const rootStat = await fs.promises.lstat(root);
    if (rootStat.isSymbolicLink()) {
      return false;
    }
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code !== 'ENOENT') {
      throw err;
    }
  }
  const candidate = path.resolve(root, targetPath);
  const relative = path.relative(root, candidate);
  if (relative.startsWith('..') || path.isAbsolute(relative)) {
    return false;
  }
  if (await pathHasSymlinkSegments(root, candidate)) {
    return false;
  }
  return true;
}

export function resolveExecutionMode(context: SessionContext): ExecutionMode {
  return getTrimmedString(context.projectID) ? 'project' : 'conversation';
}

async function resolveSessionExecutionContext(
  sessionKey: string,
  context: SessionContext,
): Promise<{ mode: ExecutionMode; projectRoot?: string }> {
  const mode = resolveExecutionMode(context);
  if (mode !== 'project') {
    return { mode: 'conversation' };
  }

  const projectID = getTrimmedString(context.projectID);
  if (!projectID) {
    return { mode: 'conversation' };
  }
  const projectRoot = resolveProjectWorktreeRoot(projectID, sessionKey);

  try {
    await fs.promises.mkdir(projectRoot, { recursive: true });
    // NOTE: v1 enforcement is policy-level only. Without OpenClaw file-write interception
    // hooks, we cannot mechanically enforce per-write path guard checks here.
    // TODO(spec-110-hardening): Wire isPathWithinProjectRoot into write/edit/apply_patch hooks.
    return {
      mode: 'project',
      projectRoot,
    };
  } catch (err) {
    console.warn(`[bridge] unable to prepare project worktree for ${sessionKey}; forcing conversation mode`, err);
    return { mode: 'conversation' };
  }
}

function buildExecutionPolicyBlock(params: {
  mode: ExecutionMode;
  context: SessionContext;
  projectRoot?: string;
}): string {
  const lines: string[] = ['[OTTERCAMP_EXECUTION_MODE]'];
  if (params.mode === 'project' && params.projectRoot) {
    lines.push('- mode: project');
    if (params.context.projectID) {
      lines.push(`- project_id: ${params.context.projectID}`);
    }
    lines.push(`- cwd: ${params.projectRoot}`);
    lines.push(`- write_guard_root: ${params.projectRoot}`);
    lines.push('- write policy: writes allowed only within write_guard_root');
    lines.push('- enforcement: policy-level only (prompt contract, no write hooks in v1)');
    lines.push('- TODO: enforce write/edit/apply_patch paths via OpenClaw file-write hooks');
    lines.push('- security: path traversal and symlink escape SHOULD NOT be used');
  } else {
    lines.push('- mode: conversation');
    lines.push('- project_id: none');
    lines.push('- write policy: deny write/edit/apply_patch and any filesystem mutation');
    lines.push('- enforcement: policy-level only (prompt contract, no write hooks in v1)');
    lines.push('- TODO: enforce mutation denial via OpenClaw tool/write interception hooks');
    lines.push('- workspaceAccess: none');
  }
  lines.push('[/OTTERCAMP_EXECUTION_MODE]');
  return lines.join('\n');
}

function clampInlineText(raw: string, maxChars: number): string {
  const normalized = raw.replace(/\s+/g, ' ').trim();
  if (!normalized) {
    return '';
  }
  if (normalized.length <= maxChars) {
    return normalized;
  }
  return `${normalized.slice(0, maxChars - 3).trimEnd()}...`;
}

function deriveTaskSummary(context: SessionContext, content: string): string {
  const fromIssueTitle = getTrimmedString(context.issueTitle);
  if (fromIssueTitle) {
    return clampInlineText(fromIssueTitle, SESSION_TASK_SUMMARY_MAX_CHARS);
  }
  const firstLine = content.split(/\r?\n/).find((line) => line.trim().length > 0) || content;
  return clampInlineText(firstLine, SESSION_TASK_SUMMARY_MAX_CHARS);
}

function normalizeWhoAmITasks(payload: Record<string, unknown>): WhoAmITaskPointer[] {
  const raw = Array.isArray(payload.active_tasks) ? payload.active_tasks : [];
  const tasks: WhoAmITaskPointer[] = [];
  for (const entry of raw) {
    const record = asRecord(entry);
    if (!record) {
      continue;
    }
    tasks.push({
      project: getTrimmedString(record.project) || undefined,
      issue: getTrimmedString(record.issue) || undefined,
      title: getTrimmedString(record.title) || undefined,
      status: getTrimmedString(record.status) || undefined,
    });
    if (tasks.length >= 4) {
      break;
    }
  }
  return tasks;
}

function renderTaskPointer(task: WhoAmITaskPointer): string {
  const parts: string[] = [];
  if (task.project) {
    parts.push(task.project);
  }
  if (task.issue) {
    parts.push(task.issue);
  }
  if (task.title) {
    parts.push(task.title);
  }
  const label = parts.join(' / ') || 'Task';
  if (task.status) {
    return `${label} [${task.status}]`;
  }
  return label;
}

function readIdentityField(
  payload: Record<string, unknown>,
  profile: 'compact' | 'full',
  compactKey: string,
  fullKey: string,
): string {
  if (profile === 'full') {
    const fullValue = getTrimmedString(payload[fullKey]);
    if (fullValue) {
      return clampInlineText(fullValue, IDENTITY_BLOCK_MAX_CHARS);
    }
  }
  const compactValue = getTrimmedString(payload[compactKey]);
  if (compactValue) {
    return clampInlineText(compactValue, IDENTITY_BLOCK_MAX_CHARS);
  }
  const fullValue = getTrimmedString(payload[fullKey]);
  if (fullValue) {
    return clampInlineText(fullValue, IDENTITY_BLOCK_MAX_CHARS);
  }
  return '';
}

export function isCompactWhoAmIInsufficient(payload: Record<string, unknown>): boolean {
  const profile = getTrimmedString(payload.profile).toLowerCase();
  if (profile && profile !== 'compact') {
    return false;
  }
  const agent = asRecord(payload.agent);
  const agentName = getTrimmedString(agent?.name);
  const soulSummary = getTrimmedString(payload.soul_summary);
  const identitySummary = getTrimmedString(payload.identity_summary);
  const instructionsSummary = getTrimmedString(payload.instructions_summary);
  const segments = [soulSummary, identitySummary, instructionsSummary].filter(Boolean);
  const totalChars = segments.reduce((sum, segment) => sum + segment.length, 0);
  if (!agentName) {
    return true;
  }
  if (segments.length < 2) {
    return true;
  }
  return totalChars < COMPACT_WHOAMI_MIN_SUMMARY_CHARS;
}

export function formatSessionDisplayLabel(agentName: string, taskSummary: string): string {
  const normalizedName = getTrimmedString(agentName);
  const normalizedTask = getTrimmedString(taskSummary);
  if (normalizedName && normalizedTask) {
    return `${normalizedName} — ${normalizedTask}`;
  }
  return normalizedName || normalizedTask;
}

export function buildIdentityPreamble(params: {
  payload: Record<string, unknown>;
  profile: 'compact' | 'full';
  taskSummary?: string;
}): string {
  const payload = params.payload;
  const profile = params.profile;
  const taskSummary = getTrimmedString(params.taskSummary);
  const agent = asRecord(payload.agent);
  const agentName = getTrimmedString(agent?.name) || 'Agent';
  const agentRole = getTrimmedString(agent?.role);
  const identityLine = agentRole ? `${agentName}, ${agentRole}` : agentName;
  const personalitySummary = readIdentityField(payload, profile, 'soul_summary', 'soul');
  const identitySummary = readIdentityField(payload, profile, 'identity_summary', 'identity');
  const instructionsSummary = readIdentityField(payload, profile, 'instructions_summary', 'instructions');
  const activeTasks = normalizeWhoAmITasks(payload).map(renderTaskPointer);

  const lines: string[] = [
    '[OtterCamp Identity Injection]',
    `You are ${identityLine}.`,
    '',
    `Identity profile: ${profile}`,
  ];
  if (personalitySummary) {
    lines.push(`Personality summary: ${personalitySummary}`);
  }
  if (identitySummary) {
    lines.push(`Identity summary: ${identitySummary}`);
  }
  if (instructionsSummary) {
    lines.push(`Instructions summary: ${instructionsSummary}`);
  }
  if (activeTasks.length > 0) {
    lines.push(`Active tasks: ${activeTasks.join(' | ')}`);
  }
  if (taskSummary) {
    lines.push(`Task: ${taskSummary}`);
  }
  lines.push('[/OtterCamp Identity Injection]');
  return lines.join('\n');
}

async function fetchWhoAmIProfile(
  agentID: string,
  sessionKey: string,
  orgID: string,
  profile: 'compact' | 'full',
): Promise<Record<string, unknown> | null> {
  const url = new URL(`/api/agents/${encodeURIComponent(agentID)}/whoami`, OTTERCAMP_URL);
  url.searchParams.set('session_key', sessionKey);
  url.searchParams.set('profile', profile);
  if (orgID) {
    url.searchParams.set('org_id', orgID);
  }

  const response = await fetchWithRetry(url.toString(), {
    method: 'GET',
    headers: {
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
  }, `fetch whoami (${profile})`);

  if (!response.ok) {
    const snippet = (await response.text().catch(() => '')).slice(0, 220);
    console.warn(
      `[bridge] whoami ${profile} request failed (${response.status} ${response.statusText} ${snippet})`,
    );
    return null;
  }

  const payload = await response.json().catch(() => null);
  return asRecord(payload);
}

async function resolveSessionIdentityMetadata(
  sessionKey: string,
  context: SessionContext,
  content: string,
): Promise<SessionIdentityMetadata | null> {
  const chameleonAgentID = parseChameleonSessionKey(sessionKey);
  if (!chameleonAgentID) {
    return null;
  }
  const orgID = getTrimmedString(context.orgID) || OTTERCAMP_ORG_ID;
  const taskSummary = deriveTaskSummary(context, content);

  const compactPayload = await fetchWhoAmIProfile(chameleonAgentID, sessionKey, orgID, 'compact');
  if (!compactPayload) {
    return null;
  }

  let selectedProfile: 'compact' | 'full' = 'compact';
  let selectedPayload = compactPayload;
  if (isCompactWhoAmIInsufficient(compactPayload)) {
    const fullPayload = await fetchWhoAmIProfile(chameleonAgentID, sessionKey, orgID, 'full');
    const fullProfile = getTrimmedString(fullPayload?.profile).toLowerCase();
    const hasFullIdentityFields =
      Boolean(getTrimmedString(fullPayload?.soul)) ||
      Boolean(getTrimmedString(fullPayload?.identity)) ||
      Boolean(getTrimmedString(fullPayload?.instructions));
    if (fullPayload && (fullProfile === 'full' || hasFullIdentityFields)) {
      selectedProfile = 'full';
      selectedPayload = fullPayload;
    }
  }

  const agent = asRecord(selectedPayload.agent);
  const displayLabel = formatSessionDisplayLabel(
    getTrimmedString(agent?.name) || getTrimmedString(context.agentName) || getTrimmedString(context.agentID),
    taskSummary,
  );
  return {
    profile: selectedProfile,
    preamble: buildIdentityPreamble({
      payload: selectedPayload,
      profile: selectedProfile,
      taskSummary,
    }),
    displayLabel: displayLabel || undefined,
  };
}

async function withAutoRecallContext(sessionKey: string, rawContent: string): Promise<string> {
  const content = rawContent.trim();
  if (!content) {
    return '';
  }
  if (content.includes('[OTTERCAMP_COMPACTION_RECOVERY]') || content.includes('[OTTERCAMP_AUTO_RECALL]')) {
    return content;
  }

  const context = sessionContexts.get(sessionKey);
  if (!context) {
    return content;
  }

  const orgID = getTrimmedString(context.orgID) || OTTERCAMP_ORG_ID;
  const agentID =
    getTrimmedString(context.agentID) ||
    getTrimmedString(context.responderAgentID) ||
    parseAgentIDFromSessionKey(sessionKey);
  if (!orgID || !agentID) {
    return content;
  }

  try {
    const url = new URL('/api/memory/recall', OTTERCAMP_URL);
    url.searchParams.set('org_id', orgID);
    url.searchParams.set('agent_id', agentID);
    url.searchParams.set('q', content.slice(0, 1500));
    url.searchParams.set('max_results', String(AUTO_RECALL_MAX_RESULTS));
    url.searchParams.set('min_relevance', String(AUTO_RECALL_MIN_RELEVANCE));
    url.searchParams.set('max_chars', String(AUTO_RECALL_MAX_CHARS));

    const response = await fetchWithRetry(url.toString(), {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json',
        ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
      },
    }, 'fetch auto recall context');
    if (!response.ok) {
      const snippet = (await response.text().catch(() => '')).slice(0, 300);
      throw new Error(`auto recall request failed: ${response.status} ${response.statusText} ${snippet}`.trim());
    }

    const payload = asRecord(await response.json().catch(() => null));
    const recallContext = getTrimmedString(payload?.context);
    if (!recallContext) {
      return content;
    }
    return [
      '[OTTERCAMP_AUTO_RECALL]',
      recallContext.slice(0, AUTO_RECALL_MAX_CHARS),
      '[/OTTERCAMP_AUTO_RECALL]',
      '',
      content,
    ].join('\n');
  } catch (err) {
    console.warn(`[bridge] auto recall fetch failed for ${sessionKey}; continuing without recall:`, err);
    return content;
  }
}

async function withSessionContext(
  sessionKey: string,
  rawContent: string,
  options?: { includeUserContent?: boolean },
): Promise<string> {
  const includeUserContent = options?.includeUserContent !== false;
  const content = rawContent.trim();
  if (!content) {
    return '';
  }
  let context = sessionContexts.get(sessionKey);
  if (!context) {
    return content;
  }
  const isCanonicalChameleonDM =
    context.kind === 'dm' && parseChameleonSessionKey(sessionKey) !== null;
  const shouldBootstrapIdentity =
    !contextPrimedSessions.has(sessionKey) ||
    (isCanonicalChameleonDM && !context.identityMetadata);

  if (shouldBootstrapIdentity) {
    const execution = await resolveSessionExecutionContext(sessionKey, context);
    context = {
      ...context,
      executionMode: execution.mode,
      projectRoot: execution.projectRoot,
    };

    let identityMetadata = context.identityMetadata;
    if (!identityMetadata) {
      try {
        const identityContent = includeUserContent ? content : '';
        identityMetadata = await resolveSessionIdentityMetadata(sessionKey, context, identityContent) || undefined;
      } catch (err) {
        console.warn(`[bridge] failed to resolve identity for ${sessionKey}:`, err);
      }
      if (identityMetadata) {
        context = {
          ...context,
          identityMetadata,
          displayLabel: identityMetadata.displayLabel,
        };
      }
    }
    setSessionContext(sessionKey, context);
    // Canonical chameleon DM sessions should only be considered primed once
    // identity metadata is available; otherwise retry bootstrap next turn.
    if (!isCanonicalChameleonDM || identityMetadata) {
      contextPrimedSessions.add(sessionKey);
    } else {
      contextPrimedSessions.delete(sessionKey);
    }
    const sections: string[] = [];
    if (identityMetadata?.preamble) {
      sections.push(identityMetadata.preamble);
    }
    sections.push(buildExecutionPolicyBlock({
      mode: execution.mode,
      context,
      projectRoot: execution.projectRoot,
    }));
    sections.push(buildContextEnvelope(context));
    const actionDefaults = buildSurfaceActionDefaults(context);
    if (actionDefaults) {
      sections.push(actionDefaults);
    }
    if (includeUserContent) {
      sections.push(content);
    }
    return sections.join('\n\n');
  }
  const reminderSections: string[] = [];
  const shouldPersistIdentityPreamble =
    context.kind === 'dm' && parseChameleonSessionKey(sessionKey) !== null;
  const identityPreamble = shouldPersistIdentityPreamble
    ? getTrimmedString(context.identityMetadata?.preamble)
    : '';
  if (identityPreamble) {
    reminderSections.push(identityPreamble);
  }
  const actionDefaults = buildSurfaceActionDefaults(context);
  if (actionDefaults) {
    reminderSections.push(actionDefaults);
  }
  reminderSections.push(
    `[OTTERCAMP_CONTEXT_REMINDER]\n- ${buildContextReminder(context)}\n[/OTTERCAMP_CONTEXT_REMINDER]`,
  );
  const reminder = reminderSections.join('\n\n');
  if (!includeUserContent) {
    return reminder;
  }
  return `${reminder}\n\n${content}`;
}

export async function formatSessionContextMessageForTest(
  sessionKey: string,
  rawContent: string,
): Promise<string> {
  return withSessionContext(sessionKey, rawContent);
}

export async function formatSessionSystemPromptForTest(
  sessionKey: string,
  rawContent: string,
): Promise<string> {
  return withSessionContext(sessionKey, rawContent, { includeUserContent: false });
}

export async function formatAutoRecallMessageForTest(
  sessionKey: string,
  rawContent: string,
): Promise<string> {
  return withAutoRecallContext(sessionKey, rawContent);
}

function extractMessageContent(value: unknown): string {
  if (typeof value === 'string') {
    return value.trim();
  }
  if (Array.isArray(value)) {
    const parts = value
      .map((entry) => {
        if (typeof entry === 'string') {
          return entry.trim();
        }
        const record = asRecord(entry);
        if (!record) {
          return '';
        }
        return getTrimmedString(record.text) || getTrimmedString(record.content) || getTrimmedString(record.body);
      })
      .filter(Boolean);
    return parts.join('\n').trim();
  }

  const record = asRecord(value);
  if (!record) {
    return '';
  }
  return (
    getTrimmedString(record.content) ||
    getTrimmedString(record.text) ||
    getTrimmedString(record.body) ||
    ''
  );
}

function normalizeQuestionnaireType(value: unknown): QuestionnaireQuestion['type'] | null {
  const normalized = getTrimmedString(value).toLowerCase();
  switch (normalized) {
    case 'text':
    case 'textarea':
    case 'boolean':
    case 'select':
    case 'multiselect':
    case 'number':
    case 'date':
      return normalized;
    default:
      return null;
  }
}

export function normalizeQuestionnairePayload(raw: unknown): QuestionnairePayload | null {
  const record = asRecord(raw);
  if (!record) {
    return null;
  }
  const id = getTrimmedString(record.id);
  if (!id) {
    return null;
  }

  const rawQuestions = Array.isArray(record.questions) ? record.questions : [];
  const questions: QuestionnaireQuestion[] = [];
  for (const entry of rawQuestions) {
    const question = asRecord(entry);
    if (!question) {
      continue;
    }
    const questionID = getTrimmedString(question.id);
    const text = getTrimmedString(question.text);
    const questionType = normalizeQuestionnaireType(question.type);
    if (!questionID || !text || !questionType) {
      continue;
    }
    const optionsRaw = Array.isArray(question.options) ? question.options : [];
    const optionSet = new Set<string>();
    for (const option of optionsRaw) {
      const trimmed = getTrimmedString(option);
      if (!trimmed) {
        continue;
      }
      optionSet.add(trimmed);
    }
    const options = Array.from(optionSet);

    questions.push({
      id: questionID,
      text,
      type: questionType,
      required: getBoolean(question.required),
      options: options.length > 0 ? options : undefined,
    });
  }
  if (questions.length === 0) {
    return null;
  }

  const responsesRecord = asRecord(record.responses);
  const responses = responsesRecord ? responsesRecord : undefined;
  return {
    id,
    contextType: getTrimmedString(record.context_type) || undefined,
    contextID: getTrimmedString(record.context_id) || undefined,
    author: getTrimmedString(record.author) || undefined,
    title: getTrimmedString(record.title) || undefined,
    questions,
    responses,
  };
}

export function formatQuestionnaireForFallback(questionnaire: QuestionnairePayload): string {
  const lines: string[] = [];
  lines.push('[QUESTIONNAIRE]');
  lines.push(`Questionnaire ID: ${questionnaire.id}`);
  if (questionnaire.title) {
    lines.push(`Title: ${questionnaire.title}`);
  }
  if (questionnaire.author) {
    lines.push(`Author: ${questionnaire.author}`);
  }
  lines.push('Reply with numbered answers, for example:');
  lines.push('1. ...');
  lines.push('2. ...');
  lines.push('');

  questionnaire.questions.forEach((question, index) => {
    const meta: string[] = [question.type];
    if (question.required) {
      meta.push('required');
    }
    lines.push(`${index + 1}. ${question.text} [id=${question.id}; ${meta.join(', ')}]`);
    if (question.options && question.options.length > 0) {
      lines.push(`   options: ${question.options.join(' | ')}`);
    }
  });
  lines.push('[/QUESTIONNAIRE]');
  return lines.join('\n');
}

export function parseNumberedAnswers(content: string): Map<number, string> {
  const lines = content.split(/\r?\n/);
  const answers = new Map<number, string>();
  let currentIndex = -1;
  let currentLines: string[] = [];

  const flush = () => {
    if (currentIndex <= 0) {
      return;
    }
    const value = currentLines.join('\n').trim();
    if (value) {
      answers.set(currentIndex, value);
    }
  };

  for (const line of lines) {
    const match = /^\s*(\d+)[\.\)]\s*(.*)$/.exec(line);
    if (match) {
      flush();
      currentIndex = Number(match[1]);
      currentLines = [match[2] || ''];
      continue;
    }
    if (currentIndex > 0) {
      currentLines.push(line);
    }
  }
  flush();

  return answers;
}

function parseBooleanText(value: string): boolean | null {
  const normalized = value.trim().toLowerCase();
  if (['true', 'yes', 'y', '1'].includes(normalized)) {
    return true;
  }
  if (['false', 'no', 'n', '0'].includes(normalized)) {
    return false;
  }
  return null;
}

export function parseQuestionnaireAnswer(
  question: QuestionnaireQuestion,
  rawAnswer: string,
): { value: unknown; valid: boolean } {
  const trimmed = rawAnswer.trim();
  if (!trimmed) {
    return { value: undefined, valid: false };
  }

  switch (question.type) {
    case 'text':
    case 'textarea':
    case 'date':
      return { value: trimmed, valid: true };
    case 'boolean': {
      const parsed = parseBooleanText(trimmed);
      if (parsed === null) {
        return { value: undefined, valid: false };
      }
      return { value: parsed, valid: true };
    }
    case 'number': {
      const value = Number(trimmed);
      if (!Number.isFinite(value)) {
        return { value: undefined, valid: false };
      }
      return { value, valid: true };
    }
    case 'select':
      if (!question.options || question.options.length === 0) {
        return { value: trimmed, valid: true };
      }
      for (const option of question.options) {
        if (option.toLowerCase() === trimmed.toLowerCase()) {
          return { value: option, valid: true };
        }
      }
      return { value: undefined, valid: false };
    case 'multiselect': {
      const parts = trimmed
        .split(/[,|]/)
        .map((part) => part.trim())
        .filter(Boolean);
      if (parts.length === 0) {
        return { value: undefined, valid: false };
      }
      if (!question.options || question.options.length === 0) {
        return { value: parts, valid: true };
      }
      const normalized: string[] = [];
      const seen = new Set<string>();
      for (const part of parts) {
        const match = question.options.find((option) => option.toLowerCase() === part.toLowerCase());
        if (!match) {
          return { value: undefined, valid: false };
        }
        if (seen.has(match)) {
          continue;
        }
        seen.add(match);
        normalized.push(match);
      }
      if (normalized.length === 0) {
        return { value: undefined, valid: false };
      }
      return { value: normalized, valid: true };
    }
    default:
      return { value: undefined, valid: false };
  }
}

export function parseNumberedQuestionnaireResponse(
  content: string,
  questionnaire: QuestionnairePayload,
): {
  responses: Record<string, unknown>;
  highConfidence: boolean;
} | null {
  const answers = parseNumberedAnswers(content);
  if (answers.size === 0) {
    return null;
  }

  const responses: Record<string, unknown> = {};
  let invalidCount = 0;
  let requiredCount = 0;
  let requiredAnswered = 0;
  let answeredCount = 0;

  questionnaire.questions.forEach((question, idx) => {
    if (question.required) {
      requiredCount += 1;
    }

    const answer = answers.get(idx + 1);
    if (!answer) {
      return;
    }

    answeredCount += 1;
    const parsed = parseQuestionnaireAnswer(question, answer);
    if (!parsed.valid) {
      invalidCount += 1;
      return;
    }

    responses[question.id] = parsed.value;
    if (question.required) {
      requiredAnswered += 1;
    }
  });

  if (Object.keys(responses).length === 0) {
    return null;
  }

  return {
    responses,
    highConfidence: invalidCount === 0 && requiredAnswered >= requiredCount && answeredCount > 0,
  };
}

async function submitQuestionnaireResponse(
  orgID: string,
  questionnaireID: string,
  respondedBy: string,
  responses: Record<string, unknown>,
): Promise<boolean> {
  if (!orgID || !questionnaireID || !respondedBy || Object.keys(responses).length === 0) {
    return false;
  }

  const url = new URL(
    `/api/questionnaires/${encodeURIComponent(questionnaireID)}/response`,
    OTTERCAMP_URL,
  );
  url.searchParams.set('org_id', orgID);

  const response = await fetchWithRetry(url.toString(), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
    body: JSON.stringify({
      responded_by: respondedBy,
      responses,
    }),
  }, 'persist questionnaire response');

  if (!response.ok) {
    const snippet = (await response.text().catch(() => '')).slice(0, 300);
    console.warn(
      `[bridge] questionnaire response submit failed (${response.status} ${response.statusText} ${snippet})`,
    );
    return false;
  }
  return true;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function shouldRetryStatus(status: number): boolean {
  return status === 429 || status >= 500;
}

function markRunIDDelivered(runID: string): void {
  const normalized = runID.trim();
  if (!normalized || deliveredRunIDs.has(normalized)) {
    return;
  }
  deliveredRunIDs.add(normalized);
  deliveredRunIDOrder.push(normalized);

  while (deliveredRunIDOrder.length > MAX_TRACKED_RUN_IDS) {
    const stale = deliveredRunIDOrder.shift();
    if (stale) {
      deliveredRunIDs.delete(stale);
    }
  }
}

function markActivityEventDelivered(eventID: string): void {
  const normalized = getTrimmedString(eventID);
  if (!normalized || deliveredActivityEventIDs.has(normalized)) {
    return;
  }
  deliveredActivityEventIDs.add(normalized);
  deliveredActivityEventIDOrder.push(normalized);
  while (deliveredActivityEventIDOrder.length > MAX_TRACKED_ACTIVITY_EVENT_IDS) {
    const stale = deliveredActivityEventIDOrder.shift();
    if (stale) {
      deliveredActivityEventIDs.delete(stale);
    }
  }
}

function resolveActivityOrgID(sessionKey: string): string {
  const fromContext = getTrimmedString(sessionContexts.get(sessionKey)?.orgID);
  if (fromContext) {
    return fromContext;
  }
  return resolveConfiguredOtterCampOrgID();
}

export function queueActivityEventsForOrg(orgID: string, events: BridgeAgentActivityEvent[]): number {
  const normalizedOrgID = getTrimmedString(orgID);
  if (!normalizedOrgID || events.length === 0) {
    return 0;
  }

  const queue = queuedActivityEventsByOrg.get(normalizedOrgID) || [];
  let queued = 0;
  for (const event of events) {
    const eventID = getTrimmedString(event.id);
    if (!eventID) {
      continue;
    }
    if (queuedActivityEventIDs.has(eventID) || deliveredActivityEventIDs.has(eventID)) {
      continue;
    }
    queue.push(event);
    queuedActivityEventIDs.add(eventID);
    queued += 1;
  }
  if (queue.length > 0) {
    queuedActivityEventsByOrg.set(normalizedOrgID, queue);
  }
  return queued;
}

function queueSessionDeltaActivityEvents(events: BridgeAgentActivityEvent[]): number {
  let queued = 0;
  const grouped = new Map<string, BridgeAgentActivityEvent[]>();
  for (const event of events) {
    const orgID = resolveActivityOrgID(event.session_key);
    if (!orgID) {
      continue;
    }
    const bucket = grouped.get(orgID) || [];
    bucket.push(event);
    grouped.set(orgID, bucket);
  }
  for (const [orgID, orgEvents] of grouped.entries()) {
    queued += queueActivityEventsForOrg(orgID, orgEvents);
  }
  return queued;
}

function buildDispatchCorrelationEvent(params: {
  orgID: string;
  trigger: 'dispatch.dm' | 'dispatch.project_chat' | 'dispatch.issue';
  correlationID: string;
  sessionKey: string;
  agentID: string;
  summary: string;
  detail?: string;
  scope?: AgentActivityScope;
}): BridgeAgentActivityEvent | null {
  const orgID = getTrimmedString(params.orgID);
  const correlationID = getTrimmedString(params.correlationID);
  const sessionKey = getTrimmedString(params.sessionKey);
  const agentID = getTrimmedString(params.agentID);
  if (!orgID || !correlationID || !sessionKey || !agentID) {
    return null;
  }

  const startedAt = new Date().toISOString();
  const idSeed = [orgID, params.trigger, correlationID, sessionKey, agentID].join('|');
  const event: BridgeAgentActivityEvent = {
    id: `dispatch_${crypto.createHash('sha1').update(idSeed).digest('hex').slice(0, 24)}`,
    agent_id: agentID,
    session_key: sessionKey,
    trigger: params.trigger,
    channel: 'system',
    summary: getTrimmedString(params.summary) || 'Dispatch processed',
    detail: getTrimmedString(params.detail) || undefined,
    tokens_used: 0,
    duration_ms: 0,
    status: 'completed',
    started_at: startedAt,
    completed_at: startedAt,
  };
  if (params.scope && (params.scope.project_id || params.scope.issue_id || params.scope.issue_number || params.scope.thread_id)) {
    event.scope = params.scope;
  }
  return event;
}

function queueDispatchCorrelationEvent(params: {
  orgID: string;
  trigger: 'dispatch.dm' | 'dispatch.project_chat' | 'dispatch.issue';
  correlationID: string;
  sessionKey: string;
  agentID: string;
  summary: string;
  detail?: string;
  scope?: AgentActivityScope;
}): void {
  const event = buildDispatchCorrelationEvent(params);
  if (!event) {
    return;
  }
  queueActivityEventsForOrg(params.orgID, [event]);
}

async function pushActivityEventBatch(orgID: string, events: BridgeAgentActivityEvent[]): Promise<boolean> {
  if (!orgID || events.length === 0) {
    return true;
  }

  const response = await fetchWithRetry(`${OTTERCAMP_URL}/api/activity/events`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN
        ? {
            Authorization: `Bearer ${OTTERCAMP_TOKEN}`,
            'X-OpenClaw-Token': OTTERCAMP_TOKEN,
          }
        : {}),
    },
    body: JSON.stringify({
      org_id: orgID,
      events,
    }),
  }, 'push activity events');
  if (!response.ok) {
    const snippet = (await response.text().catch(() => '')).slice(0, 240);
    console.error(
      `[bridge] activity event push failed (${orgID}): ${response.status} ${response.statusText} ${snippet}`.trim(),
    );
    return false;
  }
  return true;
}

export async function flushBufferedActivityEvents(reason = 'manual'): Promise<number> {
  let pushed = 0;
  for (const [orgID, queue] of Array.from(queuedActivityEventsByOrg.entries())) {
    while (queue.length > 0) {
      const batch = queue.slice(0, ACTIVITY_EVENTS_BATCH_SIZE);
      const ok = await pushActivityEventBatch(orgID, batch);
      if (!ok) {
        break;
      }
      queue.splice(0, batch.length);
      for (const event of batch) {
        const eventID = getTrimmedString(event.id);
        if (eventID) {
          queuedActivityEventIDs.delete(eventID);
          markActivityEventDelivered(eventID);
        }
      }
      pushed += batch.length;
    }
    if (queue.length === 0) {
      queuedActivityEventsByOrg.delete(orgID);
    } else {
      queuedActivityEventsByOrg.set(orgID, queue);
    }
  }
  if (pushed > 0) {
    console.log(`[bridge] flushed ${pushed} activity event(s) (${reason})`);
  }
  return pushed;
}

export function resetBufferedActivityEventsForTest(): void {
  queuedActivityEventsByOrg.clear();
  queuedActivityEventIDs.clear();
  deliveredActivityEventIDs.clear();
  deliveredActivityEventIDOrder.length = 0;
  gitCompletionDefaultsResolved = false;
  gitCompletionBranch = '';
  gitCompletionRemote = '';
}

export function getBufferedActivityEventStateForTest(): {
  queuedEventIDCount: number;
  deliveredEventIDCount: number;
} {
  return {
    queuedEventIDCount: queuedActivityEventIDs.size,
    deliveredEventIDCount: deliveredActivityEventIDs.size,
  };
}

export function resetSessionContextsForTest(): void {
  sessionContexts.clear();
  contextPrimedSessions.clear();
}

export function setSessionContextForTest(sessionKey: string, context: SessionContext): void {
  setSessionContext(sessionKey, context);
}

export function getSessionContextStateForTest(): { count: number; keys: string[] } {
  return {
    count: sessionContexts.size,
    keys: Array.from(sessionContexts.keys()),
  };
}

export function getSessionContextForTest(sessionKey: string): Record<string, unknown> | null {
  const context = sessionContexts.get(getTrimmedString(sessionKey));
  if (!context) {
    return null;
  }
  return {
    kind: context.kind,
    orgID: context.orgID,
    threadID: context.threadID,
    agentID: context.agentID,
    agentName: context.agentName,
    projectID: context.projectID,
    issueID: context.issueID,
    issueNumber: context.issueNumber,
    issueTitle: context.issueTitle,
    documentPath: context.documentPath,
    responderAgentID: context.responderAgentID,
    displayLabel: context.displayLabel,
    executionMode: context.executionMode,
    projectRoot: context.projectRoot,
  };
}

async function fetchWithRetry(
  input: RequestInfo | URL,
  init: RequestInit,
  label: string,
): Promise<Response> {
  let attempt = 0;
  for (;;) {
    attempt += 1;
    try {
      const response = await fetch(input, init);
      if (!shouldRetryStatus(response.status) || attempt > FETCH_RETRY_DELAYS_MS.length) {
        return response;
      }
      const delay = FETCH_RETRY_DELAYS_MS[attempt - 1];
      console.warn(
        `[bridge] ${label} received ${response.status}; retrying in ${delay}ms (attempt ${attempt + 1})`,
      );
      await sleep(delay);
    } catch (err) {
      if (attempt > FETCH_RETRY_DELAYS_MS.length) {
        throw err;
      }
      const delay = FETCH_RETRY_DELAYS_MS[attempt - 1];
      console.warn(
        `[bridge] ${label} network error; retrying in ${delay}ms (attempt ${attempt + 1})`,
      );
      await sleep(delay);
    }
  }
}

function normalizeModeArg(value: string | undefined): 'once' | 'continuous' {
  if (!value) {
    return 'once';
  }
  const normalized = value.replace(/^--/, '').trim().toLowerCase();
  return normalized === 'continuous' ? 'continuous' : 'once';
}

export function buildOtterCampWSURL(secret: string = OTTERCAMP_WS_SECRET): string {
  const url = new URL(OTTERCAMP_URL);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.pathname = '/ws/openclaw';
  if (secret) {
    url.searchParams.set('token', secret);
  }
  return url.toString();
}

export function sanitizeWebSocketURLForLog(rawURL: string): string {
  try {
    const parsed = new URL(rawURL);
    return `${parsed.protocol}//${parsed.host}${parsed.pathname}`;
  } catch {
    return "[invalid-url]";
  }
}

async function connectToOpenClaw(): Promise<void> {
  if (openClawWS && (openClawWS.readyState === WebSocket.OPEN || openClawWS.readyState === WebSocket.CONNECTING)) {
    return;
  }

  return new Promise((resolve, reject) => {
    const url = `ws://${OPENCLAW_HOST}:${OPENCLAW_PORT}`;
    console.log(`[bridge] connecting to OpenClaw gateway at ${url}`);
    markSocketConnectAttempt('openclaw');

    openClawWS = new WebSocket(url);
    let settled = false;
    const resolveOnce = () => {
      if (settled) {
        return;
      }
      settled = true;
      resolve();
    };
    const rejectOnce = (err: unknown) => {
      if (settled) {
        return;
      }
      settled = true;
      reject(err);
    };

    openClawWS.on('open', () => {
      markSocketOpen('openclaw');
      startHeartbeatLoop('openclaw', openClawWS!, () => {
        if (openClawWS && openClawWS.readyState === WebSocket.OPEN) {
          openClawWS.close();
        }
      });
      console.log('[bridge] OpenClaw socket opened, waiting for challenge');
    });

    openClawWS.on('message', (data) => {
      markSocketMessage('openclaw');
      try {
        const msg = JSON.parse(data.toString()) as Record<string, unknown>;

        if (msg.type === 'event' && msg.event === 'connect.challenge') {
          const role = 'operator';
          const scopes = ['operator.read', 'operator.admin'];
          const clientID = 'gateway-client';
          const clientMode = 'backend';
          const challengePayload = asRecord((msg as { payload?: unknown }).payload) || asRecord((msg as { data?: unknown }).data);
          const nonce = getTrimmedString(challengePayload?.nonce) || undefined;
          const deviceIdentity = loadDeviceIdentity();
          const deviceToken = deviceIdentity ? loadDeviceRoleToken(deviceIdentity.deviceId, role) : '';
          const authToken = OPENCLAW_TOKEN || deviceToken;

          let device:
            | {
                id: string;
                publicKey: string;
                signature: string;
                signedAt: number;
                nonce?: string;
              }
            | undefined;

          if (deviceIdentity) {
            const signedAtMs = Date.now();
            const signaturePayload = buildDeviceAuthPayload({
              deviceId: deviceIdentity.deviceId,
              clientId: clientID,
              clientMode,
              role,
              scopes,
              signedAtMs,
              token: authToken || '',
              nonce,
            });
            device = {
              id: deviceIdentity.deviceId,
              publicKey: base64UrlEncode(derivePublicKeyRaw(deviceIdentity.publicKeyPem)),
              signature: signDevicePayload(deviceIdentity.privateKeyPem, signaturePayload),
              signedAt: signedAtMs,
              ...(nonce ? { nonce } : {}),
            };
          } else {
            console.warn('[bridge] device identity not found; OpenClaw may reject connect');
          }

          const connectId = genId();
          const connectMsg = {
            type: 'req',
            id: connectId,
            method: 'connect',
            params: {
              minProtocol: 3,
              maxProtocol: 3,
              client: {
                id: clientID,
                version: '1.0.0',
                platform: 'macos',
                mode: clientMode,
              },
              role,
              scopes,
              caps: [],
              commands: [],
              permissions: {},
              auth: authToken ? { token: authToken } : undefined,
              device,
              locale: 'en-US',
              userAgent: 'ottercamp-bridge/1.0.0',
            },
          };

          pendingRequests.set(connectId, {
            resolve: () => {
              console.log('[bridge] connected to OpenClaw gateway');
              void flushDispatchReplayQueue('openclaw-connected').catch((replayErr) => {
                console.error('[bridge] failed to flush replay queue after OpenClaw connect:', replayErr);
              });
              resolveOnce();
            },
            reject: (err) => rejectOnce(err),
          });

          openClawWS!.send(JSON.stringify(connectMsg));
          return;
        }

        if (msg.type === 'res') {
          const responseID = typeof msg.id === 'string' ? msg.id : '';
          const pending = pendingRequests.get(responseID);
          if (!pending) {
            return;
          }

          pendingRequests.delete(responseID);
          if (msg.ok) {
            pending.resolve((msg as { payload?: unknown }).payload);
          } else {
            const maybeError = msg as { error?: { message?: string } };
            pending.reject(new Error(maybeError.error?.message || 'request failed'));
          }
          return;
        }

        if (msg.type === 'event') {
          void handleOpenClawEvent(msg);
        }
      } catch (err) {
        console.error('[bridge] failed to parse OpenClaw message:', err);
      }
    });

    openClawWS.on('error', (err) => {
      console.error('[bridge] OpenClaw socket error:', err);
      rejectOnce(err);
      if (openClawWS && openClawWS.readyState === WebSocket.OPEN) {
        openClawWS.close();
      }
    });

    openClawWS.on('pong', () => {
      markHeartbeatPong('openclaw');
    });

    openClawWS.on('close', (code, reason) => {
      handleOpenClawSocketClosed(
        code,
        reason.toString(),
        rejectOnce,
        () => {
          void connectToOpenClaw().catch((connectErr) => {
            console.error('[bridge] OpenClaw reconnect failed:', connectErr);
          });
        },
      );
    });

    setTimeout(() => {
      rejectOnce(new Error('OpenClaw connection timeout'));
    }, 30000);
  });
}

async function sendRequest(method: string, params: Record<string, unknown> = {}): Promise<unknown> {
  if (!openClawWS || openClawWS.readyState !== WebSocket.OPEN) {
    throw new Error('not connected to OpenClaw');
  }

  const id = genId();

  return new Promise((resolve, reject) => {
    pendingRequests.set(id, { resolve, reject });

    openClawWS!.send(
      JSON.stringify({
        type: 'req',
        id,
        method,
        params,
      }),
    );

    setTimeout(() => {
      if (!pendingRequests.has(id)) {
        return;
      }
      pendingRequests.delete(id);
      reject(new Error(`request timeout for ${method}`));
    }, 30000);
  });
}

async function sendMessageToSession(
  sessionKey: string,
  content: string,
  messageID?: string,
): Promise<void> {
  const idempotencyKey =
    (messageID || '').trim() || `dm-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
  const isFirstDispatchForSession = !contextPrimedSessions.has(sessionKey);
  const isCanonicalChameleonSession = parseChameleonSessionKey(sessionKey) !== null;
  const recallAwareContent = await withAutoRecallContext(sessionKey, content);
  if (!recallAwareContent) {
    return;
  }

  if (isFirstDispatchForSession) {
    const firstContext = sessionContexts.get(sessionKey);
    const displayLabel = getTrimmedString(firstContext?.displayLabel);
    const projectRoot = getTrimmedString(firstContext?.projectRoot);
    const shouldSetCwd = getTrimmedString(firstContext?.executionMode) === 'project' && projectRoot;
    if (displayLabel || shouldSetCwd) {
      const updatePayload: Record<string, unknown> = { sessionKey };
      if (displayLabel) {
        updatePayload.displayName = displayLabel;
      }
      if (shouldSetCwd) {
        updatePayload.cwd = projectRoot;
      }
      try {
        await sendRequest('sessions.update', updatePayload);
      } catch (err) {
        console.warn(`[bridge] unable to set session metadata for ${sessionKey}:`, err);
      }
    }
  }

  if (isCanonicalChameleonSession) {
    const extraSystemPrompt = await withSessionContext(sessionKey, recallAwareContent, {
      includeUserContent: false,
    });
    try {
      await sendRequest('agent', {
        idempotencyKey,
        sessionKey,
        message: recallAwareContent,
        deliver: false,
        ...(extraSystemPrompt ? { extraSystemPrompt } : {}),
      });
      return;
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      console.warn(
        `[bridge] agent method dispatch failed for ${sessionKey}; falling back to chat.send: ${detail}`,
      );
      // Rebuild full user-context envelope for fallback compatibility.
      contextPrimedSessions.delete(sessionKey);
    }
  }

  const contextualContent = await withSessionContext(sessionKey, recallAwareContent);
  if (!contextualContent) {
    return;
  }
  await sendRequest('chat.send', {
    idempotencyKey,
    sessionKey,
    message: contextualContent,
  });
}

async function persistAssistantReplyToOtterCamp(params: {
  sessionKey: string;
  content: string;
  runID?: string;
  assistantName?: string;
}): Promise<void> {
  const sessionKey = getTrimmedString(params.sessionKey);
  const content = params.content.trim();
  if (!sessionKey || !content) {
    return;
  }

  const context = sessionContexts.get(sessionKey);
  if (!context) {
    // Ignore non-DM assistant activity (e.g. cron/system sessions).
    return;
  }
  let persistedTarget = sessionKey;

  const pendingQuestionnaire = context.pendingQuestionnaire;
  if (
    pendingQuestionnaire &&
    !pendingQuestionnaire.responses &&
    (context.kind === 'project_chat' || context.kind === 'issue_comment')
  ) {
    const parsed = parseNumberedQuestionnaireResponse(content, pendingQuestionnaire);
    if (parsed?.highConfidence) {
      const orgID = getTrimmedString(context.orgID);
      const respondedBy =
        params.assistantName?.trim() ||
        getTrimmedString(context.agentName) ||
        getTrimmedString(context.agentID) ||
        'Bridge responder';
      try {
        const submitted = await submitQuestionnaireResponse(
          orgID,
          pendingQuestionnaire.id,
          respondedBy,
          parsed.responses,
        );
        if (submitted) {
          sessionContexts.set(sessionKey, {
            ...context,
            pendingQuestionnaire: undefined,
          });
          if (params.runID) {
            markRunIDDelivered(params.runID);
          }
          console.log(
            `[bridge] captured numbered questionnaire response for ${pendingQuestionnaire.id} from ${sessionKey}`,
          );
          return;
        }
      } catch (err) {
        console.warn(
          `[bridge] failed to persist structured questionnaire response for ${pendingQuestionnaire.id}:`,
          err,
        );
      }
    }
  }

  if (context.kind === 'dm') {
    const orgID = getTrimmedString(context.orgID);
    const threadID = getTrimmedString(context.threadID);
    if (!orgID || !threadID || !threadID.startsWith('dm_')) {
      return;
    }

    const agentID = getTrimmedString(context.agentID) || parseAgentIDFromSessionKey(sessionKey);
    const senderName = params.assistantName?.trim() || (agentID ? agentID : 'Agent');
    const body: Record<string, unknown> = {
      org_id: orgID,
      thread_id: threadID,
      content,
      sender_type: 'agent',
      sender_name: senderName,
    };
    if (agentID) {
      body.sender_id = agentID;
    }

    const response = await fetchWithRetry(`${OTTERCAMP_URL}/api/messages`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
      },
      body: JSON.stringify(body),
    }, 'persist assistant dm reply');
    if (!response.ok) {
      const snippet = (await response.text().catch(() => '')).slice(0, 300);
      throw new Error(`message persist failed: ${response.status} ${response.statusText} ${snippet}`.trim());
    }
    persistedTarget = threadID;
  } else if (context.kind === 'project_chat') {
    const orgID = getTrimmedString(context.orgID);
    const projectID = getTrimmedString(context.projectID);
    if (!orgID || !projectID) {
      return;
    }

    const body = {
      author:
        params.assistantName?.trim() ||
        getTrimmedString(context.agentName) ||
        getTrimmedString(context.agentID) ||
        'Assistant',
      body: content,
      sender_type: 'agent',
    };
    const url = `${OTTERCAMP_URL}/api/projects/${encodeURIComponent(projectID)}/chat/messages?org_id=${encodeURIComponent(orgID)}`;
    const response = await fetchWithRetry(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
      },
      body: JSON.stringify(body),
    }, 'persist assistant project-chat reply');
    if (!response.ok) {
      const snippet = (await response.text().catch(() => '')).slice(0, 300);
      throw new Error(`project chat persist failed: ${response.status} ${response.statusText} ${snippet}`.trim());
    }
    persistedTarget = `project:${projectID}`;
  } else if (context.kind === 'issue_comment') {
    const orgID = getTrimmedString(context.orgID);
    const issueID = getTrimmedString(context.issueID);
    const responderAgentID = getTrimmedString(context.responderAgentID);
    if (!orgID || !issueID || !responderAgentID) {
      return;
    }

    const body = {
      author_agent_id: responderAgentID,
      body: content,
      sender_type: 'agent',
    };
    const url = `${OTTERCAMP_URL}/api/issues/${encodeURIComponent(issueID)}/comments?org_id=${encodeURIComponent(orgID)}`;
    const response = await fetchWithRetry(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
      },
      body: JSON.stringify(body),
    }, 'persist assistant issue reply');
    if (!response.ok) {
      const snippet = (await response.text().catch(() => '')).slice(0, 300);
      throw new Error(`issue comment persist failed: ${response.status} ${response.statusText} ${snippet}`.trim());
    }
    persistedTarget = `issue:${issueID}`;
  } else {
    return;
  }

  if (params.runID) {
    markRunIDDelivered(params.runID);
  }
  console.log(
    `[bridge] persisted assistant reply to ${persistedTarget}${params.runID ? ` (run_id=${params.runID})` : ''}`,
  );
}

export type CompactionSignal = {
  sessionKey: string;
  orgID?: string;
  agentID?: string;
  summaryText: string;
  preTokens?: number;
  postTokens?: number;
  reason: 'explicit' | 'heuristic';
};

type CompactionRecoveryDeps = {
  fetchRecoveryContext: (signal: CompactionSignal) => Promise<string>;
  sendRecoveryMessage: (signal: CompactionSignal, contextText: string, idempotencyKey: string) => Promise<void>;
  recordCompaction: (signal: CompactionSignal) => Promise<void>;
  sleepFn: (ms: number) => Promise<void>;
  nowMs: () => number;
};

function toFiniteNumber(value: unknown): number | undefined {
  const numeric = typeof value === 'number' ? value : Number.parseFloat(String(value ?? ''));
  if (!Number.isFinite(numeric)) {
    return undefined;
  }
  return numeric;
}

function compactionRecoveryKey(signal: CompactionSignal): string {
  const signature = [
    signal.sessionKey,
    signal.summaryText,
    String(signal.preTokens ?? ''),
    String(signal.postTokens ?? ''),
  ].join('|');
  const hash = crypto.createHash('sha1').update(signature).digest('hex').slice(0, 16);
  return `${signal.sessionKey}:${hash}`;
}

function shouldSkipCompactionRecovery(signal: CompactionSignal, nowMs: number): boolean {
  const key = compactionRecoveryKey(signal);
  const previous = recentCompactionRecoveryByKey.get(key);
  if (!previous) {
    return false;
  }
  return nowMs - previous < COMPACTION_RECOVERY_DEDUP_WINDOW_MS;
}

function rememberCompactionRecovery(signal: CompactionSignal, nowMs: number): void {
  const key = compactionRecoveryKey(signal);
  recentCompactionRecoveryByKey.set(key, nowMs);
  for (const [existingKey, existingAt] of recentCompactionRecoveryByKey.entries()) {
    if (nowMs - existingAt > COMPACTION_RECOVERY_DEDUP_WINDOW_MS) {
      recentCompactionRecoveryByKey.delete(existingKey);
    }
  }
  while (recentCompactionRecoveryByKey.size > MAX_TRACKED_COMPACTION_RECOVERY_KEYS) {
    const oldestKey = recentCompactionRecoveryByKey.keys().next().value;
    if (!oldestKey) {
      break;
    }
    recentCompactionRecoveryByKey.delete(oldestKey);
  }
}

function buildCompactionRecoveryMessage(contextText: string): string {
  const trimmed = contextText.trim();
  if (!trimmed) {
    return '';
  }
  return [
    '[OTTERCAMP_COMPACTION_RECOVERY]',
    trimmed,
    '[/OTTERCAMP_COMPACTION_RECOVERY]',
  ].join('\n');
}

async function recordCompactionMemory(signal: CompactionSignal): Promise<void> {
  if (!signal.orgID || !signal.agentID) {
    return;
  }
  const url = `${OTTERCAMP_URL}/api/memory/entries?org_id=${encodeURIComponent(signal.orgID)}`;
  const body = {
    agent_id: signal.agentID,
    kind: 'summary',
    title: 'Compaction detected',
    content: signal.summaryText || 'Compaction detected by bridge.',
    importance: 4,
    confidence: 0.8,
    sensitivity: 'internal',
    source_session: signal.sessionKey,
    metadata: {
      compaction_reason: signal.reason,
      pre_tokens: signal.preTokens ?? null,
      post_tokens: signal.postTokens ?? null,
    },
  };
  const response = await fetchWithRetry(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
    body: JSON.stringify(body),
  }, 'record compaction memory');
  if (!response.ok) {
    const snippet = (await response.text().catch(() => '')).slice(0, 300);
    throw new Error(`record compaction memory failed: ${response.status} ${response.statusText} ${snippet}`.trim());
  }
}

async function fetchCompactionRecoveryContext(signal: CompactionSignal): Promise<string> {
  if (!signal.orgID || !signal.agentID) {
    return '';
  }
  const recallQuery = signal.summaryText || 'recent compaction context';
  const url = new URL('/api/memory/recall', OTTERCAMP_URL);
  url.searchParams.set('org_id', signal.orgID);
  url.searchParams.set('agent_id', signal.agentID);
  url.searchParams.set('q', recallQuery);
  url.searchParams.set('max_results', '3');
  url.searchParams.set('min_relevance', String(AUTO_RECALL_MIN_RELEVANCE));
  url.searchParams.set('max_chars', String(AUTO_RECALL_MAX_CHARS));

  const response = await fetchWithRetry(url.toString(), {
    method: 'GET',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
  }, 'fetch compaction recovery context');
  if (!response.ok) {
    const snippet = (await response.text().catch(() => '')).slice(0, 300);
    throw new Error(`fetch compaction recovery failed: ${response.status} ${response.statusText} ${snippet}`.trim());
  }

  const payload = asRecord(await response.json().catch(() => null));
  const contextText = getTrimmedString(payload?.context);
  return contextText.slice(0, AUTO_RECALL_MAX_CHARS);
}

async function sendCompactionRecoveryMessage(
  signal: CompactionSignal,
  contextText: string,
  idempotencyKey: string,
): Promise<void> {
  const message = buildCompactionRecoveryMessage(contextText);
  if (!message) {
    return;
  }
  await sendRequest('chat.send', {
    idempotencyKey,
    sessionKey: signal.sessionKey,
    message,
  });
}

async function runCompactionRecovery(
  signal: CompactionSignal,
  deps?: Partial<CompactionRecoveryDeps>,
): Promise<boolean> {
  const fullDeps: CompactionRecoveryDeps = {
    fetchRecoveryContext: deps?.fetchRecoveryContext ?? fetchCompactionRecoveryContext,
    sendRecoveryMessage: deps?.sendRecoveryMessage ?? sendCompactionRecoveryMessage,
    recordCompaction: deps?.recordCompaction ?? recordCompactionMemory,
    sleepFn: deps?.sleepFn ?? sleep,
    nowMs: deps?.nowMs ?? (() => Date.now()),
  };

  const now = fullDeps.nowMs();
  if (shouldSkipCompactionRecovery(signal, now)) {
    return false;
  }

  try {
    await fullDeps.recordCompaction(signal);
  } catch (err) {
    console.warn(`[bridge] failed to record compaction event for ${signal.sessionKey}:`, err);
  }

  const idempotencyKey = `compaction:${compactionRecoveryKey(signal)}`;
  for (let attempt = 0; attempt <= COMPACTION_RECOVERY_RETRY_DELAYS_MS.length; attempt += 1) {
    try {
      const recoveryContext = await fullDeps.fetchRecoveryContext(signal);
      if (!recoveryContext) {
        return false;
      }
      await fullDeps.sendRecoveryMessage(signal, recoveryContext, idempotencyKey);
      rememberCompactionRecovery(signal, fullDeps.nowMs());
      return true;
    } catch (err) {
      if (attempt >= COMPACTION_RECOVERY_RETRY_DELAYS_MS.length) {
        console.warn(`[bridge] compaction recovery failed for ${signal.sessionKey}:`, err);
        return false;
      }
      const delay = COMPACTION_RECOVERY_RETRY_DELAYS_MS[attempt];
      await fullDeps.sleepFn(delay);
    }
  }

  return false;
}

function extractCompactionSignal(eventName: string, payload: Record<string, unknown>): CompactionSignal | null {
  const normalizedEvent = getTrimmedString(eventName).toLowerCase();
  const nested = asRecord(payload.compaction);
  const sessionKey =
    getTrimmedString(payload.sessionKey) ||
    getTrimmedString(payload.session_key) ||
    getTrimmedString(nested?.session_key);
  if (!sessionKey) {
    return null;
  }

  const summaryFromProvider =
    getTrimmedString(payload.summary) ||
    getTrimmedString(payload.summary_text) ||
    getTrimmedString(payload.compaction_summary) ||
    getTrimmedString(nested?.summary_text) ||
    getTrimmedString(nested?.summary);
  const hasSummaryFromProvider = Boolean(summaryFromProvider);
  const summaryText = summaryFromProvider || 'Compaction detected; restore critical context.';
  const preTokens =
    toFiniteNumber(payload.pre_compaction_tokens) ??
    toFiniteNumber(payload.preTokens) ??
    toFiniteNumber(nested?.pre_compaction_tokens);
  const postTokens =
    toFiniteNumber(payload.post_compaction_tokens) ??
    toFiniteNumber(payload.postTokens) ??
    toFiniteNumber(nested?.post_compaction_tokens);

  const explicitSignal =
    normalizedEvent.includes('compaction') ||
    normalizedEvent.includes('compact') ||
    payload.compaction_detected === true ||
    Boolean(nested);
  if (explicitSignal) {
    return {
      sessionKey,
      orgID: getTrimmedString(payload.org_id) || undefined,
      agentID: getTrimmedString(payload.agent_id) || undefined,
      summaryText,
      preTokens,
      postTokens,
      reason: 'explicit',
    };
  }

  const heuristicSignal =
    typeof preTokens === 'number' &&
    typeof postTokens === 'number' &&
    preTokens > 0 &&
    postTokens >= 0 &&
    postTokens < preTokens * 0.65 &&
    hasSummaryFromProvider;
  if (!heuristicSignal) {
    return null;
  }

  return {
    sessionKey,
    orgID: getTrimmedString(payload.org_id) || undefined,
    agentID: getTrimmedString(payload.agent_id) || undefined,
    summaryText,
    preTokens,
    postTokens,
    reason: 'heuristic',
  };
}

export function detectCompactionSignalForTest(
  eventName: string,
  payload: Record<string, unknown>,
): CompactionSignal | null {
  return extractCompactionSignal(eventName, payload);
}

export async function runCompactionRecoveryForTest(
  signal: CompactionSignal,
  deps: Partial<CompactionRecoveryDeps>,
): Promise<boolean> {
  return runCompactionRecovery(signal, deps);
}

export async function fetchCompactionRecoveryContextForTest(signal: CompactionSignal): Promise<string> {
  return fetchCompactionRecoveryContext(signal);
}

export function resetCompactionRecoveryStateForTest(): void {
  recentCompactionRecoveryByKey.clear();
}

async function handleOpenClawEvent(message: Record<string, unknown>): Promise<void> {
  const eventName = getTrimmedString(message.event).toLowerCase();
  const payload = asRecord(message.payload) || asRecord(message.data);
  if (!payload) {
    return;
  }

  const compactionSignal = extractCompactionSignal(eventName, payload);
  if (compactionSignal) {
    const sessionContext = sessionContexts.get(compactionSignal.sessionKey);
    if (sessionContext) {
      if (!compactionSignal.orgID) {
        compactionSignal.orgID = getTrimmedString(sessionContext.orgID) || undefined;
      }
      if (!compactionSignal.agentID) {
        compactionSignal.agentID =
          getTrimmedString(sessionContext.agentID) ||
          getTrimmedString(sessionContext.responderAgentID) ||
          parseAgentIDFromSessionKey(compactionSignal.sessionKey) ||
          undefined;
      }
    } else if (!compactionSignal.agentID) {
      compactionSignal.agentID = parseAgentIDFromSessionKey(compactionSignal.sessionKey) || undefined;
    }

    await runCompactionRecovery(compactionSignal).catch((err) => {
      console.warn(`[bridge] compaction recovery failed for ${compactionSignal.sessionKey}:`, err);
    });
    return;
  }

  // Listen for assistant completions and persist them to DM threads.
  if (eventName !== 'chat') {
    return;
  }

  const state = getTrimmedString(payload.state).toLowerCase();
  if (state !== 'final') {
    return;
  }

  const sessionKey = getTrimmedString(payload.sessionKey) || getTrimmedString(payload.session_key);
  if (!sessionKey) {
    return;
  }
  if (!sessionContexts.has(sessionKey)) {
    return;
  }

  const runID = getTrimmedString(payload.runId) || getTrimmedString(payload.run_id);
  if (runID && deliveredRunIDs.has(runID)) {
    return;
  }

  const messageRecord = asRecord(payload.message);
  const assistantName =
    getTrimmedString(messageRecord?.author) ||
    getTrimmedString(messageRecord?.sender_name) ||
    getTrimmedString(payload.author) ||
    getTrimmedString(payload.agent_name) ||
    undefined;
  const role = (
    getTrimmedString(messageRecord?.role) || getTrimmedString(payload.role) || 'assistant'
  ).toLowerCase();
  if (role && role !== 'assistant') {
    return;
  }

  const content = extractMessageContent(messageRecord?.content ?? payload.content);
  if (!content) {
    return;
  }

  try {
    await persistAssistantReplyToOtterCamp({
      sessionKey,
      content,
      runID: runID || undefined,
      assistantName,
    });
  } catch (err) {
    console.error(`[bridge] failed to persist assistant reply for ${sessionKey}:`, err);
  }
}

async function fetchSessions(): Promise<OpenClawSession[]> {
  const response = (await sendRequest('sessions.list', {
    limit: 50,
  })) as SessionsListResponse;

  return response.sessions || [];
}

type ParsedCompletionProgressLine = {
  issueNumber: number;
  commitSHA: string;
  action: string;
  pushStatus: 'succeeded' | 'failed';
};

export function parseCompletionProgressLine(line: string): ParsedCompletionProgressLine | null {
  const match = COMPLETION_PROGRESS_LINE_PATTERN.exec(getTrimmedString(line));
  if (!match) {
    return null;
  }
  const issueNumber = Number(match[1]);
  const commitSHA = getTrimmedString(match[2]).toLowerCase();
  const action = getTrimmedString(match[3]).toLowerCase();
  if (!Number.isFinite(issueNumber) || issueNumber <= 0 || !commitSHA) {
    return null;
  }
  let pushStatus: 'succeeded' | 'failed' | null = null;
  if (action.includes('pushed')) {
    pushStatus = 'succeeded';
  } else if (action.includes('fail')) {
    pushStatus = 'failed';
  }
  if (!pushStatus) {
    return null;
  }
  return {
    issueNumber,
    commitSHA,
    action,
    pushStatus,
  };
}

async function resolveGitCompletionDefaults(): Promise<{ branch: string; remote: string }> {
  if (gitCompletionDefaultsResolved) {
    return {
      branch: gitCompletionBranch,
      remote: gitCompletionRemote,
    };
  }
  gitCompletionDefaultsResolved = true;

  try {
    const branchResult = await execFileAsync('git', ['rev-parse', '--abbrev-ref', 'HEAD'], {
      timeout: 5000,
      maxBuffer: 128 * 1024,
    });
    gitCompletionBranch = getTrimmedString(branchResult.stdout);
  } catch {
    gitCompletionBranch = '';
  }

  try {
    const remoteResult = await execFileAsync('git', ['remote'], {
      timeout: 5000,
      maxBuffer: 128 * 1024,
    });
    const firstRemote = getTrimmedString(remoteResult.stdout).split(/\r?\n/).find((line) => line.trim().length > 0);
    gitCompletionRemote = getTrimmedString(firstRemote) || 'origin';
  } catch {
    gitCompletionRemote = 'origin';
  }

  return {
    branch: gitCompletionBranch,
    remote: gitCompletionRemote,
  };
}

async function buildCompletionActivityEventFromProgressLine(
  orgID: string,
  line: string,
): Promise<BridgeAgentActivityEvent | null> {
  const parsed = parseCompletionProgressLine(line);
  if (!parsed) {
    return null;
  }

  const defaults = await resolveGitCompletionDefaults();
  const nowISO = new Date().toISOString();
  const idSeed = [orgID, String(parsed.issueNumber), parsed.commitSHA, parsed.action].join('|');
  const status: BridgeAgentActivityEvent['status'] =
    parsed.pushStatus === 'failed' ? 'failed' : 'completed';

  return {
    id: `completion_${crypto.createHash('sha1').update(idSeed).digest('hex').slice(0, 24)}`,
    agent_id: 'system',
    session_key: `completion:issue:${parsed.issueNumber}`,
    trigger: 'task.completion',
    channel: 'system',
    summary: `Captured completion metadata for issue #${parsed.issueNumber}`,
    detail: line.trim(),
    scope: {
      issue_number: parsed.issueNumber,
    },
    tokens_used: 0,
    model_used: undefined,
    commit_sha: parsed.commitSHA,
    commit_branch: defaults.branch || undefined,
    commit_remote: defaults.remote || undefined,
    push_status: parsed.pushStatus,
    duration_ms: 0,
    status,
    started_at: nowISO,
    completed_at: nowISO,
  };
}

async function queueCompletionEventsFromProgressLines(orgID: string, lines: string[]): Promise<number> {
  const normalizedOrgID = getTrimmedString(orgID);
  if (!normalizedOrgID || lines.length === 0) {
    return 0;
  }
  const events: BridgeAgentActivityEvent[] = [];
  for (const line of lines) {
    const event = await buildCompletionActivityEventFromProgressLine(normalizedOrgID, line);
    if (event) {
      events.push(event);
    }
  }
  if (events.length === 0) {
    return 0;
  }
  return queueActivityEventsForOrg(normalizedOrgID, events);
}

export async function buildCompletionActivityEventFromProgressLineForTest(
  orgID: string,
  line: string,
): Promise<BridgeAgentActivityEvent | null> {
  return buildCompletionActivityEventFromProgressLine(orgID, line);
}

function rememberProgressLogLine(line: string): boolean {
  const trimmed = line.trim();
  if (!trimmed) {
    return false;
  }
  const hash = crypto.createHash('sha1').update(trimmed).digest('hex');
  if (progressLogLineHashes.has(hash)) {
    return false;
  }
  progressLogLineHashes.add(hash);
  progressLogLineHashOrder.push(hash);
  while (progressLogLineHashOrder.length > MAX_TRACKED_PROGRESS_LOG_HASHES) {
    const oldest = progressLogLineHashOrder.shift();
    if (oldest) {
      progressLogLineHashes.delete(oldest);
    }
  }
  return true;
}

async function readProgressLogDeltaLines(): Promise<string[]> {
  if (!OTTER_PROGRESS_LOG_PATH) {
    return [];
  }

  let fileBuffer: Buffer;
  try {
    fileBuffer = await fs.promises.readFile(OTTER_PROGRESS_LOG_PATH);
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code === 'ENOENT') {
      progressLogOffsetInitialized = true;
      progressLogByteOffset = 0;
      return [];
    }
    throw err;
  }

  if (!progressLogOffsetInitialized) {
    progressLogOffsetInitialized = true;
    progressLogByteOffset = fileBuffer.length;
    return [];
  }

  if (progressLogByteOffset > fileBuffer.length) {
    progressLogByteOffset = 0;
  }

  if (progressLogByteOffset === fileBuffer.length) {
    return [];
  }

  const delta = fileBuffer.subarray(progressLogByteOffset);
  progressLogByteOffset = fileBuffer.length;

  const newLines = delta
    .toString('utf8')
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .filter((line) => rememberProgressLogLine(line));

  if (newLines.length <= PROGRESS_LOG_MAX_LINES_PER_SYNC) {
    return newLines;
  }

  const trimmedCount = newLines.length - PROGRESS_LOG_MAX_LINES_PER_SYNC;
  console.warn(
    `[bridge] progress-log backpressure: dropping ${trimmedCount} old lines, keeping latest ${PROGRESS_LOG_MAX_LINES_PER_SYNC}`,
  );
  return newLines.slice(newLines.length - PROGRESS_LOG_MAX_LINES_PER_SYNC);
}

function collectSessionDeltaActivityEvents(currentSessions: OpenClawSession[]): BridgeAgentActivityEvent[] {
  const events = buildActivityEventsFromSessionDeltas({
    previousByKey: previousSessionsByKey,
    currentSessions,
  });
  previousSessionsByKey = sessionsByKey(currentSessions);
  return events;
}

function applyJSONMergePatch(target: unknown, patch: unknown): unknown {
  if (patch === null) {
    return null;
  }
  if (Array.isArray(patch) || typeof patch !== 'object') {
    return patch;
  }

  const targetRecord: Record<string, unknown> =
    target && typeof target === 'object' && !Array.isArray(target) ? { ...(target as Record<string, unknown>) } : {};
  for (const [key, patchValue] of Object.entries(patch as Record<string, unknown>)) {
    if (patchValue === null) {
      delete targetRecord[key];
      continue;
    }
    const currentValue = targetRecord[key];
    if (
      patchValue &&
      typeof patchValue === 'object' &&
      !Array.isArray(patchValue) &&
      currentValue &&
      typeof currentValue === 'object' &&
      !Array.isArray(currentValue)
    ) {
      targetRecord[key] = applyJSONMergePatch(currentValue, patchValue);
      continue;
    }
    targetRecord[key] = patchValue;
  }
  return targetRecord;
}

function canonicalizeJSONValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => canonicalizeJSONValue(item));
  }
  if (value && typeof value === 'object') {
    const input = value as Record<string, unknown>;
    const keys = Object.keys(input).sort();
    const out: Record<string, unknown> = {};
    for (const key of keys) {
      out[key] = canonicalizeJSONValue(input[key]);
    }
    return out;
  }
  return value;
}

function hashCanonicalJSON(value: unknown): string {
  const canonical = canonicalizeJSONValue(value);
  const serialized = JSON.stringify(canonical);
  return crypto.createHash('sha256').update(serialized).digest('hex');
}

function readOpenClawConfigFile(configPath: string): unknown {
  try {
    const raw = fs.readFileSync(configPath, 'utf8');
    return parseJSONValue(raw);
  } catch (err) {
    const code = err && typeof err === 'object' ? (err as { code?: string }).code : '';
    if (code === 'ENOENT') {
      return {};
    }
    throw err;
  }
}

function writeOpenClawConfigFile(configPath: string, configValue: unknown): void {
  const backupPath = `${configPath}.bak.${Date.now()}`;
  try {
    if (fs.existsSync(configPath)) {
      fs.copyFileSync(configPath, backupPath);
    }
  } catch (err) {
    console.warn(`[bridge] failed to create config backup ${backupPath}:`, err);
  }
  const serialized = `${JSON.stringify(configValue, null, 2)}\n`;
  const directory = path.dirname(configPath);
  const tempPath = path.join(
    directory,
    `.${path.basename(configPath)}.tmp-${process.pid}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
  );
  try {
    fs.writeFileSync(tempPath, serialized, 'utf8');
    fs.renameSync(tempPath, configPath);
  } catch (err) {
    try {
      if (fs.existsSync(tempPath)) {
        fs.unlinkSync(tempPath);
      }
    } catch (cleanupErr) {
      console.warn(`[bridge] failed to remove temp config file ${tempPath}:`, cleanupErr);
    }
    throw err;
  }
}

async function pushToOtterCamp(sessions: OpenClawSession[]): Promise<void> {
  const [cronJobs, processes, progressLogLines] = await Promise.all([
    fetchCronJobsSnapshot(),
    fetchProcessesSnapshot(),
    readProgressLogDeltaLines(),
  ]);
  const payload = {
    type: 'full',
    timestamp: new Date().toISOString(),
    sessions,
    ...(progressLogLines.length > 0 ? { progress_log_lines: progressLogLines } : {}),
    cron_jobs: cronJobs,
    processes,
    source: 'bridge',
  };

  if (progressLogLines.length > 0 && OTTERCAMP_ORG_ID) {
    const queuedCompletionEvents = await queueCompletionEventsFromProgressLines(OTTERCAMP_ORG_ID, progressLogLines);
    if (queuedCompletionEvents > 0) {
      console.log(`[bridge] queued ${queuedCompletionEvents} completion metadata event(s) from progress log`);
    }
  }

  const url = `${OTTERCAMP_URL}/api/sync/openclaw`;
  const response = await fetchWithRetry(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
    body: JSON.stringify(payload),
  }, 'push sync snapshot');

  if (!response.ok) {
    throw new Error(`sync push failed: ${response.status} ${response.statusText}`);
  }
  lastSuccessfulSyncAtMs = Date.now();

  try {
    await syncWorkflowProjectsFromCronJobs(cronJobs);
  } catch (err) {
    console.error('[bridge] workflow project cron sync failed:', err);
  }
}

async function pullDispatchQueueJobs(limit = 50): Promise<DispatchQueueJob[]> {
  const url = new URL('/api/sync/openclaw/dispatch/pending', OTTERCAMP_URL);
  url.searchParams.set('limit', String(limit));
  const response = await fetchWithRetry(url.toString(), {
    method: 'GET',
    headers: {
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
  }, 'pull queued dispatch jobs');

  if (!response.ok) {
    throw new Error(`dispatch queue pull failed: ${response.status} ${response.statusText}`);
  }

  const payload = (await response.json().catch(() => ({}))) as { jobs?: DispatchQueueJob[] };
  if (!Array.isArray(payload.jobs)) {
    return [];
  }
  return payload.jobs;
}

async function ackDispatchQueueJob(
  jobID: number,
  claimToken: string,
  success: boolean,
  errorMessage?: string,
): Promise<void> {
  const url = `${OTTERCAMP_URL}/api/sync/openclaw/dispatch/${encodeURIComponent(String(jobID))}/ack`;
  const response = await fetchWithRetry(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
    body: JSON.stringify({
      claim_token: claimToken,
      success,
      error: errorMessage || '',
    }),
  }, `ack dispatch queue job ${jobID}`);

  if (!response.ok) {
    const text = (await response.text().catch(() => '')).slice(0, 240);
    throw new Error(`dispatch queue ack failed: ${response.status} ${response.statusText} ${text}`.trim());
  }
}

async function processDispatchQueue(): Promise<void> {
  if (isDispatchQueuePolling) {
    return;
  }
  isDispatchQueuePolling = true;

  try {
    const jobs = await pullDispatchQueueJobs(50);
    if (jobs.length === 0) {
      return;
    }

    for (const job of jobs) {
      const jobID = Number(job.id);
      const claimToken = getTrimmedString(job.claim_token);
      if (!Number.isFinite(jobID) || jobID <= 0 || !claimToken) {
        continue;
      }

      const eventType = getTrimmedString(job.event_type);
      const payload = asRecord(job.payload);
      if (!eventType || !payload) {
        try {
          await ackDispatchQueueJob(jobID, claimToken, false, 'invalid dispatch payload');
        } catch (ackErr) {
          console.error(`[bridge] failed to ack invalid dispatch job ${jobID}:`, ackErr);
        }
        continue;
      }

      try {
        await dispatchInboundEvent(eventType, payload, 'replay');
        await ackDispatchQueueJob(jobID, claimToken, true);
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        console.error(`[bridge] failed processing queued dispatch job ${jobID}:`, err);
        try {
          await ackDispatchQueueJob(jobID, claimToken, false, message);
        } catch (ackErr) {
          console.error(`[bridge] failed to ack dispatch failure for job ${jobID}:`, ackErr);
        }
      }
    }
  } finally {
    isDispatchQueuePolling = false;
  }
}

async function dispatchInboundEvent(
  eventType: string,
  payload: Record<string, unknown>,
  source: 'socket' | 'replay' = 'socket',
): Promise<void> {
  const normalizedType = getTrimmedString(eventType);
  const record = asRecord(payload);
  if (!normalizedType || !record) {
    throw new Error('invalid dispatch payload');
  }
  if (source === 'socket' && IGNORED_OTTERCAMP_SOCKET_EVENT_TYPES.has(normalizedType)) {
    return;
  }

  const dispatch = async (): Promise<boolean> => {
    if (normalizedType === 'dm.message') {
      await handleDMDispatchEvent(record as DMDispatchEvent);
      return true;
    }
    if (normalizedType === 'project.chat.message') {
      await handleProjectChatDispatchEvent(record as ProjectChatDispatchEvent);
      return true;
    }
    if (normalizedType === 'issue.comment.message') {
      await handleIssueCommentDispatchEvent(record as IssueCommentDispatchEvent);
      return true;
    }
    if (normalizedType === 'admin.command') {
      await handleAdminCommandDispatchEvent(record as AdminCommandDispatchEvent);
      return true;
    }
    return false;
  };

  try {
    const handled = await dispatch();
    if (!handled) {
      console.warn(`[bridge] ignoring unsupported ${source} event type: ${normalizedType}`);
      return;
    }
  } catch (err) {
    if (source === 'replay') {
      throw err;
    }
    const queued = queueDispatchEventForReplay(normalizedType, record);
    const queueState = getDispatchReplayQueueStateForTest();
    const message = err instanceof Error ? err.message : String(err);
    if (queued) {
      console.warn(
        `[bridge] queued dispatch event for replay (type=${normalizedType}, queue_depth=${queueState.depth}): ${message}`,
      );
      return;
    }
    throw err;
  }
}

export async function dispatchInboundEventForTest(
  eventType: string,
  payload: Record<string, unknown>,
  source: 'socket' | 'replay' = 'socket',
): Promise<void> {
  await dispatchInboundEvent(eventType, payload, source);
}

async function flushDispatchReplayQueue(reason: string): Promise<number> {
  if (isDispatchReplayFlushing) {
    return 0;
  }
  if (!openClawWS || openClawWS.readyState !== WebSocket.OPEN) {
    return 0;
  }
  if (dispatchReplayQueue.length === 0) {
    return 0;
  }

  let flushed = 0;
  isDispatchReplayFlushing = true;
  try {
    while (dispatchReplayQueue.length > 0) {
      const next = dispatchReplayQueue[0];
      if (!next) {
        break;
      }
      try {
        await dispatchInboundEvent(next.eventType, next.payload, 'replay');
      } catch (err) {
        console.error(
          `[bridge] failed replaying queued dispatch event ${next.id} (${next.eventType}):`,
          err,
        );
        break;
      }
      dispatchReplayQueue.shift();
      queuedDispatchReplayIDs.delete(next.id);
      dispatchReplayQueueBytes = Math.max(0, dispatchReplayQueueBytes - next.sizeBytes);
      rememberDeliveredDispatchReplayID(next.id);
      flushed += 1;
    }
  } finally {
    isDispatchReplayFlushing = false;
  }

  if (flushed > 0) {
    console.log(`[bridge] replayed ${flushed} queued dispatch event(s) (${reason})`);
  }
  return flushed;
}

async function runSerializedPeriodicSync(operation: () => Promise<void>): Promise<boolean> {
  if (isPeriodicSyncRunning) {
    return false;
  }
  isPeriodicSyncRunning = true;
  try {
    await operation();
    return true;
  } finally {
    isPeriodicSyncRunning = false;
  }
}

export async function runSerializedSyncOperationForTest(operation: () => Promise<void>): Promise<boolean> {
  return runSerializedPeriodicSync(operation);
}

export function resetPeriodicSyncGuardForTest(): void {
  isPeriodicSyncRunning = false;
}

async function handleDMDispatchEvent(event: DMDispatchEvent): Promise<void> {
  const sessionKey = (event.data?.session_key || '').trim();
  const content = (event.data?.content || '').trim();
  const orgID = getTrimmedString(event.org_id);
  const threadID = getTrimmedString(event.data?.thread_id);
  const agentID = getTrimmedString(event.data?.agent_id) || parseAgentIDFromSessionKey(sessionKey);
  const messageID = getTrimmedString(event.data?.message_id);

  if (!sessionKey || !content) {
    console.warn('[bridge] skipped dm.message with missing session key or content');
    return;
  }

  const existingContext = sessionContexts.get(sessionKey);
  setSessionContext(sessionKey, {
    ...existingContext,
    kind: 'dm',
    orgID: orgID || existingContext?.orgID,
    threadID: threadID || existingContext?.threadID,
    agentID: agentID || existingContext?.agentID,
  });

  try {
    await sendMessageToSession(sessionKey, content, messageID || undefined);
    console.log(
      `[bridge] delivered dm.message to ${sessionKey} (message_id=${messageID || 'n/a'})`,
    );
    if (orgID && agentID) {
      queueDispatchCorrelationEvent({
        orgID,
        trigger: 'dispatch.dm',
        correlationID: messageID || `${sessionKey}:${content.slice(0, 80)}`,
        sessionKey,
        agentID,
        summary: `Dispatched DM to ${agentID}`,
        detail: content.slice(0, 500),
        scope: {
          thread_id: threadID || undefined,
        },
      });
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (message.includes('missing scope')) {
      console.error(
        '[bridge] OpenClaw token lacks required send scope. Ensure connect requests include operator.admin and token permits it.',
      );
    }
    console.error(`[bridge] failed to deliver dm.message to ${sessionKey}:`, err);
    throw err;
  }
}

async function handleProjectChatDispatchEvent(event: ProjectChatDispatchEvent): Promise<void> {
  const projectID = getTrimmedString(event.data?.project_id);
  const agentID = getTrimmedString(event.data?.agent_id);
  const agentName = getTrimmedString(event.data?.agent_name);
  const content = getTrimmedString(event.data?.content);
  const questionnaire = normalizeQuestionnairePayload(event.data?.questionnaire);
  const orgID = getTrimmedString(event.org_id);
  const messageID = getTrimmedString(event.data?.message_id) || undefined;
  const sessionKey =
    getTrimmedString(event.data?.session_key) ||
    (agentID && projectID ? `agent:${agentID}:project:${projectID}` : '');
  let outboundContent = content;
  if (questionnaire) {
    const formatted = formatQuestionnaireForFallback(questionnaire);
    outboundContent = outboundContent ? `${outboundContent}\n\n${formatted}` : formatted;
  }

  if (!sessionKey || !projectID || !orgID || !outboundContent) {
    console.warn('[bridge] skipped project.chat.message with missing org/project/session/content');
    return;
  }

  setSessionContext(sessionKey, {
    kind: 'project_chat',
    orgID,
    agentID,
    agentName,
    projectID,
    pendingQuestionnaire: questionnaire || undefined,
  });

  try {
    await sendMessageToSession(sessionKey, outboundContent, messageID);
    console.log(
      `[bridge] delivered project.chat.message to ${sessionKey} (message_id=${messageID || 'n/a'})`,
    );
    if (orgID && agentID) {
      queueDispatchCorrelationEvent({
        orgID,
        trigger: 'dispatch.project_chat',
        correlationID: messageID || `${sessionKey}:${content.slice(0, 80)}`,
        sessionKey,
        agentID,
        summary: `Dispatched project chat for ${projectID}`,
        detail: content.slice(0, 500),
        scope: {
          project_id: projectID,
        },
      });
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (message.includes('missing scope')) {
      console.error(
        '[bridge] OpenClaw token lacks required send scope. Ensure connect requests include operator.admin and token permits it.',
      );
    }
    console.error(`[bridge] failed to deliver project.chat.message to ${sessionKey}:`, err);
    throw err;
  }
}

async function handleIssueCommentDispatchEvent(event: IssueCommentDispatchEvent): Promise<void> {
  const issueID = getTrimmedString(event.data?.issue_id);
  const projectID = getTrimmedString(event.data?.project_id);
  const agentID = getTrimmedString(event.data?.agent_id);
  const agentName = getTrimmedString(event.data?.agent_name);
  const responderAgentID = getTrimmedString(event.data?.responder_agent_id);
  const issueTitle = getTrimmedString(event.data?.issue_title);
  const documentPath = getTrimmedString(event.data?.document_path);
  const content = getTrimmedString(event.data?.content);
  const questionnaire = normalizeQuestionnairePayload(event.data?.questionnaire);
  const orgID = getTrimmedString(event.org_id);
  const messageID = getTrimmedString(event.data?.message_id) || undefined;
  const parsedIssueNumber = Number(event.data?.issue_number);
  const issueNumber = Number.isFinite(parsedIssueNumber) ? parsedIssueNumber : undefined;
  const sessionKey =
    getTrimmedString(event.data?.session_key) ||
    (agentID && issueID ? `agent:${agentID}:issue:${issueID}` : '');
  let outboundContent = content;
  if (questionnaire) {
    const formatted = formatQuestionnaireForFallback(questionnaire);
    outboundContent = outboundContent ? `${outboundContent}\n\n${formatted}` : formatted;
  }

  if (!sessionKey || !issueID || !projectID || !orgID || !outboundContent) {
    console.warn('[bridge] skipped issue.comment.message with missing org/project/issue/session/content');
    return;
  }

  setSessionContext(sessionKey, {
    kind: 'issue_comment',
    orgID,
    projectID,
    issueID,
    issueNumber,
    issueTitle,
    documentPath,
    agentID,
    agentName,
    responderAgentID,
    pendingQuestionnaire: questionnaire || undefined,
  });

  try {
    await sendMessageToSession(sessionKey, outboundContent, messageID);
    console.log(
      `[bridge] delivered issue.comment.message to ${sessionKey} (message_id=${messageID || 'n/a'})`,
    );
    const correlationAgentID = responderAgentID || agentID || parseAgentIDFromSessionKey(sessionKey);
    if (orgID && correlationAgentID) {
      const issueLabel = Number.isFinite(issueNumber) ? `#${issueNumber}` : issueID;
      queueDispatchCorrelationEvent({
        orgID,
        trigger: 'dispatch.issue',
        correlationID: messageID || `${sessionKey}:${content.slice(0, 80)}`,
        sessionKey,
        agentID: correlationAgentID,
        summary: `Dispatched issue comment for ${issueLabel}`,
        detail: content.slice(0, 500),
        scope: {
          project_id: projectID,
          issue_id: issueID,
          issue_number: issueNumber,
        },
      });
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (message.includes('missing scope')) {
      console.error(
        '[bridge] OpenClaw token lacks required send scope. Ensure connect requests include operator.admin and token permits it.',
      );
    }
    console.error(`[bridge] failed to deliver issue.comment.message to ${sessionKey}:`, err);
    throw err;
  }
}

async function runOpenClawCommandCapture(args: string[]): Promise<string> {
  const { stdout, stderr } = await execFileAsync('openclaw', args, {
    timeout: 60_000,
    maxBuffer: 2 * 1024 * 1024,
  });
  if (stdout?.trim()) {
    console.log(`[bridge] openclaw ${args.join(' ')} stdout: ${stdout.trim()}`);
  }
  if (stderr?.trim()) {
    console.warn(`[bridge] openclaw ${args.join(' ')} stderr: ${stderr.trim()}`);
  }
  return stdout || '';
}

async function runOpenClawCommand(args: string[]): Promise<void> {
  await runOpenClawCommandCapture(args);
}

/**
 * Flatten an OpenClaw schedule object into a human-readable string.
 * Input shapes: {kind:"every", everyMs:300000} or {kind:"cron", expr:"0 9 * * *", tz:"America/Denver"}
 * or {kind:"at", at:"2026-02-10T15:00:00Z"}
 */
function flattenSchedule(schedule: unknown): string | undefined {
  const obj = asRecord(schedule);
  if (!obj) return undefined;

  const kind = getTrimmedString(obj.kind);
  if (kind === 'every') {
    const ms = typeof obj.everyMs === 'number' ? obj.everyMs : 0;
    if (ms > 0) {
      if (ms >= 3600000) return `${Math.round(ms / 3600000)}h`;
      if (ms >= 60000) return `${Math.round(ms / 60000)}m`;
      return `${Math.round(ms / 1000)}s`;
    }
  }
  if (kind === 'cron') {
    const expr = getTrimmedString(obj.expr);
    const tz = getTrimmedString(obj.tz);
    if (expr) return tz ? `${expr} (${tz})` : expr;
  }
  if (kind === 'at') {
    const at = getTrimmedString(obj.at);
    if (at) return `at:${at}`;
  }
  return undefined;
}

function parseDurationStringToMs(value: string): number | undefined {
  const match = /^(\d+)\s*(ms|s|m|h)$/i.exec(value.trim());
  if (!match) return undefined;
  const amount = Number.parseInt(match[1], 10);
  if (!Number.isFinite(amount) || amount <= 0) return undefined;
  const unit = match[2].toLowerCase();
  if (unit === 'ms') return amount;
  if (unit === 's') return amount * 1000;
  if (unit === 'm') return amount * 60_000;
  return amount * 3_600_000;
}

function splitCronExprAndTZ(value: string): { expr: string; tz?: string } {
  const trimmed = value.trim();
  const tzMatch = /^(.*)\(([^)]+)\)\s*$/.exec(trimmed);
  if (tzMatch) {
    const expr = tzMatch[1].trim();
    const tz = tzMatch[2].trim();
    return { expr, ...(tz ? { tz } : {}) };
  }
  return { expr: trimmed };
}

export function shouldTreatAsSystemWorkflow(name: string): boolean {
  const lower = name.toLowerCase();
  return (
    lower.includes('heartbeat') ||
    lower.includes('memory extract') ||
    lower.includes('health sweep') ||
    lower.includes('github dispatch')
  );
}

export function workflowTemplateForCronJob(job: OpenClawCronJobSnapshot): Record<string, unknown> {
  const name = getTrimmedString(job.name) || getTrimmedString(job.id) || 'Workflow';
  const pipeline = shouldTreatAsSystemWorkflow(name) ? 'none' : 'auto_close';
  return {
    title_pattern: `${name} — {{datetime}}`,
    body: name,
    priority: 'P3',
    labels: ['automated'],
    auto_close: pipeline === 'auto_close',
    pipeline,
  };
}

export function cronJobToWorkflowSchedule(job: OpenClawCronJobSnapshot): Record<string, unknown> {
  const schedule = getTrimmedString(job.schedule);
  if (!schedule) {
    return { kind: 'manual', cron_id: getTrimmedString(job.id) };
  }
  if (schedule.startsWith('at:')) {
    return { kind: 'at', at: schedule.slice(3).trim(), cron_id: getTrimmedString(job.id) };
  }
  const everyMs = parseDurationStringToMs(schedule);
  if (everyMs !== undefined) {
    return { kind: 'every', everyMs, cron_id: getTrimmedString(job.id) };
  }
  const { expr, tz } = splitCronExprAndTZ(schedule);
  return {
    kind: 'cron',
    expr,
    ...(tz ? { tz } : {}),
    cron_id: getTrimmedString(job.id),
  };
}

function normalizeWorkflowProjectName(value: string): string {
  return value.trim().replace(/\s+/g, ' ').toLowerCase();
}

export function projectMatchesCronJob(project: BridgeWorkflowProjectSnapshot, job: OpenClawCronJobSnapshot): boolean {
  const cronID = getTrimmedString(job.id);
  const projectSchedule = asRecord(project.workflow_schedule);
  const scheduleCronID = getTrimmedString(projectSchedule?.cron_id);
  if (cronID && scheduleCronID && cronID === scheduleCronID) {
    return true;
  }
  const isWorkflowProject = project.workflow_enabled === true
    || (project.workflow_schedule !== null && project.workflow_schedule !== undefined);
  if (!isWorkflowProject) {
    return false;
  }
  const projectName = normalizeWorkflowProjectName(project.name);
  const jobName = normalizeWorkflowProjectName(getTrimmedString(job.name) || getTrimmedString(job.id));
  return projectName !== '' && projectName === jobName;
}

/**
 * Extract payload.kind from a nested payload object.
 */
function flattenPayloadType(payload: unknown): string | undefined {
  const obj = asRecord(payload);
  if (!obj) return undefined;
  return getTrimmedString(obj.kind) || undefined;
}

function extractCronJobs(raw: unknown): OpenClawCronJobSnapshot[] {
  const root = asRecord(raw);
  let jobs: unknown = raw;
  if (root) {
    if (Array.isArray(root.jobs)) jobs = root.jobs;
    else if (Array.isArray(root.items)) jobs = root.items;
    else if (Array.isArray(root.cronJobs)) jobs = root.cronJobs;
  }
  if (!Array.isArray(jobs)) {
    return [];
  }
  const out: OpenClawCronJobSnapshot[] = [];
  for (const item of jobs) {
    const row = asRecord(item);
    if (!row) {
      continue;
    }
    const id = getTrimmedString(row.id) || getTrimmedString(row.job_id) || getTrimmedString(row.jobId);
    if (!id) {
      continue;
    }
    const enabled = getBoolean(row.enabled);
    const normalized: OpenClawCronJobSnapshot = {
      id,
      name: getTrimmedString(row.name) || undefined,
      schedule:
        flattenSchedule(row.schedule) ||
        getTrimmedString(row.schedule) ||
        getTrimmedString(row.cron) ||
        getTrimmedString(row.every) ||
        undefined,
      session_target:
        getTrimmedString(row.session_target) ||
        getTrimmedString(row.sessionTarget) ||
        getTrimmedString(row.target) ||
        undefined,
      payload_type:
        flattenPayloadType(row.payload) ||
        getTrimmedString(row.payload_type) ||
        getTrimmedString(row.payloadType) ||
        getTrimmedString(row.type) ||
        undefined,
      last_run_at:
        normalizeTimeString(asRecord(row.state)?.lastRunAtMs) ||
        normalizeTimeString(row.last_run_at) ||
        normalizeTimeString(row.lastRunAt) ||
        normalizeTimeString(row.last_run) ||
        undefined,
      last_status:
        getTrimmedString(asRecord(row.state)?.lastStatus) ||
        getTrimmedString(row.last_status) ||
        getTrimmedString(row.lastStatus) ||
        getTrimmedString(row.status) ||
        undefined,
      next_run_at:
        normalizeTimeString(asRecord(row.state)?.nextRunAtMs) ||
        normalizeTimeString(row.next_run_at) ||
        normalizeTimeString(row.nextRunAt) ||
        normalizeTimeString(row.next_run) ||
        undefined,
      enabled: enabled !== undefined ? enabled : true,
    };
    out.push(normalized);
  }
  return out;
}

function extractProcesses(raw: unknown): OpenClawProcessSnapshot[] {
  const root = asRecord(raw);
  let processes: unknown = raw;
  if (root) {
    if (Array.isArray(root.processes)) processes = root.processes;
    else if (Array.isArray(root.items)) processes = root.items;
    else if (Array.isArray(root.sessions)) processes = root.sessions;
  }
  if (!Array.isArray(processes)) {
    return [];
  }
  const out: OpenClawProcessSnapshot[] = [];
  for (const item of processes) {
    const row = asRecord(item);
    if (!row) {
      continue;
    }
    const id =
      getTrimmedString(row.id) ||
      getTrimmedString(row.process_id) ||
      getTrimmedString(row.processId) ||
      getTrimmedString(row.session_id) ||
      getTrimmedString(row.sessionId) ||
      getTrimmedString(row.key);
    if (!id) {
      continue;
    }
    const durationSeconds =
      getNumeric(row.duration_seconds) ??
      getNumeric(row.durationSeconds) ??
      getNumeric(row.elapsed_seconds) ??
      getNumeric(row.elapsedSeconds);
    const normalized: OpenClawProcessSnapshot = {
      id,
      command:
        getTrimmedString(row.command) ||
        getTrimmedString(row.cmd) ||
        getTrimmedString(row.displayName) ||
        undefined,
      pid: getNumeric(row.pid) || getNumeric(row.os_pid) || getNumeric(row.osPid),
      status: getTrimmedString(row.status) || 'running',
      duration_seconds: durationSeconds !== undefined ? durationSeconds : undefined,
      agent_id: getTrimmedString(row.agent_id) || getTrimmedString(row.agentId) || undefined,
      session_key: getTrimmedString(row.session_key) || getTrimmedString(row.sessionKey) || getTrimmedString(row.key) || undefined,
      started_at:
        normalizeTimeString(row.started_at) ||
        normalizeTimeString(row.startedAt) ||
        normalizeTimeString(row.created_at) ||
        normalizeTimeString(row.createdAt) ||
        undefined,
    };
    out.push(normalized);
  }
  return out;
}

async function fetchCronJobsSnapshot(): Promise<OpenClawCronJobSnapshot[]> {
  const attempts: Array<() => Promise<unknown>> = [
    async () => sendRequest('cron.list', { limit: 200 }),
    async () => parseJSONValue(await runOpenClawCommandCapture(['cron', 'list', '--json'])),
  ];
  for (const attempt of attempts) {
    try {
      const parsed = extractCronJobs(await attempt());
      if (parsed.length > 0) {
        return parsed;
      }
      return [];
    } catch {
      // Continue through fallback attempts.
    }
  }
  return [];
}

async function fetchProcessesSnapshot(): Promise<OpenClawProcessSnapshot[]> {
  const attempts: Array<() => Promise<unknown>> = [
    async () => sendRequest('exec.sessions_list', { limit: 200 }),
    async () => parseJSONValue(await runOpenClawCommandCapture(['exec', 'list', '--json'])),
  ];
  for (const attempt of attempts) {
    try {
      const parsed = extractProcesses(await attempt());
      if (parsed.length > 0) {
        return parsed;
      }
      return [];
    } catch {
      // Continue through fallback attempts.
    }
  }
  return [];
}

function buildOtterCampAuthHeaders(contentTypeJSON = false): Record<string, string> {
  const headers: Record<string, string> = {
    ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    ...(OTTERCAMP_ORG_ID ? { 'X-Org-ID': OTTERCAMP_ORG_ID } : {}),
  };
  if (contentTypeJSON) {
    headers['Content-Type'] = 'application/json';
  }
  return headers;
}

async function fetchWorkflowProjectsSnapshot(): Promise<BridgeWorkflowProjectSnapshot[]> {
  const url = new URL('/api/projects', OTTERCAMP_URL);
  url.searchParams.set('workflow', 'true');
  const response = await fetchWithRetry(
    url.toString(),
    {
      method: 'GET',
      headers: buildOtterCampAuthHeaders(),
    },
    'list workflow projects',
  );
  if (!response.ok) {
    const detail = (await response.text().catch(() => '')).slice(0, 240);
    throw new Error(`workflow project list failed: ${response.status} ${response.statusText} ${detail}`.trim());
  }
  const payload = (await response.json().catch(() => ({}))) as { projects?: BridgeWorkflowProjectSnapshot[] };
  if (!Array.isArray(payload.projects)) {
    return [];
  }
  return payload.projects;
}

async function createWorkflowProjectFromCron(job: OpenClawCronJobSnapshot): Promise<BridgeWorkflowProjectSnapshot> {
  const name = getTrimmedString(job.name) || getTrimmedString(job.id) || 'Workflow';
  const schedule = cronJobToWorkflowSchedule(job);
  const template = workflowTemplateForCronJob(job);
  const response = await fetchWithRetry(
    `${OTTERCAMP_URL}/api/projects`,
    {
      method: 'POST',
      headers: buildOtterCampAuthHeaders(true),
      body: JSON.stringify({
        name,
        description: `Imported from OpenClaw cron job ${getTrimmedString(job.id) || name}`,
        workflow_enabled: job.enabled,
        workflow_schedule: schedule,
        workflow_template: template,
      }),
    },
    `create workflow project for cron ${job.id}`,
  );
  if (!response.ok) {
    const detail = (await response.text().catch(() => '')).slice(0, 240);
    throw new Error(`workflow project create failed: ${response.status} ${response.statusText} ${detail}`.trim());
  }
  return (await response.json()) as BridgeWorkflowProjectSnapshot;
}

async function patchWorkflowProjectFromCron(
  projectID: string,
  job: OpenClawCronJobSnapshot,
  patchPayload: Record<string, unknown>,
): Promise<void> {
  const response = await fetchWithRetry(
    `${OTTERCAMP_URL}/api/projects/${encodeURIComponent(projectID)}`,
    {
      method: 'PATCH',
      headers: buildOtterCampAuthHeaders(true),
      body: JSON.stringify(patchPayload),
    },
    `patch workflow project ${projectID} from cron ${job.id}`,
  );
  if (!response.ok) {
    const detail = (await response.text().catch(() => '')).slice(0, 240);
    throw new Error(`workflow project patch failed: ${response.status} ${response.statusText} ${detail}`.trim());
  }
}

async function triggerWorkflowRun(projectID: string, job: OpenClawCronJobSnapshot): Promise<void> {
  const response = await fetchWithRetry(
    `${OTTERCAMP_URL}/api/projects/${encodeURIComponent(projectID)}/runs/trigger`,
    {
      method: 'POST',
      headers: buildOtterCampAuthHeaders(),
    },
    `trigger workflow run for cron ${job.id}`,
  );
  if (!response.ok) {
    const detail = (await response.text().catch(() => '')).slice(0, 240);
    throw new Error(`workflow trigger failed: ${response.status} ${response.statusText} ${detail}`.trim());
  }
}

async function syncWorkflowProjectsFromCronJobs(cronJobs: OpenClawCronJobSnapshot[]): Promise<void> {
  if (cronJobs.length === 0) {
    return;
  }

  if (workflowSyncInProgress) {
    console.log('[bridge] workflow sync already in progress; skipping overlapping run');
    return;
  }

  workflowSyncInProgress = true;
  try {
    let workflowProjects = await fetchWorkflowProjectsSnapshot();
    const wasInitialized = cronRunDetectionInitialized;

    for (const job of cronJobs) {
      const jobID = getTrimmedString(job.id);
      if (!jobID) {
        continue;
      }

      let project = workflowProjects.find((candidate) => projectMatchesCronJob(candidate, job));
      if (!project) {
        try {
          project = await createWorkflowProjectFromCron(job);
          workflowProjects.push(project);
          console.log(`[bridge] created workflow project ${project.id} for cron job ${jobID}`);
        } catch (err) {
          console.error(`[bridge] failed to create workflow project for cron ${jobID}:`, err);
        }
      }

      if (project) {
        const patchPayload = {
          workflow_enabled: job.enabled,
          workflow_schedule: cronJobToWorkflowSchedule(job),
        };
        const patchFingerprint = JSON.stringify(patchPayload);
        const previousPatchFingerprint = lastPatchedWorkflowConfigByCronID.get(jobID) || '';

        try {
          if (patchFingerprint !== previousPatchFingerprint) {
            await patchWorkflowProjectFromCron(project.id, job, patchPayload);
            lastPatchedWorkflowConfigByCronID.set(jobID, patchFingerprint);
          }
        } catch (err) {
          console.error(`[bridge] failed to patch workflow project ${project.id} from cron ${jobID}:`, err);
        }
      }

      const currentLastRun = getTrimmedString(job.last_run_at);
      const previousLastRun = previousCronLastRunByID.get(jobID) || '';
      let shouldUpdateLastRun = true;

      if (wasInitialized && project && currentLastRun && currentLastRun !== previousLastRun) {
        const dedupeRunID = `cron:${jobID}:${currentLastRun}`;
        if (!deliveredRunIDs.has(dedupeRunID)) {
          try {
            await triggerWorkflowRun(project.id, job);
            markRunIDDelivered(dedupeRunID);
            console.log(`[bridge] triggered workflow run for cron ${jobID} via project ${project.id}`);
          } catch (err) {
            shouldUpdateLastRun = false;
            console.error(`[bridge] failed to trigger workflow run for cron ${jobID}:`, err);
          }
        }
      }

      if (shouldUpdateLastRun) {
        previousCronLastRunByID.set(jobID, currentLastRun);
      }
    }

    if (!cronRunDetectionInitialized) {
      cronRunDetectionInitialized = true;
    }
  } finally {
    workflowSyncInProgress = false;
  }
}

export async function syncWorkflowProjectsFromCronJobsForTest(
  cronJobs: OpenClawCronJobSnapshot[],
): Promise<void> {
  await syncWorkflowProjectsFromCronJobs(cronJobs);
}

export function resetWorkflowSyncStateForTest(): void {
  previousCronLastRunByID.clear();
  lastPatchedWorkflowConfigByCronID.clear();
  cronRunDetectionInitialized = false;
  workflowSyncInProgress = false;
  deliveredRunIDs.clear();
  deliveredRunIDOrder.length = 0;
}

export async function handleAdminCommandDispatchEvent(event: AdminCommandDispatchEvent): Promise<void> {
  const action = getTrimmedString(event.data?.action);
  const commandID = getTrimmedString(event.data?.command_id) || 'n/a';
  const agentID = getTrimmedString(event.data?.agent_id);
  const jobID = getTrimmedString(event.data?.job_id);
  const processID = getTrimmedString(event.data?.process_id);
  const configPatch = event.data?.config_patch;
  const configFull = event.data?.config_full;
  const configHash = getTrimmedString(event.data?.config_hash);
  const confirm = event.data?.confirm === true;
  const dryRun = event.data?.dry_run === true;
  const sessionKey = getTrimmedString(event.data?.session_key) || (agentID ? `agent:${agentID}:main` : '');

  if (!action) {
    throw new Error('admin.command missing action');
  }

  if (action === 'gateway.restart') {
    await runOpenClawCommand(['gateway', 'restart']);
    console.log(`[bridge] executed admin.command gateway.restart (${commandID})`);
    return;
  }

  if (action === 'agent.ping') {
    if (!sessionKey) {
      throw new Error('agent.ping missing session_key/agent_id');
    }
    await sendMessageToSession(sessionKey, '[OtterCamp admin ping] Please confirm you are responsive.', commandID);
    console.log(`[bridge] executed admin.command agent.ping for ${sessionKey} (${commandID})`);
    return;
  }

  if (action === 'agent.reset') {
    if (!sessionKey) {
      throw new Error('agent.reset missing session_key/agent_id');
    }
    try {
      await sendRequest('sessions.reset', { sessionKey });
      console.log(`[bridge] reset session via gateway RPC (${sessionKey})`);
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      console.warn(
        `[bridge] sessions.reset RPC unavailable for ${sessionKey}; falling back to local store reset + gateway restart: ${detail}`,
      );
      const localReset = resetSessionFromLocalStore(sessionKey);
      if (localReset.cleared) {
        console.log(
          `[bridge] cleared local session store for ${sessionKey} (${localReset.storePath})${
            localReset.transcriptDeleted ? ' and deleted transcript' : ''
          }`,
        );
      } else {
        console.warn(
          `[bridge] local session reset skipped for ${sessionKey}: ${
            localReset.reason || 'unknown'
          } (store=${localReset.storePath})`,
        );
      }
      await runOpenClawCommand(['gateway', 'restart']);
    }
    contextPrimedSessions.delete(sessionKey);
    const existingContext = sessionContexts.get(sessionKey);
    if (existingContext) {
      setSessionContext(sessionKey, {
        ...existingContext,
        identityMetadata: undefined,
        displayLabel: undefined,
      });
    }
    console.log(`[bridge] executed admin.command agent.reset for ${sessionKey} (${commandID})`);
    return;
  }

  if (action === 'cron.run') {
    if (!jobID) {
      throw new Error('cron.run missing job_id');
    }
    try {
      await runOpenClawCommand(['cron', 'run', '--id', jobID]);
    } catch {
      await runOpenClawCommand(['cron', 'trigger', '--id', jobID]);
    }
    console.log(`[bridge] executed admin.command cron.run for ${jobID} (${commandID})`);
    return;
  }

  if (action === 'cron.enable' || action === 'cron.disable') {
    if (!jobID) {
      throw new Error(`${action} missing job_id`);
    }
    const enable = action === 'cron.enable';
    try {
      await runOpenClawCommand(['cron', enable ? 'enable' : 'disable', '--id', jobID]);
    } catch {
      await runOpenClawCommand(['cron', 'update', '--id', jobID, '--enabled', enable ? 'true' : 'false']);
    }
    console.log(`[bridge] executed admin.command ${action} for ${jobID} (${commandID})`);
    return;
  }

  if (action === 'process.kill') {
    if (!processID) {
      throw new Error('process.kill missing process_id');
    }
    try {
      await runOpenClawCommand(['process', 'kill', '--id', processID]);
    } catch {
      await runOpenClawCommand(['exec', 'kill', '--id', processID]);
    }
    console.log(`[bridge] executed admin.command process.kill for ${processID} (${commandID})`);
    return;
  }

  if (action === 'config.patch') {
    if (!confirm) {
      throw new Error('config.patch requires confirm=true');
    }
    const patchObject = asRecord(configPatch);
    if (!patchObject) {
      throw new Error('config.patch missing config_patch object');
    }
    if (dryRun) {
      console.log(`[bridge] validated admin.command config.patch dry-run (${commandID})`);
      return;
    }

    const configPath = resolveOpenClawConfigPath();
    const currentConfig = readOpenClawConfigFile(configPath);

    const mergedConfig = applyJSONMergePatch(currentConfig, patchObject);
    writeOpenClawConfigFile(configPath, mergedConfig);
    await runOpenClawCommand(['gateway', 'restart']);
    console.log(`[bridge] executed admin.command config.patch (${commandID})`);
    return;
  }

  if (action === 'config.cutover' || action === 'config.rollback') {
    if (!confirm) {
      throw new Error(`${action} requires confirm=true`);
    }
    const fullConfigObject = asRecord(configFull);
    if (!fullConfigObject) {
      throw new Error(`${action} missing config_full object`);
    }
    if (dryRun) {
      console.log(`[bridge] validated admin.command ${action} dry-run (${commandID})`);
      return;
    }

    const configPath = resolveOpenClawConfigPath();
    const currentConfig = readOpenClawConfigFile(configPath);
    if (action === 'config.rollback') {
      if (!configHash) {
        throw new Error('config.rollback requires config_hash for integrity validation');
      }
      const currentHash = hashCanonicalJSON(currentConfig);
      if (currentHash !== configHash) {
        throw new Error(`config.rollback hash mismatch: expected ${configHash}, got ${currentHash}`);
      }
    }

    writeOpenClawConfigFile(configPath, fullConfigObject);
    await runOpenClawCommand(['gateway', 'restart']);
    console.log(`[bridge] executed admin.command ${action} (${commandID})`);
    return;
  }

  throw new Error(`unsupported admin command action: ${action}`);
}

function connectOtterCampDispatchSocket(): void {
  if (!OTTERCAMP_WS_SECRET) {
    console.warn('[bridge] OPENCLAW_WS_SECRET (or OTTERCAMP_WS_SECRET) not set; dm.message dispatch disabled');
    return;
  }

  if (otterCampWS && (otterCampWS.readyState === WebSocket.OPEN || otterCampWS.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const wsURL = buildOtterCampWSURL();
  console.log(`[bridge] connecting to OtterCamp websocket ${sanitizeWebSocketURLForLog(wsURL)}`);
  markSocketConnectAttempt('ottercamp');

  otterCampWS = new WebSocket(wsURL);

  otterCampWS.on('open', () => {
    markSocketOpen('ottercamp');
    startHeartbeatLoop('ottercamp', otterCampWS!, () => {
      if (otterCampWS && otterCampWS.readyState === WebSocket.OPEN) {
        otterCampWS.close();
      }
    });
    console.log('[bridge] connected to OtterCamp /ws/openclaw');
  });

  otterCampWS.on('message', (data) => {
    markSocketMessage('ottercamp');
    try {
      const event = JSON.parse(data.toString()) as Record<string, unknown>;
      const eventType = getTrimmedString(event.type);
      if (!eventType) {
        return;
      }
      void dispatchInboundEvent(eventType, event, 'socket').catch((err) => {
        console.error(`[bridge] failed dispatching socket event ${eventType}:`, err);
      });
    } catch (err) {
      console.error('[bridge] failed to parse OtterCamp websocket message:', err);
    }
  });

  otterCampWS.on('close', (code, reason) => {
    handleOtterCampSocketClosed(code, reason.toString(), () => {
      connectOtterCampDispatchSocket();
    });
  });

  otterCampWS.on('error', (err) => {
    console.error('[bridge] OtterCamp websocket error:', err);
    if (otterCampWS && otterCampWS.readyState === WebSocket.OPEN) {
      otterCampWS.close();
    }
  });

  otterCampWS.on('pong', () => {
    markHeartbeatPong('ottercamp');
  });
}

async function runOnce(): Promise<void> {
  continuousModeEnabled = false;
  try {
    await connectToOpenClaw();
    const sessions = await fetchSessions();
    const activityEvents = collectSessionDeltaActivityEvents(sessions);
    queueSessionDeltaActivityEvents(activityEvents);
    await pushToOtterCamp(sessions);
    const flushedCount = await flushBufferedActivityEvents('one-shot');
    if (activityEvents.length > 0 || flushedCount > 0) {
      console.log(
        `[bridge] generated ${activityEvents.length} activity event(s) from session deltas; pushed ${flushedCount}`,
      );
    }
    console.log(`[bridge] sync complete (${sessions.length} sessions)`);
  } catch (err) {
    console.error('[bridge] one-shot sync failed:', err);
    process.exit(1);
  } finally {
    if (openClawWS) {
      openClawWS.close();
    }
  }
}

async function runContinuous(): Promise<void> {
  continuousModeEnabled = true;
  startHealthEndpoint();
  await connectToOpenClaw();
  connectOtterCampDispatchSocket();

  const firstSessions = await fetchSessions();
  const firstActivityEvents = collectSessionDeltaActivityEvents(firstSessions);
  queueSessionDeltaActivityEvents(firstActivityEvents);
  await pushToOtterCamp(firstSessions);
  const firstFlushedCount = await flushBufferedActivityEvents('initial-sync');
  if (firstActivityEvents.length > 0 || firstFlushedCount > 0) {
    console.log(
      `[bridge] generated ${firstActivityEvents.length} initial activity event(s); pushed ${firstFlushedCount}`,
    );
  }
  console.log(`[bridge] initial sync complete (${firstSessions.length} sessions)`);
  await processDispatchQueue().catch((err) => {
    console.error('[bridge] initial dispatch queue drain failed:', err);
  });
  await flushDispatchReplayQueue('initial-dispatch-replay').catch((err) => {
    console.error('[bridge] failed to flush replay queue after initial dispatch drain:', err);
  });
  await flushBufferedActivityEvents('initial-dispatch');

  setInterval(async () => {
    const executed = await runSerializedPeriodicSync(async () => {
      try {
        const sessions = await fetchSessions();
        const activityEvents = collectSessionDeltaActivityEvents(sessions);
        queueSessionDeltaActivityEvents(activityEvents);
        await pushToOtterCamp(sessions);
        const flushedCount = await flushBufferedActivityEvents('periodic-sync');
        if (activityEvents.length > 0 || flushedCount > 0) {
          console.log(
            `[bridge] generated ${activityEvents.length} activity event(s) from session deltas; pushed ${flushedCount}`,
          );
        }
        console.log(`[bridge] periodic sync complete (${sessions.length} sessions)`);
      } catch (err) {
        console.error('[bridge] periodic sync failed:', err);
      }
    });
    if (!executed) {
      console.warn('[bridge] periodic sync skipped because previous run is still active');
    }
  }, SYNC_INTERVAL_MS);

  setInterval(async () => {
    try {
      await processDispatchQueue();
      await flushDispatchReplayQueue('dispatch-loop');
      await flushBufferedActivityEvents('dispatch-loop');
    } catch (err) {
      console.error('[bridge] periodic dispatch queue drain failed:', err);
    }
  }, DISPATCH_QUEUE_POLL_INTERVAL_MS);

  setInterval(async () => {
    try {
      await flushBufferedActivityEvents('activity-flush-loop');
    } catch (err) {
      console.error('[bridge] activity flush loop failed:', err);
    }
  }, ACTIVITY_EVENT_FLUSH_INTERVAL_MS);

  console.log(
    `[bridge] running continuously (Ctrl+C to stop, sync interval ${SYNC_INTERVAL_MS}ms, activity flush ${ACTIVITY_EVENT_FLUSH_INTERVAL_MS}ms)`,
  );
}

function isMainModule(): boolean {
  const argvPath = getTrimmedString(process.argv[1]);
  if (!argvPath) {
    return false;
  }
  try {
    return import.meta.url === pathToFileURL(argvPath).href;
  } catch {
    return false;
  }
}

async function runByMode(modeArg: string | undefined): Promise<void> {
  const mode = normalizeModeArg(modeArg);
  if (mode === 'continuous') {
    await runContinuous();
    return;
  }
  await runOnce();
}

if (isMainModule()) {
  runByMode(process.argv[2]).catch((err) => {
    console.error('[bridge] fatal error:', err);
    process.exit(1);
  });
}
