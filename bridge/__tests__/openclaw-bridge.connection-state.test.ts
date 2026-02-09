// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  computeReconnectDelayMs,
  shouldExitAfterReconnectFailures,
  transitionConnectionState,
  type BridgeConnectionState,
  type BridgeConnectionTransitionTrigger,
} from "../openclaw-bridge";

describe("bridge connection state + reconnect policy", () => {
  it("computes exponential reconnect delays with jitter and max cap", () => {
    expect(computeReconnectDelayMs(0, () => 0)).toBe(800);
    expect(computeReconnectDelayMs(1, () => 0.5)).toBe(2000);
    expect(computeReconnectDelayMs(4, () => 1)).toBe(19200);
    expect(computeReconnectDelayMs(5, () => 1)).toBe(30000);
    expect(computeReconnectDelayMs(8, () => 0.5)).toBe(30000);
  });

  it("marks process-exit threshold after five consecutive reconnect failures", () => {
    expect(shouldExitAfterReconnectFailures(0)).toBe(false);
    expect(shouldExitAfterReconnectFailures(4)).toBe(false);
    expect(shouldExitAfterReconnectFailures(5)).toBe(true);
    expect(shouldExitAfterReconnectFailures(6)).toBe(true);
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
});
