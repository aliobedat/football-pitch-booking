// Thin shim: the auth-aware HTTP client now lives in @malaab/shared so the
// player app and the admin dashboard share ONE implementation (httpOnly-cookie
// auth + CSRF double-submit + single-flight refresh). This file only binds the
// shared factory to the player app's API origin and keeps the legacy default
// export so existing `import api from '@/lib/api'` call-sites keep working.
import { createApiClient } from '@malaab/shared/auth';

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api/v1';
// Demo isolation: NEXT_PUBLIC_COOKIE_NAME_PREFIX must match the backend's
// COOKIE_NAME_PREFIX (e.g. malaab_demo_) so the CSRF double-submit read finds
// the right cookie. Unset in Production/dev → 'malaab_', unchanged default.
const COOKIE_PREFIX = process.env.NEXT_PUBLIC_COOKIE_NAME_PREFIX || 'malaab_';

const api = createApiClient({ baseURL: API_URL, csrfCookie: `${COOKIE_PREFIX}csrf` });

export default api;
