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
    url.includes('/auth/verify-otp') ||
    // WO-AUTH-GHOST-LOGIN: a failed password login must surface its 401 —
    // without this entry the interceptor silently refreshed the session and
    // RE-POSTED the credentials, resurrecting "logged-out" sessions.
    url.includes('/auth/password-login') ||
    // booking-session returns a neutral 403 (never 401), so this entry is
    // defensive — listed for symmetry so no future status change re-opens
    // the refresh-on-login hole.
    url.includes('/auth/booking-session') ||
    // logout ends the session; refreshing it first would be self-defeating.
    url.includes('/auth/logout')
  );
}

export interface ApiClientOptions {
  // Backend API origin, e.g. http://localhost:8080/api/v1. Each app passes its
  // own (NEXT_PUBLIC_API_URL) — the cookie/CSRF dance is identical per origin.
  baseURL: string;
  // Readable CSRF cookie name set by the backend at sign-in.
  csrfCookie?: string;
  // @deprecated No longer used. The response interceptor does not navigate on
  // auth failure; login navigation is owned by AuthContext.logout() and inline
  // auth flows. Retained so existing callers that still pass it do not break.
  loginPath?: string;
}

export function createApiClient(opts: ApiClientOptions): AxiosInstance {
  // NOTE: loginPath is intentionally NOT destructured here. The response
  // interceptor no longer navigates on auth failure (see below) — navigation is
  // exclusively the caller's / AuthContext.logout()'s responsibility. The option
  // is retained on ApiClientOptions only for backward compatibility.
  const { baseURL, csrfCookie = 'malaab_csrf' } = opts;

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
          // The session is unrecoverable. The interceptor has NO authority to
          // navigate — it simply rejects so the caller can degrade to a UI state
          // (e.g. AuthContext.refreshUser sets user=null; the booking flow shows
          // an inline error). The only login navigation in the app is the
          // user-initiated AuthContext.logout(). This keeps every public read
          // path (pitch details, availability, reviews) free of auth-driven
          // redirects: a stray 401 can never bounce a guest to /login.
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
