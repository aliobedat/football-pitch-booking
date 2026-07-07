import type { Role } from '@malaab/shared/auth';
import { canViewFinance } from '@malaab/shared/auth';
import { LayoutDashboard, CalendarCheck, CalendarRange, CalendarClock, MapPin, BarChart3, ClipboardList, Users, UserCog, FileText, type LucideIcon } from 'lucide-react';

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
  // Staff are confined to جدول اليوم + الحجوزات (V1); overview/pitches are hidden
  // for them in the sidebar and enforced by the layout confinement guard.
  { href: '/', label: 'نظرة عامة', icon: LayoutDashboard, visible: (role) => role !== 'staff' },
  { href: '/schedule', label: 'جدول اليوم', icon: ClipboardList, visible: () => true },
  { href: '/bookings', label: 'الحجوزات', icon: CalendarCheck, visible: (role) => role === 'staff' },
  { href: '/calendar', label: 'التقويم', icon: CalendarRange, visible: (role) => canViewFinance(role) },
  // الدفتر — single-pitch day-view timeline (owner/admin; backed by /owner/day-view,
  // which excludes staff). Distinct label from جدول اليوم (/schedule) so the two
  // never read as the same page.
  { href: '/day-view', label: 'جدول الملعب', icon: CalendarClock, visible: (role) => canViewFinance(role) },
  { href: '/pitches', label: 'الملاعب', icon: MapPin, visible: (role) => role !== 'staff' },
  {
    href: '/customers',
    label: 'الزبائن',
    icon: Users,
    visible: (role) => canViewFinance(role),
  },
  {
    href: '/analytics',
    label: 'التحليلات والمالية',
    icon: BarChart3,
    visible: (role) => canViewFinance(role),
  },
  // التقارير — read-only financial statements over an explicit Amman window
  // (WO-REPORTS-R2). Sibling of التحليلات والمالية, never an edit to it; backed
  // by /owner/reports/* which excludes staff (RequireRole owner/admin).
  {
    href: '/reports',
    label: 'التقارير',
    icon: FileText,
    visible: (role) => canViewFinance(role),
  },
  {
    href: '/staff',
    label: 'الموظفون',
    icon: UserCog,
    visible: (role) => canViewFinance(role),
  },
];

// Routes gated to owner/admin (finance-capable) roles. Used by the route-level
// guard so a staff user deep-linking here is redirected cleanly instead of
// rendered into a page the backend will 403. The CRM (/customers) is owner-only,
// the same boundary the backend enforces with RequireRole("owner","admin").
export const FINANCE_ROUTES = ['/analytics', '/customers', '/calendar', '/day-view', '/reports', '/staff'];

export function isFinanceRoute(pathname: string): boolean {
  return FINANCE_ROUTES.some((r) => pathname === r || pathname.startsWith(`${r}/`));
}
