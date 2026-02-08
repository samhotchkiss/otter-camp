// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  flushBufferedActivityEvents,
  queueActivityEventsForOrg,
  resetBufferedActivityEventsForTest,
  type BridgeAgentActivityEvent,
} from '../openclaw-bridge';

const ORG_ID = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';

function makeActivityEvent(id: string): BridgeAgentActivityEvent {
  const now = new Date('2026-02-08T20:00:00.000Z').toISOString();
  return {
    id,
    agent_id: 'main',
    session_key: `agent:main:${id}`,
    trigger: 'chat.slack',
    channel: 'slack',
    summary: `Event ${id}`,
    tokens_used: 1,
    duration_ms: 10,
    status: 'completed',
    started_at: now,
    completed_at: now,
  };
}

describe('activity event buffering', () => {
  beforeEach(() => {
    resetBufferedActivityEventsForTest();
    vi.restoreAllMocks();
  });

  it('pushes batches using the ingest payload shape', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock as typeof fetch);

    const events: BridgeAgentActivityEvent[] = [];
    for (let i = 0; i < 250; i += 1) {
      events.push(makeActivityEvent(`act-batch-${i}`));
    }
    const queued = queueActivityEventsForOrg(ORG_ID, events);
    expect(queued).toBe(250);

    const pushed = await flushBufferedActivityEvents('batch-test');
    expect(pushed).toBe(250);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    const firstPayload = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body)) as {
      org_id: string;
      events: BridgeAgentActivityEvent[];
    };
    expect(firstPayload.org_id).toBe(ORG_ID);
    expect(firstPayload.events).toHaveLength(200);

    const secondPayload = JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body)) as {
      org_id: string;
      events: BridgeAgentActivityEvent[];
    };
    expect(secondPayload.org_id).toBe(ORG_ID);
    expect(secondPayload.events).toHaveLength(50);
  });

  it('dedupes buffered and already-delivered event IDs', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock as typeof fetch);

    expect(queueActivityEventsForOrg(ORG_ID, [makeActivityEvent('evt-a'), makeActivityEvent('evt-a'), makeActivityEvent('evt-b')])).toBe(2);
    expect(await flushBufferedActivityEvents('first')).toBe(2);

    expect(queueActivityEventsForOrg(ORG_ID, [makeActivityEvent('evt-a'), makeActivityEvent('evt-b'), makeActivityEvent('evt-c')])).toBe(1);
    expect(await flushBufferedActivityEvents('second')).toBe(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    const secondPayload = JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body)) as {
      events: BridgeAgentActivityEvent[];
    };
    expect(secondPayload.events).toHaveLength(1);
    expect(secondPayload.events[0]?.id).toBe('evt-c');
  });

  it('keeps queued events when a flush fails and retries later', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('bad request', { status: 400, statusText: 'Bad Request' }))
      .mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock as typeof fetch);

    expect(queueActivityEventsForOrg(ORG_ID, [makeActivityEvent('evt-r1'), makeActivityEvent('evt-r2')])).toBe(2);
    expect(await flushBufferedActivityEvents('first-failure')).toBe(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    expect(await flushBufferedActivityEvents('retry-success')).toBe(2);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
