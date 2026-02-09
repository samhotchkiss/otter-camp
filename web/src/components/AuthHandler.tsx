import { useEffect, useState } from 'react';
import { isDemoMode } from '../lib/demo';
import { API_URL } from "../lib/api";

const PRIMARY_AUTH_TOKEN_KEY = 'otter_camp_token';
const LEGACY_AUTH_TOKEN_KEY = 'otter_auth_token';

interface User {
  id: string;
  name: string;
  email: string;
}

/**
 * AuthHandler - Handles magic link authentication
 * 
 * On mount, checks for ?auth= URL param and validates the token.
 * Stores token in localStorage and sets cookie via API.
 */
export default function AuthHandler({ children }: { children: React.ReactNode }) {
  const [isChecking, setIsChecking] = useState(true);
  const [, setUser] = useState<User | null>(null);

  useEffect(() => {
    const persistToken = (token: string) => {
      localStorage.setItem(PRIMARY_AUTH_TOKEN_KEY, token);
      localStorage.setItem(LEGACY_AUTH_TOKEN_KEY, token);
    };

    const getStoredToken = () => {
      const primary = localStorage.getItem(PRIMARY_AUTH_TOKEN_KEY);
      if (primary) {
        return primary;
      }
      return localStorage.getItem(LEGACY_AUTH_TOKEN_KEY);
    };

    const handleAuth = async () => {
      // Skip auth entirely in demo mode
      if (isDemoMode()) {
        setUser({
          id: 'demo-user',
          name: 'Demo User',
          email: 'demo@otter.camp',
        });
        setIsChecking(false);
        return;
      }

      // Check for auth param in URL
      const params = new URLSearchParams(window.location.search);
      const authToken = params.get('auth');
      
      if (authToken) {
        persistToken(authToken);
        try {
          // Validate token and set cookie
          const response = await fetch(`${API_URL}/api/auth/validate?token=${authToken}`, {
            credentials: 'include', // Important for cookies
          });
          
          if (response.ok) {
            const data = await response.json();
            if (data.valid && data.user) {
              setUser(data.user);
            }
          }
        } catch (err) {
          console.error('Auth validation failed:', err);
        }

        // Remove auth param from URL after processing.
        params.delete('auth');
        const newUrl = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
        window.history.replaceState({}, '', newUrl);
      } else {
        // Check for existing token in localStorage
        const storedToken = getStoredToken();
        if (storedToken) {
          // Optionally validate stored token
          // For MVP, just trust it
          setUser({
            id: 'stored-user',
            name: 'Sam',
            email: 'sam@otter.camp',
          });
        }
      }
      
      setIsChecking(false);
    };

    handleAuth();
  }, []);

  // For MVP, don't block rendering while checking auth
  // Just show the app immediately
  if (isChecking) {
    // Could show a loading spinner here, but for MVP just render children
  }

  // Make user available via context or props if needed
  // For now, just pass through
  return <>{children}</>;
}

// Export a hook for components to check auth status
export function useAuth() {
  // Demo mode is always "authenticated"
  if (isDemoMode()) {
    return {
      isAuthenticated: true,
      token: 'demo',
      isDemo: true,
    };
  }
  
  const token = localStorage.getItem(PRIMARY_AUTH_TOKEN_KEY) || localStorage.getItem(LEGACY_AUTH_TOKEN_KEY);
  return {
    isAuthenticated: !!token,
    token,
    isDemo: false,
  };
}
