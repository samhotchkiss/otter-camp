import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import useHealth from "./useHealth";

type MockResponse = {
  ok: boolean;
  status: number;
  json: () => Promise<unknown>;
};

const createResponse = (body: unknown, ok = true, status = 200): MockResponse => ({
  ok,
  status,
  json: vi.fn().mockResolvedValue(body),
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("useHealth", () => {
  it("fetches health endpoint", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(createResponse({ status: "ok" }) as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    renderHook(() => useHealth());

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith("/health");
    });
  });

  it("returns status ok", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(createResponse({ status: "ok" }) as unknown as Response);
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useHealth());

    await waitFor(() => {
      expect(result.current.status).toBe("ok");
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe(null);
    });
  });

  it("handles error gracefully", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error("Network fail"));
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(() => useHealth());

    await waitFor(() => {
      expect(result.current.error).toBe("Network fail");
      expect(result.current.isLoading).toBe(false);
    });
  });
});
