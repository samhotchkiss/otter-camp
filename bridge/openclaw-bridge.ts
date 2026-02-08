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
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

const OPENCLAW_HOST = process.env.OPENCLAW_HOST || '127.0.0.1';
const OPENCLAW_PORT = process.env.OPENCLAW_PORT || '18791';
const OPENCLAW_TOKEN = process.env.OPENCLAW_TOKEN || '';
const OTTERCAMP_URL = process.env.OTTERCAMP_URL || 'https://api.otter.camp';
const OTTERCAMP_TOKEN = process.env.OTTERCAMP_TOKEN || '';
const OTTERCAMP_WS_SECRET = process.env.OPENCLAW_WS_SECRET || '';
const OTTER_PROGRESS_LOG_PATH = (process.env.OTTER_PROGRESS_LOG_PATH || '').trim();
const FETCH_RETRY_DELAYS_MS = [300, 900, 2000];
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
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');
const PROJECT_ID_PATTERN = /(?:^|:)project:([0-9a-f-]{36})(?:$|:)/i;
const ISSUE_ID_PATTERN = /(?:^|:)issue:([0-9a-f-]{36})(?:$|:)/i;
const HEARTBEAT_PATTERN = /\bheartbeat\b/i;
const CHAT_CHANNELS = new Set(['slack', 'telegram', 'tui', 'discord']);
const OTTERCAMP_ORG_ID = (process.env.OTTERCAMP_ORG_ID || '').trim();

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
};

type DeviceIdentity = {
  deviceId: string;
  publicKeyPem: string;
  privateKeyPem: string;
};

let openClawWS: WebSocket | null = null;
let otterCampWS: WebSocket | null = null;
let otterCampReconnectTimer: ReturnType<typeof setTimeout> | null = null;
let otterCampReconnectAttempts = 0;
let isDispatchQueuePolling = false;
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
const queuedActivityEventsByOrg = new Map<string, BridgeAgentActivityEvent[]>();
const queuedActivityEventIDs = new Set<string>();
const deliveredActivityEventIDs = new Set<string>();
const deliveredActivityEventIDOrder: string[] = [];

const genId = () => `req-${++requestId}`;
const execFileAsync = promisify(execFile);

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

function parseAgentIDFromSessionKey(sessionKey: string): string {
  const match = /^agent:([^:]+):/i.exec(sessionKey.trim());
  if (!match || !match[1]) {
    return '';
  }
  return match[1].trim();
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

function resolveOpenClawIdentityPath(fileName: string): string {
  return path.join(resolveOpenClawStateDir(), 'identity', fileName);
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

function withSessionContext(sessionKey: string, rawContent: string): string {
  const content = rawContent.trim();
  if (!content) {
    return '';
  }
  const context = sessionContexts.get(sessionKey);
  if (!context) {
    return content;
  }
  if (!contextPrimedSessions.has(sessionKey)) {
    contextPrimedSessions.add(sessionKey);
    return `${buildContextEnvelope(context)}\n\n${content}`;
  }
  return `[OTTERCAMP_CONTEXT_REMINDER]\n- ${buildContextReminder(context)}\n[/OTTERCAMP_CONTEXT_REMINDER]\n\n${content}`;
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
  return OTTERCAMP_ORG_ID;
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

function buildOtterCampWSURL(): string {
  const url = new URL(OTTERCAMP_URL);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.pathname = '/ws/openclaw';
  if (OTTERCAMP_WS_SECRET) {
    url.searchParams.set('token', OTTERCAMP_WS_SECRET);
  }
  return url.toString();
}

async function connectToOpenClaw(): Promise<void> {
  return new Promise((resolve, reject) => {
    const url = `ws://${OPENCLAW_HOST}:${OPENCLAW_PORT}`;
    console.log(`[bridge] connecting to OpenClaw gateway at ${url}`);

    openClawWS = new WebSocket(url);

    openClawWS.on('open', () => {
      console.log('[bridge] OpenClaw socket opened, waiting for challenge');
    });

    openClawWS.on('message', (data) => {
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
              resolve();
            },
            reject: (err) => reject(err),
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
      reject(err);
    });

    openClawWS.on('close', (code, reason) => {
      console.warn(`[bridge] OpenClaw socket closed (${code}) ${reason.toString()}`);
      openClawWS = null;
    });

    setTimeout(() => {
      reject(new Error('OpenClaw connection timeout'));
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
  const contextualContent = withSessionContext(sessionKey, content);
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

async function handleOpenClawEvent(message: Record<string, unknown>): Promise<void> {
  const eventName = getTrimmedString(message.event).toLowerCase();
  const payload = asRecord(message.payload) || asRecord(message.data);
  if (!payload) {
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
        if (eventType === 'dm.message') {
          await handleDMDispatchEvent(payload as DMDispatchEvent);
        } else if (eventType === 'project.chat.message') {
          await handleProjectChatDispatchEvent(payload as ProjectChatDispatchEvent);
        } else if (eventType === 'issue.comment.message') {
          await handleIssueCommentDispatchEvent(payload as IssueCommentDispatchEvent);
        } else if (eventType === 'admin.command') {
          await handleAdminCommandDispatchEvent(payload as AdminCommandDispatchEvent);
        } else {
          throw new Error(`unsupported event type: ${eventType}`);
        }
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

  sessionContexts.set(sessionKey, {
    kind: 'dm',
    orgID,
    threadID,
    agentID,
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

  sessionContexts.set(sessionKey, {
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

  sessionContexts.set(sessionKey, {
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
        getTrimmedString(row.payload_type) ||
        getTrimmedString(row.payloadType) ||
        getTrimmedString(row.type) ||
        undefined,
      last_run_at:
        normalizeTimeString(row.last_run_at) ||
        normalizeTimeString(row.lastRunAt) ||
        normalizeTimeString(row.last_run) ||
        undefined,
      last_status:
        getTrimmedString(row.last_status) ||
        getTrimmedString(row.lastStatus) ||
        getTrimmedString(row.status) ||
        undefined,
      next_run_at:
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

async function handleAdminCommandDispatchEvent(event: AdminCommandDispatchEvent): Promise<void> {
  const action = getTrimmedString(event.data?.action);
  const commandID = getTrimmedString(event.data?.command_id) || 'n/a';
  const agentID = getTrimmedString(event.data?.agent_id);
  const jobID = getTrimmedString(event.data?.job_id);
  const processID = getTrimmedString(event.data?.process_id);
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
    // Prefer targeted reset if supported; fall back to gateway restart for known bridge recovery path.
    try {
      await runOpenClawCommand(['sessions', 'reset', '--session', sessionKey]);
    } catch (err) {
      console.warn(`[bridge] targeted reset failed for ${sessionKey}; falling back to gateway restart:`, err);
      await runOpenClawCommand(['gateway', 'restart']);
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

  throw new Error(`unsupported admin command action: ${action}`);
}

function connectOtterCampDispatchSocket(): void {
  if (!OTTERCAMP_WS_SECRET) {
    console.warn('[bridge] OPENCLAW_WS_SECRET not set; dm.message dispatch disabled');
    return;
  }

  if (otterCampWS && (otterCampWS.readyState === WebSocket.OPEN || otterCampWS.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const wsURL = buildOtterCampWSURL();
  console.log(`[bridge] connecting to OtterCamp websocket ${wsURL}`);

  otterCampWS = new WebSocket(wsURL);

  otterCampWS.on('open', () => {
    otterCampReconnectAttempts = 0;
    if (otterCampReconnectTimer) {
      clearTimeout(otterCampReconnectTimer);
      otterCampReconnectTimer = null;
    }
    console.log('[bridge] connected to OtterCamp /ws/openclaw');
  });

  otterCampWS.on('message', (data) => {
    try {
      const event = JSON.parse(data.toString()) as DMDispatchEvent | ProjectChatDispatchEvent | IssueCommentDispatchEvent | AdminCommandDispatchEvent;
      if (event.type === 'dm.message') {
        void handleDMDispatchEvent(event as DMDispatchEvent).catch(() => {});
        return;
      }
      if (event.type === 'project.chat.message') {
        void handleProjectChatDispatchEvent(event as ProjectChatDispatchEvent).catch(() => {});
        return;
      }
      if (event.type === 'issue.comment.message') {
        void handleIssueCommentDispatchEvent(event as IssueCommentDispatchEvent).catch(() => {});
        return;
      }
      if (event.type === 'admin.command') {
        void handleAdminCommandDispatchEvent(event as AdminCommandDispatchEvent).catch((err) => {
          console.error('[bridge] failed to execute admin.command:', err);
        });
      }
    } catch (err) {
      console.error('[bridge] failed to parse OtterCamp websocket message:', err);
    }
  });

  otterCampWS.on('close', (code, reason) => {
    console.warn(`[bridge] OtterCamp websocket closed (${code}) ${reason.toString()}`);
    otterCampWS = null;

    const reconnectDelay = Math.min(30000, 1000 * Math.pow(2, otterCampReconnectAttempts));
    otterCampReconnectAttempts += 1;
    if (otterCampReconnectTimer) {
      clearTimeout(otterCampReconnectTimer);
    }
    otterCampReconnectTimer = setTimeout(() => {
      otterCampReconnectTimer = null;
      connectOtterCampDispatchSocket();
    }, reconnectDelay);
  });

  otterCampWS.on('error', (err) => {
    console.error('[bridge] OtterCamp websocket error:', err);
  });
}

async function runOnce(): Promise<void> {
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
  await flushBufferedActivityEvents('initial-dispatch');

  setInterval(async () => {
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
  }, SYNC_INTERVAL_MS);

  setInterval(async () => {
    try {
      await processDispatchQueue();
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
