const CHUNK_RELOAD_SESSION_KEY = "otter-camp:chunk-reload-attempted";

type ChunkRetryOptions = {
  storage?: Pick<Storage, "getItem" | "setItem" | "removeItem">;
  reload?: () => void;
};

function defaultStorage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }
  return window.sessionStorage;
}

function defaultReload(): (() => void) | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }
  return () => window.location.reload();
}

export function isChunkLoadError(error: unknown): boolean {
  if (!(error instanceof Error)) {
    return false;
  }

  const message = error.message.toLowerCase();
  return (
    message.includes("failed to fetch dynamically imported module") ||
    message.includes("importing a module script failed") ||
    message.includes("chunkloaderror")
  );
}

export async function lazyWithChunkRetry<T>(
  importFn: () => Promise<T>,
  options: ChunkRetryOptions = {},
): Promise<T> {
  const storage = options.storage ?? defaultStorage();
  const reload = options.reload ?? defaultReload();

  try {
    const module = await importFn();
    storage?.removeItem(CHUNK_RELOAD_SESSION_KEY);
    return module;
  } catch (error) {
    const attempted = storage?.getItem(CHUNK_RELOAD_SESSION_KEY) === "1";
    if (isChunkLoadError(error) && !attempted) {
      storage?.setItem(CHUNK_RELOAD_SESSION_KEY, "1");
      if (reload) {
        reload();
        // Keep suspense pending while browser refreshes.
        return new Promise<T>(() => {});
      }
    }
    throw error;
  }
}

