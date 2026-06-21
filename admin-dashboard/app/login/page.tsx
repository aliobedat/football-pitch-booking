'use client';

import { useState, useEffect, type FormEvent } from 'react';
import { useRouter } from 'next/navigation';
import axios from 'axios';
import { isDashboardRole, type User } from '@malaab/shared/auth';
import { useAuth } from '@/context/AuthContext';
import api from '@/lib/api';

// Admin standalone login (PR A) — reuses the existing phone-OTP endpoints
// (request-otp → verify-otp), inventing no new auth logic. Dashboard roles
// (staff/owner/admin/super_admin) are admitted; a PLAYER-only actor is REJECTED
// at the admin door with a clear message + a link to the local B2C app — never
// granted admin access (the route guard + edge proxy enforce this independently).
const B2C_URL = process.env.NEXT_PUBLIC_B2C_URL || 'http://localhost:3000';

type Phase = 'phone' | 'code';

interface VerifyResponse {
  data: { expires_in_seconds: number; user: User };
}

function mapError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const code = err.response?.data?.error as string | undefined;
    switch (code) {
      case 'rate_limited': return 'عدد كبير من الطلبات. حاول لاحقاً.';
      case 'invalid_phone': return 'رقم الهاتف غير صالح.';
      case 'invalid_code': return 'الرمز غير صحيح أو منتهي.';
      case 'too_many_attempts': return 'محاولات كثيرة. اطلب رمزاً جديداً.';
      default: return err.response?.data?.message ?? 'حدث خطأ ما.';
    }
  }
  return 'خطأ في الشبكة.';
}

export default function AdminLoginPage() {
  const router = useRouter();
  const { user, isLoading, login } = useAuth();

  const [phase, setPhase] = useState<Phase>('phone');
  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  // A player who authenticated successfully but is NOT a dashboard role: rejected
  // at the admin door (shown an explicit message + B2C link, not silently bounced).
  const [rejected, setRejected] = useState(false);

  // Route a resolved session: dashboard roles into the app; a player is held at
  // the door with the rejection screen (never redirected straight into admin).
  useEffect(() => {
    if (isLoading || !user) return;
    if (isDashboardRole(user.role)) router.replace('/');
    else setRejected(true);
  }, [user, isLoading, router]);

  async function requestOtp(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post('/auth/request-otp', { phone: phone.trim(), opt_in: true });
      setPhase('code');
    } catch (err) {
      setError(mapError(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function verify(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const { data } = await api.post<VerifyResponse>('/auth/verify-otp', {
        phone: phone.trim(),
        code: code.trim(),
      });
      const u = data.data.user;
      // Role gate: admit dashboard roles; reject a player WITHOUT adopting the
      // session into the admin context, so admin access is never granted.
      if (isDashboardRole(u.role)) {
        login(u);
        router.push('/');
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
            <span className="text-[20px] font-bold tracking-tight">ملاعب</span>
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
          <span className="text-[20px] font-bold tracking-tight">ملاعب</span>
          <span className="text-[11px] font-bold tracking-widest text-emerald-400 uppercase">
            Admin Dashboard
          </span>
        </div>

        <div className="bg-[#1a1c1b] border border-white/[0.07] rounded-2xl p-8">
          <form onSubmit={phase === 'phone' ? requestOtp : verify} className="flex flex-col gap-5">
            {phase === 'phone' ? (
              <input
                type="tel"
                dir="ltr"
                placeholder="07X XXX XXXX"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                className="w-full rounded-lg bg-[#111312] border border-white/10 px-4 py-3 text-sm text-center"
              />
            ) : (
              <input
                type="text"
                inputMode="numeric"
                dir="ltr"
                maxLength={6}
                placeholder="● ● ● ● ● ●"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                className="w-full rounded-lg bg-[#111312] border border-white/10 px-4 py-3 text-sm text-center tracking-[0.5em]"
              />
            )}

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
              {phase === 'phone' ? 'إرسال رمز التحقق' : 'دخول'}
            </button>
          </form>
        </div>
      </div>
    </main>
  );
}
