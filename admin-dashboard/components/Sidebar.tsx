'use client';

import { useEffect, useRef } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { Role } from '@malaab/shared/auth';
import { X } from 'lucide-react';
import { NAV_ITEMS } from '@/lib/nav';

interface SidebarProps {
  role: Role;
  // Mobile drawer state — owned by the dashboard layout. Desktop ignores these
  // (the sidebar is always visible at md+).
  isOpen: boolean;
  onClose: () => void;
}

// Sidebar renders only the nav items visible to the current role. The
// Analytics & Financials item is filtered out for staff here (UX); the route
// guard + backend enforce the actual boundary.
//
// Two presentations from one item list:
//   • Desktop (md+): a persistent right-hand column (hidden below md).
//   • Mobile (<md): an off-canvas drawer that slides in from the RIGHT (RTL)
//     over a dimmed overlay; hidden at md+.
export default function Sidebar({ role, isOpen, onClose }: SidebarProps) {
  const pathname = usePathname();
  const items = NAV_ITEMS.filter((item) => item.visible(role));
  const closeBtnRef = useRef<HTMLButtonElement>(null);

  // Body scroll lock while the mobile drawer is open. Reset on close/unmount so
  // the page never gets stuck unscrollable.
  useEffect(() => {
    document.body.style.overflow = isOpen ? 'hidden' : '';
    return () => { document.body.style.overflow = ''; };
  }, [isOpen]);

  // Minimal focus management: move focus to the ✕ button when the drawer opens.
  useEffect(() => {
    if (isOpen) closeBtnRef.current?.focus();
  }, [isOpen]);

  // Shared item list. onItemClick fires only for the mobile drawer (to close it
  // on navigation); the desktop column passes nothing so clicks don't close it.
  const NavLinks = ({ onItemClick }: { onItemClick?: () => void }) => (
    <>
      {items.map(({ href, label, icon: Icon }) => {
        const active = pathname === href || (href !== '/' && pathname.startsWith(href));
        return (
          <Link
            key={href}
            href={href}
            onClick={onItemClick}
            className={`flex items-center gap-2.5 rounded-xl px-3 min-h-[48px] text-[13px] font-semibold transition-colors ${
              active
                ? 'bg-emerald-500/15 text-emerald-300'
                : 'text-white/55 hover:text-white/85 hover:bg-white/[0.04]'
            }`}
          >
            <Icon size={16} aria-hidden />
            {label}
          </Link>
        );
      })}
    </>
  );

  return (
    <>
      {/* ── Desktop: persistent right column (hidden on mobile) ── */}
      <aside className="hidden md:flex w-60 shrink-0 border-l border-white/[0.07] bg-[#141715] flex-col">
        <div className="h-16 flex items-center gap-2 px-5 border-b border-white/[0.07]">
          <span className="text-[15px] font-bold tracking-tight">ملاعب</span>
          <span className="text-[10px] font-bold tracking-widest text-emerald-400 uppercase">
            Admin
          </span>
        </div>
        <nav className="flex-1 p-3 flex flex-col gap-1">
          <NavLinks />
        </nav>
      </aside>

      {/* ── Mobile: dimmed overlay behind the drawer (hidden on desktop) ── */}
      <div
        aria-hidden="true"
        onClick={onClose}
        className={`fixed inset-0 z-40 bg-black/60 backdrop-blur-sm transition-opacity duration-200 ease-in-out md:hidden ${
          isOpen ? 'opacity-100' : 'opacity-0 pointer-events-none'
        }`}
      />

      {/* ── Mobile: off-canvas drawer, slides in from the RIGHT (RTL) ── */}
      <aside
        role="navigation"
        aria-label="القائمة الرئيسية"
        className={`fixed right-0 top-0 h-full w-64 z-50 bg-[#141715] border-l border-white/[0.07] flex flex-col transition-transform duration-200 ease-in-out md:hidden ${
          isOpen ? 'translate-x-0' : 'translate-x-full'
        }`}
      >
        <div className="h-16 flex items-center justify-between gap-2 px-5 border-b border-white/[0.07]">
          <div className="flex items-center gap-2">
            <span className="text-[15px] font-bold tracking-tight">ملاعب</span>
            <span className="text-[10px] font-bold tracking-widest text-emerald-400 uppercase">
              Admin
            </span>
          </div>
          <button
            ref={closeBtnRef}
            type="button"
            onClick={onClose}
            aria-label="إغلاق القائمة"
            className="flex items-center justify-center w-9 h-9 rounded-lg text-white/55 hover:text-white hover:bg-white/[0.06] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500"
          >
            <X size={18} aria-hidden />
          </button>
        </div>
        <nav className="flex-1 p-3 flex flex-col gap-1 overflow-y-auto">
          <NavLinks onItemClick={onClose} />
        </nav>
      </aside>
    </>
  );
}
