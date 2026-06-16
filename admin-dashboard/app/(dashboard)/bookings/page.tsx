'use client';

// Bookings list + cancellation UI — ported (copy-adapt) from the legacy B2C
// dashboard (frontend/app/dashboard/page.tsx: BookingRow, CancelModal, the
// /admin/bookings fetch + stats). The tab/Navbar shell is dropped — the admin app
// has its own sidebar layout, so this is a standalone page.
//
// Wiring is owner-scoped server-side: GET /admin/bookings (owner sees only their
// pitches' bookings) + PATCH /bookings/:id/cancel (PR 8 — confirmed → cancelled).
// State updates ONLY after the server confirms the cancel (no optimistic flip).

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import {
  BookOpen, CheckCircle2, XCircle, CalendarDays, Ban, AlertTriangle,
  SlidersHorizontal, X, Download,
} from 'lucide-react';
import api from '@/lib/api';
import { formatCurrency, formatDate, formatTime } from '@/lib/format';

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type BookingStatus = 'pending' | 'confirmed' | 'cancelled';
type BookingSource = 'player' | 'academy' | 'block' | 'manual';

interface AdminBooking {
  id:          number;
  pitch_id:    number;
  pitch_name:  string;
  player_id:   number | null;
  user_name:   string;
  user_email:  string;
  user_phone:  string;
  guest_name?:  string | null;
  guest_phone?: string | null;
  source:      BookingSource;
  start_time:  string;
  end_time:    string;
  status:      BookingStatus;
  total_price: number;
  payment_status: string; // unpaid | paid_cash
  created_at:  string;
}

const STATUS_CONFIG: Record<BookingStatus, { label: string; badge: string }> = {
  confirmed: { label: 'مؤكد',          badge: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400' },
  pending:   { label: 'قيد الانتظار',  badge: 'bg-amber-500/15 border-amber-500/30 text-amber-400'       },
  cancelled: { label: 'مرفوض',         badge: 'bg-red-500/15 border-red-500/30 text-red-400'             },
};

// Source tag for non-player rows (player rows show no tag — they are the default).
// Mirrors the BlocksModal day-grid colour language: blue manual, amber block.
const SOURCE_TAG: Partial<Record<BookingSource, { label: string; cls: string }>> = {
  manual: { label: 'يدوي',  cls: 'bg-sky-500/15 border-sky-500/30 text-sky-300'     },
  block:  { label: 'محظور', cls: 'bg-amber-500/15 border-amber-500/30 text-amber-300' },
};

// Date/time rendering goes through lib/format so digits stay Latin (0–9) while
// month/weekday names remain Arabic.
const fmtDate = (iso: string) => formatDate(iso);
const fmtTime = (iso: string) => formatTime(iso);

// ─────────────────────────────────────────────────────────────────────────────
// Stat card
// ─────────────────────────────────────────────────────────────────────────────

function StatCard({ icon: Icon, value, label, iconBg, iconColor, valueColor }: {
  icon: React.ElementType; value: number; label: string;
  iconBg: string; iconColor: string; valueColor: string;
}) {
  return (
    <div className="p-5 rounded-2xl bg-[#141715] border border-white/[0.07]">
      <div className={`w-9 h-9 rounded-xl flex items-center justify-center mb-4 ${iconBg}`}>
        <Icon size={16} className={iconColor} aria-hidden />
      </div>
      <p className={`text-[30px] font-bold tracking-tight leading-none mb-1 ${valueColor}`}>{value}</p>
      <p className="text-[12px] text-white/35">{label}</p>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Booking row
// ─────────────────────────────────────────────────────────────────────────────

function BookingRow({
  booking,
  onRequestCancel,
  onTogglePay,
  payingId,
}: {
  booking: AdminBooking;
  onRequestCancel: (booking: AdminBooking) => void;
  onTogglePay: (booking: AdminBooking) => void;
  payingId: number | null;
}) {
  const { label, badge } = STATUS_CONFIG[booking.status] ?? STATUS_CONFIG.confirmed;

  // An owner may only cancel a booking that is still active (pending/confirmed).
  // The backend is the referee — only confirmed → cancelled is permitted; a
  // non-cancellable row is rejected with 409, surfaced in the modal. Already-
  // cancelled rows expose no action.
  const canCancel = booking.status === 'pending' || booking.status === 'confirmed';

  return (
    <tr className="border-b border-white/[0.04] hover:bg-white/[0.018] transition-colors duration-150">
      <td className="px-5 py-4 text-start">
        <span className="text-[12px] font-bold text-white/45 font-mono">
          #{String(booking.id).padStart(4, '0')}
        </span>
      </td>
      <td className="px-5 py-4 text-start">
        {(() => {
          const tag = SOURCE_TAG[booking.source];
          // Manual rows carry a guest; blocks have no party. Player rows use the
          // joined user fields as before.
          const isManual = booking.source === 'manual';
          const isBlock  = booking.source === 'block';
          const name  = isManual ? (booking.guest_name || 'ضيف') : isBlock ? 'صيانة / مغلق' : (booking.user_name || '—');
          const phone = isManual ? (booking.guest_phone || '') : isBlock ? '' : booking.user_phone;
          return (
            <>
              <p className="text-[13px] font-semibold text-[#f0efe8] leading-snug flex items-center gap-1.5">
                <span className="truncate">{name}</span>
                {tag && (
                  <span className={`inline-flex flex-shrink-0 items-center px-1.5 py-0.5 rounded-md text-[9px] font-bold border ${tag.cls}`}>
                    {tag.label}
                  </span>
                )}
              </p>
              {phone
                ? <a
                    href={`tel:${phone}`}
                    dir="ltr"
                    className="inline-block text-[12px] text-emerald-400/80 hover:text-emerald-400 hover:underline mt-0.5 font-mono transition-colors"
                    aria-label={`اتصل بـ ${name} على الرقم ${phone}`}
                  >
                    {phone}
                  </a>
                : !isBlock && <p className="text-[12px] text-white/30 mt-0.5">لا يوجد رقم</p>}
              {!isManual && !isBlock && booking.user_email && (
                <p className="text-[11px] text-white/30 mt-0.5 truncate max-w-[160px]">{booking.user_email}</p>
              )}
            </>
          );
        })()}
      </td>
      <td className="px-5 py-4 text-start">
        <span className="text-[13px] text-white/65">{booking.pitch_name || `ملعب #${booking.pitch_id}`}</span>
      </td>
      <td className="px-5 py-4 text-start whitespace-nowrap">
        <span className="text-[12px] text-white/50">{fmtDate(booking.start_time)}</span>
      </td>
      <td className="px-5 py-4 text-start whitespace-nowrap">
        <span className="text-[12px] text-white/50">
          {fmtTime(booking.start_time)}
          <span className="mx-1 text-white/20">—</span>
          {fmtTime(booking.end_time)}
        </span>
      </td>
      <td className="px-5 py-4 text-start whitespace-nowrap">
        <span className="text-[13px] font-semibold text-[#f0efe8]">{formatCurrency(booking.total_price, { minimumFractionDigits: 2 })}</span>
        <span className="text-[10px] text-emerald-500 ms-1">د.أ</span>
      </td>
      <td className="px-5 py-4 text-start">
        <span className={`inline-flex items-center px-2.5 py-1 rounded-full text-[10px] font-bold tracking-wide border ${badge}`}>
          {label}
        </span>
      </td>
      {/* Cash-settlement toggle (WO-F1) — blocks have no settlement concept. */}
      <td className="px-5 py-4 text-start">
        {booking.source === 'block' ? (
          <span className="text-[12px] text-white/20">—</span>
        ) : (() => {
          const paid = booking.payment_status === 'paid_cash';
          const busy = payingId === booking.id;
          return (
            <button
              type="button"
              onClick={() => onTogglePay(booking)}
              disabled={busy}
              className={[
                'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-[10px] font-bold border transition-all disabled:opacity-50',
                paid
                  ? 'bg-emerald-500/15 border-emerald-500/30 text-emerald-300 hover:bg-emerald-500/20'
                  : 'bg-amber-500/[0.08] border-amber-500/25 text-amber-300/80 hover:bg-amber-500/15',
              ].join(' ')}
              aria-label={paid ? 'إلغاء التحصيل النقدي' : 'تحصيل نقدي'}
            >
              {busy ? '…' : paid ? 'مدفوع نقداً' : 'غير مدفوع'}
            </button>
          );
        })()}
      </td>
      {/* Actions — rendered last so, under dir="rtl", it sits left-most. */}
      <td className="px-5 py-4 text-start">
        {canCancel ? (
          <button
            type="button"
            onClick={() => onRequestCancel(booking)}
            className={[
              'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg',
              'text-[11px] font-semibold tracking-wide',
              'border border-red-500/[0.14] bg-red-500/[0.04] text-red-400/70',
              'hover:bg-red-500/[0.09] hover:border-red-500/30 hover:text-red-400',
              'transition-all duration-200 active:scale-[0.97]',
              'focus-visible:outline-none focus-visible:ring-1',
              'focus-visible:ring-red-500/40 focus-visible:ring-offset-1',
              'focus-visible:ring-offset-[#141715]',
            ].join(' ')}
            aria-label={`إلغاء الحجز رقم ${String(booking.id).padStart(4, '0')}`}
          >
            <Ban size={12} aria-hidden />
            إلغاء
          </button>
        ) : (
          <span className="text-[12px] text-white/20">—</span>
        )}
      </td>
    </tr>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Cancel-booking confirmation modal
//
// Accessibility: role="dialog" + aria-modal, labelled by its title/description.
// Handles Esc to dismiss, backdrop click to dismiss, and a focus trap that keeps
// Tab/Shift+Tab cycling within the dialog. Focus is moved to the dialog on open
// and restored to the trigger on close.
// ─────────────────────────────────────────────────────────────────────────────

function CancelModal({
  booking,
  isCancelling,
  error,
  onConfirm,
  onClose,
}: {
  booking: AdminBooking;
  isCancelling: boolean;
  error: string | null;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const dialogRef  = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  // Move focus into the dialog on open; restore it to the previously focused
  // element (the trigger button) on unmount.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    confirmRef.current?.focus();
    return () => previouslyFocused?.focus();
  }, []);

  // Esc to close + focus trap on Tab. A request in flight locks the dialog.
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (!isCancelling) onClose();
        return;
      }
      if (e.key !== 'Tab') return;

      const focusables = dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      if (!focusables || focusables.length === 0) return;

      const first = focusables[0];
      const last  = focusables[focusables.length - 1];

      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    },
    [isCancelling, onClose],
  );

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      onKeyDown={handleKeyDown}
    >
      {/* Backdrop — click to dismiss (disabled while a request is in flight). */}
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={() => { if (!isCancelling) onClose(); }}
        aria-hidden
      />

      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="cancel-modal-title"
        aria-describedby="cancel-modal-desc"
        className="relative w-full max-w-md rounded-2xl bg-[#141715] border border-white/[0.09] shadow-2xl overflow-hidden"
      >
        {/* Header */}
        <div className="flex items-start gap-3.5 px-6 pt-6 pb-4">
          <div className="w-10 h-10 rounded-xl bg-red-500/[0.08] border border-red-500/20 flex items-center justify-center flex-shrink-0">
            <AlertTriangle size={18} className="text-red-400" aria-hidden />
          </div>
          <div className="min-w-0">
            <h2 id="cancel-modal-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">
              تأكيد إلغاء الحجز
            </h2>
            <p id="cancel-modal-desc" className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
              سيتم إلغاء الحجز رقم{' '}
              <span className="font-mono font-bold text-white/60">
                #{String(booking.id).padStart(4, '0')}
              </span>{' '}
              لـ <span className="text-white/60 font-semibold">{booking.user_name || 'اللاعب'}</span>{' '}
              وإتاحة الموعد من جديد. لا يمكن التراجع عن هذا الإجراء.
            </p>
          </div>
        </div>

        {error && (
          <div className="mx-6 mb-1 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
            {error}
          </div>
        )}

        {/* Actions */}
        <div className="flex items-center justify-end gap-3 px-6 py-5 mt-1 border-t border-white/[0.05] bg-[#111312]">
          <button
            type="button"
            onClick={onClose}
            disabled={isCancelling}
            className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
          >
            تراجع
          </button>
          <button
            ref={confirmRef}
            type="button"
            onClick={onConfirm}
            disabled={isCancelling}
            className={[
              'flex items-center gap-2 px-6 py-2.5 rounded-xl',
              'text-[12px] font-bold',
              'bg-red-500/[0.12] text-red-400 border border-red-500/25',
              'hover:bg-red-500/[0.18] hover:text-red-300 hover:border-red-500/40',
              'disabled:opacity-60 disabled:cursor-not-allowed',
              'transition-all duration-200 active:scale-[0.97]',
            ].join(' ')}
          >
            {isCancelling ? (
              <>
                <span className="w-3.5 h-3.5 rounded-full border-2 border-red-400/50 border-t-transparent animate-spin" aria-hidden />
                جاري الإلغاء...
              </>
            ) : (
              <>
                <Ban size={13} aria-hidden />
                تأكيد الإلغاء
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Page
// ─────────────────────────────────────────────────────────────────────────────

// ── Filters ───────────────────────────────────────────────────────────────
// Status + Amman date-range, applied SERVER-SIDE (query params on the owner-
// scoped GET /admin/bookings). The server is the only filter authority that
// matters for tenancy: owner scoping is enforced in SQL regardless of params, so
// no client-side narrowing can ever widen the result past the owner's own rows.

type StatusFilter = '' | BookingStatus;

interface Filters {
  status: StatusFilter;
  from:   string; // YYYY-MM-DD (Amman calendar day) or ''
  to:     string; // YYYY-MM-DD (Amman calendar day) or ''
}

const EMPTY_FILTERS: Filters = { status: '', from: '', to: '' };

const STATUS_FILTER_OPTIONS: { value: StatusFilter; label: string }[] = [
  { value: '',          label: 'كل الحالات' },
  { value: 'confirmed', label: 'مؤكد'        },
  { value: 'cancelled', label: 'ملغى'        },
  { value: 'pending',   label: 'قيد الانتظار' },
];

// Build the CSV text for the (already owner-scoped, already filtered) rows. The
// export reuses exactly the rows on screen — it never issues an unscoped fetch —
// so it inherits the server's owner scoping. UTF-8 BOM so Excel renders Arabic.
function buildBookingsCsv(rows: AdminBooking[]): string {
  const headers = ['#', 'الاسم', 'الهاتف', 'الملعب', 'التاريخ', 'من', 'إلى', 'المبلغ', 'الحالة', 'الدفع', 'المصدر'];
  const esc = (v: string | number) => {
    const s = String(v ?? '');
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  const lines = rows.map(b => {
    const isManual = b.source === 'manual';
    const isBlock  = b.source === 'block';
    const name  = isManual ? (b.guest_name || 'ضيف') : isBlock ? 'صيانة / مغلق' : (b.user_name || '');
    const phone = isManual ? (b.guest_phone || '') : isBlock ? '' : b.user_phone;
    return [
      b.id, name, phone, b.pitch_name || `#${b.pitch_id}`,
      fmtDate(b.start_time), fmtTime(b.start_time), fmtTime(b.end_time),
      b.total_price, STATUS_CONFIG[b.status]?.label ?? b.status,
      b.payment_status === 'paid_cash' ? 'مدفوع نقداً' : 'غير مدفوع', b.source,
    ].map(esc).join(',');
  });
  return '﻿' + [headers.join(','), ...lines].join('\r\n');
}

export default function BookingsPage() {
  const [bookings, setBookings]             = useState<AdminBooking[]>([]);
  const [loading, setLoading]               = useState(true);
  const [error, setError]                   = useState<string | null>(null);

  // ── filter state ────────────────────────────────────────────────────────
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS);
  const hasActiveFilters = filters.status !== '' || filters.from !== '' || filters.to !== '';

  // ── cancel-booking modal state ──────────────────────────────────────────
  const [cancelTarget, setCancelTarget] = useState<AdminBooking | null>(null);
  const [isCancelling, setIsCancelling] = useState(false);
  const [cancelError,  setCancelError]  = useState<string | null>(null);

  // ── cash-settlement toggle (WO-F1) ──────────────────────────────────────
  const [payingId, setPayingId] = useState<number | null>(null);

  const togglePay = useCallback(async (booking: AdminBooking) => {
    const next = booking.payment_status === 'paid_cash' ? 'unpaid' : 'paid_cash';
    setPayingId(booking.id);
    try {
      await api.patch(`/bookings/${booking.id}/payment`, { payment_status: next });
      setBookings(prev => prev.map(b => (b.id === booking.id ? { ...b, payment_status: next } : b)));
    } catch {
      // leave state unchanged on failure (server is authoritative)
    } finally {
      setPayingId(null);
    }
  }, []);

  useEffect(() => {
    // Server-side filtering: pass only the set params. Owner scoping is applied in
    // SQL irrespective of these, so the result can never cross tenants.
    const params: Record<string, string> = {};
    if (filters.status) params.status = filters.status;
    if (filters.from)   params.from   = filters.from;
    if (filters.to)     params.to     = filters.to;

    setLoading(true);
    setError(null);
    api.get('/admin/bookings', { params })
      .then(res  => setBookings(res.data.data ?? []))
      .catch(()  => setError('تعذّر تحميل البيانات. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, [filters]);

  const clearFilters = useCallback(() => setFilters(EMPTY_FILTERS), []);

  const exportCsv = useCallback(() => {
    const csv  = buildBookingsCsv(bookings);
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
    const url  = URL.createObjectURL(blob);
    const a    = document.createElement('a');
    a.href = url;
    a.download = `bookings-${new Date().toISOString().slice(0, 10)}.csv`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }, [bookings]);

  const openCancelModal = useCallback((booking: AdminBooking) => {
    setCancelError(null);
    setCancelTarget(booking);
  }, []);

  const closeCancelModal = useCallback(() => {
    if (isCancelling) return; // don't dismiss mid-request
    setCancelTarget(null);
    setCancelError(null);
  }, [isCancelling]);

  const confirmCancel = useCallback(async () => {
    if (!cancelTarget) return;
    setIsCancelling(true);
    setCancelError(null);
    try {
      // PATCH /bookings/:id/cancel — the api client attaches the httpOnly cookie
      // JWT + the X-CSRF-Token double-submit header automatically. We update local
      // state ONLY after the server confirms (no optimistic flip): on success the
      // row goes to 'cancelled' and the مؤكدة / ملغاة stat cards re-tally via the
      // `stats` useMemo. A 4xx (404 not-owner/not-found, 409 not-cancellable) is
      // surfaced in the modal and leaves the row untouched.
      await api.patch(`/bookings/${cancelTarget.id}/cancel`);
      setBookings(prev =>
        prev.map(b => (b.id === cancelTarget.id ? { ...b, status: 'cancelled' as BookingStatus } : b)),
      );
      setCancelTarget(null);
    } catch (err: any) {
      setCancelError(
        err?.response?.data?.message ?? 'تعذّر إلغاء الحجز، يرجى المحاولة مجدداً',
      );
    } finally {
      setIsCancelling(false);
    }
  }, [cancelTarget]);

  const stats = useMemo(() => ({
    total:     bookings.length,
    confirmed: bookings.filter(b => b.status === 'confirmed').length,
    cancelled: bookings.filter(b => b.status === 'cancelled').length,
  }), [bookings]);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <h1 className="text-[20px] font-bold tracking-tight">الحجوزات</h1>
        <button
          type="button"
          onClick={exportCsv}
          disabled={loading || bookings.length === 0}
          className="inline-flex items-center gap-2 px-3.5 py-2 rounded-xl text-[12px] font-semibold border border-white/[0.09] bg-white/[0.03] text-white/65 hover:text-white/90 hover:border-white/20 disabled:opacity-40 disabled:cursor-not-allowed transition-all"
        >
          <Download size={14} aria-hidden />
          تصدير CSV
        </button>
      </div>

      {/* Filter bar — server-side status + Amman date-range. Always visible so the
          owner can clear filters even from an empty result. */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.07] p-4 flex flex-col gap-3">
        <div className="flex items-center gap-2 text-white/35">
          <SlidersHorizontal size={13} aria-hidden />
          <span className="text-[11px] font-semibold tracking-widest uppercase">تصفية</span>
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-white/40">الحالة</span>
            <select
              value={filters.status}
              onChange={e => setFilters(f => ({ ...f, status: e.target.value as StatusFilter }))}
              className="bg-[#0f1110] border border-white/[0.09] rounded-lg px-3 py-2 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40 min-w-[140px]"
            >
              {STATUS_FILTER_OPTIONS.map(o => (
                <option key={o.value} value={o.value} className="bg-[#0f1110]">{o.label}</option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-white/40">من تاريخ</span>
            <input
              type="date"
              value={filters.from}
              max={filters.to || undefined}
              onChange={e => setFilters(f => ({ ...f, from: e.target.value }))}
              className="bg-[#0f1110] border border-white/[0.09] rounded-lg px-3 py-2 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40"
              dir="ltr"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] text-white/40">إلى تاريخ</span>
            <input
              type="date"
              value={filters.to}
              min={filters.from || undefined}
              onChange={e => setFilters(f => ({ ...f, to: e.target.value }))}
              className="bg-[#0f1110] border border-white/[0.09] rounded-lg px-3 py-2 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40"
              dir="ltr"
            />
          </label>
          {hasActiveFilters && (
            <button
              type="button"
              onClick={clearFilters}
              className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg text-[12px] font-semibold text-white/50 hover:text-white/80 border border-white/[0.07] hover:border-white/[0.16] transition-all"
            >
              <X size={13} aria-hidden />
              مسح الكل
            </button>
          )}
        </div>
        {hasActiveFilters && (
          <p className="text-[11px] text-emerald-400/70">عوامل التصفية مفعّلة — تُطبّق على الخادم ضمن نطاق ملاعبك فقط.</p>
        )}
      </div>

      {loading ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center">
          <div className="inline-block w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
        </div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>
      ) : (
        <>
          {/* Stats row */}
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
            <StatCard icon={BookOpen}     value={stats.total}     label="إجمالي الحجوزات" iconBg="bg-white/[0.05]"    iconColor="text-white/50"    valueColor="text-[#f0efe8]"   />
            <StatCard icon={CheckCircle2} value={stats.confirmed} label="مؤكدة"            iconBg="bg-emerald-500/10"  iconColor="text-emerald-400" valueColor="text-emerald-400" />
            <StatCard icon={XCircle}      value={stats.cancelled} label="مرفوضة / ملغاة"  iconBg="bg-red-500/[0.08]" iconColor="text-red-400"     valueColor="text-red-400"     />
          </div>

          {bookings.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-24 gap-5">
              <div className="w-20 h-20 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
                <CalendarDays size={28} className="text-white/15" aria-hidden />
              </div>
              <div className="text-center">
                <p className="text-[16px] font-semibold text-white/45 mb-1">
                  {hasActiveFilters ? 'لا توجد حجوزات مطابقة' : 'لا توجد حجوزات بعد'}
                </p>
                <p className="text-[13px] text-white/25">
                  {hasActiveFilters ? 'جرّب تغيير عوامل التصفية أو مسحها' : 'ستظهر هنا الحجوزات الواردة على ملاعبك'}
                </p>
              </div>
            </div>
          ) : (
            <div className="rounded-2xl border border-white/[0.07] overflow-hidden">
              <div className="px-5 py-3.5 border-b border-white/[0.06] bg-[#0f1110] flex items-center justify-between">
                <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase">الحجوزات الواردة</p>
                <p className="text-[11px] text-white/25">{bookings.length} {bookings.length === 1 ? 'حجز' : 'حجوزات'}</p>
              </div>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[860px] bg-[#141715]">
                  <thead>
                    <tr className="border-b border-white/[0.06] bg-[#111312]">
                      {['# الحجز', 'اللاعب', 'الملعب', 'التاريخ', 'الوقت', 'المبلغ', 'الحالة', 'الدفع', 'الإجراءات'].map(col => (
                        <th key={col} className="px-5 py-3.5 text-start text-[10px] font-semibold text-white/30 tracking-widest uppercase whitespace-nowrap">
                          {col}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {bookings.map(b => (
                      <BookingRow key={b.id} booking={b} onRequestCancel={openCancelModal} onTogglePay={togglePay} payingId={payingId} />
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}

      {cancelTarget && (
        <CancelModal
          booking={cancelTarget}
          isCancelling={isCancelling}
          error={cancelError}
          onConfirm={confirmCancel}
          onClose={closeCancelModal}
        />
      )}
    </div>
  );
}
