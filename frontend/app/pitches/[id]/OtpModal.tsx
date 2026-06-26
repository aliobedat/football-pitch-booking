'use client';

// OTP verification modal for the guest booking flow.
// Reuses the same endpoints and error-code → Arabic copy mapping as login/page.tsx.
// The caller (BookingForm) is responsible for calling login(user) and proceeding
// with the booking POST after onVerified fires.

import { useState, useEffect, useRef, useCallback, type FormEvent } from 'react';
import axios from 'axios';
import api from '@/lib/api';
import type { User } from '@/context/AuthContext';
import Button from '@/components/ui/Button';

// Mirrors mapError in login/page.tsx — same backend error codes, same Arabic copy.
// Duplicated intentionally (no shared export exists yet); extract if a third
// callsite appears.
function mapError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const code = err.response?.data?.error as string | undefined;
    switch (code) {
      case 'opt_in_required':   return 'يجب الموافقة على استلام رسائل التحقق أولاً.';
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

interface VerifyResponse {
  data: { expires_in_seconds: number; user: User };
}

export interface OtpModalProps {
  // E.164 normalized phone, e.g. "+962791234567". Sent as-is to the backend.
  phone: string;
  // Fired on successful verify-otp. The backend has already set httpOnly cookies
  // at this point; the caller calls login(user) then proceeds with booking.
  onVerified: (user: User) => void;
  // Fired when the user cancels or clicks the backdrop. Name/phone fields in
  // the parent are untouched so the user can edit and retry.
  onDismiss: () => void;
}

const RESEND_COOLDOWN_SECONDS = 30;

export default function OtpModal({ phone, onVerified, onDismiss }: OtpModalProps) {
  const [code,         setCode]         = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [apiError,     setApiError]     = useState<string | null>(null);
  const [info,         setInfo]         = useState<string | null>(null);
  const [cooldown,     setCooldown]     = useState(0); // seconds remaining before resend allowed
  const codeInputRef = useRef<HTMLInputElement>(null);
  // Prevents the mount-time POST from firing twice under React StrictMode's
  // intentional double-invoke. A ref (not state) so it doesn't cause a re-render.
  const hasSentRef = useRef(false);

  const startCooldown = useCallback(() => {
    setCooldown(RESEND_COOLDOWN_SECONDS);
  }, []);

  // Tick the cooldown counter down once per second.
  useEffect(() => {
    if (cooldown <= 0) return;
    const id = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(id);
  }, [cooldown]);

  // Send OTP exactly once on mount, regardless of StrictMode double-invoke.
  useEffect(() => {
    if (hasSentRef.current) return;
    hasSentRef.current = true;
    api
      .post('/auth/request-otp', { phone, opt_in: true, purpose: 'booking' })
      .then(() => {
        setInfo('تم إرسال رمز التحقق إلى هاتفك.');
        codeInputRef.current?.focus();
        startCooldown();
      })
      .catch((err) => setApiError(mapError(err)));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []); // intentionally empty — phone is stable for the modal's lifetime

  async function handleVerify(e: FormEvent) {
    e.preventDefault();
    if (code.trim().length < 4) {
      setApiError('أدخل رمز التحقق المرسل إليك');
      return;
    }
    setIsSubmitting(true);
    setApiError(null);
    try {
      const { data } = await api.post<VerifyResponse>('/auth/verify-otp', {
        phone,
        code: code.trim(),
      });
      onVerified(data.data.user);
    } catch (err) {
      setApiError(mapError(err));
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleResend() {
    if (isSubmitting || cooldown > 0) return; // strict guard — no double-fire
    setIsSubmitting(true);
    setApiError(null);
    setInfo(null);
    try {
      await api.post('/auth/request-otp', { phone, opt_in: true, purpose: 'booking' });
      setInfo('تم إرسال رمز جديد.');
      startCooldown();
    } catch (err) {
      setApiError(mapError(err));
    } finally {
      setIsSubmitting(false);
    }
  }

  const resendDisabled = isSubmitting || cooldown > 0;

  return (
    // Backdrop — click outside to dismiss (fields in parent are preserved)
    <div
      role="dialog"
      aria-modal="true"
      aria-label="التحقق من رقم الهاتف"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget) onDismiss(); }}
    >
      <div
        dir="rtl"
        className="w-full max-w-sm rounded-2xl bg-[#141715] border border-white/[0.08] shadow-2xl p-6 flex flex-col gap-5"
      >
        {/* Header */}
        <div className="flex items-start justify-between gap-3">
          <div>
            <p className="text-[10px] font-bold tracking-widest text-emerald-500 uppercase mb-1">
              التحقق من الهاتف
            </p>
            <h3 className="text-[17px] font-bold text-[#f0efe8] leading-snug">
              أدخل رمز التحقق
            </h3>
          </div>
          <button
            type="button"
            onClick={onDismiss}
            aria-label="إغلاق"
            className={[
              'mt-0.5 w-7 h-7 flex-shrink-0 flex items-center justify-center rounded-lg',
              'text-[13px] text-white/30 hover:text-white/60 hover:bg-white/[0.06]',
              'transition-colors duration-150 focus-visible:outline-none',
              'focus-visible:ring-2 focus-visible:ring-emerald-500',
            ].join(' ')}
          >
            ✕
          </button>
        </div>

        {/* Phone display */}
        <div className="px-3 py-2.5 rounded-xl bg-[#0d0f0e] border border-white/[0.06] text-center">
          <p className="text-[12px] text-white/40 font-mono" dir="ltr">{phone}</p>
        </div>

        <form onSubmit={handleVerify} noValidate className="flex flex-col gap-4">
          {/* Code input */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="otp-modal-code" className="text-[11px] font-bold text-white/40 tracking-wide">
              رمز التحقق
            </label>
            <input
              ref={codeInputRef}
              id="otp-modal-code"
              type="text"
              inputMode="numeric"
              dir="ltr"
              maxLength={6}
              placeholder="● ● ● ● ● ●"
              autoComplete="one-time-code"
              value={code}
              disabled={isSubmitting}
              onChange={(e) => {
                setCode(e.target.value.replace(/\D/g, ''));
                if (apiError) setApiError(null);
              }}
              className={[
                'w-full rounded-xl px-4 py-3 text-center text-[22px] font-mono font-bold tracking-[0.45em]',
                'bg-[#0d0f0e] text-[#f0efe8] border transition-all duration-150 focus:outline-none',
                'placeholder:text-white/15 disabled:opacity-50',
                apiError
                  ? 'border-red-500/50 focus:ring-1 focus:ring-red-500/30'
                  : 'border-white/[0.09] hover:border-white/[0.18] focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15',
              ].join(' ')}
            />
          </div>

          {info && !apiError && (
            <p className="text-[11px] text-emerald-400/80 text-center">{info}</p>
          )}

          {apiError && (
            <div
              role="alert"
              className="rounded-lg px-4 py-3 text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] leading-relaxed"
            >
              {apiError}
            </div>
          )}

          <Button
            type="submit"
            fullWidth
            loading={isSubmitting}
            className="font-bold tracking-wide"
          >
            تأكيد رمز التحقق
          </Button>
        </form>

        {/* Footer actions */}
        <div className="flex items-center justify-between text-[12px]">
          <button
            type="button"
            onClick={onDismiss}
            disabled={isSubmitting}
            className="text-white/35 hover:text-white/60 transition-colors duration-150 disabled:opacity-40"
          >
            إلغاء والعودة
          </button>
          <button
            type="button"
            onClick={handleResend}
            disabled={resendDisabled}
            className="text-emerald-500 hover:text-emerald-400 font-semibold transition-colors duration-150 disabled:opacity-40"
          >
            {cooldown > 0 ? `إعادة الإرسال (${cooldown}ث)` : 'إعادة إرسال الرمز'}
          </button>
        </div>
      </div>
    </div>
  );
}
