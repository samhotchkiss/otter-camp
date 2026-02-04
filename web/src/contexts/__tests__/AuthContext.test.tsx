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

    expect(screen.getByText("Not authenticated")).toBeInTheDocument();
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
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () =>
        Promise.resolve({
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

    expect(mockFetch).toHaveBeenCalledWith("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ org_id: "org-1" }),
    });
  });

  it("exchangeToken stores session and user", async () => {
    const mockUser: User = { id: "user-1", email: "", name: "OpenClaw User" };
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ token: "oc_sess_token", user: mockUser }),
      headers: {
        get: () => new Date(Date.now() + 3600 * 1000).toISOString(),
      },
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
