'use client';

import { LogOut, Menu } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';

const ROLE_LABEL: Record<string, string> = {
  staff: 'موظف',
  owner: 'مالك',
  admin: 'مشرف',
  super_admin: 'مشرف عام',
};

interface HeaderProps {
  // Mobile drawer controls — owned by the dashboard layout. The hamburger is
  // hidden at md+ (the sidebar is persistent there).
  isOpen: boolean;
  onOpen: () => void;
  onClose: () => void;
}

export default function Header({ isOpen, onOpen, onClose }: HeaderProps) {
  const { user, logout } = useAuth();

  return (
    <header className="h-16 shrink-0 border-b border-white/[0.07] bg-[#141715] flex items-center justify-between px-6">
      <div className="flex flex-col">
        <span className="text-[13px] font-bold">{user?.full_name ?? '—'}</span>
        <span className="text-[11px] text-emerald-300/80">
          {user ? ROLE_LABEL[user.role] ?? user.role : ''}
        </span>
      </div>
      <div className="flex items-center gap-4">
        <button
          type="button"
          onClick={() => logout()}
          className="inline-flex items-center gap-1.5 text-[12.5px] font-semibold text-white/45 hover:text-white/80 transition-colors"
        >
          <LogOut size={14} aria-hidden />
          تسجيل الخروج
        </button>
        {/* Hamburger sits on the LEFT (RTL — opposite the right-hand drawer).
            Mobile only; the sidebar is persistent at md+. */}
        <button
          type="button"
          onClick={() => (isOpen ? onClose() : onOpen())}
          aria-label={isOpen ? 'إغلاق القائمة' : 'فتح القائمة'}
          aria-expanded={isOpen}
          className="md:hidden flex items-center justify-center w-9 h-9 rounded-lg text-white/55 hover:text-white hover:bg-white/[0.06] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500"
        >
          <Menu size={20} aria-hidden />
        </button>
      </div>
    </header>
  );
}
