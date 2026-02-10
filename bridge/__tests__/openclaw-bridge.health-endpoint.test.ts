// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  buildHealthPayload,
  type BridgeConnectionHealthInput,
} from "../openclaw-bridge";

const BASE_INPUT: BridgeConnectionHealthInput = {
  uptimeSeconds: 3600,
  queueDepth: 0,
  lastSuccessfulSyncAtMs: Date.parse("2026-02-08T19:58:00.000Z"),
  openclaw: {
    connected: true,
    lastConnectedAtMs: Date.parse("2026-02-08T20:00:00.000Z"),
    disconnectedSinceMs: 0,
    consecutiveFailures: 0,
    totalReconnectAttempts: 2,
  },
  ottercamp: {
    connected: true,
    lastConnectedAtMs: Date.parse("2026-02-08T20:00:01.000Z"),
    disconnectedSinceMs: 0,
    consecutiveFailures: 1,
    totalReconnectAttempts: 3,
  },
};

describe("bridge health payload", () => {
  it("returns healthy status when both sockets are connected", () => {
    const payload = buildHealthPayload(BASE_INPUT);
    expect(payload.status).toBe("healthy");
    expect(payload.queueDepth).toBe(0);
    expect(payload.uptime).toBe("1h");
    expect(payload.lastSuccessfulSync).toBe("2026-02-08T19:58:00.000Z");
    expect(payload.openclaw.connected).toBe(true);
    expect(payload.ottercamp.connected).toBe(true);
    expect(payload.openclaw.lastConnectedAt).toBe("2026-02-08T20:00:00.000Z");
    expect(payload.openclaw.disconnectedSince).toBeNull();
    expect(payload.openclaw.consecutiveFailures).toBe(0);
    expect(payload.openclaw.totalReconnectAttempts).toBe(2);
  });

  it("returns degraded status when only one side is connected", () => {
    const payload = buildHealthPayload({
      ...BASE_INPUT,
      ottercamp: {
        ...BASE_INPUT.ottercamp,
        connected: false,
        disconnectedSinceMs: Date.parse("2026-02-08T20:01:00.000Z"),
        consecutiveFailures: 7,
      },
    });
    expect(payload.status).toBe("degraded");
    expect(payload.ottercamp.disconnectedSince).toBe("2026-02-08T20:01:00.000Z");
    expect(payload.ottercamp.consecutiveFailures).toBe(7);
  });

  it("renders null disconnectedSince when disconnectedSinceMs is zero", () => {
    const payload = buildHealthPayload({
      ...BASE_INPUT,
      openclaw: {
        ...BASE_INPUT.openclaw,
        connected: false,
        disconnectedSinceMs: 0,
      },
    });

    expect(payload.status).toBe("degraded");
    expect(payload.openclaw.disconnectedSince).toBeNull();
  });

  it("returns disconnected when both sides are disconnected", () => {
    const payload = buildHealthPayload({
      ...BASE_INPUT,
      openclaw: {
        ...BASE_INPUT.openclaw,
        connected: false,
        disconnectedSinceMs: Date.parse("2026-02-08T20:00:30.000Z"),
      },
      ottercamp: {
        ...BASE_INPUT.ottercamp,
        connected: false,
        disconnectedSinceMs: Date.parse("2026-02-08T20:00:45.000Z"),
      },
      queueDepth: 14,
    });
    expect(payload.status).toBe("disconnected");
    expect(payload.queueDepth).toBe(14);
  });
});
