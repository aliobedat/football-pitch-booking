'use client';

import { useEffect, useState } from 'react';
import { usePathname, useRouter } from 'next/navigation';
import { canViewFinance, isDashboardRole } from '@malaab/shared/auth';
import { useAuth } from '@/context/AuthContext';
import { isFinanceRoute } from '@/lib/nav';
import Sidebar from '@/components/Sidebar';
import Header from '@/components/Header';

// The single unified (dashboard) shell for admin/owner/staff/super_admin. No
// separate owner/staff folders — visibility is role-driven inside one tree.
//
// Two layers of UX protection (the Go backend is the real boundary):
//   • Sidebar hides items the role can't use.
//   • This route-level guard redirects a staff user who deep-links a finance
//     route, instead of rendering a page the backend will 403.
export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const { user, isLoading } = useAuth();
  const pathname = usePathname();
  const router = useRouter();
  // Mobile drawer open/closed. Desktop ignores it (sidebar is persistent at md+).
  const [isOpen, setIsOpen] = useState(false);

  // Close the drawer whenever the route changes (covers any nav not routed
  // through the drawer's own onClick).
  useEffect(() => { setIsOpen(false); }, [pathname]);

  useEffect(() => {
    if (isLoading) return;
    // No session / non-dashboard role: middleware normally catches this, but
    // guard client-side too for direct client transitions.
    if (!user || !isDashboardRole(user.role)) {
      router.replace('/login');
      return;
    }
    // Staff deep-linking finance/analytics → clean redirect to overview.
    if (isFinanceRoute(pathname) && !canViewFinance(user.role)) {
      router.replace('/');
    }
  }, [user, isLoading, pathname, router]);

  if (isLoading || !user || !isDashboardRole(user.role)) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div
          className="w-5 h-5 rounded-full border-2 border-emerald-600 border-t-transparent animate-spin"
          aria-hidden
        />
      </div>
    );
  }

  return (
    <div className="min-h-screen flex">
      <Sidebar role={user.role} isOpen={isOpen} onClose={() => setIsOpen(false)} />
      <div className="flex-1 flex flex-col min-w-0">
        <Header isOpen={isOpen} onOpen={() => setIsOpen(true)} onClose={() => setIsOpen(false)} />
        <main className="flex-1 p-6 overflow-auto">{children}</main>
      </div>
    </div>
  );
}
