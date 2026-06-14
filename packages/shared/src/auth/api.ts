import axios, { AxiosError, type AxiosInstance } from 'axios';

// ── Shared auth-aware HTTP client ─────────────────────────────────────────────
// Extracted from the player app so the admin dashboard imports the SAME client
// (httpOnly-cookie auth + CSRF double-submit + single-flight refresh) rather
// than carrying a copy. Each app calls createApiClient() with its own baseURL.

// Per-request flags. _retry guards the refresh loop; _silent suppresses the
// redirect-to-login on auth failure (used by the session probe so logged-out
// visitors on public pages are not bounced).
declare module 'axios' {
  export interface AxiosRequestConfig {
    _retry?: boolean;
    _silent?: boolean;
  }
}

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

function isAuthEndpoint(url?: string): boolean {
  if (!url) return false;
  return (
    url.includes('/auth/refresh') ||
    url.includes('/auth/request-otp') ||
    url.includes('/auth/verify-otp')
  );
}

export interface ApiClientOptions {
  // Backend API origin, e.g. http://localhost:8080/api/v1. Each app passes its
  // own (NEXT_PUBLIC_API_URL) — the cookie/CSRF dance is identical per origin.
  baseURL: string;
  // Readable CSRF cookie name set by the backend at sign-in.
  csrfCookie?: string;
  // Where to send the browser when the session is unrecoverable.
  loginPath?: string;
}

export function createApiClient(opts: ApiClientOptions): AxiosInstance {
  const { baseURL, csrfCookie = 'malaab_csrf', loginPath = '/login' } = opts;

  // withCredentials makes the browser send/store the httpOnly session cookies
  // (malaab_access / malaab_refresh). No token is read or written by JS.
  const api = axios.create({
    baseURL,
    headers: { 'Content-Type': 'application/json' },
    withCredentials: true,
  });

  // CSRF double-submit: echo the readable cookie into X-CSRF-Token on every
  // state-changing request. A cross-site attacker can send the cookie but
  // cannot read it to forge this header.
  api.interceptors.request.use((config) => {
    if (UNSAFE_METHODS.has((config.method ?? 'get').toLowerCase())) {
      const token = readCookie(csrfCookie);
      if (token) config.headers.set('X-CSRF-Token', token);
    }
    return config;
  });

  // Single-flight refresh: many requests can 401 at once when the access cookie
  // expires; keep exactly one /auth/refresh in flight with the rest awaiting it.
  let refreshing: Promise<void> | null = null;
  const refreshSession = () => api.post('/auth/refresh').then(() => undefined);

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
          if (!original._silent && typeof window !== 'undefined') {
            window.location.href = loginPath;
          }
          return Promise.reject(refreshErr);
        } finally {
          refreshing = null;
        }
      }
      return Promise.reject(error);
    },
  );

  return api;
}
