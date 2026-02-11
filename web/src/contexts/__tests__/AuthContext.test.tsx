import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, renderHook, act } from "@testing-library/react";
import { AuthProvider, useAuth, type User } from "../AuthContext";

const mockFetch = vi.fn();
global.fetch = mockFetch;

const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: vi.fn((key: string) => store[key] || null),
    setItem: vi.fn((key: string, value: string) => {
      store[key] = value;
    }),
    removeItem: vi.fn((key: string) => {
      delete store[key];
    }),
    clear: vi.fn(() => {
      store = {};
    }),
    get store() {
      return store;
    },
  };
})();

Object.defineProperty(window, "localStorage", {
  value: localStorageMock,
});

function TestComponent() {
  const { user, isLoading, isAuthenticated } = useAuth();
  if (isLoading) return <div>Loading...</div>;
  if (!isAuthenticated) return <div>Not authenticated</div>;
  return (
    <div>
      <span>Welcome, {user?.name}</span>
      <span>Email: {user?.email}</span>
    </div>
  );
}

describe("AuthContext", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorageMock.clear();
    mockFetch.mockImplementation(async () => ({
      ok: true,
      json: async () => ({}),
      headers: { get: () => null },
    }));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("provides auth context to children", () => {
    render(
      <AuthProvider>
        <TestComponent />
      </AuthProvider>
    );

    return waitFor(() => {
      expect(screen.getByText("Not authenticated")).toBeInTheDocument();
    });
  });

  it("restores auth state from localStorage when token is valid", async () => {
    const mockUser: User = { id: "user-1", email: "", name: "OpenClaw User" };
    localStorageMock.setItem("otter_camp_token", "oc_sess_token");
    localStorageMock.setItem("otter_camp_user", JSON.stringify(mockUser));
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() + 3600 * 1000).toISOString()
    );

    render(
      <AuthProvider>
        <TestComponent />
      </AuthProvider>
    );

    await waitFor(() => {
      expect(screen.getByText("Welcome, OpenClaw User")).toBeInTheDocument();
    });
  });

  it("reconciles stale org selection from validated session org on startup", async () => {
    const mockUser: User = { id: "user-1", email: "", name: "OpenClaw User" };
    localStorageMock.setItem("otter_camp_token", "oc_sess_token");
    localStorageMock.setItem("otter_camp_user", JSON.stringify(mockUser));
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() + 3600 * 1000).toISOString(),
    );
    localStorageMock.setItem("otter-camp-org-id", "stale-org");

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ org_id: "org-123" }),
    });

    render(
      <AuthProvider>
        <TestComponent />
      </AuthProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Welcome, OpenClaw User")).toBeInTheDocument();
    });

    await waitFor(() => {
      expect(localStorageMock.setItem).toHaveBeenCalledWith("otter-camp-org-id", "org-123");
    });
  });

  it("migrates local magic token sessions to canonical local auth token", async () => {
    const staleUser: User = { id: "stale-user", email: "admin@localhost", name: "Admin" };
    localStorageMock.setItem("otter_camp_token", "oc_magic_stale");
    localStorageMock.setItem("otter_camp_user", JSON.stringify(staleUser));
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() + 3600 * 1000).toISOString(),
    );
    localStorageMock.setItem("otter-camp-org-id", "stale-org");

    mockFetch.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/auth/magic")) {
        return {
          ok: true,
          json: async () => ({ token: "oc_local_shared" }),
          headers: { get: () => null },
        } as Response;
      }
      if (url.includes("/api/auth/validate?token=oc_local_shared")) {
        return {
          ok: true,
          json: async () => ({
            session_token: "oc_local_shared",
            org_id: "org-146",
            user_id: "user-1",
            name: "Admin",
            email: "admin@localhost",
            expires_at: new Date(Date.now() + 3600 * 1000).toISOString(),
          }),
          headers: { get: () => null },
        } as Response;
      }
      return {
        ok: true,
        json: async () => ({}),
        headers: { get: () => null },
      } as Response;
    });

    render(
      <AuthProvider>
        <TestComponent />
      </AuthProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Welcome, Admin")).toBeInTheDocument();
    });

    expect(localStorageMock.setItem).toHaveBeenCalledWith("otter_camp_token", "oc_local_shared");
    expect(localStorageMock.setItem).toHaveBeenCalledWith("otter-camp-org-id", "org-146");
    expect(
      mockFetch.mock.calls.some(([request]) => String(request).includes("/api/auth/magic")),
    ).toBe(true);
  });

  it("clears expired token on mount", async () => {
    const mockUser: User = { id: "user-1", email: "", name: "OpenClaw User" };
    localStorageMock.setItem("otter_camp_token", "oc_sess_token");
    localStorageMock.setItem("otter_camp_user", JSON.stringify(mockUser));
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() - 3600 * 1000).toISOString()
    );

    render(
      <AuthProvider>
        <TestComponent />
      </AuthProvider>
    );

    await waitFor(() => {
      expect(screen.getByText("Not authenticated")).toBeInTheDocument();
    });

    expect(localStorageMock.removeItem).toHaveBeenCalledWith("otter_camp_token");
    expect(localStorageMock.removeItem).toHaveBeenCalledWith("otter_camp_user");
    expect(localStorageMock.removeItem).toHaveBeenCalledWith("otter_camp_token_expires_at");
  });

  it("handles corrupted user data in localStorage", async () => {
    localStorageMock.setItem("otter_camp_token", "oc_sess_token");
    localStorageMock.setItem("otter_camp_user", "invalid-json");
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() + 3600 * 1000).toISOString()
    );

    render(
      <AuthProvider>
        <TestComponent />
      </AuthProvider>
    );

    await waitFor(() => {
      expect(screen.getByText("Not authenticated")).toBeInTheDocument();
    });
  });

  it("requestLogin calls the auth request endpoint", async () => {
    const mockUser: User = { id: "user-1", email: "", name: "OpenClaw User" };
    localStorageMock.setItem("otter_camp_token", "oc_sess_token");
    localStorageMock.setItem("otter_camp_user", JSON.stringify(mockUser));
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() + 3600 * 1000).toISOString(),
    );
    mockFetch.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/orgs")) {
        return {
          ok: true,
          json: async () => ({ orgs: [{ id: "org-1" }] }),
          headers: { get: () => null },
        } as Response;
      }
      if (url.includes("/api/auth/login")) {
        return {
          ok: true,
          json: async () => ({
            request_id: "req-1",
            state: "state-1",
            expires_at: new Date().toISOString(),
            exchange_url: "/api/auth/exchange",
            openclaw_request: {
              request_id: "req-1",
              state: "state-1",
              org_id: "org-1",
              callback_url: "http://localhost/api/auth/exchange",
              expires_at: new Date().toISOString(),
            },
          }),
          headers: { get: () => null },
        } as Response;
      }
      return {
        ok: true,
        json: async () => ({}),
        headers: { get: () => null },
      } as Response;
    });

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    let response: { request_id: string } | undefined;
    await act(async () => {
      response = await result.current.requestLogin("org-1");
    });
    expect(response?.request_id).toBe("req-1");

    expect(
      mockFetch.mock.calls.some(([request, options]) => (
        String(request).includes("/api/auth/login") &&
        (options as RequestInit)?.method === "POST" &&
        (options as RequestInit)?.body === JSON.stringify({ org_id: "org-1" })
      )),
    ).toBe(true);
  });

  it("exchangeToken stores session and user", async () => {
    const mockUser: User = { id: "user-1", email: "", name: "OpenClaw User" };
    localStorageMock.setItem("otter_camp_token", "existing-token");
    localStorageMock.setItem("otter_camp_user", JSON.stringify(mockUser));
    localStorageMock.setItem(
      "otter_camp_token_expires_at",
      new Date(Date.now() + 3600 * 1000).toISOString(),
    );
    mockFetch.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/orgs")) {
        return {
          ok: true,
          json: async () => ({ orgs: [{ id: "org-1" }] }),
          headers: { get: () => null },
        } as Response;
      }
      if (url.includes("/api/auth/exchange")) {
        return {
          ok: true,
          json: async () => ({ token: "oc_sess_token", user: mockUser }),
          headers: {
            get: () => new Date(Date.now() + 3600 * 1000).toISOString(),
          },
        } as Response;
      }
      return {
        ok: true,
        json: async () => ({}),
        headers: { get: () => null },
      } as Response;
    });

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    await act(async () => {
      await result.current.exchangeToken("req-1", "oc_auth_token");
    });

    expect(localStorageMock.setItem).toHaveBeenCalledWith("otter_camp_token", "oc_sess_token");
    expect(localStorageMock.setItem).toHaveBeenCalledWith(
      "otter_camp_user",
      JSON.stringify(mockUser)
    );
  });
});
