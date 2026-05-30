'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useAuth } from '@/context/AuthContext';
import { Users, Plus } from 'lucide-react';

export default function Navbar() {
  const pathname = usePathname();
  const { user, logout } = useAuth();
  const isOwner = user?.role === 'owner' || user?.role === 'admin';

  const navLinks = isOwner
    ? [{ href: '/dashboard', label: 'لوحة التحكم' }]
    : [
        { href: '/pitches',  label: 'ملاعبنا'  },
        { href: '/bookings', label: 'حجوزاتي'  },
      ];

  const brandHref = isOwner ? '/dashboard' : '/pitches';

  return (
    <header className="sticky top-0 z-50 border-b border-white/[0.06] bg-[#0d0f0e]/90 backdrop-blur-md">
      <nav
        className="max-w-7xl mx-auto px-6 h-16 flex items-center justify-between"
        aria-label="التنقل الرئيسي"
      >
        {/* Brand */}
        <Link
          href={brandHref}
          className="flex items-center gap-2.5 group focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 rounded-md"
        >
          <span className="text-[15px] font-bold text-[#f0efe8] tracking-tight">ملاعب</span>
          <div className="w-2 h-2 rounded-full bg-emerald-500 group-hover:scale-110 transition-transform duration-200" />
        </Link>

        {/* Centre links */}
        <div className="hidden md:flex items-center gap-8">
          {navLinks.map((link) => {
            const isActive = pathname === link.href || pathname.startsWith(link.href + '/');
            return (
              <Link
                key={link.href}
                href={link.href}
                className={[
                  'text-[13px] transition-colors duration-150',
                  isActive
                    ? 'text-emerald-500 font-semibold'
                    : 'text-white/40 hover:text-white/70',
                ].join(' ')}
              >
                {link.label}
              </Link>
            );
          })}
        </div>

        {/* Right side — actions */}
        <div className="flex items-center gap-3">
          {user ? (
            <div className="flex items-center gap-3">
              {/* Owner-only: Add Pitch button */}
              {isOwner && (
                <Link
                  href="/dashboard?tab=pitches&action=add"
                  className={[
                    'hidden sm:flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg',
                    'text-[12px] font-semibold',
                    'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20',
                    'hover:bg-emerald-500/20 hover:text-emerald-300 hover:border-emerald-500/40',
                    'transition-all duration-200 active:scale-[0.97]',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
                  ].join(' ')}
                >
                  <Plus size={12} aria-hidden="true" />
                  إضافة ملعب
                </Link>
              )}

              {/* Greeting chip */}
              <div className="flex items-center gap-2 text-emerald-400 bg-emerald-500/10 px-3 py-1.5 rounded-full border border-emerald-500/20">
                <Users size={12} aria-hidden="true" />
                <span className="text-[12px] font-bold tracking-wide">
                  أهلاً، {user.full_name ? user.full_name.split(' ')[0] : 'كابتن'}
                </span>
              </div>

              <button
                onClick={logout}
                className="text-[11px] text-white/40 hover:text-red-400 transition-colors duration-150 font-semibold"
              >
                تسجيل خروج
              </button>
            </div>
          ) : (
            <>
              <Link
                href="/login"
                className="hidden sm:block text-[12px] text-white/40 hover:text-white/70 transition-colors duration-150"
              >
                تسجيل الدخول
              </Link>
              <Link
                href="/register"
                className={[
                  'flex items-center gap-2 px-4 py-2 rounded-lg',
                  'text-[12px] font-semibold',
                  'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
                  'hover:bg-[#1a6b52] hover:text-emerald-300 transition-all duration-200',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
                ].join(' ')}
              >
                ابدأ الآن
              </Link>
            </>
          )}
        </div>
      </nav>
    </header>
  );
}
