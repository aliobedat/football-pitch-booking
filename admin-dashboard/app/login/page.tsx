'use client';

import { useState, useEffect, type FormEvent } from 'react';
import axios from 'axios';
import { isDashboardRole, type User } from '@malaab/shared/auth';
import { useAuth } from '@/context/AuthContext';
import api from '@/lib/api';

// Admin standalone login: phone + password (no OTP/SMS dependency). It calls the
// backend POST /auth/password-login, which mints a roled session ONLY for a
// dashboard role (owner/admin/staff/super_admin) on a correct phone+password; a
// player or any credential failure returns a generic 401. The OTP endpoints
// (request-otp/verify-otp) still exist server-side but are intentionally NOT used
// here. The route guard + edge proxy enforce role access independently.
const B2C_URL = process.env.NEXT_PUBLIC_B2C_URL || 'http://localhost:3000';

interface LoginResponse {
  data: { expires_in_seconds: number; user: User };
}

// mapError maps the backend envelope to Arabic copy. Every credential failure is a
// single generic message (no oracle revealing which field was wrong).
function mapError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const code = err.response?.data?.error as string | undefined;
    switch (code) {
      case 'too_many_attempts': return 'محاولات كثيرة. حاول لاحقاً.';
      case 'invalid_credentials': return 'رقم الهاتف أو كلمة المرور غير صحيحة.';
      default: return 'رقم الهاتف أو كلمة المرور غير صحيحة.';
    }
  }
  return 'خطأ في الشبكة.';
}

export default function AdminLoginPage() {
  const { user, isLoading, login } = useAuth();

  const [phone, setPhone] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  // A player who authenticated successfully but is NOT a dashboard role: rejected
  // at the admin door (shown an explicit message + B2C link, not silently bounced).
  const [rejected, setRejected] = useState(false);

  // Route a resolved session: dashboard roles into the app; a player is held at
  // the door with the rejection screen (never redirected straight into admin).
  // Full-document navigation (WO-AUTH-GHOST-LOGIN): a client-side
  // router.replace here could serve the Next client cache's stale
  // "/ → /login" resolution from the expiry bounce, leaving the user stuck on
  // the login screen with a live session. window.location bypasses the client
  // cache and re-runs the edge guard — the same pattern AuthContext.logout uses.
  useEffect(() => {
    if (isLoading || !user) return;
    if (isDashboardRole(user.role)) window.location.replace('/');
    else setRejected(true);
  }, [user, isLoading]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const { data } = await api.post<LoginResponse>('/auth/password-login', {
        phone: phone.trim(),
        password,
      });
      const u = data.data.user;
      // Role gate: admit dashboard roles; reject a player WITHOUT adopting the
      // session into the admin context, so admin access is never granted.
      if (isDashboardRole(u.role)) {
        login(u);
        // Full-document navigation — see the effect above; router.push('/')
        // was the "clicking Login does nothing" half of the ghost-login bug.
        window.location.replace('/');
      } else {
        setRejected(true);
      }
    } catch (err) {
      setError(mapError(err));
    } finally {
      setSubmitting(false);
    }
  }

  // ── Rejection door: authenticated but not a dashboard role (player) ─────────
  if (rejected) {
    return (
      <main dir="rtl" className="min-h-screen flex items-center justify-center px-4">
        <div className="w-full max-w-sm">
          <div className="flex flex-col items-center gap-1 mb-8">
            <span className="text-[20px] font-bold tracking-tight">مرمى</span>
            <span className="text-[11px] font-bold tracking-widest text-emerald-400 uppercase">
              Admin Dashboard
            </span>
          </div>

          <div className="bg-[#141715] border border-white/[0.08] rounded-2xl p-8 text-center flex flex-col gap-4">
            <h1 className="text-[16px] font-bold text-[#f0efe8]">هذه اللوحة مخصّصة لأصحاب الملاعب والمشرفين</h1>
            <p className="text-[13px] text-white/45 leading-relaxed">
              حسابك حساب لاعب ولا يملك صلاحية الدخول إلى لوحة التحكم. يمكنك متابعة الحجز عبر تطبيق اللاعبين.
            </p>
            <a
              href={`${B2C_URL}/pitches`}
              className="rounded-lg bg-emerald-600 hover:bg-emerald-500 px-4 py-3 text-sm font-bold transition-colors"
            >
              الذهاب إلى تطبيق اللاعبين
            </a>
          </div>
        </div>
      </main>
    );
  }

  return (
    <main dir="rtl" className="min-h-screen flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="flex flex-col items-center gap-1 mb-8">
          <span className="text-[20px] font-bold tracking-tight">مرمى</span>
          <span className="text-[11px] font-bold tracking-widest text-emerald-400 uppercase">
            Admin Dashboard
          </span>
        </div>

        <div className="bg-[#1a1c1b] border border-white/[0.07] rounded-2xl p-8">
          <form onSubmit={submit} className="flex flex-col gap-5">
            <input
              type="tel"
              dir="ltr"
              autoComplete="username"
              placeholder="07X XXX XXXX"
              value={phone}
              onChange={(e) => setPhone(e.target.value)}
              className="w-full rounded-lg bg-[#111312] border border-white/10 px-4 py-3 text-sm text-center"
            />
            <input
              type="password"
              dir="ltr"
              autoComplete="current-password"
              placeholder="كلمة المرور"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full rounded-lg bg-[#111312] border border-white/10 px-4 py-3 text-sm text-center"
            />

            {error && (
              <div role="alert" className="rounded-lg px-4 py-3 text-sm text-red-400 bg-red-500/[0.08] border border-red-500/[0.18]">
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="rounded-lg bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 px-4 py-3 text-sm font-bold transition-colors"
            >
              دخول
            </button>
          </form>
        </div>
      </div>
    </main>
  );
}
