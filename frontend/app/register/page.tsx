'use client';

import {
  useState,
  useEffect,
  useMemo,
  type FormEvent,
  type ChangeEvent,
} from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import axios from 'axios';
import { useAuth } from '@/context/AuthContext';
import api from '@/lib/api';
import Button from '@/components/ui/Button';
import Input from '@/components/ui/Input';
import { cn } from '@/lib/utils';

// ─── Types ────────────────────────────────────────────────────────────────────

type Role = 'player' | 'owner';

interface AuthApiResponse {
  access_token:       string;
  refresh_token:      string;
  expires_in_seconds: number;
  user: {
    id:         number;
    full_name:  string;
    email:      string;
    role:       Role;
    created_at: string;
  };
}

interface FormValues {
  fullName: string;
  email:    string;
  password: string;
  phone:    string;
  role:     Role;
}

interface FormErrors {
  fullName?: string;
  email?:    string;
  password?: string;
}

// ─── Constants ────────────────────────────────────────────────────────────────

const EMAIL_REGEX = /^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$/;

interface PasswordRule {
  label: string;
  test:  (pw: string) => boolean;
}

const PASSWORD_RULES: PasswordRule[] = [
  { label: '8 أحرف على الأقل',  test: (pw) => pw.length >= 8 },
  { label: 'حرف كبير واحد (A-Z)',   test: (pw) => /[A-Z]/.test(pw) },
  { label: 'حرف صغير واحد (a-z)',   test: (pw) => /[a-z]/.test(pw) },
  { label: 'رقم واحد على الأقل',    test: (pw) => /\d/.test(pw) },
];

const ROLE_OPTIONS: { value: Role; label: string; description: string }[] = [
  { value: 'player', label: 'لاعب',      description: 'ابحث واحجز الملاعب' },
  { value: 'owner',  label: 'صاحب ملعب', description: 'أضف وأدر ملاعبك' },
];

// ─── Validation ───────────────────────────────────────────────────────────────

function validate(values: FormValues): FormErrors {
  const errors: FormErrors = {};

  const name = values.fullName.trim();
  if (!name) {
    errors.fullName = 'الاسم الكامل مطلوب';
  } else if (name.length < 2) {
    errors.fullName = 'الاسم يجب أن يتكون من حرفين على الأقل';
  } else if (name.length > 100) {
    errors.fullName = 'الاسم يجب أن لا يتجاوز 100 حرف';
  }

  if (!values.email.trim()) {
    errors.email = 'البريد الإلكتروني مطلوب';
  } else if (!EMAIL_REGEX.test(values.email.trim())) {
    errors.email = 'يرجى إدخال بريد إلكتروني صحيح';
  }

  const failedRules = PASSWORD_RULES.filter((r) => !r.test(values.password));
  if (failedRules.length > 0) {
    errors.password = 'كلمة المرور لا تستوفي جميع الشروط أدناه';
  }

  return errors;
}

// ─── Sub-components ───────────────────────────────────────────────────────────

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

// Role selector — ARIA radiogroup pattern for accessibility
function RoleToggle({
  value,
  onChange,
}: {
  value: Role;
  onChange: (role: Role) => void;
}) {
  return (
    <div className="flex flex-col gap-2">
      <span
        id="role-group-label"
        className="text-[10px] font-bold tracking-wide text-white/45"
      >
        أنا
      </span>
      <div
        role="radiogroup"
        aria-labelledby="role-group-label"
        className="grid grid-cols-2 gap-2"
      >
        {ROLE_OPTIONS.map((opt) => {
          const isSelected = value === opt.value;
          return (
            <button
              key={opt.value}
              type="button"
              role="radio"
              aria-checked={isSelected}
              onClick={() => onChange(opt.value)}
              className={cn(
                'flex flex-col items-start px-4 py-3 rounded-lg border text-start',
                'transition-all duration-150',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#0f4c3a]',
                isSelected
                  ? 'bg-[#0f4c3a]/20 border-[#0f4c3a]/60'
                  : 'bg-transparent border-white/[0.08] hover:border-white/[0.16]'
              )}
            >
              <span
                className={cn(
                  'text-sm font-bold transition-colors duration-150',
                  isSelected ? 'text-[#f0efe8]' : 'text-white/50'
                )}
              >
                {opt.label}
              </span>
              <span
                className={cn(
                  'text-[11px] mt-0.5 transition-colors duration-150',
                  isSelected ? 'text-white/45' : 'text-white/22'
                )}
              >
                {opt.description}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

// Real-time password requirement checklist
function PasswordStrength({ password }: { password: string }) {
  return (
    <ul
      aria-label="شروط كلمة المرور"
      className="flex flex-col gap-1.5 pt-0.5"
    >
      {PASSWORD_RULES.map((rule) => {
        const passing = rule.test(password);
        return (
          <li
            key={rule.label}
            className={cn(
              'flex items-center gap-2 text-[11px] font-medium transition-colors duration-200',
              passing ? 'text-emerald-500' : 'text-white/28'
            )}
          >
            {/* Indicator dot / checkmark */}
            <span
              className={cn(
                'flex-shrink-0 flex items-center justify-center',
                'w-3.5 h-3.5 rounded-full transition-colors duration-200',
                passing ? 'bg-emerald-500/20' : 'bg-white/[0.06]'
              )}
              aria-hidden="true"
            >
              {passing ? (
                <svg width="8" height="8" viewBox="0 0 8 8" fill="none">
                  <path
                    d="M1.5 4L3 5.5L6.5 2.5"
                    stroke="#10b981"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              ) : (
                <span className="w-1 h-1 rounded-full bg-white/20" />
              )}
            </span>
            {rule.label}
          </li>
        );
      })}
    </ul>
  );
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function RegisterPage() {
  const router = useRouter();
  const { user, isLoading: authLoading, login } = useAuth();

  const [form, setForm] = useState<FormValues>({
    fullName: '',
    email:    '',
    password: '',
    phone:    '',
    role:     'player',
  });
  const [errors, setErrors]             = useState<FormErrors>({});
  const [apiError, setApiError]         = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  // Show password strength checklist once the user focuses the field
  const [pwFocused, setPwFocused]       = useState(false);

  // Redirect already-authenticated users
  useEffect(() => {
    if (!authLoading && user) {
      router.replace('/pitches');
    }
  }, [user, authLoading, router]);

  // True when every password rule passes — used to hide errors when met
  const allPasswordRulesMet = useMemo(
    () => PASSWORD_RULES.every((r) => r.test(form.password)),
    [form.password]
  );

  // Generic field change handler
  function handleChange(field: keyof Omit<FormValues, 'role'>) {
    return (e: ChangeEvent<HTMLInputElement>) => {
      setForm((prev) => ({ ...prev, [field]: e.target.value }));
      if (field in errors) {
        setErrors((prev) => ({ ...prev, [field]: undefined }));
      }
      if (apiError) setApiError(null);
    };
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setPwFocused(true); // Ensure requirements are visible on submit attempt

    const validation = validate(form);
    if (Object.keys(validation).length > 0) {
      setErrors(validation);
      return;
    }

    setIsSubmitting(true);
    setApiError(null);

    try {
      // Build payload — only include phone when non-empty
      const payload: Record<string, string> = {
        full_name: form.fullName.trim(),
        email:     form.email.trim().toLowerCase(),
        password:  form.password,
        role:      form.role,
      };
      if (form.phone.trim()) {
        payload.phone = form.phone.trim();
      }

      const { data } = await api.post<{ message: string; data: AuthApiResponse }>(
        '/auth/register',
        payload
      );

      login(data.data);
      router.push('/pitches');
    } catch (err) {
      if (axios.isAxiosError(err)) {
        setApiError(
          err.response?.data?.message ??
          'فشل إنشاء الحساب. يرجى المحاولة مرة أخرى.'
        );
      } else {
        setApiError('حدث خطأ في الشبكة. يرجى التحقق من اتصالك بالإنترنت.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

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
    <main dir="rtl" className="min-h-screen bg-[#121413] flex items-center justify-center px-4 py-14">
      <div className="w-full max-w-sm">
        <BrandMark subtitle="انضم إلى Malaab اليوم" />

        {/* Form card */}
        <div className="bg-[#1a1c1b] border border-white/[0.07] rounded-2xl p-8">
          <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-5">
            {/* Role selector */}
            <RoleToggle
              value={form.role}
              onChange={(role) => setForm((prev) => ({ ...prev, role }))}
            />

            <div className="h-px bg-white/[0.06]" />

            {/* Personal details */}
            <Input
              label="الاسم الكامل"
              type="text"
              placeholder="أحمد الحسن"
              autoComplete="name"
              value={form.fullName}
              onChange={handleChange('fullName')}
              error={errors.fullName}
            />

            <Input
              label="البريد الإلكتروني"
              type="email"
              placeholder="you@example.com"
              autoComplete="email"
              value={form.email}
              onChange={handleChange('email')}
              error={errors.email}
            />

            {/* Password + strength checklist */}
            <div className="flex flex-col gap-2">
              <Input
                label="كلمة المرور"
                type="password"
                placeholder="••••••••"
                autoComplete="new-password"
                value={form.password}
                onChange={handleChange('password')}
                // Show error message only when not all rules are met,
                // so it disappears once requirements are satisfied
                error={errors.password && !allPasswordRulesMet ? errors.password : undefined}
                onFocus={() => setPwFocused(true)}
              />
              {/* Show checklist once focused or on submit attempt */}
              {(pwFocused || errors.password) && (
                <PasswordStrength password={form.password} />
              )}
            </div>

            <Input
              label="رقم الهاتف (اختياري)"
              type="tel"
              placeholder="+962 7X XXX XXXX"
              autoComplete="tel"
              value={form.phone}
              onChange={handleChange('phone')}
              hint="يستخدم لإرسال إشعارات الحجوزات"
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
              إنشاء الحساب
            </Button>
          </form>
        </div>

        <p className="text-center text-sm text-white/30 mt-6">
          لديك حساب بالفعل؟{' '}
          <Link
            href="/login"
            className="text-emerald-500 hover:text-emerald-400 transition-colors duration-150 font-bold"
          >
            تسجيل الدخول
          </Link>
        </p>
      </div>
    </main>
  );
}