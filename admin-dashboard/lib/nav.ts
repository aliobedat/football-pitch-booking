import type { Role } from '@malaab/shared/auth';
import { canViewFinance } from '@malaab/shared/auth';
import { LayoutDashboard, CalendarCheck, MapPin, BarChart3, type LucideIcon } from 'lucide-react';

export interface NavItem {
  href: string;
  label: string;
  icon: LucideIcon;
  // visible decides UX rendering only. The Go backend remains the boundary.
  visible: (role: Role) => boolean;
}

// Single source of truth for sidebar items + their role visibility.
// Analytics & Financials renders only for owner/admin/super_admin — hidden for
// staff. (super_admin's global platform sections land here later as admin-only
// items in this same shell.)
export const NAV_ITEMS: NavItem[] = [
  { href: '/', label: 'نظرة عامة', icon: LayoutDashboard, visible: () => true },
  { href: '/bookings', label: 'الحجوزات', icon: CalendarCheck, visible: () => true },
  { href: '/pitches', label: 'الملاعب', icon: MapPin, visible: () => true },
  {
    href: '/analytics',
    label: 'التحليلات والمالية',
    icon: BarChart3,
    visible: (role) => canViewFinance(role),
  },
];

// Routes gated to finance-capable roles. Used by the route-level guard so a
// staff user deep-linking here is redirected cleanly instead of rendered into a
// page the backend will 403.
export const FINANCE_ROUTES = ['/analytics'];

export function isFinanceRoute(pathname: string): boolean {
  return FINANCE_ROUTES.some((r) => pathname === r || pathname.startsWith(`${r}/`));
}
