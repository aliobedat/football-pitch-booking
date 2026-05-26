'use client';

import { forwardRef, type ButtonHTMLAttributes } from 'react';
import { cn } from '@/lib/utils';

// ─── Types ────────────────────────────────────────────────────────────────────

type Variant = 'primary' | 'secondary' | 'ghost';
type Size    = 'sm' | 'md' | 'lg';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?:  Variant;
  size?:     Size;
  loading?:  boolean;
  fullWidth?: boolean;
}

// ─── Style maps ───────────────────────────────────────────────────────────────

const variantClasses: Record<Variant, string> = {
  primary:
    'bg-[#0f4c3a] hover:bg-[#1a6b52] active:bg-[#0d4033] ' +
    'text-[#f0efe8] border border-transparent',
  secondary:
    'bg-transparent hover:bg-white/[0.04] active:bg-white/[0.07] ' +
    'text-[#f0efe8] border border-white/[0.12]',
  ghost:
    'bg-transparent hover:bg-white/[0.04] active:bg-white/[0.07] ' +
    'text-white/60 hover:text-white/90 border border-transparent',
};

const sizeClasses: Record<Size, string> = {
  sm: 'h-8  px-3 text-xs  rounded-md gap-1.5',
  md: 'h-10 px-5 text-sm  rounded-lg gap-2',
  lg: 'h-12 px-6 text-sm  rounded-lg gap-2',
};

// ─── Component ────────────────────────────────────────────────────────────────

const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant   = 'primary',
    size      = 'md',
    loading   = false,
    fullWidth = false,
    disabled,
    className,
    children,
    ...props
  },
  ref
) {
  const isDisabled = disabled || loading;

  return (
    <button
      ref={ref}
      disabled={isDisabled}
      aria-busy={loading}
      className={cn(
        // Base
        'inline-flex items-center justify-center',
        'font-medium tracking-wide',
        'transition-all duration-150 ease-in-out',
        'select-none',
        // Focus ring
        'focus-visible:outline-none',
        'focus-visible:ring-2 focus-visible:ring-[#0f4c3a]',
        'focus-visible:ring-offset-2 focus-visible:ring-offset-[#121413]',
        // Disabled
        'disabled:opacity-40 disabled:cursor-not-allowed disabled:pointer-events-none',
        // Variant + size
        variantClasses[variant],
        sizeClasses[size],
        fullWidth && 'w-full',
        className
      )}
      {...props}
    >
      {loading && (
        <svg
          className="animate-spin shrink-0"
          style={{ width: 14, height: 14 }}
          fill="none"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <circle
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            strokeWidth="3"
            strokeOpacity="0.25"
          />
          <path
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
          />
        </svg>
      )}
      {children}
    </button>
  );
});

Button.displayName = 'Button';
export default Button;