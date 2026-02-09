import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import AuthHandler, { useAuth } from "./AuthHandler";

vi.mock("../lib/demo", () => ({
  isDemoMode: () => false,
}));

describe("AuthHandler", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    window.history.replaceState({}, "", "/");
  });

  it("stores ?auth token using otter_camp_token and removes query param", async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ valid: true, user: { id: "u1", name: "Sam", email: "sam@example.com" } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    window.history.replaceState({}, "", "/?auth=oc_local_test_token");

    render(
      <AuthHandler>
        <div>child</div>
      </AuthHandler>,
    );

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    expect(localStorage.getItem("otter_camp_token")).toBe("oc_local_test_token");
    expect(window.location.search).toBe("");
  });

  it("useAuth reads the primary otter_camp_token key", () => {
    localStorage.setItem("otter_camp_token", "oc_local_saved");
    const state = useAuth();
    expect(state.isAuthenticated).toBe(true);
    expect(state.token).toBe("oc_local_saved");
    expect(state.isDemo).toBe(false);
  });
});
