import { useEffect, useState } from 'react';

const API_URL = import.meta.env.VITE_API_URL || 'https://api.otter.camp';

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
  const [user, setUser] = useState<User | null>(null);

  useEffect(() => {
    const handleAuth = async () => {
      // Check for auth param in URL
      const params = new URLSearchParams(window.location.search);
      const authToken = params.get('auth');
      
      if (authToken) {
        try {
          // Validate token and set cookie
          const response = await fetch(`${API_URL}/api/auth/validate?token=${authToken}`, {
            credentials: 'include', // Important for cookies
          });
          
          if (response.ok) {
            const data = await response.json();
            if (data.valid && data.user) {
              setUser(data.user);
              // Store token in localStorage as backup
              localStorage.setItem('otter_auth_token', authToken);
              // Remove auth param from URL
              params.delete('auth');
              const newUrl = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
              window.history.replaceState({}, '', newUrl);
            }
          }
        } catch (err) {
          console.error('Auth validation failed:', err);
        }
      } else {
        // Check for existing token in localStorage
        const storedToken = localStorage.getItem('otter_auth_token');
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
  const token = localStorage.getItem('otter_auth_token');
  return {
    isAuthenticated: !!token,
    token,
  };
}
