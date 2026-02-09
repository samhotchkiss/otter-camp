import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { execFileMock } = vi.hoisted(() => ({
  execFileMock: vi.fn(
    (
      _cmd: string,
      _args: string[],
      optionsOrCallback: unknown,
      maybeCallback?: (error: Error | null, stdout?: string, stderr?: string) => void,
    ) => {
      const callback =
        typeof optionsOrCallback === 'function'
          ? (optionsOrCallback as (error: Error | null, stdout?: string, stderr?: string) => void)
          : maybeCallback;
      if (callback) {
        callback(null, '', '');
      }
    },
  ),
}));

vi.mock('node:child_process', () => ({
  execFile: execFileMock,
  default: { execFile: execFileMock },
}));

import { handleAdminCommandDispatchEvent } from '../openclaw-bridge';

describe('admin.command config.patch', () => {
  let tempDir = '';
  let configPath = '';
  const originalConfigPath = process.env.OPENCLAW_CONFIG_PATH;

  beforeEach(() => {
    execFileMock.mockClear();
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'oc-bridge-config-'));
    configPath = path.join(tempDir, 'openclaw.json');
    process.env.OPENCLAW_CONFIG_PATH = configPath;
  });

  afterEach(() => {
    if (originalConfigPath === undefined) {
      delete process.env.OPENCLAW_CONFIG_PATH;
    } else {
      process.env.OPENCLAW_CONFIG_PATH = originalConfigPath;
    }
    if (tempDir) {
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });

  it('applies config patch and restarts gateway', async () => {
    fs.writeFileSync(
      configPath,
      JSON.stringify(
        {
          gateway: { port: 18791, host: '127.0.0.1' },
          agents: { main: { enabled: true, model: { primary: 'claude-opus-4-6' } } },
        },
        null,
        2,
      ),
    );

    await handleAdminCommandDispatchEvent({
      type: 'admin.command',
      data: {
        command_id: 'cmd-1',
        action: 'config.patch',
        confirm: true,
        config_patch: {
          gateway: { port: 18888 },
          agents: { main: { model: { primary: 'gpt-5.2-codex' } } },
        },
      },
    });

    const updated = JSON.parse(fs.readFileSync(configPath, 'utf8')) as Record<string, unknown>;
    expect(updated.gateway).toEqual({ port: 18888, host: '127.0.0.1' });
    expect(updated.agents).toEqual({
      main: { enabled: true, model: { primary: 'gpt-5.2-codex' } },
    });
    expect(execFileMock).toHaveBeenCalled();
    expect(execFileMock.mock.calls[0]?.[1]).toEqual(['gateway', 'restart']);
  });

  it('supports dry-run validation without file mutation', async () => {
    fs.writeFileSync(configPath, JSON.stringify({ gateway: { port: 18791 } }, null, 2));

    await handleAdminCommandDispatchEvent({
      type: 'admin.command',
      data: {
        command_id: 'cmd-2',
        action: 'config.patch',
        confirm: true,
        dry_run: true,
        config_patch: {
          gateway: { port: 20001 },
        },
      },
    });

    const current = JSON.parse(fs.readFileSync(configPath, 'utf8')) as Record<string, unknown>;
    expect(current.gateway).toEqual({ port: 18791 });
    expect(execFileMock).not.toHaveBeenCalled();
  });
});
