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

const OPENCLAW_HOST = process.env.OPENCLAW_HOST || '127.0.0.1';
const OPENCLAW_PORT = process.env.OPENCLAW_PORT || '18791';
const OPENCLAW_TOKEN = process.env.OPENCLAW_TOKEN || '';
const OTTERCAMP_URL = process.env.OTTERCAMP_URL || 'https://api.otter.camp';
const OTTERCAMP_TOKEN = process.env.OTTERCAMP_TOKEN || '';
const OTTERCAMP_WS_SECRET = process.env.OPENCLAW_WS_SECRET || '';
const FETCH_RETRY_DELAYS_MS = [300, 900, 2000];
const MAX_TRACKED_RUN_IDS = 2000;
const DISPATCH_QUEUE_POLL_INTERVAL_MS = 5000;

interface OpenClawSession {
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

const genId = () => `req-${++requestId}`;

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
          const connectId = genId();
          const connectMsg = {
            type: 'req',
            id: connectId,
            method: 'connect',
            params: {
              minProtocol: 3,
              maxProtocol: 3,
              client: {
                id: 'gateway-client',
                version: '1.0.0',
                platform: 'macos',
                mode: 'backend',
              },
              role: 'operator',
              scopes: ['operator.read', 'operator.admin'],
              caps: [],
              commands: [],
              permissions: {},
              auth: OPENCLAW_TOKEN ? { token: OPENCLAW_TOKEN } : undefined,
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

async function pushToOtterCamp(sessions: OpenClawSession[]): Promise<void> {
  const payload = {
    type: 'full',
    timestamp: new Date().toISOString(),
    sessions,
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

  if (!sessionKey || !content) {
    console.warn('[bridge] skipped dm.message with missing session key or content');
    return;
  }

  sessionContexts.set(sessionKey, {
    kind: 'dm',
    orgID: getTrimmedString(event.org_id),
    threadID: getTrimmedString(event.data?.thread_id),
    agentID:
      getTrimmedString(event.data?.agent_id) || parseAgentIDFromSessionKey(sessionKey),
  });

  try {
    await sendMessageToSession(sessionKey, content, event.data?.message_id);
    console.log(
      `[bridge] delivered dm.message to ${sessionKey} (message_id=${event.data?.message_id || 'n/a'})`,
    );
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
  const orgID = getTrimmedString(event.org_id);
  const messageID = getTrimmedString(event.data?.message_id) || undefined;
  const sessionKey =
    getTrimmedString(event.data?.session_key) ||
    (agentID && projectID ? `agent:${agentID}:project:${projectID}` : '');

  if (!sessionKey || !projectID || !orgID || !content) {
    console.warn('[bridge] skipped project.chat.message with missing org/project/session/content');
    return;
  }

  sessionContexts.set(sessionKey, {
    kind: 'project_chat',
    orgID,
    agentID,
    agentName,
    projectID,
  });

  try {
    await sendMessageToSession(sessionKey, content, messageID);
    console.log(
      `[bridge] delivered project.chat.message to ${sessionKey} (message_id=${messageID || 'n/a'})`,
    );
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
  const orgID = getTrimmedString(event.org_id);
  const messageID = getTrimmedString(event.data?.message_id) || undefined;
  const parsedIssueNumber = Number(event.data?.issue_number);
  const issueNumber = Number.isFinite(parsedIssueNumber) ? parsedIssueNumber : undefined;
  const sessionKey =
    getTrimmedString(event.data?.session_key) ||
    (agentID && issueID ? `agent:${agentID}:issue:${issueID}` : '');

  if (!sessionKey || !issueID || !projectID || !orgID || !content) {
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
  });

  try {
    await sendMessageToSession(sessionKey, content, messageID);
    console.log(
      `[bridge] delivered issue.comment.message to ${sessionKey} (message_id=${messageID || 'n/a'})`,
    );
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
      const event = JSON.parse(data.toString()) as DMDispatchEvent | ProjectChatDispatchEvent | IssueCommentDispatchEvent;
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
    await pushToOtterCamp(sessions);
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
  await pushToOtterCamp(firstSessions);
  console.log(`[bridge] initial sync complete (${firstSessions.length} sessions)`);
  await processDispatchQueue().catch((err) => {
    console.error('[bridge] initial dispatch queue drain failed:', err);
  });

  setInterval(async () => {
    try {
      const sessions = await fetchSessions();
      await pushToOtterCamp(sessions);
      console.log(`[bridge] periodic sync complete (${sessions.length} sessions)`);
    } catch (err) {
      console.error('[bridge] periodic sync failed:', err);
    }
  }, 30000);

  setInterval(async () => {
    try {
      await processDispatchQueue();
    } catch (err) {
      console.error('[bridge] periodic dispatch queue drain failed:', err);
    }
  }, DISPATCH_QUEUE_POLL_INTERVAL_MS);

  console.log('[bridge] running continuously (Ctrl+C to stop)');
}

const mode = normalizeModeArg(process.argv[2]);

if (mode === 'continuous') {
  runContinuous().catch((err) => {
    console.error('[bridge] fatal error in continuous mode:', err);
    process.exit(1);
  });
} else {
  runOnce().catch((err) => {
    console.error('[bridge] fatal error in one-shot mode:', err);
    process.exit(1);
  });
}
