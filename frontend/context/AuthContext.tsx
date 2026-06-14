'use client';

// Thin shim: the AuthProvider/useAuth implementation now lives in
// @malaab/shared so both apps share it. We bind it to the player app's api
// client here and re-export, preserving existing `@/context/AuthContext`
// imports (and the `User` type) across the player app unchanged.
import { createAuthContext, type User as SharedUser } from '@malaab/shared/auth';
import api from '@/lib/api';

export type User = SharedUser;

const { AuthProvider, useAuth } = createAuthContext(api, '/login');

export { AuthProvider, useAuth };
