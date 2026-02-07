#!/usr/bin/env npx tsx
/**
 * OpenClaw <-> Otter Camp Bridge
 *
 * Responsibilities:
 * 1) Pull sessions from OpenClaw and push sync snapshots to Otter Camp.
 * 2) Keep /ws/openclaw connected so Otter Camp can dispatch dm.message events.
 * 3) Forward dm.message events to OpenClaw via sessions.send.
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
  data?: {
    thread_id?: string;
    session_key?: string;
    content?: string;
    message_id?: string;
    agent_id?: string;
    sender_name?: string;
  };
};

let openClawWS: WebSocket | null = null;
let otterCampWS: WebSocket | null = null;
let requestId = 0;
const pendingRequests = new Map<string, PendingRequest>();

const genId = () => `req-${++requestId}`;

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

  await sendRequest('chat.send', {
    idempotencyKey,
    sessionKey,
    message: content,
  });
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
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { Authorization: `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
    body: JSON.stringify(payload),
  });

  if (!response.ok) {
    throw new Error(`sync push failed: ${response.status} ${response.statusText}`);
  }
}

async function handleDMDispatchEvent(event: DMDispatchEvent): Promise<void> {
  const sessionKey = (event.data?.session_key || '').trim();
  const content = (event.data?.content || '').trim();

  if (!sessionKey || !content) {
    console.warn('[bridge] skipped dm.message with missing session key or content');
    return;
  }

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
  }
}

function connectOtterCampDispatchSocket(): void {
  if (!OTTERCAMP_WS_SECRET) {
    console.warn('[bridge] OPENCLAW_WS_SECRET not set; dm.message dispatch disabled');
    return;
  }

  const wsURL = buildOtterCampWSURL();
  console.log(`[bridge] connecting to OtterCamp websocket ${wsURL}`);

  otterCampWS = new WebSocket(wsURL);

  otterCampWS.on('open', () => {
    console.log('[bridge] connected to OtterCamp /ws/openclaw');
  });

  otterCampWS.on('message', (data) => {
    try {
      const event = JSON.parse(data.toString()) as DMDispatchEvent;
      if (event.type !== 'dm.message') {
        return;
      }
      void handleDMDispatchEvent(event);
    } catch (err) {
      console.error('[bridge] failed to parse OtterCamp websocket message:', err);
    }
  });

  otterCampWS.on('close', (code, reason) => {
    console.warn(`[bridge] OtterCamp websocket closed (${code}) ${reason.toString()}`);
    otterCampWS = null;

    setTimeout(() => {
      if (!otterCampWS) {
        connectOtterCampDispatchSocket();
      }
    }, 2000);
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

  setInterval(async () => {
    try {
      const sessions = await fetchSessions();
      await pushToOtterCamp(sessions);
      console.log(`[bridge] periodic sync complete (${sessions.length} sessions)`);
    } catch (err) {
      console.error('[bridge] periodic sync failed:', err);
    }
  }, 30000);

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
