'use client';

import { useState, useEffect, type FormEvent, type ChangeEvent } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import axios from 'axios';
import { useAuth } from '@/context/AuthContext';
import api from '@/lib/api';
import Button from '@/components/ui/Button';
import Input from '@/components/ui/Input';

// ─── Types ────────────────────────────────────────────────────────────────────

interface AuthApiResponse {
  access_token:      string;
  refresh_token:     string;
  expires_in_seconds: number;
  user: {
    id:         number;
    full_name:  string;
    email:      string;
    role:       'player' | 'owner';
    created_at: string;
  };
}

interface FormValues {
  email:    string;
  password: string;
}

interface FormErrors {
  email?:    string;
  password?: string;
}

// ─── Constants ────────────────────────────────────────────────────────────────

const EMAIL_REGEX = /^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$/;

// ─── Validation ───────────────────────────────────────────────────────────────

function validate(values: FormValues): FormErrors {
  const errors: FormErrors = {};

  if (!values.email.trim()) {
    errors.email = 'البريد الإلكتروني مطلوب';
  } else if (!EMAIL_REGEX.test(values.email.trim())) {
    errors.email = 'يرجى إدخال بريد إلكتروني صحيح';
  }

  if (!values.password) {
    errors.password = 'كلمة المرور مطلوبة';
  }

  return errors;
}

// ─── Brand mark (shared between login and register) ───────────────────────────

function BrandMark({ subtitle }: { subtitle: string }) {
  return (
    <div className="flex flex-col items-center gap-2 mb-10">
      <div className="flex items-center justify-center w-11 h-11 rounded-xl bg-[#0f4c3a]/30">
        <svg
          width="22"
          height="22"
          viewBox="0 0 24 24"
          fill="none"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" stroke="#3dba8a" strokeWidth="1.5" />
          <path
            d="M12 7c-.5 0-1 .2-1.4.6L8 10.2c-.4.4-.6.9-.6 1.4v2.8c0 .5.2 1 .6 1.4l2.6 2.6c.4.4.9.6 1.4.6s1-.2 1.4-.6l2.6-2.6c.4-.4.6-.9.6-1.4v-2.8c0-.5-.2-1-.6-1.4L13.4 7.6C13 7.2 12.5 7 12 7z"
            fill="#3dba8a"
            opacity="0.8"
          />
        </svg>
      </div>
      {/* حافظنا على Malaab بالإنجليزية كعلامة تجارية */}
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

  const [form, setForm]           = useState<FormValues>({ email: '', password: '' });
  const [errors, setErrors]       = useState<FormErrors>({});
  const [apiError, setApiError]   = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  // Redirect already-authenticated users away from this page
  useEffect(() => {
    if (!authLoading && user) {
      router.replace('/pitches');
    }
  }, [user, authLoading, router]);

  function handleChange(field: keyof FormValues) {
    return (e: ChangeEvent<HTMLInputElement>) => {
      setForm((prev) => ({ ...prev, [field]: e.target.value }));
      // Clear the field error as soon as the user types
      if (errors[field]) setErrors((prev) => ({ ...prev, [field]: undefined }));
      if (apiError) setApiError(null);
    };
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();

    const validation = validate(form);
    if (Object.keys(validation).length > 0) {
      setErrors(validation);
      return;
    }

    setIsSubmitting(true);
    setApiError(null);

    try {
      const { data } = await api.post<{ message: string; data: AuthApiResponse }>(
        '/auth/login',
        {
          email:    form.email.trim().toLowerCase(),
          password: form.password,
        }
      );

      login(data.data);
      router.push('/pitches');
    } catch (err) {
      if (axios.isAxiosError(err)) {
        setApiError(
          err.response?.data?.message ??
          'حدث خطأ ما. يرجى المحاولة مرة أخرى.'
        );
      } else {
        setApiError('حدث خطأ في الشبكة. يرجى التحقق من اتصالك بالإنترنت.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  // Skeleton while AuthContext resolves
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
        <BrandMark subtitle="تسجيل الدخول إلى حسابك" />

        {/* Form card */}
        <div className="bg-[#1a1c1b] border border-white/[0.07] rounded-2xl p-8">
          <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-5">
            <Input
              label="البريد الإلكتروني"
              type="email"
              placeholder="you@example.com"
              autoComplete="email"
              value={form.email}
              onChange={handleChange('email')}
              error={errors.email}
            />

            <Input
              label="كلمة المرور"
              type="password"
              placeholder="••••••••"
              autoComplete="current-password"
              value={form.password}
              onChange={handleChange('password')}
              error={errors.password}
            />

            {apiError && (
              <div
                role="alert"
                className="rounded-lg px-4 py-3 text-sm text-red-400 bg-red-500/[0.08] border border-red-500/[0.18]"
              >
                {apiError}
              </div>
            )}

            <Button type="submit" fullWidth loading={isSubmitting} className="mt-1 font-bold tracking-wide">
              تسجيل الدخول
            </Button>
          </form>
        </div>

        <p className="text-center text-sm text-white/30 mt-6">
          ليس لديك حساب؟{' '}
          <Link
            href="/register"
            className="text-emerald-500 hover:text-emerald-400 transition-colors duration-150 font-bold"
          >
            إنشاء حساب جديد
          </Link>
        </p>
      </div>
    </main>
  );
}