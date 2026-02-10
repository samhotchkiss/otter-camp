import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";
import {
  detectCompactionSignalForTest,
  resetCompactionRecoveryStateForTest,
  runCompactionRecoveryForTest,
  type CompactionSignal,
} from "../openclaw-bridge";

describe("bridge compaction detection + recovery", () => {
  beforeEach(() => {
    resetCompactionRecoveryStateForTest();
  });

  it("prefers explicit compaction signals", () => {
    const signal = detectCompactionSignalForTest("session.compaction", {
      session_key: "agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab",
      org_id: "org-1",
      agent_id: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      summary_text: "Compacted old context",
      pre_compaction_tokens: 3000,
      post_compaction_tokens: 900,
    });

    assert.notEqual(signal, null);
    assert.equal(signal?.reason, "explicit");
    assert.equal(signal?.summaryText, "Compacted old context");
    assert.equal(signal?.preTokens, 3000);
    assert.equal(signal?.postTokens, 900);
  });

  it("falls back to heuristic detection when token drop is large", () => {
    const signal = detectCompactionSignalForTest("chat", {
      session_key: "agent:main:dm",
      org_id: "org-1",
      pre_compaction_tokens: 2400,
      post_compaction_tokens: 600,
      summary: "Conversation summary after context compact",
    });

    assert.notEqual(signal, null);
    assert.equal(signal?.reason, "heuristic");
    assert.equal(signal?.sessionKey, "agent:main:dm");
  });

  it("injects recovery context and records compaction on success", async () => {
    const signal: CompactionSignal = {
      sessionKey: "agent:main:dm",
      orgID: "org-1",
      agentID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      summaryText: "Compacted summary text",
      reason: "explicit",
    };

    const calls: string[] = [];
    const ok = await runCompactionRecoveryForTest(signal, {
      fetchRecoveryContext: async () => {
        calls.push("fetch");
        return "Recovered context block";
      },
      sendRecoveryMessage: async (_sig, contextText, idempotencyKey) => {
        calls.push(`send:${idempotencyKey}`);
        assert.ok(contextText.includes("Recovered context block"));
      },
      recordCompaction: async () => {
        calls.push("record");
      },
      sleepFn: async () => undefined,
      nowMs: () => 1000,
    });

    assert.equal(ok, true);
    assert.equal(calls[0], "record");
    assert.equal(calls[1], "fetch");
    assert.match(calls[2] ?? "", /^send:compaction:agent:main:dm:[0-9a-f]{16}$/);
  });

  it("fails closed when recovery fetch keeps failing", async () => {
    const signal: CompactionSignal = {
      sessionKey: "agent:main:dm",
      orgID: "org-1",
      agentID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      summaryText: "Compacted summary text",
      reason: "explicit",
    };

    let sleepCalls = 0;
    const ok = await runCompactionRecoveryForTest(signal, {
      fetchRecoveryContext: async () => {
        throw new Error("network timeout");
      },
      sendRecoveryMessage: async () => {
        assert.fail("sendRecoveryMessage should not run on repeated fetch failures");
      },
      recordCompaction: async () => undefined,
      sleepFn: async () => {
        sleepCalls += 1;
      },
      nowMs: () => 1000,
    });

    assert.equal(ok, false);
    assert.equal(sleepCalls, 3);
  });

  it("skips duplicate recovery attempts within dedupe window", async () => {
    const signal: CompactionSignal = {
      sessionKey: "agent:main:dm",
      orgID: "org-1",
      agentID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      summaryText: "Compacted summary text",
      reason: "explicit",
    };

    let sendCount = 0;
    const deps = {
      fetchRecoveryContext: async () => "Recovered context block",
      sendRecoveryMessage: async () => {
        sendCount += 1;
      },
      recordCompaction: async () => undefined,
      sleepFn: async () => undefined,
      nowMs: () => 1000,
    };

    const first = await runCompactionRecoveryForTest(signal, deps);
    const second = await runCompactionRecoveryForTest(signal, deps);

    assert.equal(first, true);
    assert.equal(second, false);
    assert.equal(sendCount, 1);
  });
});
