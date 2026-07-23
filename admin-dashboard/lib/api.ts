// Binds the shared auth-aware HTTP client to the ADMIN origin. Same
// httpOnly-cookie + CSRF double-submit + single-flight refresh implementation
// the player app uses — imported from @malaab/shared, never copied.
//
// The admin app talks to the same backend API. For cross-origin cookies to ride
// along, the backend must (PR 2) add this origin to the CORS allowlist and set
// the session cookie Domain to a parent shared by both origins. See
// docs/DASHBOARD_PR2_BACKEND_NOTES.md.
import { createApiClient } from '@malaab/shared/auth';

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api/v1';
// Demo isolation: NEXT_PUBLIC_COOKIE_NAME_PREFIX must match the backend's
// COOKIE_NAME_PREFIX (e.g. malaab_demo_) so the CSRF double-submit read finds
// the right cookie. Unset in Production/dev → 'malaab_', unchanged default.
const COOKIE_PREFIX = process.env.NEXT_PUBLIC_COOKIE_NAME_PREFIX || 'malaab_';

const api = createApiClient({ baseURL: API_URL, loginPath: '/login', csrfCookie: `${COOKIE_PREFIX}csrf` });

export default api;
