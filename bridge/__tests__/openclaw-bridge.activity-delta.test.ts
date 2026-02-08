import { describe, expect, it } from 'vitest';
import {
  buildActivityEventsFromSessionDeltas,
  inferActivityTrigger,
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
    expect(
      inferActivityTrigger(
        makeSession({
          key: 'cron:summary:job-1',
          kind: 'isolated',
          channel: 'system',
        }),
      ),
    ).toEqual({ trigger: 'cron.scheduled', channel: 'cron' });

    expect(
      inferActivityTrigger(
        makeSession({
          key: 'spawn:sub-agent:1',
          kind: 'sub',
          channel: 'system',
        }),
      ),
    ).toEqual({ trigger: 'spawn.sub_agent', channel: 'system' });

    expect(
      inferActivityTrigger(
        makeSession({
          key: 'agent:main:isolated',
          kind: 'isolated',
          channel: 'system',
        }),
      ),
    ).toEqual({ trigger: 'spawn.isolated', channel: 'system' });

    expect(
      inferActivityTrigger(
        makeSession({
          key: 'agent:main:heartbeat',
          displayName: 'Heartbeat check',
          channel: 'system',
        }),
      ),
    ).toEqual({ trigger: 'heartbeat', channel: 'system' });

    expect(
      inferActivityTrigger(
        makeSession({
          key: 'agent:main:slack',
          kind: 'main',
          channel: 'slack',
        }),
      ),
    ).toEqual({ trigger: 'chat.slack', channel: 'slack' });

    expect(
      inferActivityTrigger(
        makeSession({
          key: 'worker:maintenance',
          kind: 'worker',
          channel: '',
        }),
      ),
    ).toEqual({ trigger: 'system.event', channel: 'system' });
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
    expect(events).toHaveLength(2);

    const updated = events[0];
    expect(updated.id.startsWith('act_')).toBe(true);
    expect(updated.agent_id).toBe('main');
    expect(updated.session_key).toBe(`agent:main:project:${projectID}`);
    expect(updated.trigger).toBe('chat.slack');
    expect(updated.tokens_used).toBe(60);
    expect(updated.duration_ms).toBe(5000);
    expect(updated.status).toBe('completed');
    expect(updated.scope).toEqual({ project_id: projectID });
    expect(updated.model_used).toBe('opus-4-6');
    expect(updated.completed_at).toBe(updated.started_at);

    const created = events[1];
    expect(created.agent_id).toBe('worker');
    expect(created.trigger).toBe('chat.telegram');
    expect(created.status).toBe('started');
    expect(created.tokens_used).toBe(20);
    expect(created.model_used).toBe('sonnet-4');
    expect(created.scope).toEqual({
      issue_id: issueID,
      issue_number: 42,
      thread_id: 'issue-thread-42',
    });
    expect(created.completed_at).toBeUndefined();
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

    expect(first).toEqual([]);
    expect(second).toEqual([]);
  });
});
