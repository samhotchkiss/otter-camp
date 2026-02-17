import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const originalLocation = window.location;

async function loadApiModule(hostname: string, origin: string) {
  vi.resetModules();
  vi.stubGlobal("location", {
    ...originalLocation,
    hostname,
    origin,
  });
  return import("./api");
}

describe("API_URL", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("falls back to window.location.origin when VITE_API_URL is not configured", async () => {
    const { API_URL } = await loadApiModule("localhost", "http://localhost:5173");
    expect(API_URL).toBe("http://localhost:5173");
  });
});

describe("hostedOrgSlugFromHostname", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("extracts the slug from hosted subdomains", async () => {
    const { hostedOrgSlugFromHostname } = await loadApiModule("localhost", "http://localhost:5173");
    expect(hostedOrgSlugFromHostname("swh.otter.camp")).toBe("swh");
  });

  it("ignores reserved and invalid hostnames", async () => {
    const { hostedOrgSlugFromHostname } = await loadApiModule("localhost", "http://localhost:5173");
    expect(hostedOrgSlugFromHostname("api.otter.camp")).toBe("");
    expect(hostedOrgSlugFromHostname("www.otter.camp")).toBe("");
    expect(hostedOrgSlugFromHostname("otter.camp")).toBe("");
    expect(hostedOrgSlugFromHostname("bad slug.otter.camp")).toBe("");
    expect(hostedOrgSlugFromHostname("foo.bar.otter.camp")).toBe("");
  });
});

describe("apiFetch hosted org headers", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("sends X-Org-ID and X-Otter-Org for hosted org subdomains", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const { apiFetch } = await loadApiModule("swh.otter.camp", "https://swh.otter.camp");
    window.localStorage.setItem("otter_camp_token", "oc_sess_test");
    window.localStorage.setItem("otter-camp-org-id", "550e8400-e29b-41d4-a716-446655440000");

    await apiFetch<{ ok: boolean }>("/api/test");

    const [, requestInit] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = requestInit.headers as Record<string, string>;
    expect(headers["X-Org-ID"]).toBe("550e8400-e29b-41d4-a716-446655440000");
    expect(headers["X-Otter-Org"]).toBe("swh");
  });

  it("does not send X-Otter-Org for api.otter.camp", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const { apiFetch } = await loadApiModule("api.otter.camp", "https://api.otter.camp");
    window.localStorage.setItem("otter_camp_token", "oc_sess_test");
    window.localStorage.setItem("otter-camp-org-id", "550e8400-e29b-41d4-a716-446655440000");

    await apiFetch<{ ok: boolean }>("/api/test");

    const [, requestInit] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = requestInit.headers as Record<string, string>;
    expect(headers["X-Otter-Org"]).toBeUndefined();
  });
});
