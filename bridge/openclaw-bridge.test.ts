import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, it } from "node:test";
import {
  buildOtterCampWSURL,
  buildGatewayConnectCapsForTest,
  buildCompletionActivityEventFromProgressLineForTest,
  buildIdentityPreamble,
  compactMemoryExtractOutput,
  ensureGatewayCallCredentials,
  ensureChameleonWorkspaceGuideInstalledForTest,
  ensureChameleonWorkspaceOtterCLIConfigInstalledForTest,
  formatSessionContextMessageForTest,
  formatSessionSystemPromptForTest,
  formatIncrementalDMContentForTest,
  formatSessionDisplayLabel,
  getSessionContextForTest,
  extractMutationToolTargetPathsForTest,
  isPathWithinProjectRoot,
  formatQuestionnaireForFallback,
  isCompactWhoAmIInsufficient,
  isCanonicalChameleonSessionKey,
  isPermanentOpenClawAgentForTest,
  normalizeQuestionnairePayload,
  parseAgentIDFromSessionKeyForTest,
  parseChameleonSessionKey,
  parseCompletionProgressLine,
  parseNumberedAnswers,
  parseNumberedQuestionnaireResponse,
  parseQuestionnaireAnswer,
  dispatchInboundEventForTest,
  resetBufferedActivityEventsForTest,
  resetIngestedToolEventsForTest,
  resetMutationEnforcementStateForTest,
  resolveExecutionMode,
  resolveProjectWorktreeRoot,
  resetSessionFromLocalStoreForTest,
  resetPeriodicSyncGuardForTest,
  resetReconnectStateForTest,
  resolveOtterCampWSSecret,
  resetSessionContextsForTest,
  resolveOpenClawCommandTimeoutMSForTest,
  setExecFileForTest,
  setOtterCampSocketForTest,
  setOtterCampOrgIDForTest,
  setSendRequestForTest,
  runSerializedSyncOperationForTest,
  setContinuousModeEnabledForTest,
  setPathWithinProjectRootForTest,
  sanitizeWebSocketURLForLog,
  setMutationAbortForTest,
  setSessionContextForTest,
  triggerOpenClawCloseForTest,
  handleOpenClawEventForTest,
  getIngestedToolEventsStateForTest,
  getMutationEnforcementStateForTest,
  validateMutationToolTargetsWithinProjectRootForTest,
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

describe("bridge tool-event ingestion helpers", () => {
  beforeEach(() => {
    resetIngestedToolEventsForTest();
    resetMutationEnforcementStateForTest();
    setMutationAbortForTest(async () => {});
  });

  afterEach(() => {
    setMutationAbortForTest(null);
  });

  it("includes tool-events capability in gateway connect handshake caps", () => {
    const caps = buildGatewayConnectCapsForTest();
    assert.ok(caps.includes("tool-events"));
  });

  it("ingests agent tool-stream events for downstream enforcement", async () => {
    await handleOpenClawEventForTest({
      event: "agent",
      payload: {
        stream: "tool",
        phase: "start",
        sessionKey: "agent:main:project:11111111-2222-3333-4444-555555555555",
        tool: "write",
        toolCallId: "toolcall-1",
        args: {
          path: "notes/today.md",
        },
      },
    });

    const state = getIngestedToolEventsStateForTest();
    assert.equal(state.count, 1);
    assert.equal(state.last?.sessionKey, "agent:main:project:11111111-2222-3333-4444-555555555555");
    assert.equal(state.last?.tool, "write");
    assert.equal(state.last?.phase, "start");
  });

  it("ignores non-tool agent stream events", async () => {
    await handleOpenClawEventForTest({
      event: "agent",
      payload: {
        stream: "message",
        phase: "final",
        sessionKey: "agent:main:slack",
      },
    });

    const state = getIngestedToolEventsStateForTest();
    assert.equal(state.count, 0);
    assert.equal(state.last, null);
  });
});

describe("bridge chameleon session key helpers", () => {
  it("validates canonical chameleon session keys", () => {
    assert.equal(
      isCanonicalChameleonSessionKey("agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab"),
      true,
    );
    assert.equal(
      isCanonicalChameleonSessionKey(
        "agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab:11111111-2222-3333-4444-555555555555",
      ),
      true,
    );
    assert.equal(
      isCanonicalChameleonSessionKey("agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB"),
      true,
    );
    assert.equal(isCanonicalChameleonSessionKey("agent:main:slack"), false);
    assert.equal(isCanonicalChameleonSessionKey("agent:chameleon:oc:not-a-uuid"), false);
  });

  it("extracts project/issue UUIDs from canonical chameleon session keys", () => {
    assert.deepEqual(
      parseChameleonSessionKey("agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB"),
      {
        projectID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
      },
    );
    assert.deepEqual(
      parseChameleonSessionKey(
        "agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB:11111111-2222-3333-4444-555555555555",
      ),
      {
        projectID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
        issueID: "11111111-2222-3333-4444-555555555555",
      },
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
    assert.equal(
      parseAgentIDFromSessionKeyForTest("agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab"),
      "",
    );
  });

  it("tracks permanent OpenClaw agent IDs", () => {
    assert.equal(isPermanentOpenClawAgentForTest("main"), true);
    assert.equal(isPermanentOpenClawAgentForTest("elephant"), true);
    assert.equal(isPermanentOpenClawAgentForTest("ellie-extractor"), true);
    assert.equal(isPermanentOpenClawAgentForTest("lori"), true);
    assert.equal(isPermanentOpenClawAgentForTest("chameleon"), true);
    assert.equal(isPermanentOpenClawAgentForTest("technonymous"), false);
  });
});

describe("bridge project dispatch routing", () => {
  const rpcCalls: Array<{ method: string; params: Record<string, unknown> }> = [];

  beforeEach(() => {
    rpcCalls.length = 0;
    resetSessionContextsForTest();
    setSendRequestForTest(async (method, params) => {
      rpcCalls.push({ method, params });
      return {};
    });
  });

  afterEach(() => {
    setSendRequestForTest(null);
  });

  it("routes non-permanent project agents through chameleon session keys", async () => {
    const projectID = "11111111-2222-3333-4444-555555555555";
    await dispatchInboundEventForTest("project.chat.message", {
      type: "project.chat.message",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        message_id: "msg-1",
        project_id: projectID,
        project_name: "Spec 521",
        agent_id: "technonymous",
        agent_name: "Technonymous",
        content: "Ship the patch.",
      },
    });

    const dispatchCall = rpcCalls.find((call) => call.method === "agent");
    assert.equal(dispatchCall?.method, "agent");
    assert.equal(dispatchCall?.params.sessionKey, `agent:chameleon:oc:${projectID}`);

    const context = getSessionContextForTest(`agent:chameleon:oc:${projectID}`);
    assert.equal(context?.kind, "project_chat");
    assert.equal(context?.agentID, "technonymous");
    assert.equal(context?.agentName, "Technonymous");
  });

  it("routes main project dispatches to the main session via agent method", async () => {
    await dispatchInboundEventForTest("project.chat.message", {
      type: "project.chat.message",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        message_id: "msg-main",
        project_id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
        project_name: "Main routing",
        agent_id: "main",
        agent_name: "Frank",
        content: "Please review this project update.",
      },
    });

    const dispatchCall = rpcCalls.find((call) => call.method === "agent");
    assert.equal(dispatchCall?.method, "agent");
    assert.equal(dispatchCall?.params.sessionKey, "agent:main:main");
  });
});

describe("bridge issue dispatch routing", () => {
  const rpcCalls: Array<{ method: string; params: Record<string, unknown> }> = [];

  beforeEach(() => {
    rpcCalls.length = 0;
    resetSessionContextsForTest();
    setSendRequestForTest(async (method, params) => {
      rpcCalls.push({ method, params });
      return {};
    });
  });

  afterEach(() => {
    setSendRequestForTest(null);
  });

  it("routes non-permanent issue dispatches through compound chameleon keys", async () => {
    const projectID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
    const issueID = "11111111-2222-3333-4444-555555555555";
    await dispatchInboundEventForTest("issue.comment.message", {
      type: "issue.comment.message",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        message_id: "issue-msg-1",
        project_id: projectID,
        issue_id: issueID,
        issue_number: 52,
        issue_title: "Bridge routing",
        agent_id: "technonymous",
        responder_agent_id: "technonymous",
        content: "Please update this issue thread.",
      },
    });

    const dispatchCall = rpcCalls.find((call) => call.method === "agent");
    assert.equal(dispatchCall?.method, "agent");
    assert.equal(dispatchCall?.params.sessionKey, `agent:chameleon:oc:${projectID}:${issueID}`);

    const context = getSessionContextForTest(`agent:chameleon:oc:${projectID}:${issueID}`);
    assert.equal(context?.kind, "issue_comment");
    assert.equal(context?.agentID, "technonymous");
    assert.equal(context?.responderAgentID, "technonymous");
    assert.equal(context?.issueID, issueID);
    assert.equal(context?.projectID, projectID);
  });

  it("routes main issue dispatches to the main session via agent method", async () => {
    await dispatchInboundEventForTest("issue.comment.message", {
      type: "issue.comment.message",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        message_id: "issue-msg-main",
        project_id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
        issue_id: "66666666-7777-8888-9999-aaaaaaaaaaaa",
        issue_number: 89,
        issue_title: "Main issue routing",
        agent_id: "main",
        responder_agent_id: "main",
        content: "Frank, please respond on this issue.",
      },
    });

    const dispatchCall = rpcCalls.find((call) => call.method === "agent");
    assert.equal(dispatchCall?.method, "agent");
    assert.equal(dispatchCall?.params.sessionKey, "agent:main:main");
  });
});

describe("bridge chameleon identity + persistence fallbacks", () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
    setOtterCampOrgIDForTest(null);
    resetSessionContextsForTest();
  });

  it("resolves whoami identity using context agent slug for chameleon sessions", async () => {
    const projectID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
    const sessionKey = `agent:chameleon:oc:${projectID}`;
    const fetchURLs: string[] = [];
    globalThis.fetch = (async (input: URL | string) => {
      fetchURLs.push(String(input));
      return {
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => ({
          profile: "compact",
          agent: { id: "technonymous", name: "Technonymous" },
          soul_summary: "Focuses on delivery.",
          identity_summary: "Bridges project context.",
          instructions_summary: "Keep updates concise.",
        }),
        text: async () => "",
      } as Response;
    }) as typeof fetch;

    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "00000000-0000-0000-0000-000000000123",
      projectID,
      agentID: "technonymous",
      agentName: "Technonymous",
    });

    await formatSessionContextMessageForTest(sessionKey, "Please plan the next step.");

    assert.equal(fetchURLs.some((url) => url.includes("/api/agents/technonymous/whoami")), true);
    assert.equal(fetchURLs.some((url) => url.includes(`/api/agents/${projectID}/whoami`)), false);
  });

  it("persists project replies for chameleon sessions using inferred project context", async () => {
    const projectID = "bbbbbbbb-1111-2222-3333-444444444444";
    const sessionKey = `agent:chameleon:oc:${projectID}`;
    const fetchURLs: string[] = [];
    globalThis.fetch = (async (input: URL | string, init?: RequestInit) => {
      fetchURLs.push(String(input));
      return {
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => ({}),
        text: async () => "",
        ...(init ? { headers: init.headers } : {}),
      } as Response;
    }) as typeof fetch;
    setOtterCampOrgIDForTest("00000000-0000-0000-0000-000000000123");

    await handleOpenClawEventForTest({
      event: "chat",
      payload: {
        state: "final",
        sessionKey,
        message: {
          role: "assistant",
          content: [{ type: "text", text: "Project update from inferred context." }],
        },
      },
    });

    assert.equal(
      fetchURLs.some((url) =>
        url.includes(`/api/projects/${projectID}/chat/messages?org_id=00000000-0000-0000-0000-000000000123`)
      ),
      true,
    );
  });

  it("persists issue replies for compound chameleon issue sessions", async () => {
    const orgID = "00000000-0000-0000-0000-000000000123";
    const projectID = "bbbbbbbb-1111-2222-3333-444444444444";
    const issueID = "99999999-aaaa-bbbb-cccc-dddddddddddd";
    const sessionKey = `agent:chameleon:oc:${projectID}:${issueID}`;
    const fetchURLs: string[] = [];
    globalThis.fetch = (async (input: URL | string, init?: RequestInit) => {
      fetchURLs.push(String(input));
      return {
        ok: true,
        status: 200,
        statusText: "OK",
        json: async () => ({}),
        text: async () => "",
        ...(init ? { headers: init.headers } : {}),
      } as Response;
    }) as typeof fetch;

    setSessionContextForTest(sessionKey, {
      kind: "issue_comment",
      orgID,
      projectID,
      issueID,
      responderAgentID: "technonymous",
      agentID: "technonymous",
      agentName: "Technonymous",
    });

    await handleOpenClawEventForTest({
      event: "chat",
      payload: {
        state: "final",
        sessionKey,
        message: {
          role: "assistant",
          content: [{ type: "text", text: "Issue update from compound chameleon session." }],
        },
      },
    });

    assert.equal(fetchURLs.some((url) => url.includes(`/api/issues/${issueID}/comments?org_id=${orgID}`)), true);
  });
});

describe("bridge local session reset helpers", () => {
  it("clears a canonical chameleon session key from local store", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "oc-session-reset-"));
    const sessionsDir = path.join(tempRoot, "agents", "chameleon", "sessions");
    fs.mkdirSync(sessionsDir, { recursive: true });
    const sessionKey = "agent:chameleon:oc:28d27f83-5518-468a-83bf-750f7ec1c9f5";
    const sessionID = "2f6f15dd-32d0-496b-9c0d-db1a2804ee64";
    const storePath = path.join(sessionsDir, "sessions.json");
    const transcriptPath = path.join(sessionsDir, `${sessionID}.jsonl`);
    fs.writeFileSync(
      storePath,
      `${JSON.stringify({
        [sessionKey]: {
          sessionId: sessionID,
          updatedAt: Date.now(),
        },
      }, null, 2)}\n`,
      "utf8",
    );
    fs.writeFileSync(transcriptPath, `{"type":"session","id":"${sessionID}"}` + "\n", "utf8");

    const result = resetSessionFromLocalStoreForTest(sessionKey, tempRoot);
    assert.equal(result.cleared, true);
    assert.equal(result.transcriptDeleted, true);
    assert.equal(result.storePath, storePath);

    const parsed = JSON.parse(fs.readFileSync(storePath, "utf8")) as Record<string, unknown>;
    assert.equal(Object.keys(parsed).length, 0);
    assert.equal(fs.existsSync(transcriptPath), false);
  });

  it("returns a non-cleared result when the session key is absent", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "oc-session-reset-miss-"));
    const sessionsDir = path.join(tempRoot, "agents", "chameleon", "sessions");
    fs.mkdirSync(sessionsDir, { recursive: true });
    const storePath = path.join(sessionsDir, "sessions.json");
    fs.writeFileSync(storePath, `${JSON.stringify({}, null, 2)}\n`, "utf8");

    const result = resetSessionFromLocalStoreForTest(
      "agent:chameleon:oc:28d27f83-5518-468a-83bf-750f7ec1c9f5",
      tempRoot,
    );
    assert.equal(result.cleared, false);
    assert.match(result.reason || "", /session not found|store not found|invalid session key/i);
  });
});

describe("bridge identity preamble helpers", () => {
  const originalFetch = globalThis.fetch;
  const agentID = "a1b2c3d4-5678-90ab-cdef-1234567890ab";
  const sessionKey = `agent:chameleon:oc:${agentID}`;
  const reminderGuidePointer =
    "Refer to OTTERCAMP.md and OTTER_COMMANDS.md for CLI syntax and operating rules.";

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
    assert.ok(
      contextual.includes('Do not identify yourself as "Chameleon" unless your injected identity name is exactly "Chameleon".'),
    );
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
    assert.ok(systemPrompt.includes("[OTTERCAMP_ACTION_DEFAULTS]"));
    assert.ok(systemPrompt.includes('Default meaning: "project" refers to an OtterCamp project record'));
    assert.equal(systemPrompt.includes(userText), false);

    const secondPrompt = await formatSessionSystemPromptForTest(sessionKey, "next turn");
    assert.equal(secondPrompt.includes("[OtterCamp Identity Injection]"), false);
    assert.ok(secondPrompt.includes("[OTTERCAMP_CONTEXT_REMINDER]"));
    assert.ok(secondPrompt.includes(reminderGuidePointer));
    assert.equal(secondPrompt.includes("[OTTERCAMP_OPERATING_GUIDE_REMINDER]"), false);
    assert.equal(secondPrompt.includes("[OTTERCAMP_ACTION_DEFAULTS]"), false);
    assert.equal(secondPrompt.includes("next turn"), false);
  });

  it("formats optional incremental context updates without forcing full identity resend", () => {
    const formatted = formatIncrementalDMContentForTest(
      "Please continue.",
      "New context: release blocker resolved.",
    );
    assert.ok(formatted.includes("[OtterCamp Context Update]"));
    assert.ok(formatted.includes("New context: release blocker resolved."));
    assert.ok(formatted.includes("[/OtterCamp Context Update]"));
    assert.ok(formatted.endsWith("Please continue."));
  });

  it("installs and injects the OtterCamp guide from the chameleon workspace", async () => {
    const originalConfigPath = process.env.OPENCLAW_CONFIG_PATH;
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "oc-guide-"));
    const workspacePath = path.join(tempRoot, "workspace-chameleon");
    const configPath = path.join(tempRoot, "openclaw.json");

    fs.writeFileSync(
      configPath,
      `${JSON.stringify({ agents: { chameleon: { workspace: workspacePath } } }, null, 2)}\n`,
      "utf8",
    );
    process.env.OPENCLAW_CONFIG_PATH = configPath;

    try {
      const installedPath = ensureChameleonWorkspaceGuideInstalledForTest();
      assert.equal(installedPath, path.join(workspacePath, "OTTERCAMP.md"));
      assert.equal(fs.existsSync(installedPath), true);

      setSessionContextForTest(sessionKey, {
        kind: "dm",
        orgID: "org-1",
        threadID: "dm_guide",
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

      const firstPrompt = await formatSessionSystemPromptForTest(sessionKey, "create project Testerooni");
      assert.ok(firstPrompt.includes("[OTTERCAMP_OPERATING_GUIDE]"));
      assert.ok(firstPrompt.includes('otter project create "<name>"'));
      assert.ok(firstPrompt.includes("otter issue ask <issue-id|number>"));
      assert.ok(firstPrompt.includes("otter knowledge list"));
      assert.ok(firstPrompt.includes("questionnaire primitive"));
      assert.ok(firstPrompt.includes('Never self-identify as "Chameleon"'));
      assert.ok(firstPrompt.includes("always include a clickable UI jump link"));
      assert.ok(firstPrompt.includes("`/projects/<project-id>/issues/<issue-id>`"));

      const secondPrompt = await formatSessionSystemPromptForTest(sessionKey, "next turn");
      assert.ok(secondPrompt.includes("[OTTERCAMP_CONTEXT_REMINDER]"));
      assert.ok(secondPrompt.includes(reminderGuidePointer));
      assert.equal(secondPrompt.includes("[OTTERCAMP_OPERATING_GUIDE]"), false);
      assert.equal(secondPrompt.includes("[OTTERCAMP_OPERATING_GUIDE_REMINDER]"), false);
    } finally {
      if (originalConfigPath === undefined) {
        delete process.env.OPENCLAW_CONFIG_PATH;
      } else {
        process.env.OPENCLAW_CONFIG_PATH = originalConfigPath;
      }
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("injects the OtterCamp guide for elephant sessions too", async () => {
    const originalConfigPath = process.env.OPENCLAW_CONFIG_PATH;
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "oc-guide-elephant-"));
    const workspacePath = path.join(tempRoot, "workspace-chameleon");
    const configPath = path.join(tempRoot, "openclaw.json");
    const elephantSessionKey = "agent:elephant:main";

    fs.writeFileSync(
      configPath,
      `${JSON.stringify({ agents: { chameleon: { workspace: workspacePath } } }, null, 2)}\n`,
      "utf8",
    );
    process.env.OPENCLAW_CONFIG_PATH = configPath;

    try {
      global.fetch = (async () => new Response(
        JSON.stringify({
          profile: "compact",
          agent: {
            id: "elephant",
            name: "Elephant",
            role: "Memory Archivist",
          },
          soul_summary: "Signal over noise.",
          identity_summary: "Organization memory specialist.",
          instructions_summary: "Extract durable context and share only high-signal entries.",
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      )) as typeof fetch;

      const installedPath = ensureChameleonWorkspaceGuideInstalledForTest();
      assert.equal(installedPath, path.join(workspacePath, "OTTERCAMP.md"));
      assert.equal(fs.existsSync(installedPath), true);

      setSessionContextForTest(elephantSessionKey, {
        kind: "dm",
        orgID: "org-1",
        threadID: "dm_elephant",
        agentID: "elephant",
      });

      const firstPrompt = await formatSessionSystemPromptForTest(elephantSessionKey, "create issue");
      assert.ok(firstPrompt.includes("[OTTERCAMP_OPERATING_GUIDE]"));
      assert.ok(firstPrompt.includes("otter issue create"));
      assert.ok(firstPrompt.includes('Never self-identify as "Chameleon"'));
      assert.ok(firstPrompt.includes("always include a clickable UI jump link"));
      assert.ok(firstPrompt.includes("`/projects/<project-id>/issues/<issue-id>`"));

      const secondPrompt = await formatSessionSystemPromptForTest(elephantSessionKey, "next turn");
      assert.ok(secondPrompt.includes("[OTTERCAMP_CONTEXT_REMINDER]"));
      assert.ok(secondPrompt.includes(reminderGuidePointer));
      assert.equal(secondPrompt.includes("[OTTERCAMP_OPERATING_GUIDE]"), false);
      assert.equal(secondPrompt.includes("[OTTERCAMP_OPERATING_GUIDE_REMINDER]"), false);
    } finally {
      if (originalConfigPath === undefined) {
        delete process.env.OPENCLAW_CONFIG_PATH;
      } else {
        process.env.OPENCLAW_CONFIG_PATH = originalConfigPath;
      }
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("syncs otter CLI auth config into the chameleon workspace home paths", () => {
    const originalConfigPath = process.env.OPENCLAW_CONFIG_PATH;
    const originalHome = process.env.HOME;
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "oc-cli-config-sync-"));
    const hostHome = path.join(tempRoot, "host-home");
    const workspacePath = path.join(tempRoot, "workspace-chameleon");
    const configPath = path.join(tempRoot, "openclaw.json");

    fs.mkdirSync(path.join(hostHome, "Library", "Application Support", "otter"), { recursive: true });
    fs.writeFileSync(
      path.join(hostHome, "Library", "Application Support", "otter", "config.json"),
      `${JSON.stringify({
        apiBaseUrl: "http://localhost:4200",
        token: "oc_local_test_token",
        defaultOrg: "146ca0fd-cf4c-4ed8-9f54-552d862e9a51",
      }, null, 2)}\n`,
      "utf8",
    );
    fs.writeFileSync(
      configPath,
      `${JSON.stringify({ agents: { chameleon: { workspace: workspacePath } } }, null, 2)}\n`,
      "utf8",
    );
    process.env.HOME = hostHome;
    process.env.OPENCLAW_CONFIG_PATH = configPath;

    try {
      const updated = ensureChameleonWorkspaceOtterCLIConfigInstalledForTest();
      assert.ok(updated.length >= 1);

      const workspaceDarwinPath = path.join(
        workspacePath,
        "Library",
        "Application Support",
        "otter",
        "config.json",
      );
      const workspaceUnixPath = path.join(workspacePath, ".config", "otter", "config.json");
      assert.equal(fs.existsSync(workspaceDarwinPath), true);
      assert.equal(fs.existsSync(workspaceUnixPath), true);

      const darwinConfig = JSON.parse(fs.readFileSync(workspaceDarwinPath, "utf8")) as Record<string, unknown>;
      assert.equal(darwinConfig.token, "oc_local_test_token");
      assert.equal(darwinConfig.defaultOrg, "146ca0fd-cf4c-4ed8-9f54-552d862e9a51");
    } finally {
      if (originalConfigPath === undefined) {
        delete process.env.OPENCLAW_CONFIG_PATH;
      } else {
        process.env.OPENCLAW_CONFIG_PATH = originalConfigPath;
      }
      if (originalHome === undefined) {
        delete process.env.HOME;
      } else {
        process.env.HOME = originalHome;
      }
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
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
    assert.ok(contextual.includes("- enforcement: hard runtime deny (mutation tool calls are aborted)"));
    assert.equal(contextual.includes("policy-level only"), false);
    assert.equal(contextual.includes("TODO: enforce"), false);
    assert.ok(contextual.includes("- workspaceAccess: none"));
    assert.ok(contextual.includes("include a clickable UI jump link"));
    assert.ok(contextual.includes("`/projects/<project-id>/issues/<issue-id>`"));
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
      assert.ok(contextual.includes("- enforcement: hard runtime guard (tool-event interception + path/symlink validation)"));
      assert.ok(contextual.includes("- security: path traversal and symlink escape are blocked at runtime"));
      assert.equal(contextual.includes("policy-level only"), false);
      assert.equal(contextual.includes("TODO: enforce"), false);
      assert.ok(contextual.includes(`- Project jump link template: \`/projects/${projectID}\`.`));
      assert.ok(contextual.includes(`- Issue jump link template: \`/projects/${projectID}/issues/<issue-id>\`.`));
      assert.ok(contextual.includes("- After creating/updating an issue, include a direct jump link in your reply."));

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

describe("bridge mutation target extraction + validation helpers", () => {
  it("extracts mutation targets across write/edit/apply_patch arg shapes", () => {
    const writeTargets = extractMutationToolTargetPathsForTest("write", {
      path: "notes/today.md",
      content: "hello",
    });
    assert.deepEqual(writeTargets, ["notes/today.md"]);

    const editTargets = extractMutationToolTargetPathsForTest("edit", {
      file_path: "src/app.ts",
      old_string: "before",
      new_string: "after",
    });
    assert.deepEqual(editTargets, ["src/app.ts"]);

    const applyPatchTargets = extractMutationToolTargetPathsForTest("apply_patch", {
      patch: [
        "*** Begin Patch",
        "*** Update File: web/src/main.tsx",
        "*** Add File: docs/notes.md",
        "*** Move to: web/src/app.tsx",
        "*** End Patch",
      ].join("\n"),
    });
    assert.deepEqual(applyPatchTargets, ["web/src/main.tsx", "docs/notes.md", "web/src/app.tsx"]);
  });

  it("validates mutation targets against project root and rejects traversal/symlink escapes", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-targets-"));
    const projectRoot = path.join(tempRoot, "project");
    const outsideRoot = path.join(tempRoot, "outside");
    fs.mkdirSync(projectRoot, { recursive: true });
    fs.mkdirSync(outsideRoot, { recursive: true });
    fs.symlinkSync(outsideRoot, path.join(projectRoot, "linked-outside"));

    try {
      const allowed = await validateMutationToolTargetsWithinProjectRootForTest(
        projectRoot,
        "write",
        { path: "notes/today.md" },
      );
      assert.equal(allowed.allowed, true);
      assert.deepEqual(allowed.invalidTargets, []);

      const traversalBlocked = await validateMutationToolTargetsWithinProjectRootForTest(
        projectRoot,
        "write",
        { path: "../outside/secret.md" },
      );
      assert.equal(traversalBlocked.allowed, false);
      assert.deepEqual(traversalBlocked.invalidTargets, ["../outside/secret.md"]);

      const symlinkBlocked = await validateMutationToolTargetsWithinProjectRootForTest(
        projectRoot,
        "write",
        { path: "linked-outside/secret.md" },
      );
      assert.equal(symlinkBlocked.allowed, false);
      assert.deepEqual(symlinkBlocked.invalidTargets, ["linked-outside/secret.md"]);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });
});

describe("bridge runtime mutation enforcement", () => {
  const abortCalls: Array<{ sessionKey: string; runId?: string; toolCallId?: string; reason: string }> = [];

  beforeEach(() => {
    resetSessionContextsForTest();
    resetMutationEnforcementStateForTest();
    abortCalls.length = 0;
    setMutationAbortForTest(async (request) => {
      abortCalls.push({
        sessionKey: request.sessionKey,
        runId: request.runId,
        ...(request.toolCallId ? { toolCallId: request.toolCallId } : {}),
        reason: request.reason,
      });
    });
    setPathWithinProjectRootForTest(null);
  });

  afterEach(() => {
    setMutationAbortForTest(null);
    setPathWithinProjectRootForTest(null);
  });

  it("aborts mutation tool runs in conversation mode", async () => {
    const sessionKey = "agent:main:slack";
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm-1",
      agentID: "main",
    });

    await handleOpenClawEventForTest({
      event: "agent",
      payload: {
        stream: "tool",
        phase: "start",
        sessionKey,
        runId: "run-conversation-write",
        tool: "write",
        args: { path: "notes/today.md", content: "hello" },
      },
    });

    const enforcement = getMutationEnforcementStateForTest();
    assert.equal(enforcement.count, 1);
    assert.equal(enforcement.last?.blocked, true);
    assert.match(enforcement.last?.reason || "", /conversation mode/i);
    assert.deepEqual(abortCalls, [
      {
        sessionKey,
        runId: "run-conversation-write",
        reason: "mutation denied: conversation mode requires project_id context",
      },
    ]);
  });

  it("forwards toolCallId in abort payload when provided", async () => {
    const sessionKey = "agent:main:slack";
    setSessionContextForTest(sessionKey, {
      kind: "dm",
      orgID: "org-1",
      threadID: "dm-1",
      agentID: "main",
    });

    await handleOpenClawEventForTest({
      event: "agent",
      payload: {
        stream: "tool",
        phase: "start",
        sessionKey,
        runId: "run-toolcall",
        toolCallId: "toolcall-123",
        tool: "write",
        args: { path: "notes/today.md", content: "hello" },
      },
    });

    assert.deepEqual(abortCalls, [
      {
        sessionKey,
        runId: "run-toolcall",
        toolCallId: "toolcall-123",
        reason: "mutation denied: conversation mode requires project_id context",
      },
    ]);
  });

  it("aborts project-mode mutation tools when target paths are missing", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-missing-"));
    const projectRoot = path.join(tempRoot, "project");
    fs.mkdirSync(projectRoot, { recursive: true });

    const sessionKey = "agent:main:project:99999999-aaaa-bbbb-cccc-dddddddddddd";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "99999999-aaaa-bbbb-cccc-dddddddddddd",
      agentID: "main",
      projectRoot,
    });

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-missing-targets",
          tool: "write",
          args: {},
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.count, 1);
      assert.equal(enforcement.last?.blocked, true);
      assert.match(enforcement.last?.reason || "", /missing target path/i);
      assert.deepEqual(abortCalls, [
        {
          sessionKey,
          runId: "run-missing-targets",
          reason: "mutation denied: missing target path(s) in tool args",
        },
      ]);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("blocks null-byte target paths and denies the mutation", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-null-"));
    const projectRoot = path.join(tempRoot, "project");
    fs.mkdirSync(projectRoot, { recursive: true });

    const sessionKey = "agent:main:project:12121212-3434-5656-7878-909090909090";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "12121212-3434-5656-7878-909090909090",
      agentID: "main",
      projectRoot,
    });

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-null-byte-target",
          tool: "write",
          args: { path: "file\u0000../../etc/passwd", content: "x" },
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.last?.blocked, true);
      assert.match(enforcement.last?.reason || "", /missing target path/i);
      assert.deepEqual(abortCalls, [
        {
          sessionKey,
          runId: "run-null-byte-target",
          reason: "mutation denied: missing target path(s) in tool args",
        },
      ]);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("allows in-root project mutations and passes through non-mutation tools", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-allow-"));
    const projectRoot = path.join(tempRoot, "project");
    fs.mkdirSync(projectRoot, { recursive: true });

    const sessionKey = "agent:main:project:11111111-2222-3333-4444-555555555555";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "11111111-2222-3333-4444-555555555555",
      agentID: "main",
      projectRoot,
    });

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-project-write",
          tool: "write",
          args: { path: "notes/today.md", content: "ok" },
        },
      });

      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-non-mutation",
          tool: "search",
          args: { query: "find me" },
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.count, 2);
      assert.equal(enforcement.last?.blocked, false);
      assert.deepEqual(abortCalls, []);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("aborts edit/apply_patch mutation runs when targets escape project root", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-block-"));
    const projectRoot = path.join(tempRoot, "project");
    const outsideRoot = path.join(tempRoot, "outside");
    fs.mkdirSync(projectRoot, { recursive: true });
    fs.mkdirSync(outsideRoot, { recursive: true });
    fs.symlinkSync(outsideRoot, path.join(projectRoot, "linked-outside"));

    const sessionKey = "agent:main:project:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      agentID: "main",
      projectRoot,
    });

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-edit-traversal",
          tool: "edit",
          args: { file_path: "../outside/secret.md", old_string: "x", new_string: "y" },
        },
      });

      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-patch-symlink",
          tool: "apply_patch",
          args: {
            patch: [
              "*** Begin Patch",
              "*** Update File: linked-outside/secret.md",
              "*** End Patch",
            ].join("\n"),
          },
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.count, 2);
      assert.equal(enforcement.last?.blocked, true);
      assert.match(enforcement.last?.reason || "", /write_guard_root|target path escapes/i);
      assert.deepEqual(abortCalls, [
        {
          sessionKey,
          runId: "run-edit-traversal",
          reason: "mutation denied: target path escapes active project write_guard_root",
        },
        {
          sessionKey,
          runId: "run-patch-symlink",
          reason: "mutation denied: target path escapes active project write_guard_root",
        },
      ]);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("aborts apply_patch runs when unified diff targets escape project root", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-unified-"));
    const projectRoot = path.join(tempRoot, "project");
    fs.mkdirSync(projectRoot, { recursive: true });

    const sessionKey = "agent:main:project:67676767-1111-2222-3333-444444444444";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "67676767-1111-2222-3333-444444444444",
      agentID: "main",
      projectRoot,
    });

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-unified-diff-escape",
          tool: "apply_patch",
          args: {
            patch: [
              "--- a/docs/notes.md",
              "+++ b/../outside/secret.md",
              "@@ -1 +1 @@",
              "-before",
              "+after",
            ].join("\n"),
          },
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.last?.blocked, true);
      assert.match(enforcement.last?.reason || "", /write_guard_root|escapes/i);
      assert.deepEqual(abortCalls, [
        {
          sessionKey,
          runId: "run-unified-diff-escape",
          reason: "mutation denied: target path escapes active project write_guard_root",
        },
      ]);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it("fails closed and aborts when path validation throws unexpectedly", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-throw-"));
    const projectRoot = path.join(tempRoot, "project");
    fs.mkdirSync(projectRoot, { recursive: true });

    const sessionKey = "agent:main:project:78787878-aaaa-bbbb-cccc-dddddddddddd";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "78787878-aaaa-bbbb-cccc-dddddddddddd",
      agentID: "main",
      projectRoot,
    });

    setPathWithinProjectRootForTest(async () => {
      throw new Error("simulated validation failure");
    });

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-validation-throws",
          tool: "write",
          args: { path: "notes/today.md", content: "x" },
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.last?.blocked, true);
      assert.match(enforcement.last?.reason || "", /enforcement error/i);
      assert.deepEqual(abortCalls, [
        {
          sessionKey,
          runId: "run-validation-throws",
          reason: "mutation denied: enforcement error while validating target paths",
        },
      ]);
    } finally {
      fs.rmSync(tempRoot, { recursive: true, force: true });
      setPathWithinProjectRootForTest(null);
    }
  });

  it("fails closed when lstat raises non-ENOENT errors during symlink checks", async () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "otter-mutation-enforce-lstat-"));
    const projectRoot = path.join(tempRoot, "project");
    fs.mkdirSync(path.join(projectRoot, "trigger-eacces"), { recursive: true });

    const sessionKey = "agent:main:project:89898989-aaaa-bbbb-cccc-eeeeeeeeeeee";
    setSessionContextForTest(sessionKey, {
      kind: "project_chat",
      orgID: "org-1",
      projectID: "89898989-aaaa-bbbb-cccc-eeeeeeeeeeee",
      agentID: "main",
      projectRoot,
    });

    const originalLstat = fs.promises.lstat.bind(fs.promises);
    const lstatAny = originalLstat as unknown as (...args: unknown[]) => Promise<unknown>;
    (fs.promises as unknown as { lstat: typeof fs.promises.lstat }).lstat = async (...args: unknown[]) => {
      const targetPath = args[0];
      if (String(targetPath).includes("trigger-eacces")) {
        const err = new Error("permission denied") as NodeJS.ErrnoException;
        err.code = "EACCES";
        throw err;
      }
      return lstatAny(...args);
    };

    try {
      await handleOpenClawEventForTest({
        event: "agent",
        payload: {
          stream: "tool",
          phase: "start",
          sessionKey,
          runId: "run-lstat-eacces",
          tool: "write",
          args: { path: "trigger-eacces/file.txt", content: "x" },
        },
      });

      const enforcement = getMutationEnforcementStateForTest();
      assert.equal(enforcement.last?.blocked, true);
      assert.match(enforcement.last?.reason || "", /enforcement error/i);
      assert.deepEqual(abortCalls, [
        {
          sessionKey,
          runId: "run-lstat-eacces",
          reason: "mutation denied: enforcement error while validating target paths",
        },
      ]);
    } finally {
      (fs.promises as unknown as { lstat: typeof fs.promises.lstat }).lstat = originalLstat;
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

describe("bridge memory extraction dispatch helpers", () => {
  const sentMessages: Record<string, unknown>[] = [];

  beforeEach(() => {
    sentMessages.length = 0;
    setOtterCampSocketForTest({
      readyState: 1,
      send: (payload: string) => {
        sentMessages.push(JSON.parse(payload) as Record<string, unknown>);
      },
    });
  });

  afterEach(() => {
    setExecFileForTest(null);
    setOtterCampSocketForTest(null);
  });

  it("executes memory.extract.request and sends success response", async () => {
    setExecFileForTest((cmd, args, _options, callback) => {
      assert.equal(cmd, "openclaw");
      assert.equal(args[0], "gateway");
      assert.equal(args[1], "call");
      assert.equal(args[2], "agent");
      assert.ok(args.includes("--json"));
      callback(
        null,
        '{"runId":"trace-1","status":"ok","result":{"payloads":[{"text":"{\\"candidates\\":[]}"}],"meta":{"agentMeta":{"model":"claude-haiku-4-5"},"systemPromptReport":{"chars":99999}}}}',
        "",
      );
    });

    await dispatchInboundEventForTest("memory.extract.request", {
      type: "memory.extract.request",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        request_id: "req-1",
        args: ["gateway", "call", "agent", "--json"],
      },
    });

    assert.equal(sentMessages.length, 1);
    assert.equal(sentMessages[0]?.type, "memory.extract.response");
    const data = sentMessages[0]?.data as Record<string, unknown>;
    assert.equal(data.request_id, "req-1");
    assert.equal(data.ok, true);
    const output = JSON.parse(String(data.output || "{}")) as Record<string, unknown>;
    assert.equal(output.runId, "trace-1");
    assert.equal(output.status, "ok");
    assert.deepEqual(
      (output.result as Record<string, unknown>)?.payloads,
      [{ text: '{"candidates":[]}' }],
    );
    assert.equal(
      ((output.result as Record<string, unknown>)?.meta as Record<string, unknown>)?.agentMeta
        ? ((output.result as Record<string, unknown>)?.meta as Record<string, unknown>).agentMeta.model
        : undefined,
      "claude-haiku-4-5",
    );
    assert.equal(String(data.output || "").includes("systemPromptReport"), false);
  });

  it("rejects unsupported memory.extract.request commands with error response", async () => {
    await dispatchInboundEventForTest("memory.extract.request", {
      type: "memory.extract.request",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        request_id: "req-2",
        args: ["chat", "send"],
      },
    });

    assert.equal(sentMessages.length, 1);
    const data = sentMessages[0]?.data as Record<string, unknown>;
    assert.equal(data.request_id, "req-2");
    assert.equal(data.ok, false);
    assert.match(String(data.error || ""), /requires args beginning/);
  });

  it("injects gateway token when credentials are missing", () => {
    const args = ensureGatewayCallCredentials(
      ["gateway", "call", "agent", "--json"],
      "token-123",
    );
    assert.deepEqual(args, ["gateway", "call", "agent", "--json", "--token", "token-123"]);
  });

  it("preserves explicit gateway credentials", () => {
    const withToken = ensureGatewayCallCredentials(
      ["gateway", "call", "agent", "--json", "--token", "token-abc"],
      "token-123",
    );
    assert.deepEqual(withToken, ["gateway", "call", "agent", "--json", "--token", "token-abc"]);

    const withPassword = ensureGatewayCallCredentials(
      ["gateway", "call", "agent", "--json", "--password", "secret"],
      "token-123",
    );
    assert.deepEqual(withPassword, ["gateway", "call", "agent", "--json", "--password", "secret"]);
  });

  it("resolves gateway command timeout from --timeout with safety headroom", () => {
    assert.equal(
      resolveOpenClawCommandTimeoutMSForTest(["gateway", "call", "agent", "--json"]),
      60000,
    );
    assert.equal(
      resolveOpenClawCommandTimeoutMSForTest(["gateway", "call", "agent", "--timeout", "90000"]),
      105000,
    );
    assert.equal(
      resolveOpenClawCommandTimeoutMSForTest(["gateway", "call", "agent", "--timeout", "9999999"]),
      300000,
    );
  });

  it("passes computed timeout to openclaw command execution", async () => {
    let capturedTimeout: number | undefined;
    setExecFileForTest((_cmd, _args, options, callback) => {
      capturedTimeout = options?.timeout;
      callback(
        null,
        '{"runId":"trace-timeout","status":"ok","result":{"payloads":[{"text":"{\\"candidates\\":[]}"}],"meta":{"agentMeta":{"model":"claude-haiku-4-5"}}}}',
        "",
      );
    });

    await dispatchInboundEventForTest("memory.extract.request", {
      type: "memory.extract.request",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        request_id: "req-timeout-1",
        args: ["gateway", "call", "agent", "--json", "--timeout", "90000"],
      },
    });

    assert.equal(capturedTimeout, 105000);
  });

  it("strips explicit --url overrides for bridge-side gateway execution", () => {
    const args = ensureGatewayCallCredentials(
      ["gateway", "call", "agent", "--json", "--url", "ws://127.0.0.1:18791"],
      "token-123",
    );
    assert.deepEqual(args, ["gateway", "call", "agent", "--json", "--token", "token-123"]);
  });

  it("compacts oversized gateway output payloads", () => {
    const compacted = compactMemoryExtractOutput(
      '{"runId":"trace-2","status":"ok","result":{"payloads":[{"text":"{\\"candidates\\":[{\\"kind\\":\\"fact\\"}]}"}],"meta":{"agentMeta":{"model":"claude-haiku-4-5"},"systemPromptReport":{"chars":999999}}}}',
    );
    assert.equal(compacted.includes("systemPromptReport"), false);
    const parsed = JSON.parse(compacted) as Record<string, unknown>;
    assert.equal(parsed.runId, "trace-2");
    assert.equal(parsed.status, "ok");
  });

  it("truncates oversized payload text for websocket safety", () => {
    const longText = "a".repeat(20000);
    const compacted = compactMemoryExtractOutput(
      `{"runId":"trace-3","status":"ok","result":{"payloads":[{"text":"${longText}"}],"meta":{"agentMeta":{"model":"claude-haiku-4-5"}}}}`,
    );
    const parsed = JSON.parse(compacted) as Record<string, unknown>;
    const payloads = ((parsed.result as Record<string, unknown>).payloads ?? []) as Array<Record<string, unknown>>;
    assert.equal(String(payloads[0]?.text || "").length, 8000);
  });

  it("replaces oversized json payload text with an empty candidates envelope", () => {
    const hugeJsonPayload = `{"candidates":[{"kind":"fact","title":"${"t".repeat(12000)}"}]}`;
    const compacted = compactMemoryExtractOutput(
      `{"runId":"trace-3b","status":"ok","result":{"payloads":[{"text":"${hugeJsonPayload.replace(/"/g, '\\"')}"}],"meta":{"agentMeta":{"model":"claude-haiku-4-5"}}}}`,
    );
    const parsed = JSON.parse(compacted) as Record<string, unknown>;
    const payloads = ((parsed.result as Record<string, unknown>).payloads ?? []) as Array<Record<string, unknown>>;
    assert.equal(String(payloads[0]?.text || ""), '{"candidates":[]}');
  });

  it("extracts a compact response from noisy command output", () => {
    const noisyOutput = `warning: retrying\n{"runId":"trace-4","status":"ok","result":{"payloads":[{"text":"{\\"candidates\\":[]}"}],"meta":{"agentMeta":{"model":"claude-haiku-4-5"}}}}`;
    const compacted = compactMemoryExtractOutput(noisyOutput);
    const parsed = JSON.parse(compacted) as Record<string, unknown>;
    assert.equal(parsed.runId, "trace-4");
    assert.equal(parsed.status, "ok");
  });

  it("falls back to a minimal compact envelope when output is not json", () => {
    const compacted = compactMemoryExtractOutput("bridge output unavailable");
    const parsed = JSON.parse(compacted) as Record<string, unknown>;
    assert.equal(parsed.status, "ok");
    const payloads = ((parsed.result as Record<string, unknown>).payloads ?? []) as Array<Record<string, unknown>>;
    assert.equal(payloads.length, 1);
    assert.equal(String(payloads[0]?.text || "").includes('"candidates"'), true);
  });

  it("sanitizes oversized command errors before sending memory.extract.response", async () => {
    const hugeError = `Command failed: openclaw gateway call agent --params ${"x".repeat(12000)}`;
    setExecFileForTest((_cmd, _args, _options, callback) => {
      callback(new Error(hugeError));
    });

    await dispatchInboundEventForTest("memory.extract.request", {
      type: "memory.extract.request",
      org_id: "00000000-0000-0000-0000-000000000123",
      data: {
        request_id: "req-oversized-error",
        args: ["gateway", "call", "agent", "--json"],
      },
    });

    assert.equal(sentMessages.length, 1);
    const data = sentMessages[0]?.data as Record<string, unknown>;
    assert.equal(data.ok, false);
    assert.equal(String(data.error || "").length <= 1003, true);
  });
});
