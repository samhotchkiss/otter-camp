import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";
import {
  formatAutoRecallMessageForTest,
  resetSessionContextsForTest,
  setSessionContextForTest,
} from "../openclaw-bridge";

describe("bridge auto recall injection", () => {
  const originalFetch = globalThis.fetch;
  const sessionKey = "agent:main:dm";
  const basePrompt = "Ship migration 058 and confirm recall quality gates.";

  beforeEach(() => {
    resetSessionContextsForTest();
    if (originalFetch) {
      globalThis.fetch = originalFetch;
    }
  });

  it("injects recall context with delimiters and default quality gate params", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      agentID: "agent-123",
    });

    let calledURL = "";
    globalThis.fetch = (async (input) => {
      calledURL = String(input);
      return new Response(
        JSON.stringify({
          context: "[RECALLED CONTEXT]\n- [decision] Use pgvector for semantic memory.",
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }) as typeof fetch;

    const formatted = await formatAutoRecallMessageForTest(sessionKey, basePrompt);
    assert.match(formatted, /\[OTTERCAMP_AUTO_RECALL\]/);
    assert.match(formatted, /\[\/OTTERCAMP_AUTO_RECALL\]/);
    assert.ok(formatted.endsWith(basePrompt));

    const parsed = new URL(calledURL);
    assert.equal(parsed.pathname, "/api/memory/recall");
    assert.equal(parsed.searchParams.get("org_id"), "org-1");
    assert.equal(parsed.searchParams.get("agent_id"), "agent-123");
    assert.equal(parsed.searchParams.get("q"), basePrompt);
    assert.equal(parsed.searchParams.get("max_results"), "3");
    assert.equal(parsed.searchParams.get("min_relevance"), "0.7");
    assert.equal(parsed.searchParams.get("max_chars"), "2000");
  });

  it("uses responder_agent_id fallback when agent_id is not set in session context", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      responderAgentID: "responder-789",
    });

    let calledURL = "";
    globalThis.fetch = (async (input) => {
      calledURL = String(input);
      return new Response(JSON.stringify({ context: "" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;

    const formatted = await formatAutoRecallMessageForTest(sessionKey, basePrompt);
    assert.equal(formatted, basePrompt);
    assert.equal(new URL(calledURL).searchParams.get("agent_id"), "responder-789");
  });

  it("fails closed when recall request is non-OK and returns original content", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      agentID: "agent-123",
    });

    globalThis.fetch = (async () =>
      new Response("recall unavailable", {
        status: 404,
        statusText: "Not Found",
      })) as typeof fetch;

    const formatted = await formatAutoRecallMessageForTest(sessionKey, basePrompt);
    assert.equal(formatted, basePrompt);
  });

  it("skips recall lookup when payload already contains a recall marker", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      agentID: "agent-123",
    });

    let fetchCalls = 0;
    globalThis.fetch = (async () => {
      fetchCalls += 1;
      return new Response(JSON.stringify({ context: "unused" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }) as typeof fetch;

    const seeded = "[OTTERCAMP_AUTO_RECALL]\nexisting context\n[/OTTERCAMP_AUTO_RECALL]\n\nhello";
    const formatted = await formatAutoRecallMessageForTest(sessionKey, seeded);
    assert.equal(formatted, seeded);
    assert.equal(fetchCalls, 0);
  });
});
