import axios, { AxiosError } from 'axios';

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api/v1';

// Per-request flags we attach internally. _retry guards against an infinite
// refresh loop; _silent suppresses the redirect-to-login on auth failure (used
// by the AuthContext session probe, which must not bounce logged-out visitors
// off public pages).
declare module 'axios' {
  export interface AxiosRequestConfig {
    _retry?: boolean;
    _silent?: boolean;
  }
}

// withCredentials makes the browser send and store the httpOnly session cookies
// (malaab_access / malaab_refresh) set by the backend. No token is ever read or
// written by JavaScript — that is the whole point of the httpOnly migration.
const api = axios.create({
  baseURL: API_URL,
  headers: { 'Content-Type': 'application/json' },
  withCredentials: true,
});

// ── CSRF double-submit ────────────────────────────────────────────────────────
// The backend sets a readable malaab_csrf cookie at sign-in. For every
// state-changing request we echo it back in the X-CSRF-Token header; the
// RequireCSRF middleware checks the two match. A cross-site attacker can make
// the browser send the cookie but cannot read it to forge this header.
const UNSAFE_METHODS = new Set(['post', 'put', 'patch', 'delete']);

function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null;
  for (const part of document.cookie.split('; ')) {
    const eq = part.indexOf('=');
    if (eq > -1 && part.slice(0, eq) === name) {
      return decodeURIComponent(part.slice(eq + 1));
    }
  }
  return null;
}

api.interceptors.request.use((config) => {
  if (UNSAFE_METHODS.has((config.method ?? 'get').toLowerCase())) {
    const token = readCookie('malaab_csrf');
    if (token) config.headers.set('X-CSRF-Token', token);
  }
  return config;
});

// Endpoints that must never trigger the silent-refresh retry: refreshing on a
// failed refresh (or a failed login) would loop.
function isAuthEndpoint(url?: string): boolean {
  if (!url) return false;
  return (
    url.includes('/auth/refresh') ||
    url.includes('/auth/login') ||
    url.includes('/auth/request-otp') ||
    url.includes('/auth/verify-otp')
  );
}

// Single-flight guard: many requests can 401 at once when the access cookie
// expires; we want exactly one /auth/refresh in flight, with the rest awaiting it.
let refreshing: Promise<void> | null = null;

async function refreshSession(): Promise<void> {
  // The refresh token rides in its httpOnly cookie, so there is nothing to pass
  // in the body — the browser attaches it. Routed through `api` so the request
  // interceptor adds the X-CSRF-Token header that /auth/refresh now requires.
  await api.post('/auth/refresh');
}

api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const original = error.config;

    if (
      error.response?.status === 401 &&
      original &&
      !original._retry &&
      !isAuthEndpoint(original.url)
    ) {
      original._retry = true;
      try {
        refreshing = refreshing ?? refreshSession();
        await refreshing;
        return api(original);
      } catch (refreshErr) {
        // Refresh failed — the session is dead. Bounce to login UNLESS this was
        // a silent probe (e.g. the initial /auth/me check on a public page).
        if (!original._silent && typeof window !== 'undefined') {
          window.location.href = '/login';
        }
        return Promise.reject(refreshErr);
      } finally {
        refreshing = null;
      }
    }

    return Promise.reject(error);
  }
);

export default api;
