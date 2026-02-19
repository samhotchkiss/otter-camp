import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";
import {
  detectCompactionSignalForTest,
  fetchCompactionRecoveryContextForTest,
  reportCompactionSignalToOtterCampForTest,
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

  it("does not trigger heuristic detection without provider summary text", () => {
    const signal = detectCompactionSignalForTest("chat", {
      session_key: "agent:main:dm",
      org_id: "org-1",
      pre_compaction_tokens: 2400,
      post_compaction_tokens: 600,
    });

    assert.equal(signal, null);
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

  it("passes quality gates and truncates recovery context fetch response", async () => {
    const signal: CompactionSignal = {
      sessionKey: "agent:main:dm",
      orgID: "org-1",
      agentID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      summaryText: "Compacted summary text",
      reason: "explicit",
    };

    const originalFetch = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (input: RequestInfo | URL) => {
      calls.push(String(input));
      return new Response(
        JSON.stringify({
          context: "x".repeat(6000),
        }),
        {
          status: 200,
          headers: { "content-type": "application/json" },
        },
      );
    }) as typeof fetch;

    try {
      const context = await fetchCompactionRecoveryContextForTest(signal);
      assert.ok(calls.length >= 1);
      assert.match(calls[0] ?? "", /min_relevance=/);
      assert.match(calls[0] ?? "", /max_chars=/);
      assert.equal(context.length, 2000);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it("evicts oldest dedupe entries when compaction recovery key map exceeds max size", async () => {
    const maxTrackedCompactionRecoveryKeys = 500;
    const deps = {
      fetchRecoveryContext: async () => "Recovered context block",
      sendRecoveryMessage: async () => undefined,
      recordCompaction: async () => undefined,
      sleepFn: async () => undefined,
      nowMs: () => 1000,
    };

    const signals: CompactionSignal[] = [];
    for (let index = 0; index < maxTrackedCompactionRecoveryKeys + 1; index += 1) {
      signals.push({
        sessionKey: `agent:main:dm:${index}`,
        orgID: "org-1",
        agentID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
        summaryText: `Compacted summary text ${index}`,
        reason: "explicit",
      });
    }

    for (const signal of signals) {
      const ok = await runCompactionRecoveryForTest(signal, deps);
      assert.equal(ok, true);
    }

    const oldestSignalReplayed = await runCompactionRecoveryForTest(signals[0]!, deps);
    assert.equal(oldestSignalReplayed, true);

    const newestSignalReplayed = await runCompactionRecoveryForTest(
      signals[signals.length - 1]!,
      deps,
    );
    assert.equal(newestSignalReplayed, false);
  });

  it("reports compaction signals to OtterCamp events endpoint", async () => {
    const signal: CompactionSignal = {
      sessionKey: "agent:main:dm",
      orgID: "org-1",
      agentID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      summaryText: "Compacted summary text",
      reason: "explicit",
    };

    const originalFetch = globalThis.fetch;
    const calls: Array<{ url: string; body: Record<string, unknown> }> = [];
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      const bodyText = typeof init?.body === "string" ? init.body : "";
      calls.push({
        url: String(input),
        body: bodyText ? JSON.parse(bodyText) : {},
      });
      return new Response(JSON.stringify({ ok: true, updated: 1 }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }) as typeof fetch;

    try {
      await reportCompactionSignalToOtterCampForTest(signal);
      assert.equal(calls.length, 1);
      assert.match(calls[0]?.url ?? "", /\/api\/openclaw\/events$/);
      assert.equal(calls[0]?.body.event, "session.compaction");
      assert.equal(calls[0]?.body.org_id, "org-1");
      assert.equal(calls[0]?.body.session_key, "agent:main:dm");
      assert.equal(calls[0]?.body.compaction_detected, true);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
