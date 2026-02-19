import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, it } from "node:test";
import {
  dispatchInboundEventForTest,
  resetSessionContextsForTest,
  setSendRequestForTest,
} from "../openclaw-bridge";

describe("bridge DM attachment dispatch", () => {
  beforeEach(() => {
    resetSessionContextsForTest();
  });

  afterEach(() => {
    setSendRequestForTest(null);
  });

  it("forwards dm.message attachments in chat.send payload", async () => {
    const calls: Array<{ method: string; params: Record<string, unknown> }> = [];
    setSendRequestForTest(async (method, params) => {
      calls.push({ method, params });
      return { ok: true };
    });

    await dispatchInboundEventForTest("dm.message", {
      data: {
        session_key: "dm:test-session",
        message_id: "msg-1",
        content: "See attached image",
        attachments: [
          {
            url: "/api/attachments/11111111-2222-3333-4444-555555555555",
            filename: "diagram.png",
            content_type: "image/png",
            size_bytes: 1024,
          },
        ],
      },
    });

    assert.equal(calls.length, 1);
    assert.equal(calls[0]?.method, "chat.send");
    assert.match(String(calls[0]?.params.message ?? ""), /See attached image/);
    const attachments = calls[0]?.params.attachments as Array<Record<string, unknown>>;
    assert.equal(attachments.length, 1);
    assert.equal(attachments[0]?.filename, "diagram.png");
    assert.equal(attachments[0]?.content_type, "image/png");
    assert.equal(attachments[0]?.size_bytes, 1024);
    assert.match(String(attachments[0]?.url ?? ""), /\/api\/attachments\/11111111-2222-3333-4444-555555555555$/);
  });

  it("dispatches attachment-only dm.message payloads", async () => {
    const calls: Array<{ method: string; params: Record<string, unknown> }> = [];
    setSendRequestForTest(async (method, params) => {
      calls.push({ method, params });
      return { ok: true };
    });

    await dispatchInboundEventForTest("dm.message", {
      data: {
        session_key: "dm:test-session",
        message_id: "msg-2",
        content: "",
        attachments: [
          {
            url: "/api/attachments/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
            filename: "notes.pdf",
            content_type: "application/pdf",
            size_bytes: 4096,
          },
        ],
      },
    });

    assert.equal(calls.length, 1);
    assert.equal(calls[0]?.method, "chat.send");
    assert.match(String(calls[0]?.params.message ?? ""), /\[Attachments\]/);
    const attachments = calls[0]?.params.attachments as Array<Record<string, unknown>>;
    assert.equal(attachments.length, 1);
    assert.equal(attachments[0]?.filename, "notes.pdf");
  });
});
