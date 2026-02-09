import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, it } from 'node:test';

import { handleAdminCommandDispatchEvent, setExecFileForTest } from '../openclaw-bridge';

type ExecCall = {
  cmd: string;
  args: string[];
};

let execCalls: ExecCall[] = [];

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((entry) => canonicalize(entry));
  }
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(record).sort()) {
      out[key] = canonicalize(record[key]);
    }
    return out;
  }
  return value;
}

function hashCanonical(value: unknown): string {
  return crypto.createHash('sha256').update(JSON.stringify(canonicalize(value))).digest('hex');
}

describe('admin.command config patch/cutover/rollback', () => {
  let tempDir = '';
  let configPath = '';
  const originalConfigPath = process.env.OPENCLAW_CONFIG_PATH;

  beforeEach(() => {
    execCalls = [];
    setExecFileForTest((cmd, args, _options, callback) => {
      execCalls.push({ cmd, args: [...args] });
      callback(null, '', '');
    });
    tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'oc-bridge-config-'));
    configPath = path.join(tempDir, 'openclaw.json');
    process.env.OPENCLAW_CONFIG_PATH = configPath;
  });

  afterEach(() => {
    setExecFileForTest(null);
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
    assert.deepEqual(updated.gateway, { port: 18888, host: '127.0.0.1' });
    assert.deepEqual(updated.agents, {
      main: { enabled: true, model: { primary: 'gpt-5.2-codex' } },
    });
    assert.equal(execCalls.length, 1);
    assert.deepEqual(execCalls[0]?.args, ['gateway', 'restart']);
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
    assert.deepEqual(current.gateway, { port: 18791 });
    assert.equal(execCalls.length, 0);
  });

  it('applies full config cutover payloads and restarts gateway', async () => {
    fs.writeFileSync(
      configPath,
      JSON.stringify(
        {
          gateway: { port: 18791 },
          agents: {
            main: { model: { primary: 'claude-opus-4-6' } },
            writer: { model: { primary: 'gpt-4.1' } },
          },
        },
        null,
        2,
      ),
    );

    const cutoverConfig = {
      gateway: { port: 18791 },
      agents: {
        main: { model: { primary: 'claude-opus-4-6' } },
        chameleon: { model: { primary: 'claude-opus-4-6' }, workspace: '~/.openclaw/workspace-chameleon' },
      },
    };

    await handleAdminCommandDispatchEvent({
      type: 'admin.command',
      data: {
        command_id: 'cmd-cutover',
        action: 'config.cutover',
        confirm: true,
        config_full: cutoverConfig,
      },
    });

    const updated = JSON.parse(fs.readFileSync(configPath, 'utf8')) as Record<string, unknown>;
    assert.deepEqual(updated, cutoverConfig);
    assert.equal(execCalls.length, 1);
    assert.deepEqual(execCalls[0]?.args, ['gateway', 'restart']);
  });

  it('validates rollback hash and restores full snapshot config', async () => {
    const cutoverConfig = {
      gateway: { port: 18791 },
      agents: {
        main: { model: { primary: 'claude-opus-4-6' } },
        chameleon: { model: { primary: 'claude-opus-4-6' } },
      },
    };
    fs.writeFileSync(configPath, JSON.stringify(cutoverConfig, null, 2));

    const rollbackConfig = {
      gateway: { port: 18791 },
      agents: {
        main: { model: { primary: 'claude-opus-4-6' } },
        writer: { model: { primary: 'gpt-4.1' } },
      },
    };

    await handleAdminCommandDispatchEvent({
      type: 'admin.command',
      data: {
        command_id: 'cmd-rollback',
        action: 'config.rollback',
        confirm: true,
        config_hash: hashCanonical(cutoverConfig),
        config_full: rollbackConfig,
      },
    });

    const updated = JSON.parse(fs.readFileSync(configPath, 'utf8')) as Record<string, unknown>;
    assert.deepEqual(updated, rollbackConfig);
    assert.equal(execCalls.length, 1);
    assert.deepEqual(execCalls[0]?.args, ['gateway', 'restart']);
  });

  it('rejects rollback when current config hash does not match expected checkpoint', async () => {
    const cutoverConfig = {
      gateway: { port: 18791 },
      agents: {
        main: { model: { primary: 'claude-opus-4-6' } },
        chameleon: { model: { primary: 'claude-opus-4-6' } },
      },
    };
    fs.writeFileSync(configPath, JSON.stringify(cutoverConfig, null, 2));

    await assert.rejects(
      handleAdminCommandDispatchEvent({
        type: 'admin.command',
        data: {
          command_id: 'cmd-rollback-mismatch',
          action: 'config.rollback',
          confirm: true,
          config_hash: 'deadbeef',
          config_full: {
            gateway: { port: 18791 },
            agents: { main: { model: { primary: 'claude-opus-4-6' } } },
          },
        },
      }),
      /config\.rollback hash mismatch/,
    );
    assert.equal(execCalls.length, 0);
  });
});
