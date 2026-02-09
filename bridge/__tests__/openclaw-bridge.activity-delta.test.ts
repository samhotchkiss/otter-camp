import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  buildActivityEventsFromSessionDeltas,
  getSessionContextStateForTest,
  inferActivityTrigger,
  resetSessionContextsForTest,
  setSessionContextForTest,
  type OpenClawSession,
} from '../openclaw-bridge';

function makeSession(overrides: Partial<OpenClawSession> = {}): OpenClawSession {
  return {
    key: 'agent:main:default',
    kind: 'main',
    channel: 'slack',
    updatedAt: 1_707_340_000_000,
    sessionId: 'session-1',
    model: 'opus-4-6',
    contextTokens: 0,
    totalTokens: 0,
    systemSent: false,
    ...overrides,
  };
}

describe('inferActivityTrigger', () => {
  it('maps cron/spawn/chat/heartbeat/system trigger patterns', () => {
    assert.deepEqual(
      inferActivityTrigger(
        makeSession({
          key: 'cron:summary:job-1',
          kind: 'isolated',
          channel: 'system',
        }),
      ),
      { trigger: 'cron.scheduled', channel: 'cron' },
    );

    assert.deepEqual(
      inferActivityTrigger(
        makeSession({
          key: 'spawn:sub-agent:1',
          kind: 'sub',
          channel: 'system',
        }),
      ),
      { trigger: 'spawn.sub_agent', channel: 'system' },
    );

    assert.deepEqual(
      inferActivityTrigger(
        makeSession({
          key: 'agent:main:isolated',
          kind: 'isolated',
          channel: 'system',
        }),
      ),
      { trigger: 'spawn.isolated', channel: 'system' },
    );

    assert.deepEqual(
      inferActivityTrigger(
        makeSession({
          key: 'agent:main:heartbeat',
          displayName: 'Heartbeat check',
          channel: 'system',
        }),
      ),
      { trigger: 'heartbeat', channel: 'system' },
    );

    assert.deepEqual(
      inferActivityTrigger(
        makeSession({
          key: 'agent:main:slack',
          kind: 'main',
          channel: 'slack',
        }),
      ),
      { trigger: 'chat.slack', channel: 'slack' },
    );

    assert.deepEqual(
      inferActivityTrigger(
        makeSession({
          key: 'worker:maintenance',
          kind: 'worker',
          channel: '',
        }),
      ),
      { trigger: 'system.event', channel: 'system' },
    );
  });
});

describe('buildActivityEventsFromSessionDeltas', () => {
  it('generates activity records from session updates with scope/model/token metadata', () => {
    const projectID = '11111111-1111-1111-1111-111111111111';
    const issueID = '22222222-2222-2222-2222-222222222222';
    const previousByKey = new Map<string, OpenClawSession>();
    previousByKey.set(
      `agent:main:project:${projectID}`,
      makeSession({
        key: `agent:main:project:${projectID}`,
        displayName: 'Triaging inbox',
        updatedAt: 1_707_340_000_000,
        totalTokens: 100,
        model: 'opus-4-6',
      }),
    );

    const currentSessions: OpenClawSession[] = [
      makeSession({
        key: `agent:main:project:${projectID}`,
        displayName: 'Triaging inbox',
        updatedAt: 1_707_340_005_000,
        totalTokens: 160,
        model: 'opus-4-6',
        channel: 'slack',
      }),
      makeSession({
        key: `agent:worker:issue:${issueID}`,
        displayName: 'Fix failing test',
        kind: 'main',
        channel: 'telegram',
        updatedAt: 1_707_340_010_000,
        totalTokens: 20,
        model: 'sonnet-4',
        deliveryContext: {
          issue_number: 42,
          thread_id: 'issue-thread-42',
        },
      }),
    ];

    const events = buildActivityEventsFromSessionDeltas({ previousByKey, currentSessions });
    assert.equal(events.length, 2);

    const updated = events[0];
    assert.equal(updated.id.startsWith('act_'), true);
    assert.equal(updated.agent_id, 'main');
    assert.equal(updated.session_key, `agent:main:project:${projectID}`);
    assert.equal(updated.trigger, 'chat.slack');
    assert.equal(updated.tokens_used, 60);
    assert.equal(updated.duration_ms, 5000);
    assert.equal(updated.status, 'completed');
    assert.deepEqual(updated.scope, { project_id: projectID });
    assert.equal(updated.model_used, 'opus-4-6');
    assert.equal(updated.completed_at, updated.started_at);

    const created = events[1];
    assert.equal(created.agent_id, 'worker');
    assert.equal(created.trigger, 'chat.telegram');
    assert.equal(created.status, 'started');
    assert.equal(created.tokens_used, 20);
    assert.equal(created.model_used, 'sonnet-4');
    assert.deepEqual(created.scope, {
      issue_id: issueID,
      issue_number: 42,
      thread_id: 'issue-thread-42',
    });
    assert.equal(created.completed_at, undefined);
  });

  it('returns deterministic results and skips unchanged sessions', () => {
    const previous = makeSession({
      key: 'agent:main:slack',
      displayName: 'No changes',
      updatedAt: 1_707_340_000_000,
      totalTokens: 10,
    });
    const previousByKey = new Map<string, OpenClawSession>([[previous.key, previous]]);
    const currentSessions = [makeSession({ ...previous })];

    const first = buildActivityEventsFromSessionDeltas({ previousByKey, currentSessions });
    const second = buildActivityEventsFromSessionDeltas({ previousByKey, currentSessions });

    assert.deepEqual(first, []);
    assert.deepEqual(second, []);
  });
});

describe('session context retention', () => {
  it('evicts oldest contexts once max size is exceeded', () => {
    resetSessionContextsForTest();

    for (let i = 0; i <= 5000; i += 1) {
      setSessionContextForTest(`session-${i}`, { orgID: 'org-a' });
    }

    const state = getSessionContextStateForTest();
    assert.equal(state.count, 5000);
    assert.equal(state.keys.includes('session-0'), false);
    assert.equal(state.keys[0], 'session-1');
    assert.equal(state.keys[state.keys.length - 1], 'session-5000');
  });
});
