'use client';

import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react';
import api from '@/lib/api';

interface User {
  id: number;
  full_name: string;
  email: string;
  role: 'player' | 'owner' | 'admin';
}

interface AuthContextType {
  user: User | null;
  accessToken: string | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  login: (data: any) => void;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider = ({ children }: { children: ReactNode }) => {
  const [user, setUser] = useState<User | null>(null);
  const [accessToken, setAccessToken] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem('accessToken');
    const storedUser = localStorage.getItem('user');
    if (token && storedUser) {
      setAccessToken(token);
      setUser(JSON.parse(storedUser));
    }
    setIsLoading(false);
  }, []);

  const login = (authData: any) => {
    const { access_token, refresh_token, user: userData } = authData;
    localStorage.setItem('accessToken', access_token);
    localStorage.setItem('refreshToken', refresh_token);
    localStorage.setItem('user', JSON.stringify(userData));
    // Lightweight role cookie so Next.js edge middleware can guard /dashboard
    // without touching the JWT. The real role is enforced by the backend JWT.
    document.cookie = `malaab_role=${userData.role}; path=/; max-age=${60 * 60 * 24 * 7}; SameSite=Lax`;
    setAccessToken(access_token);
    setUser(userData);
  };

  const logout = async () => {
    try { await api.post('/auth/logout'); } catch (err) {}
    finally {
      localStorage.removeItem('accessToken');
      localStorage.removeItem('refreshToken');
      localStorage.removeItem('user');
      document.cookie = 'malaab_role=; path=/; max-age=0';
      setUser(null);
      setAccessToken(null);
      window.location.href = '/login';
    }
  };

  return (
    <AuthContext.Provider value={{ user, accessToken, isAuthenticated: !!user, isLoading, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = () => {
  const context = useContext(AuthContext);
  if (!context) throw new Error('useAuth must be used within an AuthProvider');
  return context;
};