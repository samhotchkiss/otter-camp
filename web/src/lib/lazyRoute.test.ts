import { describe, expect, it, vi } from "vitest";
import { isChunkLoadError, lazyWithChunkRetry } from "./lazyRoute";

function createMemoryStorage(initial: Record<string, string> = {}) {
  const data = new Map<string, string>(Object.entries(initial));
  return {
    getItem: (key: string) => (data.has(key) ? data.get(key)! : null),
    setItem: (key: string, value: string) => {
      data.set(key, value);
    },
    removeItem: (key: string) => {
      data.delete(key);
    },
  };
}

describe("lazyRoute", () => {
  it("detects chunk load errors", () => {
    expect(isChunkLoadError(new Error("Failed to fetch dynamically imported module"))).toBe(true);
    expect(isChunkLoadError(new Error("ChunkLoadError: Loading chunk 42 failed"))).toBe(true);
    expect(isChunkLoadError(new Error("network timeout"))).toBe(false);
    expect(isChunkLoadError("not-an-error")).toBe(false);
  });

  it("reloads once on first chunk load failure", async () => {
    const storage = createMemoryStorage();
    const reload = vi.fn();

    const promise = lazyWithChunkRetry(
      () => Promise.reject(new Error("Failed to fetch dynamically imported module")),
      { storage, reload },
    );

    await Promise.resolve();

    expect(reload).toHaveBeenCalledTimes(1);
    expect(storage.getItem("otter-camp:chunk-reload-attempted")).toBe("1");
    await expect(Promise.race([promise, Promise.resolve("pending")])).resolves.toBe("pending");
  });

  it("throws on repeated chunk load failure in same session", async () => {
    const storage = createMemoryStorage({
      "otter-camp:chunk-reload-attempted": "1",
    });
    const reload = vi.fn();
    const error = new Error("Failed to fetch dynamically imported module");

    await expect(
      lazyWithChunkRetry(() => Promise.reject(error), { storage, reload }),
    ).rejects.toBe(error);
    expect(reload).not.toHaveBeenCalled();
  });

  it("clears chunk reload marker after a successful load", async () => {
    const storage = createMemoryStorage({
      "otter-camp:chunk-reload-attempted": "1",
    });

    const module = await lazyWithChunkRetry(() => Promise.resolve({ default: "ok" }), {
      storage,
      reload: vi.fn(),
    });

    expect(module).toEqual({ default: "ok" });
    expect(storage.getItem("otter-camp:chunk-reload-attempted")).toBeNull();
  });
});

