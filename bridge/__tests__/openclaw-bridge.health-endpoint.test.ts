// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  buildHealthPayload,
  type BridgeConnectionHealthInput,
} from "../openclaw-bridge";

const BASE_INPUT: BridgeConnectionHealthInput = {
  uptimeSeconds: 3600,
  queueDepth: 0,
  openclaw: {
    connected: true,
    state: "connected",
    lastMessageAtMs: Date.parse("2026-02-08T20:00:00.000Z"),
    reconnects: 0,
  },
  ottercamp: {
    connected: true,
    state: "connected",
    lastMessageAtMs: Date.parse("2026-02-08T20:00:01.000Z"),
    reconnects: 1,
  },
};

describe("bridge health payload", () => {
  it("returns healthy status when both sockets are connected", () => {
    const payload = buildHealthPayload(BASE_INPUT);
    expect(payload.status).toBe("healthy");
    expect(payload.queueDepth).toBe(0);
    expect(payload.uptime).toBe(3600);
    expect(payload.openclaw.connected).toBe(true);
    expect(payload.ottercamp.connected).toBe(true);
    expect(payload.openclaw.lastMessage).toBe("2026-02-08T20:00:00.000Z");
  });

  it("returns degraded status when a socket is degraded but connected", () => {
    const payload = buildHealthPayload({
      ...BASE_INPUT,
      ottercamp: {
        ...BASE_INPUT.ottercamp,
        state: "degraded",
      },
    });
    expect(payload.status).toBe("degraded");
  });

  it("returns unhealthy when either socket is disconnected/reconnecting", () => {
    const payload = buildHealthPayload({
      ...BASE_INPUT,
      openclaw: {
        ...BASE_INPUT.openclaw,
        connected: false,
        state: "reconnecting",
      },
      queueDepth: 14,
    });
    expect(payload.status).toBe("unhealthy");
    expect(payload.queueDepth).toBe(14);
  });
});
