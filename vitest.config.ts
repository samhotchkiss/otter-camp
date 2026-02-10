export default {
  test: {
    environment: "node",
    include: [
      "bridge/__tests__/openclaw-bridge.activity-buffer.test.ts",
      "bridge/__tests__/openclaw-bridge.connection-state.test.ts",
      "bridge/__tests__/openclaw-bridge.dispatch-durability.test.ts",
      "bridge/__tests__/openclaw-bridge.health-endpoint.test.ts",
      "bridge/__tests__/openclaw-bridge.heartbeat.test.ts",
      "bridge/__tests__/bridge-monitor.test.ts",
    ],
  },
};
