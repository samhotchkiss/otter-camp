import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { beforeEach, describe, it } from "node:test";
import {
  buildOtterCampWSURL,
  buildCompletionActivityEventFromProgressLineForTest,
  buildIdentityPreamble,
  formatSessionContextMessageForTest,
  formatSessionSystemPromptForTest,
  formatSessionDisplayLabel,
  getSessionContextForTest,
  isPathWithinProjectRoot,
  formatQuestionnaireForFallback,
  isCompactWhoAmIInsufficient,
  isCanonicalChameleonSessionKey,
  normalizeQuestionnairePayload,
  parseAgentIDFromSessionKeyForTest,
  parseChameleonSessionKey,
  parseCompletionProgressLine,
  parseNumberedAnswers,
  parseNumberedQuestionnaireResponse,
  parseQuestionnaireAnswer,
  resetBufferedActivityEventsForTest,
  resolveExecutionMode,
  resolveProjectWorktreeRoot,
  resetPeriodicSyncGuardForTest,
  resetReconnectStateForTest,
  resolveOtterCampWSSecret,
  resetSessionContextsForTest,
  runSerializedSyncOperationForTest,
  setContinuousModeEnabledForTest,
  sanitizeWebSocketURLForLog,
  setSessionContextForTest,
  triggerOpenClawCloseForTest,
  type QuestionnairePayload,
  type QuestionnaireQuestion,
} from "./openclaw-bridge";

describe("bridge completion metadata helpers", () => {
  beforeEach(() => {
    resetBufferedActivityEventsForTest();
  });

  it("parses pushed progress lines into completion metadata", () => {
    const parsed = parseCompletionProgressLine(
      "[2026-02-09 11:42 MST] Issue #471 | Commit 758bcc8 | pushed | Added worktree guards | Tests: npm run test:bridge",
    );
    assert.deepEqual(parsed, {
      issueNumber: 471,
      commitSHA: "758bcc8",
      action: "pushed",
      pushStatus: "succeeded",
    });
  });

  it("parses failed push progress lines and ignores non-push activity lines", () => {
    const failed = parseCompletionProgressLine(
      "[2026-02-09 11:42 MST] Issue #471 | Commit 758bcc8 | push failed | remote rejected",
    );
    assert.deepEqual(failed, {
      issueNumber: 471,
      commitSHA: "758bcc8",
      action: "push failed",
      pushStatus: "failed",
    });

    const ignored = parseCompletionProgressLine(
      "[2026-02-09 11:42 MST] Issue #471 | Commit 758bcc8 | closed | Added worktree guards | Tests: n/a",
    );
    assert.equal(ignored, null);
  });

  it("builds completion activity events with commit and push metadata", async () => {
    const event = await buildCompletionActivityEventFromProgressLineForTest(
      "00000000-0000-0000-0000-000000000123",
      "[2026-02-09 11:42 MST] Issue #471 | Commit 758bcc8 | pushed | Added worktree guards | Tests: n/a",
    );

    assert.notEqual(event, null);
    assert.equal(event?.trigger, "task.completion");
    assert.equal(event?.session_key, "completion:issue:471");
    assert.equal(event?.scope?.issue_number, 471);
    assert.equal(event?.commit_sha, "758bcc8");
    assert.equal(event?.push_status, "succeeded");
    assert.equal(event?.status, "completed");
  });
});

describe("bridge chameleon session key helpers", () => {
  it("validates canonical chameleon session keys", () => {
    assert.equal(
      isCanonicalChameleonSessionKey("agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab"),
      true,
    );
    assert.equal(
      isCanonicalChameleonSessionKey("agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB"),
      true,
    );
    assert.equal(isCanonicalChameleonSessionKey("agent:main:slack"), false);
    assert.equal(isCanonicalChameleonSessionKey("agent:chameleon:oc:not-a-uuid"), false);
  });

  it("extracts the agent UUID from canonical chameleon session keys", () => {
    assert.equal(
      parseChameleonSessionKey("agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB"),
      "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    );
    assert.equal(parseChameleonSessionKey("agent:main:slack"), null);
    assert.equal(parseChameleonSessionKey("agent:chameleon:oc:not-a-uuid"), null);
  });

  it("sanitizes fallback agent id extraction from session keys", () => {
    assert.equal(parseAgentIDFromSessionKeyForTest("agent:main:slack"), "main");
    assert.equal(parseAgentIDFromSessionKeyForTest("agent:three-stones:webchat"), "three-stones");
    assert.equal(
      parseAgentIDFromSessionKeyForTest("agent:A1B2C3D4-5678-90AB-CDEF-1234567890AB:main"),
      "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    );
    assert.equal(parseAgentIDFromSessionKeyForTest("agent:../../etc:main"), "");
    assert.equal(parseAgentIDFromSessionKeyForTest("agent:foo/bar:main"), "");
  });
});

describe("bridge identity preamble helpers", () => {
  const originalFetch = globalThis.fetch;
  const agentID = "a1b2c3d4-5678-90ab-cdef-1234567890ab";
  const sessionKey = `agent:chameleon:oc:${agentID}`;

  beforeEach(() => {
    resetSessionContextsForTest();
    if (originalFetch) {
      globalThis.fetch = originalFetch;
    }
  });

  it("injects identity preamble before first task content for chameleon sessions", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm_1",
      agentID,
    });

    globalThis.fetch = (async () =>
      new Response(
        JSON.stringify({
          profile: "compact",
          agent: {
            id: agentID,
            name: "Derek",
            role: "Engineering Lead",
          },
          soul_summary: "Calm systems thinker.",
          identity_summary: "Leads platform reliability.",
          instructions_summary: "Prefer concrete plans with tests.",
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      )) as typeof fetch;

    const content = "Ship the release checklist now.";
    const contextual = await formatSessionContextMessageForTest(sessionKey, content);
    assert.ok(contextual.includes("[OtterCamp Identity Injection]"));
    assert.ok(contextual.includes("Identity profile: compact"));
    assert.ok(contextual.indexOf("[OtterCamp Identity Injection]") < contextual.indexOf(content));
  });

  it("builds a system prompt envelope without echoing user content", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm_sys",
      agentID,
    });

    globalThis.fetch = (async () =>
      new Response(
        JSON.stringify({
          profile: "compact",
          agent: {
            id: agentID,
            name: "Marcus",
            role: "Operator",
          },
          soul_summary: "Grounded and direct.",
          identity_summary: "Execution-focused.",
          instructions_summary: "Be explicit and pragmatic.",
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      )) as typeof fetch;

    const userText = "are you sure you don't know your name?";
    const systemPrompt = await formatSessionSystemPromptForTest(sessionKey, userText);
    assert.ok(systemPrompt.includes("[OtterCamp Identity Injection]"));
    assert.ok(systemPrompt.includes("[OTTERCAMP_CONTEXT]"));
    assert.equal(systemPrompt.includes(userText), false);

    const secondPrompt = await formatSessionSystemPromptForTest(sessionKey, "next turn");
    assert.ok(secondPrompt.includes("[OTTERCAMP_CONTEXT_REMINDER]"));
    assert.equal(secondPrompt.includes("next turn"), false);
  });

  it("falls back from compact to full profile when compact context is insufficient", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm_2",
      agentID,
    });

    let fetchCalls = 0;
    globalThis.fetch = (async () => {
      fetchCalls += 1;
      if (fetchCalls === 1) {
        return new Response(
          JSON.stringify({
            profile: "compact",
            agent: { id: agentID, name: "Derek" },
            soul_summary: "",
            identity_summary: "Short",
            instructions_summary: "",
          }),
          {
            status: 200,
            headers: { "Content-Type": "application/json" },
          },
        );
      }
      return new Response(
        JSON.stringify({
          profile: "full",
          agent: { id: agentID, name: "Derek", role: "Engineering Lead" },
          soul: "Full soul context with deeply specific guidance.",
          identity: "Full identity context with priorities and role.",
          instructions: "Full instructions context with operating constraints.",
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }) as typeof fetch;

    const contextual = await formatSessionContextMessageForTest(sessionKey, "Draft migration plan.");
    assert.equal(fetchCalls, 2);
    assert.ok(contextual.includes("Identity profile: full"));
    assert.ok(contextual.includes("Full soul context with deeply specific guidance."));
  });

  it("retries identity bootstrap when whoami is temporarily unavailable", async () => {
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm_3",
      agentID,
    });

    let fetchCalls = 0;
    globalThis.fetch = (async () => {
      fetchCalls += 1;
      if (fetchCalls === 1) {
        return new Response("temporary failure", {
          status: 403,
          statusText: "Forbidden",
        });
      }
      return new Response(
        JSON.stringify({
          profile: "compact",
          agent: {
            id: agentID,
            name: "Marcus",
            role: "Operator",
          },
          soul_summary: "Grounded and direct.",
          identity_summary: "Leads execution and closes loops.",
          instructions_summary: "State assumptions and confirm outcomes.",
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }) as typeof fetch;

    const first = await formatSessionContextMessageForTest(sessionKey, "Status check.");
    assert.equal(first.includes("[OtterCamp Identity Injection]"), false);

    const second = await formatSessionContextMessageForTest(sessionKey, "Status check (retry).");
    assert.ok(second.includes("[OtterCamp Identity Injection]"));
    assert.ok(second.includes("You are Marcus, Operator."));
    assert.ok(fetchCalls >= 2);
  });

  it("uses deterministic compact insufficiency checks and label formatting", () => {
    assert.equal(
      isCompactWhoAmIInsufficient({
        profile: "compact",
        agent: { id: "a1", name: "Derek" },
        soul_summary: "",
        identity_summary: "brief",
        instructions_summary: "",
      }),
      true,
    );
    assert.equal(
      isCompactWhoAmIInsufficient({
        profile: "compact",
        agent: { id: "a1", name: "Derek" },
        soul_summary: "Calm systems thinker.",
        identity_summary: "Owns execution quality and delivery reliability.",
        instructions_summary: "Work in small commits and explicit tests.",
      }),
      false,
    );

    assert.equal(
      formatSessionDisplayLabel("Derek", "Fix flaky release gate"),
      "Derek — Fix flaky release gate",
    );
    assert.equal(formatSessionDisplayLabel("Derek", ""), "Derek");
  });

  it("renders an identity preamble from profile payload data", () => {
    const preamble = buildIdentityPreamble({
      profile: "compact",
      payload: {
        agent: { name: "Nova", role: "Writer" },
        soul_summary: "Creative and structured.",
        identity_summary: "Specializes in technical storytelling.",
        instructions_summary: "Ask clarifying questions before drafting.",
        active_tasks: [{ project: "Docs", issue: "#42", title: "Draft release note", status: "in_progress" }],
      },
      taskSummary: "Prepare launch draft",
    });
    assert.ok(preamble.includes("You are Nova, Writer."));
    assert.ok(preamble.includes("Active tasks: Docs / #42 / Draft release note [in_progress]"));
    assert.ok(preamble.includes("Task: Prepare launch draft"));
  });
});

describe("bridge execution mode + path guard helpers", () => {
  beforeEach(() => {
    resetSessionContextsForTest();
  });

  it("enforces conversation mode policy when no project_id is present", async () => {
    const sessionKey = "agent:main:slack";
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm_3",
      agentID: "main",
    });

    assert.equal(
      resolveExecutionMode({
        kind: "dm",
      }),
      "conversation",
    );

    const contextual = await formatSessionContextMessageForTest(sessionKey, "Please review this plan.");
    assert.ok(contextual.includes("[OTTERCAMP_EXECUTION_MODE]"));
    assert.ok(contextual.includes("- mode: conversation"));
    assert.ok(contextual.includes("deny write/edit/apply_patch"));
    assert.ok(contextual.includes("- enforcement: policy-level only (prompt contract, no write hooks in v1)"));
    assert.ok(contextual.includes("- TODO: enforce mutation denial via OpenClaw tool/write interception hooks"));
    assert.ok(contextual.includes("- workspaceAccess: none"));
  });

  it("assigns deterministic project worktree roots and exposes cwd metadata", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-worktree-test-"));
    process.env.OTTER_PROJECT_WORKTREE_ROOT = tempRoot;
    const projectID = "11111111-2222-3333-4444-555555555555";
    const sessionKey = `agent:main:project:${projectID}`;

    try {
      setSessionContextForTest(sessionKey, {
        kind: "project_chat",
        orgID: "org-1",
        projectID,
        agentID: "main",
      });

      assert.equal(
        resolveExecutionMode({
          kind: "project_chat",
          projectID,
        }),
        "project",
      );

      const expectedRoot = resolveProjectWorktreeRoot(projectID, sessionKey);
      const contextual = await formatSessionContextMessageForTest(sessionKey, "Implement API handlers.");
      assert.ok(contextual.includes("- mode: project"));
      assert.ok(contextual.includes(`- cwd: ${expectedRoot}`));
      assert.ok(contextual.includes(`- write_guard_root: ${expectedRoot}`));
      assert.ok(contextual.includes("- write policy: writes allowed only within write_guard_root"));
      assert.ok(contextual.includes("- enforcement: policy-level only (prompt contract, no write hooks in v1)"));
      assert.ok(contextual.includes("- TODO: enforce write/edit/apply_patch paths via OpenClaw file-write hooks"));
      assert.ok(contextual.includes("- security: path traversal and symlink escape SHOULD NOT be used"));

      const updatedContext = getSessionContextForTest(sessionKey);
      assert.equal(updatedContext?.executionMode, "project");
      assert.equal(updatedContext?.projectRoot, expectedRoot);
      assert.equal(fs.existsSync(expectedRoot), true);
    } finally {
      delete process.env.OTTER_PROJECT_WORKTREE_ROOT;
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("blocks traversal and symlink escapes outside project root", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-path-guard-"));
    const projectRoot = path.join(tempRoot, "project");
    const projectRootLink = path.join(tempRoot, "project-link");
    const outsideRoot = path.join(tempRoot, "outside");
    fs.mkdirSync(projectRoot, { recursive: true });
    fs.mkdirSync(outsideRoot, { recursive: true });
    fs.symlinkSync(projectRoot, projectRootLink);
    fs.symlinkSync(outsideRoot, path.join(projectRoot, "linked-outside"));

    try {
      assert.equal(await isPathWithinProjectRoot(projectRoot, "notes/today.md"), true);
      assert.equal(await isPathWithinProjectRoot(projectRoot, "../outside/secret.md"), false);
      assert.equal(await isPathWithinProjectRoot(projectRoot, "linked-outside/secret.md"), false);
      assert.equal(await isPathWithinProjectRoot(projectRootLink, "notes/today.md"), false);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });
});

describe("bridge websocket URL helpers", () => {
  it("builds ws URL with token while redacting token in logs", () => {
    const wsURL = buildOtterCampWSURL("super-secret-token");
    assert.ok(wsURL.includes("token=super-secret-token"));

    const redacted = sanitizeWebSocketURLForLog(wsURL);
    assert.equal(redacted.includes("token="), false);
    assert.equal(redacted.includes("super-secret-token"), false);
    assert.ok(redacted.endsWith("/ws/openclaw"));
  });

  it("returns a safe fallback for malformed websocket URLs", () => {
    assert.equal(sanitizeWebSocketURLForLog("://not-a-valid-url"), "[invalid-url]");
  });
});

describe("bridge websocket secret env resolution", () => {
  it("prefers OPENCLAW_WS_SECRET and falls back to OTTERCAMP_WS_SECRET", () => {
    assert.equal(
      resolveOtterCampWSSecret({
        OPENCLAW_WS_SECRET: "openclaw-secret",
        OTTERCAMP_WS_SECRET: "legacy-secret",
      }),
      "openclaw-secret",
    );
    assert.equal(
      resolveOtterCampWSSecret({
        OTTERCAMP_WS_SECRET: "legacy-secret",
      }),
      "legacy-secret",
    );
    assert.equal(resolveOtterCampWSSecret({}), "");
  });
});

describe("bridge periodic sync guard", () => {
  beforeEach(() => {
    resetPeriodicSyncGuardForTest();
  });

  it("serializes overlapping sync invocations", async () => {
    let started = 0;
    let releaseFirst: (() => void) | null = null;
    const firstDone = new Promise<void>((resolve) => {
      releaseFirst = resolve;
    });

    const first = runSerializedSyncOperationForTest(async () => {
      started += 1;
      await firstDone;
    });

    const secondExecuted = await runSerializedSyncOperationForTest(async () => {
      started += 1;
    });
    assert.equal(secondExecuted, false);

    releaseFirst?.();
    assert.equal(await first, true);

    const thirdExecuted = await runSerializedSyncOperationForTest(async () => {
      started += 1;
    });
    assert.equal(thirdExecuted, true);
    assert.equal(started, 2);
  });
});

describe("bridge OpenClaw reconnect behavior", () => {
  beforeEach(() => {
    resetReconnectStateForTest("openclaw");
    setContinuousModeEnabledForTest(false);
  });

  it("attempts reconnect after websocket close in continuous mode", async () => {
    setContinuousModeEnabledForTest(true);
    let reconnectAttempts = 0;

    triggerOpenClawCloseForTest(1006, "test-close", () => {
      reconnectAttempts += 1;
    });

    await new Promise((resolve) => setTimeout(resolve, 1700));
    assert.equal(reconnectAttempts > 0, true);
  });
});

describe("bridge questionnaire helpers", () => {
  it("formats fallback text for all questionnaire field types", () => {
    const questionnaire: QuestionnairePayload = {
      id: "qn-1",
      title: "Design intake",
      author: "Planner",
      questions: [
        { id: "q1", text: "Text", type: "text", required: true },
        { id: "q2", text: "Textarea", type: "textarea" },
        { id: "q3", text: "Boolean", type: "boolean" },
        { id: "q4", text: "Select", type: "select", options: ["A", "B"] },
        { id: "q5", text: "Multiselect", type: "multiselect", options: ["X", "Y"] },
        { id: "q6", text: "Number", type: "number" },
        { id: "q7", text: "Date", type: "date" },
      ],
    };

    const rendered = formatQuestionnaireForFallback(questionnaire);
    assert.ok(rendered.includes("[QUESTIONNAIRE]"));
    assert.ok(rendered.includes("Questionnaire ID: qn-1"));
    assert.ok(rendered.includes("Title: Design intake"));
    assert.ok(rendered.includes("Author: Planner"));
    assert.ok(rendered.includes("1. Text [id=q1; text, required]"));
    assert.ok(rendered.includes("2. Textarea [id=q2; textarea]"));
    assert.ok(rendered.includes("3. Boolean [id=q3; boolean]"));
    assert.ok(rendered.includes("4. Select [id=q4; select]"));
    assert.ok(rendered.includes("options: A | B"));
    assert.ok(rendered.includes("5. Multiselect [id=q5; multiselect]"));
    assert.ok(rendered.includes("options: X | Y"));
    assert.ok(rendered.includes("6. Number [id=q6; number]"));
    assert.ok(rendered.includes("7. Date [id=q7; date]"));
  });

  it("parses numbered answers with multiline bodies and ignores 0-index entries", () => {
    const answers = parseNumberedAnswers(
      [
        "0. ignored",
        "1. first line",
        "continuation line",
        "2) second",
        "3. third",
        "third continuation",
      ].join("\n"),
    );

    assert.equal(answers.has(0), false);
    assert.equal(answers.get(1), "first line\ncontinuation line");
    assert.equal(answers.get(2), "second");
    assert.equal(answers.get(3), "third\nthird continuation");
  });

  it("parses individual questionnaire answers by type", () => {
    const boolQuestion: QuestionnaireQuestion = { id: "q1", text: "Boolean", type: "boolean" };
    const numberQuestion: QuestionnaireQuestion = { id: "q2", text: "Number", type: "number" };
    const selectQuestion: QuestionnaireQuestion = {
      id: "q3",
      text: "Select",
      type: "select",
      options: ["WebSocket", "Polling"],
    };
    const multiselectQuestion: QuestionnaireQuestion = {
      id: "q4",
      text: "Multi",
      type: "multiselect",
      options: ["Desktop", "Mobile"],
    };

    assert.deepEqual(parseQuestionnaireAnswer(boolQuestion, "yes"), { value: true, valid: true });
    assert.deepEqual(parseQuestionnaireAnswer(numberQuestion, "1.5"), { value: 1.5, valid: true });
    assert.deepEqual(parseQuestionnaireAnswer(selectQuestion, "websocket"), {
      value: "WebSocket",
      valid: true,
    });
    assert.deepEqual(parseQuestionnaireAnswer(multiselectQuestion, "desktop, mobile | desktop"), {
      value: ["Desktop", "Mobile"],
      valid: true,
    });
    assert.equal(parseQuestionnaireAnswer(numberQuestion, "not-a-number").valid, false);
  });

  it("maps numbered responses to questionnaire ids with typed values", () => {
    const questionnaire: QuestionnairePayload = {
      id: "qn-typed",
      questions: [
        { id: "q1", text: "Enable?", type: "boolean", required: true },
        { id: "q2", text: "Latency", type: "number", required: true },
        {
          id: "q3",
          text: "Transport",
          type: "select",
          required: true,
          options: ["WebSocket", "Polling"],
        },
        {
          id: "q4",
          text: "Platforms",
          type: "multiselect",
          options: ["Desktop", "Mobile"],
        },
      ],
    };

    const parsed = parseNumberedQuestionnaireResponse(
      ["1. yes", "2. 1.5", "3. websocket", "4. desktop, mobile"].join("\n"),
      questionnaire,
    );

    assert.notEqual(parsed, null);
    assert.equal(parsed?.highConfidence, true);
    assert.deepEqual(parsed?.responses, {
      q1: true,
      q2: 1.5,
      q3: "WebSocket",
      q4: ["Desktop", "Mobile"],
    });
  });

  it("normalizes valid questionnaire payloads and rejects invalid payloads", () => {
    assert.equal(normalizeQuestionnairePayload(null), null);
    assert.equal(normalizeQuestionnairePayload({ id: "qn-1", questions: [] }), null);
    assert.equal(
      normalizeQuestionnairePayload({ questions: [{ id: "q1", text: "Q", type: "text" }] }),
      null,
    );

    const normalized = normalizeQuestionnairePayload({
      id: " qn-2 ",
      context_type: "project_chat",
      context_id: "project-1",
      author: "Planner",
      title: " Intake ",
      questions: [
        { id: "q1", text: "Protocol?", type: "select", options: ["A", "A", " B "] },
        { id: "q2", text: "Enabled?", type: "boolean", required: true },
        { id: "q3", text: "Skip", type: "bad-type" },
      ],
    });

    assert.deepEqual(normalized, {
      id: "qn-2",
      contextType: "project_chat",
      contextID: "project-1",
      author: "Planner",
      title: "Intake",
      questions: [
        { id: "q1", text: "Protocol?", type: "select", required: undefined, options: ["A", "B"] },
        { id: "q2", text: "Enabled?", type: "boolean", required: true, options: undefined },
      ],
      responses: undefined,
    });
  });
});
