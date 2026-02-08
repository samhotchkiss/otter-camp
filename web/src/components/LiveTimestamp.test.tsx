import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
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
});
