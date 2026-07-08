'use client';

// Booking details bottom sheet (WO-BOOKING-SHEET / PR-B, generalized in
// PR-B.2b for Day View + جدول اليوم). Opens on a booked, non-block row:
// payment tracking (amount_paid source of truth) + booking extension. Consumes
// the frozen PR-A backend contract:
//   PATCH /bookings/:id/payment  { amount_paid: number|null, total_price?: number }
//   PATCH /bookings/:id/extend   { minutes: 30|60 }
// Never optimistic — every successful action refetches the parent's data and
// the sheet re-renders from the fresh payload (the parent re-derives `booking`
// by id). Capability props mirror the backend role rules (staff: extend is
// 403'd at the route, total_price 403'd in the handler) — UX mirror only.
//
// Visual language mirrors DayViewManualSheet (bottom sheet on mobile, centred
// modal on desktop). PaymentStatusPill is deliberately NOT reused — it only
// knows paid_cash/unpaid; this surface needs the four payment_display states.

import { useMemo, useState } from 'react';
import {
  X, Loader2, AlertTriangle, BanknoteArrowUp, Pencil, Check, Clock, Plus,
} from 'lucide-react';
import api from '@/lib/api';
import { formatDate, formatTime, formatCurrency } from '@/lib/format';

export interface SheetBooking {
  id: number;
  source: string;
  status: string;
  attendance: string;
  payment_status: string; // legacy
  start_time: string;
  end_time: string;
  total_price: number;
  amount_paid: number | null;
  payment_display: 'untracked' | 'unpaid' | 'partial' | 'paid';
  remaining: number | null;
}

// Strict 3-dp JOD (fils) — mirrors the reports `jod3` convention; lib/format
// untouched (see docs/followups/jod3-consolidation.md).
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });
const hm = (iso: string) => formatTime(iso, { hour: '2-digit', minute: '2-digit', hour12: false });

// paymentDisplayBadge — the shared payment_display → badge mapping used by BOTH
// this sheet and the Day View slot row. `untracked` renders NOTHING (ruling 2:
// the board must not scream "unpaid" at bookings the owner never tracked).
export function paymentDisplayBadge(display: string): { label: string; cls: string } | null {
  switch (display) {
    case 'paid':    return { label: 'مدفوع',        cls: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-300' };
    case 'partial': return { label: 'مدفوع جزئياً', cls: 'bg-amber-500/15 border-amber-500/30 text-amber-300' };
    case 'unpaid':  return { label: 'غير مدفوع',    cls: 'bg-red-500/15 border-red-500/30 text-red-400' };
    default:        return null; // untracked → neutral, no badge
  }
}

// Server error-code → Levantine inline copy (WO §2).
const ERROR_COPY: Record<string, string> = {
  slot_conflict:           'لا يمكن التمديد — الوقت التالي محجوز',
  booking_ended:           'الحجز خلص — التمديد غير متاح',
  outside_operating_hours: 'التمديد يتجاوز ساعات دوام الملعب',
  paid_exceeds_total:      'المبلغ المدفوع أكبر من إجمالي الحجز',
  booking_cancelled:       'هذا الحجز ملغي',
};
const GENERIC_ERROR = 'صار خطأ — جرّب مرة ثانية';
const copyFor = (code?: string) => (code && ERROR_COPY[code]) || GENERIC_ERROR;

// Sanitise a money keystroke: digits + a single dot, max 3 decimals.
function sanitizeMoney(raw: string): string {
  let s = raw.replace(/[^\d.]/g, '');
  const dot = s.indexOf('.');
  if (dot >= 0) s = s.slice(0, dot + 1) + s.slice(dot + 1).replace(/\./g, '');
  const m = s.match(/^(\d*)(?:\.(\d{0,3}))?/);
  return m ? (m[2] !== undefined ? `${m[1]}.${m[2]}` : m[1]) : '';
}

type ErrorState = { context: 'payment' | 'extend'; text: string } | null;

export default function BookingSheet({
  booking,
  title,
  pricePerHour,
  canExtend,
  canEditTotal,
  onClose,
  onRefetch,
}: {
  booking: SheetBooking;
  title: string;
  pricePerHour: number;
  canExtend: boolean;
  canEditTotal: boolean;
  onClose: () => void;
  onRefetch: () => Promise<void>;
}) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<ErrorState>(null);
  const [amountInput, setAmountInput] = useState<string>(
    booking.amount_paid != null ? String(booking.amount_paid) : '',
  );
  const [editingTotal, setEditingTotal] = useState(false);
  const [totalInput, setTotalInput] = useState<string>(String(booking.total_price));
  const [extendConfirm, setExtendConfirm] = useState<30 | 60 | null>(null);

  const ended = useMemo(() => new Date(booking.end_time).getTime() < Date.now(), [booking.end_time]);
  const badge = paymentDisplayBadge(booking.payment_display);

  const dateLabel = formatDate(booking.start_time, { weekday: 'long', day: 'numeric', month: 'long' });

  // After a successful mutation: refetch the day, then let the fresh `booking`
  // prop re-render this sheet. Clears transient edit/confirm state.
  const afterSuccess = async () => {
    setExtendConfirm(null);
    setEditingTotal(false);
    setError(null);
    await onRefetch();
  };

  const mapError = (err: any, context: 'payment' | 'extend') => {
    setError({ context, text: copyFor(err?.response?.data?.error) });
  };

  const patchPayment = async (body: { amount_paid: number | null; total_price?: number }) => {
    setSubmitting(true);
    setError(null);
    try {
      await api.patch(`/bookings/${booking.id}/payment`, body);
      await afterSuccess();
    } catch (err) {
      mapError(err, 'payment');
    } finally {
      setSubmitting(false);
    }
  };

  const extend = async (minutes: 30 | 60) => {
    setSubmitting(true);
    setError(null);
    try {
      await api.patch(`/bookings/${booking.id}/extend`, { minutes });
      await afterSuccess();
    } catch (err) {
      mapError(err, 'extend');
    } finally {
      setSubmitting(false);
    }
  };

  // «دفع كامل» — one tap settles the full total.
  const payFull = () => {
    setAmountInput(String(booking.total_price));
    patchPayment({ amount_paid: booking.total_price });
  };

  // Save the typed amount: empty → revert to untracked (null); else the number.
  const saveAmount = () => {
    const trimmed = amountInput.trim();
    patchPayment({ amount_paid: trimmed === '' ? null : Number(trimmed) });
  };

  // Save an edited total. Sends the CURRENT amount_paid alongside so the server
  // can 422 if the new total drops below it (ruling 6 — surface, don't pre-block).
  const saveTotal = () => {
    const t = totalInput.trim();
    if (t === '' || Number.isNaN(Number(t))) { setEditingTotal(false); return; }
    patchPayment({ amount_paid: booking.amount_paid, total_price: Number(t) });
  };

  const projectedTotal = (minutes: 30 | 60) => booking.total_price + (pricePerHour * minutes) / 60;

  const badgeEl = badge && (
    <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold border ${badge.cls}`}>
      {badge.label}
    </span>
  );

  return (
    <div className="fixed inset-0 z-[60] flex md:items-center md:justify-center" dir="rtl">
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={() => { if (!submitting) onClose(); }}
        aria-hidden
      />

      <div className="relative w-full md:max-w-md md:rounded-2xl rounded-t-2xl bg-[#141715] border border-white/[0.1] shadow-2xl mt-auto md:mt-0 max-h-[92vh] overflow-y-auto p-5 pb-8">
        {/* Header */}
        <div className="flex items-start justify-between gap-3 mb-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h2 className="text-[15px] font-bold text-[#f0efe8] truncate">{title || 'حجز'}</h2>
              {badgeEl}
            </div>
            <p className="text-[12px] text-white/40 mt-1" dir="rtl">
              {dateLabel}، <span dir="ltr" className="font-mono tabular-nums">{hm(booking.start_time)}–{hm(booking.end_time)}</span>
            </p>
          </div>
          <button type="button" onClick={onClose} aria-label="إغلاق" className="text-white/40 hover:text-white/80 shrink-0">
            <X size={18} aria-hidden />
          </button>
        </div>

        {/* ── Money block ── */}
        <div className="rounded-2xl border border-white/[0.08] bg-white/[0.02] p-4 flex flex-col gap-4">
          {/* Total (editable) */}
          <div className="flex items-center justify-between gap-3">
            <span className="text-[12px] text-white/45">الإجمالي</span>
            {!canEditTotal ? (
              <span className="text-[15px] font-bold text-[#f0efe8] tabular-nums">
                {jod3(booking.total_price)} <span className="text-[11px] text-emerald-500/80 font-semibold">د.أ</span>
              </span>
            ) : editingTotal ? (
              <div className="flex items-center gap-2">
                <input
                  value={totalInput}
                  onChange={e => setTotalInput(sanitizeMoney(e.target.value))}
                  inputMode="decimal"
                  dir="ltr"
                  autoFocus
                  disabled={submitting}
                  className="w-24 bg-white/[0.05] border border-white/[0.15] rounded-lg px-3 py-1.5 text-[13px] text-[#f0efe8] text-right tabular-nums focus:outline-none focus:border-emerald-500/50 disabled:opacity-50"
                />
                <button
                  type="button"
                  onClick={saveTotal}
                  disabled={submitting}
                  aria-label="حفظ الإجمالي"
                  className="w-9 h-9 inline-flex items-center justify-center rounded-lg bg-emerald-500/15 border border-emerald-500/35 text-emerald-300 disabled:opacity-50"
                >
                  <Check size={15} aria-hidden />
                </button>
                <button
                  type="button"
                  onClick={() => { setEditingTotal(false); setTotalInput(String(booking.total_price)); }}
                  disabled={submitting}
                  aria-label="إلغاء"
                  className="w-9 h-9 inline-flex items-center justify-center rounded-lg border border-white/[0.1] text-white/50 disabled:opacity-50"
                >
                  <X size={15} aria-hidden />
                </button>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => { setTotalInput(String(booking.total_price)); setEditingTotal(true); }}
                disabled={submitting}
                className="inline-flex items-center gap-1.5 text-[15px] font-bold text-[#f0efe8] tabular-nums disabled:opacity-50 group"
              >
                <span>{jod3(booking.total_price)} <span className="text-[11px] text-emerald-500/80 font-semibold">د.أ</span></span>
                <Pencil size={13} className="text-white/30 group-hover:text-white/60 transition-colors" aria-hidden />
              </button>
            )}
          </div>

          {/* Paid / remaining */}
          <div className="flex items-center justify-between gap-3">
            <span className="text-[12px] text-white/45">المدفوع</span>
            <div className="flex items-center gap-2">
              <input
                value={amountInput}
                onChange={e => setAmountInput(sanitizeMoney(e.target.value))}
                inputMode="decimal"
                dir="ltr"
                placeholder="غير مسجّل"
                disabled={submitting}
                className="w-24 bg-white/[0.05] border border-white/[0.15] rounded-lg px-3 py-1.5 text-[13px] text-[#f0efe8] text-right tabular-nums placeholder:text-white/25 placeholder:text-[11px] focus:outline-none focus:border-emerald-500/50 disabled:opacity-50"
              />
              <button
                type="button"
                onClick={saveAmount}
                disabled={submitting}
                aria-label="حفظ المبلغ المدفوع"
                className="w-9 h-9 inline-flex items-center justify-center rounded-lg bg-white/[0.05] border border-white/[0.12] text-white/70 hover:text-white disabled:opacity-50"
              >
                <Check size={15} aria-hidden />
              </button>
            </div>
          </div>

          {booking.payment_display === 'partial' && booking.remaining != null && (
            <div className="flex items-center justify-between gap-3 -mt-1">
              <span className="text-[12px] text-amber-300/70">المتبقي</span>
              <span className="text-[13px] font-bold text-amber-300 tabular-nums">{jod3(booking.remaining)} <span className="text-[11px]">د.أ</span></span>
            </div>
          )}

          {/* Primary: pay in full */}
          <button
            type="button"
            onClick={payFull}
            disabled={submitting || booking.payment_display === 'paid'}
            className="inline-flex items-center justify-center gap-2 px-6 py-3 rounded-xl text-[13px] font-bold bg-emerald-500/15 text-emerald-300 border border-emerald-500/35 hover:bg-emerald-500/20 disabled:opacity-50 disabled:cursor-not-allowed transition-all active:scale-[0.98]"
          >
            {submitting ? <Loader2 size={15} className="animate-spin" aria-hidden /> : <BanknoteArrowUp size={15} aria-hidden />}
            دفع كامل
          </button>

          {error?.context === 'payment' && (
            <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-red-500/[0.07] border border-red-500/20 text-[12px] text-red-300">
              <AlertTriangle size={13} aria-hidden className="shrink-0" />
              {error.text}
            </div>
          )}
        </div>

        {/* ── Extension ── (hidden for staff and once the booking has ended) */}
        {canExtend && !ended && (
          <div className="mt-4 flex flex-col gap-2">
            <span className="text-[12px] text-white/45">تمديد الحجز</span>
            <div className="flex items-center gap-2">
              {([[30, '+30 دقيقة'], [60, '+ساعة']] as [30 | 60, string][]).map(([mins, label]) => {
                const confirming = extendConfirm === mins;
                return (
                  <button
                    key={mins}
                    type="button"
                    disabled={submitting}
                    onClick={() => {
                      if (confirming) extend(mins);
                      else setExtendConfirm(mins);
                    }}
                    aria-pressed={confirming}
                    className={[
                      'flex-1 min-h-[48px] px-3 rounded-xl text-[12.5px] font-bold border transition-all active:scale-[0.98] disabled:opacity-50',
                      confirming
                        ? 'bg-emerald-500 text-[#08130d] border-emerald-400'
                        : 'bg-white/[0.03] border-white/[0.1] text-white/70 hover:text-white hover:border-white/25',
                    ].join(' ')}
                  >
                    {confirming ? (
                      <span className="inline-flex flex-col items-center leading-tight">
                        <span className="inline-flex items-center gap-1"><Check size={13} aria-hidden /> تأكيد</span>
                        <span className="text-[10px] font-semibold opacity-80 tabular-nums">
                          {jod3(projectedTotal(mins))} د.أ
                        </span>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1.5">
                        {mins === 30 ? <Plus size={13} aria-hidden /> : <Clock size={13} aria-hidden />}
                        {label}
                      </span>
                    )}
                  </button>
                );
              })}
              {extendConfirm != null && (
                <button
                  type="button"
                  onClick={() => setExtendConfirm(null)}
                  disabled={submitting}
                  aria-label="إلغاء التمديد"
                  className="w-12 h-12 inline-flex items-center justify-center rounded-xl border border-white/[0.1] text-white/50 hover:text-white/80 disabled:opacity-50"
                >
                  <X size={16} aria-hidden />
                </button>
              )}
            </div>
            {extendConfirm != null && (
              <p className="text-[11px] text-white/35">اضغط «تأكيد» مرة ثانية لتثبيت التمديد</p>
            )}

            {error?.context === 'extend' && (
              <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-red-500/[0.07] border border-red-500/20 text-[12px] text-red-300">
                <AlertTriangle size={13} aria-hidden className="shrink-0" />
                {error.text}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
