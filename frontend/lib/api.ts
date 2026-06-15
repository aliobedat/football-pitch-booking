// Thin shim: the auth-aware HTTP client now lives in @malaab/shared so the
// player app and the admin dashboard share ONE implementation (httpOnly-cookie
// auth + CSRF double-submit + single-flight refresh). This file only binds the
// shared factory to the player app's API origin and keeps the legacy default
// export so existing `import api from '@/lib/api'` call-sites keep working.
import { createApiClient } from '@malaab/shared/auth';

const API_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api/v1';

const api = createApiClient({ baseURL: API_URL });

export default api;
