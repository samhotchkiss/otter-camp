// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  nextMissedPongCount,
  shouldForceReconnectFromHeartbeat,
  transitionConnectionState,
  type BridgeConnectionState,
} from "../openclaw-bridge";

describe("bridge heartbeat policy", () => {
  it("increments and resets missed pong counts", () => {
    expect(nextMissedPongCount(0, false)).toBe(1);
    expect(nextMissedPongCount(1, false)).toBe(2);
    expect(nextMissedPongCount(2, false)).toBe(3);
    expect(nextMissedPongCount(3, true)).toBe(0);
  });

  it("forces reconnect after two consecutive missed pongs", () => {
    expect(shouldForceReconnectFromHeartbeat(0)).toBe(false);
    expect(shouldForceReconnectFromHeartbeat(1)).toBe(false);
    expect(shouldForceReconnectFromHeartbeat(2)).toBe(true);
    expect(shouldForceReconnectFromHeartbeat(3)).toBe(true);
  });

  it("uses degraded/disconnected transitions for heartbeat warnings", () => {
    let state: BridgeConnectionState = "connected";
    state = transitionConnectionState(state, "health_warning");
    expect(state).toBe("degraded");
    state = transitionConnectionState(state, "heartbeat_missed");
    expect(state).toBe("disconnected");
  });
});
