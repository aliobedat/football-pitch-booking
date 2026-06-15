'use client';

import React, {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  type ReactNode,
} from 'react';
import type { AxiosInstance } from 'axios';
import type { Role } from './roles';

export interface User {
  id: number;
  full_name: string;
  email: string;
  phone?: string;
  role: Role;
}

export interface AuthContextType {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  // login adopts the returned profile after a successful auth response. The
  // backend has already set the httpOnly cookies; no token is stored client-side.
  login: (user: User) => void;
  logout: () => Promise<void>;
  // refreshUser re-fetches /auth/me; also rehydrates the session on first load.
  refreshUser: () => Promise<void>;
}

// createAuthContext binds the provider to a specific api client so each app
// (player B2C / admin dashboard) shares this implementation while pointing at
// its own origin + login path.
export function createAuthContext(api: AxiosInstance, loginPath = '/login') {
  const AuthContext = createContext<AuthContextType | undefined>(undefined);

  const AuthProvider = ({ children }: { children: ReactNode }) => {
    const [user, setUser] = useState<User | null>(null);
    const [isLoading, setIsLoading] = useState(true);

    const refreshUser = useCallback(async () => {
      try {
        const { data } = await api.get<{ data: User }>('/auth/me', { _silent: true });
        setUser(data.data);
      } catch {
        setUser(null);
      }
    }, []);

    useEffect(() => {
      (async () => {
        await refreshUser();
        setIsLoading(false);
      })();
    }, [refreshUser]);

    const login = useCallback((u: User) => setUser(u), []);

    const logout = useCallback(async () => {
      try {
        await api.post('/auth/logout');
      } catch {
        // ignore — still clear local state below
      } finally {
        setUser(null);
        if (typeof window !== 'undefined') window.location.href = loginPath;
      }
    }, []);

    return (
      <AuthContext.Provider
        value={{ user, isAuthenticated: !!user, isLoading, login, logout, refreshUser }}
      >
        {children}
      </AuthContext.Provider>
    );
  };

  const useAuth = (): AuthContextType => {
    const ctx = useContext(AuthContext);
    if (!ctx) throw new Error('useAuth must be used within an AuthProvider');
    return ctx;
  };

  return { AuthProvider, useAuth };
}
