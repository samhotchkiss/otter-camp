// @vitest-environment node
import { beforeEach, describe, expect, it } from 'vitest';
import {
  dispatchInboundEventForTest,
  getDispatchReplayQueueStateForTest,
  queueDispatchEventForReplay,
  replayQueuedDispatchEventsForTest,
  resetDispatchReplayQueueForTest,
} from '../openclaw-bridge';

function buildDMDispatchPayload(messageID: string, content = 'hello'): Record<string, unknown> {
  return {
    type: 'dm.message',
    data: {
      message_id: messageID,
      session_key: 'agent:test:session',
      content,
    },
  };
}

describe('dispatch durability queue', () => {
  beforeEach(() => {
    resetDispatchReplayQueueForTest();
  });

  it('enqueues dispatch events and deduplicates by event/message id', () => {
    const first = buildDMDispatchPayload('msg-1', 'hello');
    const duplicate = buildDMDispatchPayload('msg-1', 'hello again');

    expect(queueDispatchEventForReplay('dm.message', first)).toBe(true);
    expect(queueDispatchEventForReplay('dm.message', duplicate)).toBe(false);

    const state = getDispatchReplayQueueStateForTest();
    expect(state.depth).toBe(1);
    expect(state.ids).toEqual(['dm.message:msg-1']);
  });

  it('drops oldest entries deterministically when max item count is exceeded', () => {
    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-1'), undefined, { maxItems: 2 })).toBe(true);
    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-2'), undefined, { maxItems: 2 })).toBe(true);
    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-3'), undefined, { maxItems: 2 })).toBe(true);

    const state = getDispatchReplayQueueStateForTest();
    expect(state.depth).toBe(2);
    expect(state.ids).toEqual(['dm.message:msg-2', 'dm.message:msg-3']);
  });

  it('drops oldest entries when byte budget is exceeded', () => {
    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-1', 'same-size'))).toBe(true);
    const firstState = getDispatchReplayQueueStateForTest();

    expect(
      queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-2', 'same-size'), undefined, {
        maxItems: 10,
        maxBytes: firstState.totalBytes + 8,
      }),
    ).toBe(true);

    const state = getDispatchReplayQueueStateForTest();
    expect(state.depth).toBe(1);
    expect(state.ids).toEqual(['dm.message:msg-2']);
  });

  it('replays queued events in FIFO order and suppresses delivered duplicates', async () => {
    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-1'))).toBe(true);
    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-2'))).toBe(true);

    const replayed: string[] = [];
    const flushed = await replayQueuedDispatchEventsForTest(async (eventType, payload) => {
      replayed.push(`${eventType}:${String((payload.data as { message_id?: string }).message_id || '')}`);
    });

    expect(replayed).toEqual(['dm.message:msg-1', 'dm.message:msg-2']);
    expect(flushed).toEqual(['dm.message:msg-1', 'dm.message:msg-2']);
    expect(getDispatchReplayQueueStateForTest().depth).toBe(0);

    expect(queueDispatchEventForReplay('dm.message', buildDMDispatchPayload('msg-1'))).toBe(false);
  });

  it('ignores non-dispatch websocket events without enqueuing replay', async () => {
    await expect(
      dispatchInboundEventForTest('connected', { type: 'connected', message: 'welcome' }, 'socket'),
    ).resolves.toBeUndefined();
    await expect(
      dispatchInboundEventForTest('bridge.status', { type: 'bridge.status' }, 'socket'),
    ).resolves.toBeUndefined();

    const state = getDispatchReplayQueueStateForTest();
    expect(state.depth).toBe(0);
    expect(state.ids).toEqual([]);
  });
});
