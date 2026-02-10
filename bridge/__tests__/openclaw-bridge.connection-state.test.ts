// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  computeReconnectDelayMs,
  getBufferedActivityEventStateForTest,
  getReconnectStateForTest,
  reconnectEscalationTierForFailures,
  resetBufferedActivityEventsForTest,
  resetReconnectStateForTest,
  setConnectionStateForTest,
  setContinuousModeEnabledForTest,
  setOtterCampOrgIDForTest,
  setProcessExitForTest,
  shouldExitAfterReconnectFailures,
  triggerOpenClawCloseForTest,
  triggerOtterCampCloseForTest,
  triggerSocketMessageForTest,
  transitionConnectionState,
  type BridgeConnectionState,
  type BridgeConnectionTransitionTrigger,
} from "../openclaw-bridge";

describe("bridge connection state + reconnect policy", () => {
  beforeEach(() => {
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(console, "error").mockImplementation(() => {});
    resetReconnectStateForTest("openclaw");
    resetReconnectStateForTest("ottercamp");
    resetBufferedActivityEventsForTest();
    setContinuousModeEnabledForTest(true);
    setProcessExitForTest(null);
    setConnectionStateForTest("ottercamp", "disconnected");
    setOtterCampOrgIDForTest(null);
  });

  afterEach(() => {
    setProcessExitForTest(null);
    setConnectionStateForTest("ottercamp", "disconnected");
    setOtterCampOrgIDForTest(null);
    vi.restoreAllMocks();
  });

  it("computes exponential reconnect delays with jitter and max cap", () => {
    expect(computeReconnectDelayMs(0, () => 0)).toBe(800);
    expect(computeReconnectDelayMs(1, () => 0.5)).toBe(2000);
    expect(computeReconnectDelayMs(4, () => 1)).toBe(19200);
    expect(computeReconnectDelayMs(5, () => 1)).toBe(30000);
    expect(computeReconnectDelayMs(8, () => 0.5)).toBe(30000);
  });

  it("marks process-exit threshold after sixty consecutive reconnect failures", () => {
    expect(shouldExitAfterReconnectFailures(0)).toBe(false);
    expect(shouldExitAfterReconnectFailures(59)).toBe(false);
    expect(shouldExitAfterReconnectFailures(60)).toBe(true);
    expect(shouldExitAfterReconnectFailures(61)).toBe(true);
  });

  it("classifies reconnect escalation tiers at warning/alert/restart thresholds", () => {
    expect(reconnectEscalationTierForFailures(0)).toBe("none");
    expect(reconnectEscalationTierForFailures(4)).toBe("none");
    expect(reconnectEscalationTierForFailures(5)).toBe("warn");
    expect(reconnectEscalationTierForFailures(29)).toBe("warn");
    expect(reconnectEscalationTierForFailures(30)).toBe("alert");
    expect(reconnectEscalationTierForFailures(59)).toBe("alert");
    expect(reconnectEscalationTierForFailures(60)).toBe("restart");
  });

  it("transitions across connecting/connected/degraded/disconnected/reconnecting states", () => {
    let state: BridgeConnectionState = "disconnected";
    const apply = (trigger: BridgeConnectionTransitionTrigger) => {
      state = transitionConnectionState(state, trigger);
      return state;
    };

    expect(apply("connect_attempt")).toBe("connecting");
    expect(apply("socket_open")).toBe("connected");
    expect(apply("health_warning")).toBe("degraded");
    expect(apply("heartbeat_missed")).toBe("disconnected");
    expect(apply("reconnect_scheduled")).toBe("reconnecting");
    expect(apply("socket_open")).toBe("connected");
  });

  it("invokes process exit hook at restart tier and escalates to hard exit after repeated hook failures", () => {
    const reconnectFn = vi.fn();
    const restartHook = vi
      .fn<(code: number) => never>()
      .mockImplementationOnce(() => {
        throw new Error("restart-failed-1");
      })
      .mockImplementationOnce(() => {
        throw new Error("restart-failed-2");
      });
    setProcessExitForTest(restartHook);

    const hardExit = vi
      .spyOn(process, "exit")
      .mockImplementation((() => {
        throw new Error("hard-exit");
      }) as (code?: string | number | null | undefined) => never);

    for (let attempt = 0; attempt < 61; attempt += 1) {
      try {
        triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);
      } catch (err) {
        if ((err as Error).message !== "hard-exit") {
          throw err;
        }
      }
    }

    expect(restartHook).toHaveBeenCalledTimes(2);
    expect(restartHook).toHaveBeenNthCalledWith(1, 1);
    expect(restartHook).toHaveBeenNthCalledWith(2, 1);
    expect(getReconnectStateForTest("openclaw").restartFailures).toBe(2);
    expect(hardExit).toHaveBeenCalledTimes(1);
    expect(hardExit).toHaveBeenCalledWith(1);
  });

  it("schedules reconnect after a single restart hook failure below exit threshold", () => {
    vi.useFakeTimers();
    const reconnectFn = vi.fn();
    const restartHook = vi.fn<(code: number) => never>().mockImplementation(() => {
      throw new Error("restart-failed-once");
    });
    setProcessExitForTest(restartHook);

    for (let attempt = 0; attempt < 59; attempt += 1) {
      triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);
      vi.advanceTimersToNextTimer();
    }

    expect(getReconnectStateForTest("openclaw").hasReconnectTimer).toBe(false);

    triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);

    const reconnectState = getReconnectStateForTest("openclaw");
    expect(restartHook).toHaveBeenCalledTimes(1);
    expect(reconnectState.restartFailures).toBe(1);
    expect(reconnectState.hasReconnectTimer).toBe(true);

    vi.useRealTimers();
  });

  it("invokes restart escalation for prolonged ottercamp close failures", () => {
    const reconnectFn = vi.fn();
    const restartHook = vi.fn<(code: number) => never>().mockImplementation(() => {
      throw new Error("restart-failed-once");
    });
    setProcessExitForTest(restartHook);

    for (let attempt = 0; attempt < 60; attempt += 1) {
      triggerOtterCampCloseForTest(1006, "test-close", reconnectFn);
    }

    expect(restartHook).toHaveBeenCalledTimes(1);
    expect(getReconnectStateForTest("ottercamp").restartFailures).toBe(1);
  });

  it("queues reconnect escalation alert once at alert tier when ottercamp is connected", async () => {
    const reconnectFn = vi.fn();
    setOtterCampOrgIDForTest("org-test");
    setConnectionStateForTest("ottercamp", "connected");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false, status: 503, statusText: "Unavailable", text: async () => "fail" })),
    );

    for (let attempt = 0; attempt < 35; attempt += 1) {
      triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);
    }
    await Promise.resolve();

    const queued = getBufferedActivityEventStateForTest();
    expect(queued.queuedEventIDCount).toBe(1);
  });

  it("skips reconnect escalation alerts when ottercamp is disconnected", async () => {
    const reconnectFn = vi.fn();
    setOtterCampOrgIDForTest("org-test");
    setConnectionStateForTest("ottercamp", "disconnected");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false, status: 503, statusText: "Unavailable", text: async () => "fail" })),
    );

    for (let attempt = 0; attempt < 35; attempt += 1) {
      triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);
    }
    await Promise.resolve();

    const queued = getBufferedActivityEventStateForTest();
    expect(queued.queuedEventIDCount).toBe(0);
  });

  it("emits alert after threshold once ottercamp reconnects even if attempt 30 was skipped", async () => {
    const reconnectFn = vi.fn();
    setOtterCampOrgIDForTest("org-test");
    setConnectionStateForTest("ottercamp", "disconnected");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false, status: 503, statusText: "Unavailable", text: async () => "fail" })),
    );

    for (let attempt = 0; attempt < 35; attempt += 1) {
      triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);
    }
    await Promise.resolve();
    expect(getBufferedActivityEventStateForTest().queuedEventIDCount).toBe(0);

    setConnectionStateForTest("ottercamp", "connected");
    triggerOpenClawCloseForTest(1006, "test-close", reconnectFn);
    await Promise.resolve();

    expect(getBufferedActivityEventStateForTest().queuedEventIDCount).toBe(1);
  });

  it("covers ottercamp close alert tier behavior when ottercamp connection is unavailable", async () => {
    const reconnectFn = vi.fn();
    setOtterCampOrgIDForTest("org-test");
    setConnectionStateForTest("ottercamp", "connected");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false, status: 503, statusText: "Unavailable", text: async () => "fail" })),
    );

    for (let attempt = 0; attempt < 35; attempt += 1) {
      triggerOtterCampCloseForTest(1006, "test-close", reconnectFn);
    }
    await Promise.resolve();

    expect(getBufferedActivityEventStateForTest().queuedEventIDCount).toBe(0);
  });

  it("resets alert dedupe after reconnect and emits again on the next outage", async () => {
    vi.useFakeTimers();
    const reconnectFn = vi.fn();
    setOtterCampOrgIDForTest("org-test");
    setConnectionStateForTest("ottercamp", "connected");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ ok: false, status: 503, statusText: "Unavailable", text: async () => "fail" })),
    );

    vi.setSystemTime(new Date("2026-02-10T21:00:00.000Z"));
    for (let attempt = 0; attempt < 35; attempt += 1) {
      triggerOpenClawCloseForTest(1006, "first-outage", reconnectFn);
    }
    await Promise.resolve();
    expect(getBufferedActivityEventStateForTest().queuedEventIDCount).toBe(1);

    setConnectionStateForTest("openclaw", "connected");
    triggerSocketMessageForTest("openclaw");
    expect(getReconnectStateForTest("openclaw").alertEmittedForOutage).toBe(false);

    vi.advanceTimersByTime(1000);
    for (let attempt = 0; attempt < 35; attempt += 1) {
      triggerOpenClawCloseForTest(1006, "second-outage", reconnectFn);
    }
    await Promise.resolve();

    expect(getBufferedActivityEventStateForTest().queuedEventIDCount).toBe(2);
    vi.useRealTimers();
  });
});
