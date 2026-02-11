import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  type ReactNode,
} from "react";
import { API_URL } from "../lib/api";

export type User = {
  id: string;
  email: string;
  name: string;
};

type AuthContextValue = {
  user: User | null;
  isLoading: boolean;
  isAuthenticated: boolean;
  login: (email: string, name?: string, org?: string) => Promise<void>;
  requestLogin: (orgId: string) => Promise<AuthRequest>;
  exchangeToken: (requestId: string, token: string) => Promise<void>;
  logout: () => void;
};

const AuthContext = createContext<AuthContextValue | null>(null);

const TOKEN_KEY = "otter_camp_token";
const USER_KEY = "otter_camp_user";
const TOKEN_EXP_KEY = "otter_camp_token_expires_at";
const ORG_KEY = "otter-camp-org-id";

type AuthRequest = {
  request_id: string;
  state: string;
  expires_at: string;
  exchange_url: string;
  openclaw_request: {
    request_id: string;
    state: string;
    org_id: string;
    callback_url: string;
    expires_at: string;
  };
};

function isTokenExpired(): boolean {
  const expiresAt = localStorage.getItem(TOKEN_EXP_KEY);
  if (!expiresAt) return false;
  const expiry = Date.parse(expiresAt);
  if (Number.isNaN(expiry)) return true;
  return Date.now() >= expiry;
}

type AuthProviderProps = {
  children: ReactNode;
};

export function AuthProvider({ children }: AuthProviderProps) {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const reconcileWorkspaceOrg = useCallback(async (sessionToken: string) => {
    const token = sessionToken.trim();
    if (!token) return;
    try {
      const validateResponse = await fetch(
        `${API_URL}/api/auth/validate?token=${encodeURIComponent(token)}`,
      );
      if (validateResponse.ok) {
        const validatePayload = await validateResponse.json().catch(() => null);
        const validatedOrgID =
          typeof validatePayload?.org_id === "string" ? validatePayload.org_id.trim() : "";
        if (validatedOrgID) {
          localStorage.setItem(ORG_KEY, validatedOrgID);
          return;
        }
      }

      const orgsResponse = await fetch(`${API_URL}/api/orgs`, {
        headers: {
          Authorization: `Bearer ${token}`,
          "X-Session-Token": token,
        },
      });
      if (!orgsResponse.ok) {
        return;
      }
      const payload = await orgsResponse.json().catch(() => null);
      const orgs = Array.isArray(payload?.orgs) ? payload.orgs : [];
      const normalized = orgs
        .map((entry: unknown): Record<string, unknown> | null =>
          entry && typeof entry === "object" ? (entry as Record<string, unknown>) : null,
        )
        .map((entry: Record<string, unknown> | null): string =>
          entry && typeof entry.id === "string" ? entry.id.trim() : "",
        )
        .filter((value: string) => value.length > 0);
      if (normalized.length === 0) {
        return;
      }
      const current = (localStorage.getItem(ORG_KEY) ?? "").trim();
      if (current && normalized.includes(current)) {
        return;
      }
      localStorage.setItem(ORG_KEY, normalized[0]);
    } catch {
      // Non-blocking: keep existing org selection if reconciliation fails.
    }
  }, []);

  // Initialize auth state from localStorage or magic link
  useEffect(() => {
    const applyValidatedSession = (data: unknown, fallbackToken: string): string | null => {
      const payload = data && typeof data === "object" ? (data as Record<string, unknown>) : {};
      const validatedUser: User = {
        id: String(payload.user_id || (payload.user as Record<string, unknown> | undefined)?.id || "user"),
        email: String(payload.email || (payload.user as Record<string, unknown> | undefined)?.email || ""),
        name: String(payload.name || (payload.user as Record<string, unknown> | undefined)?.name || "User"),
      };
      const orgId = typeof payload.org_id === "string" ? payload.org_id.trim() : "";
      const sessionToken =
        typeof payload.session_token === "string" && payload.session_token.trim()
          ? payload.session_token.trim()
          : fallbackToken;
      const expiresAt = typeof payload.expires_at === "string" ? payload.expires_at.trim() : "";

      if (!sessionToken) {
        return null;
      }

      localStorage.setItem(TOKEN_KEY, sessionToken);
      localStorage.setItem(USER_KEY, JSON.stringify(validatedUser));
      if (orgId) {
        localStorage.setItem(ORG_KEY, orgId);
      } else {
        localStorage.removeItem(ORG_KEY);
      }
      if (expiresAt) {
        localStorage.setItem(TOKEN_EXP_KEY, expiresAt);
      } else {
        localStorage.removeItem(TOKEN_EXP_KEY);
      }
      setUser(validatedUser);
      return sessionToken;
    };

    // Check for magic link auth param FIRST
    const params = new URLSearchParams(window.location.search);
    const magicToken = params.get('auth');
    
    if (magicToken) {
      // Validate magic link token via API
      fetch(`${API_URL}/api/auth/validate?token=${encodeURIComponent(magicToken)}`)
        .then(res => {
          if (!res.ok) throw new Error('Invalid token');
          return res.json();
        })
        .then(data => {
          const sessionToken = applyValidatedSession(data, magicToken);
          if (sessionToken) {
            void reconcileWorkspaceOrg(sessionToken);
          }

          // Remove auth param from URL
          params.delete('auth');
          const newUrl = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
          window.history.replaceState({}, '', newUrl);
          setIsLoading(false);
        })
        .catch(() => {
          // Remove auth param from URL
          params.delete('auth');
          const newUrl = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
          window.history.replaceState({}, '', newUrl);
          setIsLoading(false);
        });
      return;
    }

    const isLocal = window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1';
    const token = (localStorage.getItem(TOKEN_KEY) ?? '').trim();
    const storedUser = localStorage.getItem(USER_KEY);
    const shouldBootstrapLocalToken = isLocal && (!token || token.startsWith('oc_magic_'));

    if (shouldBootstrapLocalToken) {
      fetch(`${API_URL}/api/auth/magic`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'Admin', email: 'admin@localhost' }),
      })
        .then(res => res.json())
        .then(data => {
          const authToken = typeof data?.token === "string" ? data.token.trim() : '';
          if (!authToken) {
            throw new Error('missing token');
          }
          return fetch(`${API_URL}/api/auth/validate?token=${encodeURIComponent(authToken)}`)
            .then(vRes => vRes.ok ? vRes.json() : Promise.reject('invalid'))
            .then(vData => {
              const sessionToken = applyValidatedSession(vData, authToken);
              if (sessionToken) {
                void reconcileWorkspaceOrg(sessionToken);
              }
              setIsLoading(false);
            });
        })
        .catch(() => {
          if (!token) {
            localStorage.removeItem(TOKEN_KEY);
            localStorage.removeItem(USER_KEY);
            localStorage.removeItem(TOKEN_EXP_KEY);
            localStorage.removeItem(ORG_KEY);
            setUser(null);
          }
          setIsLoading(false);
        });
      return;
    }

    if (token && storedUser && !isTokenExpired()) {
      try {
        setUser(JSON.parse(storedUser));
        void reconcileWorkspaceOrg(token);
      } catch {
        localStorage.removeItem(TOKEN_KEY);
        localStorage.removeItem(USER_KEY);
        localStorage.removeItem(TOKEN_EXP_KEY);
        localStorage.removeItem(ORG_KEY);
      }
    } else if (token) {
      // Token expired, clean up
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(USER_KEY);
      localStorage.removeItem(TOKEN_EXP_KEY);
      localStorage.removeItem(ORG_KEY);
    }

    setIsLoading(false);
  }, [reconcileWorkspaceOrg]);

  const requestLogin = useCallback(async (orgId: string) => {
    const response = await fetch(`${API_URL}/api/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ org_id: orgId }),
    });

    if (!response.ok) {
      const error = await response.json().catch(() => ({ error: "Login request failed" }));
      throw new Error(error.error || "Login request failed");
    }

    const data = (await response.json()) as AuthRequest;
    return data;
  }, []);

  const exchangeToken = useCallback(async (requestId: string, token: string) => {
    const response = await fetch(`${API_URL}/api/auth/exchange`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: requestId, token }),
    });

    if (!response.ok) {
      const error = await response.json().catch(() => ({ error: "Login failed" }));
      throw new Error(error.error || "Login failed");
    }

    const data = await response.json();
    const { token: sessionToken, user: userData } = data;

    const expiresAt = response.headers.get("X-Session-Expires-At");
    if (expiresAt) {
      localStorage.setItem(TOKEN_EXP_KEY, expiresAt);
    } else {
      localStorage.removeItem(TOKEN_EXP_KEY);
    }

    localStorage.setItem(TOKEN_KEY, sessionToken);
    localStorage.setItem(USER_KEY, JSON.stringify(userData));
    setUser(userData);
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USER_KEY);
    localStorage.removeItem(TOKEN_EXP_KEY);
    localStorage.removeItem(ORG_KEY);
    setUser(null);
  }, []);

  // Magic-link login for MVP
  const login = useCallback(async (email: string, name?: string, org?: string) => {
    const response = await fetch(`${API_URL}/api/auth/magic`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, name, org }),
    });

    if (!response.ok) {
      const error = await response.json().catch(() => ({ error: "Failed to create magic link" }));
      throw new Error(error.error || "Failed to create magic link");
    }

    const data = await response.json();
    if (data?.url) {
      window.location.href = data.url;
      return;
    }

    throw new Error("Magic link response missing URL");
  }, []);

  const value: AuthContextValue = {
    user,
    isLoading,
    isAuthenticated: !!user,
    login,
    requestLogin,
    exchangeToken,
    logout,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return context;
}

export function getAuthToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
