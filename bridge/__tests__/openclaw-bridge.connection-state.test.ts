// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  computeReconnectDelayMs,
  getReconnectStateForTest,
  reconnectEscalationTierForFailures,
  resetReconnectStateForTest,
  setContinuousModeEnabledForTest,
  setProcessExitForTest,
  shouldExitAfterReconnectFailures,
  triggerOpenClawCloseForTest,
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
    setContinuousModeEnabledForTest(true);
    setProcessExitForTest(null);
  });

  afterEach(() => {
    setProcessExitForTest(null);
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
});
