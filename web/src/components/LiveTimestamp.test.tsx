import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import AgentWorkingIndicator from "./AgentWorkingIndicator";
import LiveTimestamp, { formatLiveTimestamp } from "./LiveTimestamp";

describe("formatLiveTimestamp", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns Never for empty or invalid timestamps", () => {
    expect(formatLiveTimestamp(undefined, new Date("2026-02-08T12:00:00Z"))).toBe("Never");
    expect(formatLiveTimestamp(null, new Date("2026-02-08T12:00:00Z"))).toBe("Never");
    expect(formatLiveTimestamp("bad-value", new Date("2026-02-08T12:00:00Z"))).toBe("Never");
  });

  it("formats relative past and future values", () => {
    const now = new Date("2026-02-08T12:00:00Z");
    expect(formatLiveTimestamp("2026-02-08T11:59:57Z", now)).toBe("3s ago");
    expect(formatLiveTimestamp("2026-02-08T12:00:05Z", now)).toBe("in 5s");
  });

  it("formats verbose relative values for emission timeline rows", () => {
    const now = new Date("2026-02-08T12:00:00Z");
    expect(formatLiveTimestamp("2026-02-08T11:59:57Z", now, { verbose: true })).toBe("just now");
    expect(formatLiveTimestamp("2026-02-08T11:59:54Z", now, { verbose: true })).toBe("6 seconds ago");
    expect(formatLiveTimestamp("2026-02-08T11:59:00Z", now, { verbose: true })).toBe("1 minute ago");
    expect(formatLiveTimestamp("2026-02-08T11:58:00Z", now, { verbose: true })).toBe("2 minutes ago");
  });
});

describe("LiveTimestamp", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("updates displayed relative time every second", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-08T12:00:00Z"));

    render(<LiveTimestamp timestamp="2026-02-08T11:59:58Z" />);
    expect(screen.getByText("2s ago")).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(3000);
    });

    expect(screen.getByText("5s ago")).toBeInTheDocument();
  });

  it("updates verbose relative time every second", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-08T12:00:00Z"));

    render(<LiveTimestamp timestamp="2026-02-08T11:59:54Z" verbose />);
    expect(screen.getByText("6 seconds ago")).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(3000);
    });

    expect(screen.getByText("9 seconds ago")).toBeInTheDocument();
  });

  it("uses one shared ticker interval across multiple consumers", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-08T12:00:00Z"));
    const setIntervalSpy = vi.spyOn(window, "setInterval");
    const initialCalls = setIntervalSpy.mock.calls.length;

    render(
      <>
        <LiveTimestamp timestamp="2026-02-08T11:59:58Z" />
        <LiveTimestamp timestamp="2026-02-08T11:59:40Z" />
        <AgentWorkingIndicator
          latestEmission={{
            id: "em-1",
            source_type: "agent",
            source_id: "agent-1",
            kind: "status",
            summary: "working",
            timestamp: "2026-02-08T11:59:59Z",
          }}
        />
      </>,
    );

    expect(setIntervalSpy.mock.calls.length - initialCalls).toBe(1);
  });
});
