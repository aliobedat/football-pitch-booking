'use client';

import React, {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  type ReactNode,
} from 'react';
import api from '@/lib/api';

export interface User {
  id: number;
  full_name: string;
  email: string;
  phone?: string;
  role: 'player' | 'owner' | 'admin';
}

interface AuthContextType {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  // login is called after a successful auth response. The backend has already
  // set the httpOnly session cookies; we only adopt the returned profile into
  // in-memory state. No token is ever stored client-side.
  login: (user: User) => void;
  logout: () => Promise<void>;
  // refreshUser re-fetches the current profile from /auth/me (e.g. after a
  // profile edit). It is also how the session is rehydrated on first load.
  refreshUser: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider = ({ children }: { children: ReactNode }) => {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const refreshUser = useCallback(async () => {
    try {
      // Ask the server who we are; the httpOnly access cookie authenticates us.
      // _silent so a logged-out visitor on a public page is not redirected.
      const { data } = await api.get<{ data: User }>('/auth/me', { _silent: true });
      setUser(data.data);
    } catch {
      setUser(null);
    }
  }, []);

  useEffect(() => {
    // Rehydrate the session on first mount. There is no token to read from
    // storage anymore — we recover identity from the cookie via /auth/me.
    (async () => {
      await refreshUser();
      setIsLoading(false);
    })();
  }, [refreshUser]);

  const login = useCallback((u: User) => {
    setUser(u);
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post('/auth/logout'); // clears the httpOnly cookies server-side
    } catch {
      // ignore — we still clear local state below
    } finally {
      setUser(null);
      if (typeof window !== 'undefined') {
        window.location.href = '/login';
      }
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

export const useAuth = () => {
  const context = useContext(AuthContext);
  if (!context) throw new Error('useAuth must be used within an AuthProvider');
  return context;
};
