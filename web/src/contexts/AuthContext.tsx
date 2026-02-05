import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  type ReactNode,
} from "react";

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

export type User = {
  id: string;
  email: string;
  name: string;
};

type AuthContextValue = {
  user: User | null;
  isLoading: boolean;
  isAuthenticated: boolean;
  login: (email: string, name?: string) => void;
  requestLogin: (orgId: string) => Promise<AuthRequest>;
  exchangeToken: (requestId: string, token: string) => Promise<void>;
  logout: () => void;
};

const AuthContext = createContext<AuthContextValue | null>(null);

const TOKEN_KEY = "otter_camp_token";
const USER_KEY = "otter_camp_user";
const TOKEN_EXP_KEY = "otter_camp_token_expires_at";

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

  // Initialize auth state from localStorage or magic link
  useEffect(() => {
    // Check for magic link auth param FIRST
    const params = new URLSearchParams(window.location.search);
    const magicToken = params.get('auth');
    
    // Valid magic link tokens (MVP: hardcoded, will be replaced with DB)
    const VALID_TOKENS = [
      'otter-sam-2026',      // Sam's magic link
      'sam',                  // Short alias
    ];
    
    // Magic link auth - validate token before allowing login
    if (magicToken && VALID_TOKENS.includes(magicToken)) {
      const samUser: User = {
        id: 'sam',
        email: 'sam@otter.camp',
        name: 'Sam',
      };
      localStorage.setItem(TOKEN_KEY, magicToken);
      localStorage.setItem(USER_KEY, JSON.stringify(samUser));
      // Set org_id for Sam's organization (MVP: hardcoded)
      localStorage.setItem('otter-camp-org-id', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11');
      setUser(samUser);
      
      // Remove auth param from URL
      params.delete('auth');
      const newUrl = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
      window.history.replaceState({}, '', newUrl);
      setIsLoading(false);
      return;
    }
    
    const token = localStorage.getItem(TOKEN_KEY);
    const storedUser = localStorage.getItem(USER_KEY);

    if (token && storedUser && !isTokenExpired()) {
      try {
        setUser(JSON.parse(storedUser));
      } catch {
        localStorage.removeItem(TOKEN_KEY);
        localStorage.removeItem(USER_KEY);
        localStorage.removeItem(TOKEN_EXP_KEY);
      }
    } else if (token) {
      // Token expired, clean up
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(USER_KEY);
      localStorage.removeItem(TOKEN_EXP_KEY);
    }

    setIsLoading(false);
  }, []);

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
    setUser(null);
  }, []);

  // Simple login for MVP - just sets the user directly
  const login = useCallback((email: string, name?: string) => {
    const newUser: User = {
      id: email.split('@')[0] || 'user',
      email,
      name: name || email.split('@')[0] || 'User',
    };
    localStorage.setItem(TOKEN_KEY, 'magic-link-token');
    localStorage.setItem(USER_KEY, JSON.stringify(newUser));
    setUser(newUser);
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
