'use client';

import { forwardRef, type InputHTMLAttributes } from 'react';
import { cn } from '@/lib/utils';

// ─── Types ────────────────────────────────────────────────────────────────────

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  hint?:  string;
}

// ─── Component ────────────────────────────────────────────────────────────────

const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { label, error, hint, className, id, ...props },
  ref
) {
  // Derive a stable id for label–input association
  const inputId = id ?? (label ? `field-${label.toLowerCase().replace(/\s+/g, '-')}` : undefined);
  const errorId = inputId ? `${inputId}-error` : undefined;
  const hintId  = inputId ? `${inputId}-hint`  : undefined;

  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label
          htmlFor={inputId}
          className="text-[10px] font-medium tracking-widest uppercase text-white/45"
        >
          {label}
        </label>
      )}

      <input
        ref={ref}
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={
          error   ? errorId :
          hint    ? hintId  :
          undefined
        }
        className={cn(
          'h-11 w-full rounded-lg px-4',
          'bg-[#111312] text-[#f0efe8] text-sm',
          'placeholder:text-white/20',
          'border transition-all duration-150 ease-in-out',
          'outline-none',
          // Focus ring & border color
          error
            ? [
                'border-red-500/50',
                'focus:border-red-500',
                'focus:ring-1 focus:ring-red-500/25',
              ]
            : [
                'border-white/[0.08]',
                'hover:border-white/[0.14]',
                'focus:border-[#0f4c3a]/80',
                'focus:ring-1 focus:ring-[#0f4c3a]/20',
              ],
          // Autofill kill: Chrome overrides bg — reset it
          '[&:-webkit-autofill]:bg-[#111312]',
          '[&:-webkit-autofill]:shadow-[inset_0_0_0px_1000px_#111312]',
          '[&:-webkit-autofill]:[-webkit-text-fill-color:#f0efe8]',
          className
        )}
        {...props}
      />

      {error && (
        <p
          id={errorId}
          role="alert"
          className="text-xs text-red-400 leading-snug"
        >
          {error}
        </p>
      )}

      {hint && !error && (
        <p id={hintId} className="text-xs text-white/25 leading-snug">
          {hint}
        </p>
      )}
    </div>
  );
});

Input.displayName = 'Input';
export default Input;