'use client';

// Binds the shared AuthProvider/useAuth to the admin app's api client.
import { createAuthContext, type User as SharedUser } from '@malaab/shared/auth';
import api from '@/lib/api';

export type User = SharedUser;

const { AuthProvider, useAuth } = createAuthContext(api, '/login');

export { AuthProvider, useAuth };
