'use client';

// Just-In-Time full-name capture. A controlled, presentational field plus two
// helpers shared by every place that may need to collect a missing name (booking
// checkout, review submission). It performs NO navigation and owns no network
// call itself beyond the explicit `saveFullName` helper, so callers stay in
// control of ordering (e.g. "save name THEN book").

import api from '@/lib/api';

// Validation mirrors the backend (PATCH /me): trimmed, 2–100 characters counted
// by code point so Arabic/RTL names are measured fairly. [...str] iterates by
// rune, matching Go's utf8.RuneCountInString.
export function isValidFullName(raw: string): boolean {
  const n = [...raw.trim()].length;
  return n >= 2 && n <= 100;
}

// saveFullName persists the name via PATCH /me and returns the updated profile.
// Throws on validation/network failure so the caller can abort its own flow.
export async function saveFullName(raw: string): Promise<void> {
  const full_name = raw.trim();
  if (!isValidFullName(full_name)) {
    throw new Error('invalid_full_name');
  }
  await api.patch('/me', { full_name });
}

interface FullNameFieldProps {
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  // Show the inline validation hint only after the user has interacted/submitted.
  showError?: boolean;
  id?: string;
}

export default function FullNameField({
  value,
  onChange,
  disabled,
  showError,
  id = 'full-name',
}: FullNameFieldProps) {
  const invalid = showError && !isValidFullName(value);

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-[11px] font-bold text-white/40 tracking-wide">
        الاسم الكامل <span className="text-emerald-500">*</span>
      </label>
      <input
        id={id}
        type="text"
        dir="rtl"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        placeholder="مثال: أحمد خالد"
        aria-invalid={invalid || undefined}
        className={[
          'w-full rounded-xl px-4 py-2.5 bg-[#0d0f0e] text-[13px] text-[#f0efe8]',
          'border transition-all duration-150 focus:outline-none',
          'placeholder:text-white/20 disabled:opacity-50',
          invalid
            ? 'border-red-500/50 focus:ring-1 focus:ring-red-500/30'
            : 'border-white/[0.09] hover:border-white/[0.18] focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15',
        ].join(' ')}
      />
      {invalid && (
        <span className="text-[11px] text-red-400/80">
          الاسم يجب أن يكون بين حرفين و100 حرف
        </span>
      )}
    </div>
  );
}
