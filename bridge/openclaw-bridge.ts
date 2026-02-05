#!/usr/bin/env npx tsx
/**
 * OpenClaw → Otter Camp Bridge
 * 
 * This script connects to the OpenClaw Gateway WebSocket and pushes
 * session/agent data to api.otter.camp in real-time.
 * 
 * Usage:
 *   OPENCLAW_TOKEN=xxx OTTERCAMP_URL=https://api.otter.camp npx tsx bridge/openclaw-bridge.ts
 */

import WebSocket from 'ws';

const OPENCLAW_HOST = process.env.OPENCLAW_HOST || '127.0.0.1';
const OPENCLAW_PORT = process.env.OPENCLAW_PORT || '18791';
const OPENCLAW_TOKEN = process.env.OPENCLAW_TOKEN || '';
const OTTERCAMP_URL = process.env.OTTERCAMP_URL || 'https://api.otter.camp';
const OTTERCAMP_TOKEN = process.env.OTTERCAMP_TOKEN || '';

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

let ws: WebSocket | null = null;
let requestId = 0;
const genId = () => `req-${++requestId}`;
let pendingRequests = new Map<string, { resolve: (value: unknown) => void; reject: (reason?: unknown) => void }>();
let challengeNonce: string | null = null;

async function connectToOpenClaw(): Promise<void> {
  return new Promise((resolve, reject) => {
    const url = `ws://${OPENCLAW_HOST}:${OPENCLAW_PORT}`;
    console.log(`Connecting to OpenClaw at ${url}...`);
    
    ws = new WebSocket(url);
    
    ws.on('open', () => {
      console.log('WebSocket connected, waiting for challenge...');
    });
    
    ws.on('message', async (data) => {
      try {
        const msg = JSON.parse(data.toString());
        
        // Handle challenge event - gateway sends this first
        if (msg.type === 'event' && msg.event === 'connect.challenge') {
          console.log('Received connect challenge');
          challengeNonce = msg.payload?.nonce;
          
          // Send connect request with auth
          const connectId = genId();
          const connectMsg = {
            type: 'req',
            id: connectId,
            method: 'connect',
            params: {
              minProtocol: 3,
              maxProtocol: 3,
              client: {
                id: 'cli',
                version: '1.0.0',
                platform: 'macos',
                mode: 'operator',
              },
              role: 'operator',
              scopes: ['operator.read'],
              caps: [],
              commands: [],
              permissions: {},
              auth: OPENCLAW_TOKEN ? { token: OPENCLAW_TOKEN } : undefined,
              locale: 'en-US',
              userAgent: 'openclaw-cli/1.0.0',
            },
          };
          
          // Register a handler for the connect response
          pendingRequests.set(connectId, {
            resolve: () => {
              console.log('Connected to OpenClaw Gateway');
              resolve();
            },
            reject: (err) => {
              reject(err);
            }
          });
          
          console.log('Sending connect request with token:', OPENCLAW_TOKEN ? 'present' : 'missing');
          ws!.send(JSON.stringify(connectMsg));
          return;
        }
        
        // Handle response
        if (msg.type === 'res') {
          const pending = pendingRequests.get(msg.id);
          if (pending) {
            pendingRequests.delete(msg.id);
            if (msg.ok) {
              pending.resolve(msg.payload);
            } else {
              pending.reject(new Error(msg.error?.message || 'Request failed'));
            }
          }
          return;
        }
        
        // Handle other events
        if (msg.type === 'event') {
          console.log(`Event: ${msg.event}`);
        }
      } catch (err) {
        console.error('Failed to parse message:', err);
      }
    });
    
    ws.on('error', (err) => {
      console.error('WebSocket error:', err);
      reject(err);
    });
    
    ws.on('close', (code, reason) => {
      console.log(`Disconnected from OpenClaw - code: ${code}, reason: ${reason.toString()}`);
      ws = null;
    });
    
    // Timeout for connection
    setTimeout(() => {
      reject(new Error('Connection timeout'));
    }, 30000);
  });
}

async function sendRequest(method: string, params: Record<string, unknown> = {}): Promise<unknown> {
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    throw new Error('Not connected to OpenClaw');
  }
  
  const id = genId();
  
  return new Promise((resolve, reject) => {
    pendingRequests.set(id, { resolve, reject });
    
    const msg = {
      type: 'req',
      id,
      method,
      params,
    };
    
    ws!.send(JSON.stringify(msg));
    
    // Timeout after 30 seconds
    setTimeout(() => {
      if (pendingRequests.has(id)) {
        pendingRequests.delete(id);
        reject(new Error('Request timeout'));
      }
    }, 30000);
  });
}

async function fetchSessions(): Promise<OpenClawSession[]> {
  // Use the sessions.list method
  const response = await sendRequest('sessions.list', {
    limit: 50,
    includeMessages: false,
  }) as SessionsListResponse;
  
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
  console.log(`Pushing ${sessions.length} sessions to ${url}...`);
  
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(OTTERCAMP_TOKEN ? { 'Authorization': `Bearer ${OTTERCAMP_TOKEN}` } : {}),
    },
    body: JSON.stringify(payload),
  });
  
  if (!response.ok) {
    throw new Error(`Push failed: ${response.status} ${response.statusText}`);
  }
  
  const result = await response.json();
  console.log('Push result:', result);
}

async function runOnce(): Promise<void> {
  try {
    await connectToOpenClaw();
    const sessions = await fetchSessions();
    await pushToOtterCamp(sessions);
    console.log('Sync complete');
  } catch (err) {
    console.error('Sync failed:', err);
    process.exit(1);
  } finally {
    if (ws) {
      ws.close();
    }
  }
}

async function runContinuous(): Promise<void> {
  await connectToOpenClaw();
  
  // Initial sync
  const sessions = await fetchSessions();
  await pushToOtterCamp(sessions);
  
  // Sync every 30 seconds
  setInterval(async () => {
    try {
      const sessions = await fetchSessions();
      await pushToOtterCamp(sessions);
    } catch (err) {
      console.error('Periodic sync failed:', err);
    }
  }, 30000);
  
  console.log('Bridge running in continuous mode (Ctrl+C to stop)');
}

// Main
const mode = process.argv[2] || 'once';

if (mode === 'continuous') {
  runContinuous().catch(console.error);
} else {
  runOnce().catch(console.error);
}
