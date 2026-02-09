import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import useNowTicker from "./useNowTicker";

describe("useNowTicker", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("starts interval on first subscriber", () => {
    const setIntervalSpy = vi.spyOn(window, "setInterval");

    const { unmount } = renderHook(() => useNowTicker());

    expect(setIntervalSpy).toHaveBeenCalledTimes(1);
    expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), 1000);

    unmount();
  });

  it("shares a single interval across multiple subscribers", () => {
    const setIntervalSpy = vi.spyOn(window, "setInterval");

    const first = renderHook(() => useNowTicker());
    const second = renderHook(() => useNowTicker());

    expect(setIntervalSpy).toHaveBeenCalledTimes(1);

    first.unmount();
    second.unmount();
  });

  it("stops interval when all subscribers unmount", () => {
    const clearIntervalSpy = vi.spyOn(window, "clearInterval");

    const first = renderHook(() => useNowTicker());
    const second = renderHook(() => useNowTicker());

    first.unmount();
    expect(clearIntervalSpy).not.toHaveBeenCalled();

    second.unmount();
    expect(clearIntervalSpy).toHaveBeenCalledTimes(1);
  });
});
