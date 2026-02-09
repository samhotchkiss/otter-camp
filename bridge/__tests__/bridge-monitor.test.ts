// @vitest-environment node
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

function quoteForShell(value: string): string {
  return `'${value.replace(/'/g, `'"'"'`)}'`;
}

function readState(stateFile: string): Record<string, string> {
  if (!fs.existsSync(stateFile)) {
    return {};
  }
  const lines = fs.readFileSync(stateFile, 'utf8').split(/\r?\n/);
  const state: Record<string, string> = {};
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) {
      continue;
    }
    const index = trimmed.indexOf('=');
    if (index <= 0) {
      continue;
    }
    const key = trimmed.slice(0, index).trim();
    const value = trimmed.slice(index + 1).trim();
    state[key] = value;
  }
  return state;
}

async function startHealthServer(handler: (req: http.IncomingMessage, res: http.ServerResponse) => void): Promise<{
  url: string;
  close: () => Promise<void>;
}> {
  const server = http.createServer(handler);
  await new Promise<void>((resolve) => {
    server.listen(0, '127.0.0.1', () => resolve());
  });
  const address = server.address();
  if (!address || typeof address === 'string') {
    throw new Error('failed to bind health test server');
  }
  return {
    url: `http://127.0.0.1:${address.port}/health`,
    close: () =>
      new Promise<void>((resolve, reject) => {
        server.close((err) => {
          if (err) {
            reject(err);
            return;
          }
          resolve();
        });
      }),
  };
}

describe('bridge monitor script', () => {
  let tempDir = '';
  let stateFile = '';
  let eventLog = '';

  beforeEach(() => {
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bridge-monitor-test-'));
    stateFile = path.join(tempDir, 'monitor.state');
    eventLog = path.join(tempDir, 'events.log');
    fs.writeFileSync(eventLog, '', 'utf8');
  });

  afterEach(() => {
    if (tempDir) {
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });

  async function runMonitor(healthURL: string): Promise<{ code: number; stderr: string }> {
    const restartCmd = `printf 'restart\\n' >> ${quoteForShell(eventLog)}`;
    const alertCmd = `printf \"alert:$BRIDGE_MONITOR_REASON\\\\n\" >> ${quoteForShell(eventLog)}`;
    return await new Promise((resolve) => {
      const child = spawn('bash', ['bridge/bridge-monitor.sh'], {
        cwd: path.resolve(__dirname, '..', '..'),
        env: {
          ...process.env,
          BRIDGE_HEALTH_URL: healthURL,
          BRIDGE_MONITOR_STATE_FILE: stateFile,
          BRIDGE_MONITOR_TIMEOUT_SECONDS: '2',
          BRIDGE_RESTART_CMD: restartCmd,
          BRIDGE_ALERT_CMD: alertCmd,
        },
      });
      let stderr = '';
      child.stderr.on('data', (chunk) => {
        stderr += chunk.toString();
      });
      child.on('close', (code) => {
        resolve({
          code: code ?? -1,
          stderr,
        });
      });
    });
  }

  it('resets failure tracking when health endpoint is healthy', async () => {
    fs.writeFileSync(stateFile, 'failures=2\nreason=unreachable\n', 'utf8');
    const server = await startHealthServer((_req, res) => {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{"status":"healthy"}');
    });

    try {
      const result = await runMonitor(server.url);
      expect(result.code).toBe(0);
      expect(fs.readFileSync(eventLog, 'utf8')).toBe('');
      const state = readState(stateFile);
      expect(state.failures).toBe('0');
      expect(state.reason).toBe('healthy');
    } finally {
      await server.close();
    }
  });

  it('restarts on first unhealthy response and escalates on second consecutive failure', async () => {
    const server = await startHealthServer((_req, res) => {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{"status":"unhealthy"}');
    });

    try {
      const first = await runMonitor(server.url);
      expect(first.code).toBe(1);
      let events = fs.readFileSync(eventLog, 'utf8').trim().split(/\r?\n/).filter(Boolean);
      expect(events).toEqual(['restart']);
      let state = readState(stateFile);
      expect(state.failures).toBe('1');
      expect(state.reason).toBe('unhealthy');

      const second = await runMonitor(server.url);
      expect(second.code).toBe(1);
      events = fs.readFileSync(eventLog, 'utf8').trim().split(/\r?\n/).filter(Boolean);
      expect(events).toEqual(['restart', 'restart', 'alert:unhealthy']);
      state = readState(stateFile);
      expect(state.failures).toBe('2');
      expect(state.reason).toBe('unhealthy');
    } finally {
      await server.close();
    }
  });

  it('treats unreachable bridge health endpoint as a distinct failure reason', async () => {
    const unreachableURL = 'http://127.0.0.1:1/health';
    const result = await runMonitor(unreachableURL);

    expect(result.code).toBe(1);
    const state = readState(stateFile);
    expect(state.failures).toBe('1');
    expect(state.reason).toBe('unreachable');

    const events = fs.readFileSync(eventLog, 'utf8').trim().split(/\r?\n/).filter(Boolean);
    expect(events).toEqual(['restart']);
  });
});
