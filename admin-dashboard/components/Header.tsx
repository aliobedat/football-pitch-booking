'use client';

import { LogOut } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';

const ROLE_LABEL: Record<string, string> = {
  staff: 'موظف',
  owner: 'مالك',
  admin: 'مشرف',
  super_admin: 'مشرف عام',
};

export default function Header() {
  const { user, logout } = useAuth();

  return (
    <header className="h-16 shrink-0 border-b border-white/[0.07] bg-[#141715] flex items-center justify-between px-6">
      <div className="flex flex-col">
        <span className="text-[13px] font-bold">{user?.full_name ?? '—'}</span>
        <span className="text-[11px] text-emerald-300/80">
          {user ? ROLE_LABEL[user.role] ?? user.role : ''}
        </span>
      </div>
      <button
        type="button"
        onClick={() => logout()}
        className="inline-flex items-center gap-1.5 text-[12.5px] font-semibold text-white/45 hover:text-white/80 transition-colors"
      >
        <LogOut size={14} aria-hidden />
        تسجيل الخروج
      </button>
    </header>
  );
}
