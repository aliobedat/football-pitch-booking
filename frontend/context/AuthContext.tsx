'use client';

// Thin shim: the AuthProvider/useAuth implementation now lives in
// @malaab/shared so both apps share it. We bind it to the player app's api
// client here and re-export, preserving existing `@/context/AuthContext`
// imports (and the `User` type) across the player app unchanged.
import { createAuthContext, type User as SharedUser } from '@malaab/shared/auth';
import api from '@/lib/api';

export type User = SharedUser;

// No standalone player login UI for launch: player booking is JIT/OTP-free, so
// logout lands on home ('/') rather than a dedicated login page. (The shared
// default loginPath stays '/login' for the admin app — we override per-binding.)
const { AuthProvider, useAuth } = createAuthContext(api, '/');

export { AuthProvider, useAuth };
