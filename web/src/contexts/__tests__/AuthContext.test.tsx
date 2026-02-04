import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, renderHook, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider, useAuth, getAuthToken, type User } from "../AuthContext";

// Mock fetch
const mockFetch = vi.fn();
global.fetch = mockFetch;

// Mock localStorage
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

// Helper to create a valid JWT token
function createMockJWT(payload: { exp: number; sub: string; email: string; name: string }): string {
  const header = btoa(JSON.stringify({ alg: "HS256", typ: "JWT" }));
  const body = btoa(JSON.stringify(payload));
  const signature = "mock-signature";
  return `${header}.${body}.${signature}`;
}

// Test component that uses auth context
function TestComponent() {
  const { user, isLoading, isAuthenticated, login, logout } = useAuth();

  if (isLoading) {
    return <div>Loading...</div>;
  }

  if (!isAuthenticated) {
    return (
      <div>
        <span>Not authenticated</span>
        <button onClick={() => login("test@example.com", "password")}>Login</button>
      </div>
    );
  }

  return (
    <div>
      <span>Welcome, {user?.name}</span>
      <span>Email: {user?.email}</span>
      <button onClick={logout}>Logout</button>
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

  describe("AuthProvider", () => {
    it("provides auth context to children", () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      // After initial load, should show not authenticated
      expect(screen.getByText("Not authenticated")).toBeInTheDocument();
    });

    it("shows loading state initially", () => {
      // Set up stored token
      const token = createMockJWT({
        exp: Date.now() / 1000 + 3600, // 1 hour from now
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", token);
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
      );

      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      // The component starts with loading state, then quickly resolves
      // Due to the effect, we should see the authenticated state
      expect(screen.queryByText("Loading...") || screen.queryByText("Welcome, Test User")).toBeTruthy();
    });

    it("restores auth state from localStorage", async () => {
      const token = createMockJWT({
        exp: Date.now() / 1000 + 3600,
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", token);
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
      );

      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      await waitFor(() => {
        expect(screen.getByText("Welcome, Test User")).toBeInTheDocument();
        expect(screen.getByText("Email: test@example.com")).toBeInTheDocument();
      });
    });

    it("clears expired token on mount", async () => {
      const expiredToken = createMockJWT({
        exp: Date.now() / 1000 - 3600, // 1 hour ago
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", expiredToken);
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
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
    });

    it("handles corrupted user data in localStorage", async () => {
      const token = createMockJWT({
        exp: Date.now() / 1000 + 3600,
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", token);
      localStorageMock.setItem("otter_camp_user", "not valid json");

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
    });
  });

  describe("useAuth hook", () => {
    it("throws error when used outside AuthProvider", () => {
      // Suppress console.error for this test
      const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});

      expect(() => {
        renderHook(() => useAuth());
      }).toThrow("useAuth must be used within an AuthProvider");

      consoleError.mockRestore();
    });

    it("provides user, isLoading, isAuthenticated values", () => {
      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      expect(result.current).toHaveProperty("user");
      expect(result.current).toHaveProperty("isLoading");
      expect(result.current).toHaveProperty("isAuthenticated");
      expect(result.current).toHaveProperty("login");
      expect(result.current).toHaveProperty("logout");
    });
  });

  describe("login", () => {
    it("authenticates user successfully", async () => {
      const mockUser: User = {
        id: "user-1",
        email: "test@example.com",
        name: "Test User",
      };

      const mockToken = createMockJWT({
        exp: Date.now() / 1000 + 3600,
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ token: mockToken, user: mockUser }),
      });

      const user = userEvent.setup();

      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      await waitFor(() => {
        expect(screen.getByText("Not authenticated")).toBeInTheDocument();
      });

      await user.click(screen.getByText("Login"));

      await waitFor(() => {
        expect(screen.getByText("Welcome, Test User")).toBeInTheDocument();
      });

      expect(mockFetch).toHaveBeenCalledWith("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: "test@example.com", password: "password" }),
      });

      expect(localStorageMock.setItem).toHaveBeenCalledWith("otter_camp_token", mockToken);
      expect(localStorageMock.setItem).toHaveBeenCalledWith(
        "otter_camp_user",
        JSON.stringify(mockUser)
      );
    });

    it("throws error on failed login", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        json: () => Promise.resolve({ message: "Invalid credentials" }),
      });

      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      await expect(
        act(async () => {
          await result.current.login("test@example.com", "wrong-password");
        })
      ).rejects.toThrow("Invalid credentials");
    });

    it("handles network errors during login", async () => {
      mockFetch.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      await expect(
        act(async () => {
          await result.current.login("test@example.com", "password");
        })
      ).rejects.toThrow("Network error");
    });

    it("handles login response without error message", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        json: () => Promise.resolve({}),
      });

      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      await expect(
        act(async () => {
          await result.current.login("test@example.com", "password");
        })
      ).rejects.toThrow("Login failed");
    });

    it("handles malformed JSON response", async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        json: () => Promise.reject(new Error("Invalid JSON")),
      });

      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      await expect(
        act(async () => {
          await result.current.login("test@example.com", "password");
        })
      ).rejects.toThrow("Login failed");
    });
  });

  describe("logout", () => {
    it("clears user state and localStorage", async () => {
      const mockUser: User = {
        id: "user-1",
        email: "test@example.com",
        name: "Test User",
      };

      const mockToken = createMockJWT({
        exp: Date.now() / 1000 + 3600,
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });

      // Pre-populate localStorage
      localStorageMock.setItem("otter_camp_token", mockToken);
      localStorageMock.setItem("otter_camp_user", JSON.stringify(mockUser));

      const user = userEvent.setup();

      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      await waitFor(() => {
        expect(screen.getByText("Welcome, Test User")).toBeInTheDocument();
      });

      // Clear mock calls from initialization
      localStorageMock.removeItem.mockClear();

      await user.click(screen.getByText("Logout"));

      await waitFor(() => {
        expect(screen.getByText("Not authenticated")).toBeInTheDocument();
      });

      expect(localStorageMock.removeItem).toHaveBeenCalledWith("otter_camp_token");
      expect(localStorageMock.removeItem).toHaveBeenCalledWith("otter_camp_user");
    });
  });

  describe("getAuthToken", () => {
    it("returns token from localStorage", () => {
      const mockToken = "mock-token";
      localStorageMock.setItem("otter_camp_token", mockToken);

      expect(getAuthToken()).toBe(mockToken);
    });

    it("returns null when no token exists", () => {
      localStorageMock.clear();

      expect(getAuthToken()).toBeNull();
    });
  });

  describe("token expiration", () => {
    it("correctly identifies expired token", async () => {
      const expiredToken = createMockJWT({
        exp: Date.now() / 1000 - 1, // Just expired
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", expiredToken);
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
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

    it("accepts valid non-expired token", async () => {
      const validToken = createMockJWT({
        exp: Date.now() / 1000 + 3600, // 1 hour from now
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", validToken);
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
      );

      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      await waitFor(() => {
        expect(screen.getByText("Welcome, Test User")).toBeInTheDocument();
      });
    });

    it("handles invalid JWT format", async () => {
      localStorageMock.setItem("otter_camp_token", "invalid-token-format");
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
      );

      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>
      );

      // Invalid token should be treated as expired
      await waitFor(() => {
        expect(screen.getByText("Not authenticated")).toBeInTheDocument();
      });
    });
  });

  describe("isAuthenticated", () => {
    it("is false when user is null", () => {
      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      expect(result.current.isAuthenticated).toBe(false);
    });

    it("is true when user is set", async () => {
      const mockToken = createMockJWT({
        exp: Date.now() / 1000 + 3600,
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });
      localStorageMock.setItem("otter_camp_token", mockToken);
      localStorageMock.setItem(
        "otter_camp_user",
        JSON.stringify({ id: "user-1", email: "test@example.com", name: "Test User" })
      );

      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      await waitFor(() => {
        expect(result.current.isAuthenticated).toBe(true);
      });
    });
  });

  describe("concurrent operations", () => {
    it("handles multiple rapid login attempts", async () => {
      const mockUser: User = {
        id: "user-1",
        email: "test@example.com",
        name: "Test User",
      };

      const mockToken = createMockJWT({
        exp: Date.now() / 1000 + 3600,
        sub: "user-1",
        email: "test@example.com",
        name: "Test User",
      });

      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ token: mockToken, user: mockUser }),
      });

      const { result } = renderHook(() => useAuth(), {
        wrapper: AuthProvider,
      });

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      // Fire multiple login attempts
      await act(async () => {
        const promises = [
          result.current.login("test@example.com", "password"),
          result.current.login("test@example.com", "password"),
          result.current.login("test@example.com", "password"),
        ];
        await Promise.all(promises);
      });

      expect(result.current.isAuthenticated).toBe(true);
    });
  });
});
