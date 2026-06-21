'use client';

import { useState, useEffect, type FormEvent, type ChangeEvent } from 'react';
import { useRouter } from 'next/navigation';
import axios from 'axios';
import { useAuth, type User } from '@/context/AuthContext';
import api from '@/lib/api';
import Button from '@/components/ui/Button';
import Input from '@/components/ui/Input';

// ─── Types ────────────────────────────────────────────────────────────────────

type Phase = 'phone' | 'code';

interface VerifyResponse {
  data: { expires_in_seconds: number; user: User };
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// The B2C app is player-facing only: EVERY successful login lands on the player
// pitch list, regardless of role. Owners manage pitches in the separate admin app
// (see the passive link in the Navbar) — the B2C app never routes to a dashboard.
const PLAYER_HOME = '/pitches';

// mapError turns a backend error envelope into an Arabic, user-facing message.
function mapError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const code = err.response?.data?.error as string | undefined;
    switch (code) {
      case 'opt_in_required':   return 'يجب الموافقة على استلام الرسائل أولاً.';
      case 'rate_limited':      return 'عدد كبير من الطلبات. يرجى المحاولة لاحقاً.';
      case 'resend_too_soon':   return 'تم إرسال رمز للتو. انتظر قليلاً قبل إعادة الإرسال.';
      case 'invalid_phone':     return 'رقم الهاتف غير صالح.';
      case 'invalid_code':      return 'الرمز غير صحيح أو منتهي الصلاحية.';
      case 'too_many_attempts': return 'محاولات كثيرة. اطلب رمزاً جديداً.';
      default:
        return err.response?.data?.message ?? 'حدث خطأ ما. يرجى المحاولة مرة أخرى.';
    }
  }
  return 'حدث خطأ في الشبكة. تحقق من اتصالك بالإنترنت.';
}

// ─── Brand mark ───────────────────────────────────────────────────────────────

function BrandMark({ subtitle }: { subtitle: string }) {
  return (
    <div className="flex flex-col items-center gap-2 mb-10">
      <div className="flex items-center justify-center w-11 h-11 rounded-xl bg-[#0f4c3a]/30">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="12" r="10" stroke="#3dba8a" strokeWidth="1.5" />
          <path
            d="M12 7c-.5 0-1 .2-1.4.6L8 10.2c-.4.4-.6.9-.6 1.4v2.8c0 .5.2 1 .6 1.4l2.6 2.6c.4.4.9.6 1.4.6s1-.2 1.4-.6l2.6-2.6c.4-.4.6-.9.6-1.4v-2.8c0-.5-.2-1-.6-1.4L13.4 7.6C13 7.2 12.5 7 12 7z"
            fill="#3dba8a"
            opacity="0.8"
          />
        </svg>
      </div>
      <h1 className="text-[22px] font-medium text-[#f0efe8] tracking-tight" dir="ltr">
        Malaab
      </h1>
      <p className="text-sm text-white/35">{subtitle}</p>
    </div>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function LoginPage() {
  const router = useRouter();
  const { user, isLoading: authLoading, login } = useAuth();

  const [phase, setPhase]   = useState<Phase>('phone');
  const [phone, setPhone]   = useState('');
  const [consent, setConsent] = useState(false);
  const [code, setCode]     = useState('');

  const [fieldError, setFieldError] = useState<string | null>(null);
  const [apiError, setApiError]     = useState<string | null>(null);
  const [info, setInfo]             = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  // Redirect already-authenticated users away from this page.
  useEffect(() => {
    if (!authLoading && user) {
      router.replace(PLAYER_HOME);
    }
  }, [user, authLoading, router]);

  function handlePhoneChange(e: ChangeEvent<HTMLInputElement>) {
    setPhone(e.target.value);
    if (fieldError) setFieldError(null);
    if (apiError) setApiError(null);
  }

  function handleCodeChange(e: ChangeEvent<HTMLInputElement>) {
    // Keep digits only; OTP codes are numeric.
    setCode(e.target.value.replace(/\D/g, ''));
    if (fieldError) setFieldError(null);
    if (apiError) setApiError(null);
  }

  // ── Step 1: request a code ──────────────────────────────────────────────────
  async function handleRequestOtp(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!phone.trim()) {
      setFieldError('رقم الهاتف مطلوب');
      return;
    }
    if (!consent) {
      setFieldError('يجب الموافقة على استلام رسائل التحقق للمتابعة');
      return;
    }

    setIsSubmitting(true);
    setApiError(null);
    try {
      await api.post('/auth/request-otp', { phone: phone.trim(), opt_in: true });
      setPhase('code');
      setCode('');
      setInfo('تم إرسال رمز التحقق إلى هاتفك.');
    } catch (err) {
      setApiError(mapError(err));
    } finally {
      setIsSubmitting(false);
    }
  }

  // ── Step 2: verify the code ─────────────────────────────────────────────────
  async function handleVerify(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (code.trim().length < 4) {
      setFieldError('أدخل رمز التحقق المرسل إليك');
      return;
    }

    setIsSubmitting(true);
    setApiError(null);
    try {
      const { data } = await api.post<VerifyResponse>('/auth/verify-otp', {
        phone: phone.trim(),
        code: code.trim(),
      });
      // The backend has already set the httpOnly session cookies; we only adopt
      // the returned profile into context.
      login(data.data.user);
      router.push(PLAYER_HOME);
    } catch (err) {
      setApiError(mapError(err));
    } finally {
      setIsSubmitting(false);
    }
  }

  // ── Resend / change number ──────────────────────────────────────────────────
  async function handleResend() {
    setIsSubmitting(true);
    setApiError(null);
    setInfo(null);
    try {
      await api.post('/auth/request-otp', { phone: phone.trim(), opt_in: true });
      setInfo('تم إرسال رمز جديد.');
    } catch (err) {
      setApiError(mapError(err));
    } finally {
      setIsSubmitting(false);
    }
  }

  function changeNumber() {
    setPhase('phone');
    setCode('');
    setFieldError(null);
    setApiError(null);
    setInfo(null);
  }

  // Skeleton while AuthContext resolves the session.
  if (authLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-[#121413]">
        <span className="sr-only">جاري التحميل...</span>
        <div
          className="w-5 h-5 rounded-full border-2 border-[#0f4c3a] border-t-transparent animate-spin"
          aria-hidden="true"
        />
      </div>
    );
  }

  return (
    <main dir="rtl" className="min-h-screen bg-[#121413] flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <BrandMark
          subtitle={phase === 'phone' ? 'سجّل الدخول برقم هاتفك' : 'أدخل رمز التحقق'}
        />

        <div className="bg-[#1a1c1b] border border-white/[0.07] rounded-2xl p-8">
          {phase === 'phone' ? (
            <form onSubmit={handleRequestOtp} noValidate className="flex flex-col gap-5">
              <Input
                label="رقم الهاتف"
                type="tel"
                inputMode="tel"
                dir="ltr"
                placeholder="07X XXX XXXX"
                autoComplete="tel"
                value={phone}
                onChange={handlePhoneChange}
                error={fieldError ?? undefined}
                hint="نرسل لك رمز تحقق لمرة واحدة. يمكنك إدخال رقمك المحلي أو الدولي."
              />

              <label className="flex items-start gap-2.5 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={consent}
                  onChange={(e) => {
                    setConsent(e.target.checked);
                    if (fieldError) setFieldError(null);
                  }}
                  className="mt-0.5 h-4 w-4 shrink-0 rounded border-white/20 bg-[#111312] accent-emerald-600"
                />
                <span className="text-[12px] leading-relaxed text-white/55">
                  أوافق على استلام رسائل التحقق عبر الواتساب أو الرسائل النصية.
                </span>
              </label>

              {apiError && (
                <div
                  role="alert"
                  className="rounded-lg px-4 py-3 text-sm text-red-400 bg-red-500/[0.08] border border-red-500/[0.18]"
                >
                  {apiError}
                </div>
              )}

              <Button type="submit" fullWidth loading={isSubmitting} className="mt-1 font-bold tracking-wide">
                إرسال رمز التحقق
              </Button>
            </form>
          ) : (
            <form onSubmit={handleVerify} noValidate className="flex flex-col gap-5">
              <div className="text-center text-[12px] text-white/45" dir="ltr">
                {phone}
              </div>

              <Input
                label="رمز التحقق"
                type="text"
                inputMode="numeric"
                dir="ltr"
                maxLength={6}
                placeholder="● ● ● ● ● ●"
                autoComplete="one-time-code"
                value={code}
                onChange={handleCodeChange}
                error={fieldError ?? undefined}
                className="text-center tracking-[0.5em]"
              />

              {info && !apiError && (
                <p className="text-[12px] text-emerald-400/80 text-center">{info}</p>
              )}

              {apiError && (
                <div
                  role="alert"
                  className="rounded-lg px-4 py-3 text-sm text-red-400 bg-red-500/[0.08] border border-red-500/[0.18]"
                >
                  {apiError}
                </div>
              )}

              <Button type="submit" fullWidth loading={isSubmitting} className="mt-1 font-bold tracking-wide">
                تأكيد وتسجيل الدخول
              </Button>

              <div className="flex items-center justify-between text-[12px]">
                <button
                  type="button"
                  onClick={changeNumber}
                  disabled={isSubmitting}
                  className="text-white/40 hover:text-white/70 transition-colors disabled:opacity-40"
                >
                  تغيير الرقم
                </button>
                <button
                  type="button"
                  onClick={handleResend}
                  disabled={isSubmitting}
                  className="text-emerald-500 hover:text-emerald-400 font-semibold transition-colors disabled:opacity-40"
                >
                  إعادة إرسال الرمز
                </button>
              </div>
            </form>
          )}
        </div>

        <p className="text-center text-sm text-white/30 mt-6">
          بتسجيلك للدخول، يتم إنشاء حسابك تلقائياً عند أول تحقق.
        </p>
      </div>
    </main>
  );
}
