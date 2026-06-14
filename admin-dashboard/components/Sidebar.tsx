'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { Role } from '@malaab/shared/auth';
import { NAV_ITEMS } from '@/lib/nav';

// Sidebar renders only the nav items visible to the current role. The
// Analytics & Financials item is filtered out for staff here (UX); the route
// guard + backend enforce the actual boundary.
export default function Sidebar({ role }: { role: Role }) {
  const pathname = usePathname();
  const items = NAV_ITEMS.filter((item) => item.visible(role));

  return (
    <aside className="w-60 shrink-0 border-l border-white/[0.07] bg-[#141715] flex flex-col">
      <div className="h-16 flex items-center gap-2 px-5 border-b border-white/[0.07]">
        <span className="text-[15px] font-bold tracking-tight">ملاعب</span>
        <span className="text-[10px] font-bold tracking-widest text-emerald-400 uppercase">
          Admin
        </span>
      </div>
      <nav className="flex-1 p-3 flex flex-col gap-1">
        {items.map(({ href, label, icon: Icon }) => {
          const active = pathname === href || (href !== '/' && pathname.startsWith(href));
          return (
            <Link
              key={href}
              href={href}
              className={`flex items-center gap-2.5 rounded-xl px-3 py-2.5 text-[13px] font-semibold transition-colors ${
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
      </nav>
    </aside>
  );
}
