'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useState } from 'react';
import { useAuth } from '@/context/AuthContext';
import { Users, Menu, X } from 'lucide-react';

// The separate admin app (pitch owners/staff manage pitches there). A passive,
// logic-free link points owners to it — the B2C app itself is player-only.
const ADMIN_URL = process.env.NEXT_PUBLIC_ADMIN_URL || 'http://localhost:3001';

export default function Navbar() {
  const pathname = usePathname();
  const { user, logout } = useAuth();
  const [menuOpen, setMenuOpen] = useState(false);

  // B2C is player-facing only: the nav is role-agnostic (no owner/admin management
  // links). Every visitor — including an owner-role account — sees the player nav.
  const navLinks = [
    { href: '/pitches',  label: 'ملاعبنا'  },
    { href: '/bookings', label: 'حجوزاتي'  },
  ];

  const brandHref = '/pitches';

  const closeMenu = () => setMenuOpen(false);

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
          onClick={closeMenu}
        >
          <span className="text-[15px] font-bold text-[#f0efe8] tracking-tight">ملاعب</span>
          <div className="w-2 h-2 rounded-full bg-emerald-500 group-hover:scale-110 transition-transform duration-200" />
        </Link>

        {/* Centre links — desktop only */}
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
          {/* Passive, logic-free pointer to the separate admin app for pitch owners. */}
          <a
            href={ADMIN_URL}
            className="hidden sm:inline text-[12px] text-white/35 hover:text-emerald-400 transition-colors duration-150"
          >
            صاحب ملعب؟ أدر ملاعبك →
          </a>
          {user ? (
            <div className="flex items-center gap-3">
              {/* Greeting chip */}
              <div className="flex items-center gap-2 text-emerald-400 bg-emerald-500/10 px-3 py-1.5 rounded-full border border-emerald-500/20">
                <Users size={12} aria-hidden="true" />
                <span className="text-[12px] font-bold tracking-wide">
                  أهلاً، {user.full_name ? user.full_name.split(' ')[0] : 'كابتن'}
                </span>
              </div>

              <button
                onClick={logout}
                className="hidden md:block text-[11px] text-white/40 hover:text-red-400 transition-colors duration-150 font-semibold"
              >
                تسجيل خروج
              </button>
            </div>
          ) : (
            // Phone-first: a single OTP entry point. The first verification both
            // signs in and creates the account, so there is no separate register.
            <Link
              href="/login"
              onClick={closeMenu}
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
          )}

          {/* Hamburger — mobile only */}
          <button
            type="button"
            aria-label={menuOpen ? 'إغلاق القائمة' : 'فتح القائمة'}
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((v) => !v)}
            className="md:hidden flex items-center justify-center w-9 h-9 rounded-lg text-white/60 hover:text-white hover:bg-white/[0.06] transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500"
          >
            {menuOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>
      </nav>

      {/* Mobile dropdown */}
      {menuOpen && (
        <div className="md:hidden border-t border-white/[0.06] bg-[#0d0f0e]/95 backdrop-blur-md">
          <div className="max-w-7xl mx-auto px-6 py-4 flex flex-col gap-1">
            {navLinks.map((link) => {
              const isActive = pathname === link.href || pathname.startsWith(link.href + '/');
              return (
                <Link
                  key={link.href}
                  href={link.href}
                  onClick={closeMenu}
                  className={[
                    'px-3 py-3 rounded-lg text-[14px] font-semibold transition-colors duration-150',
                    isActive
                      ? 'text-emerald-400 bg-emerald-500/10'
                      : 'text-white/60 hover:text-white hover:bg-white/[0.05]',
                  ].join(' ')}
                >
                  {link.label}
                </Link>
              );
            })}

            {/* Admin link — also surface on mobile */}
            <a
              href={ADMIN_URL}
              onClick={closeMenu}
              className="px-3 py-3 rounded-lg text-[13px] text-white/35 hover:text-emerald-400 transition-colors duration-150"
            >
              صاحب ملعب؟ أدر ملاعبك →
            </a>

            {/* Logout in mobile menu */}
            {user && (
              <button
                onClick={() => { closeMenu(); logout(); }}
                className="mt-1 px-3 py-3 rounded-lg text-right text-[13px] text-white/40 hover:text-red-400 transition-colors duration-150 font-semibold"
              >
                تسجيل خروج
              </button>
            )}
          </div>
        </div>
      )}
    </header>
  );
}
