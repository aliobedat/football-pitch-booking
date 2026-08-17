'use client';

// Cancel flow, extracted verbatim from BookingSheet.tsx (WO-SHEET-EXTRACT). Pure
// relocation — same handlers, same JSX, same styling, same API calls. cancelStage
// and its sibling state stay owned by BookingSheet (the "إلغاء الحجز" trigger
// button also stays there, unmoved, since the dialog below relies on being a
// direct sibling of BookingSheet's outer `fixed inset-0` container for its
// `absolute inset-0` overlay to cover the full viewport rather than just the
// card — moving the trigger+dialog into one self-contained component would
// either break that positioning or require a styling change, both out of scope
// for a pure extraction).

import { AlertTriangle, Loader2, Repeat, Trash2 } from 'lucide-react';
import api from '@/lib/api';
import type { SheetBooking } from './BookingSheet';

// WO-OWNER-NOTIFY-CANCEL-REASON-UI: fixed cancellation-reason options, verbatim
// (not placeholders — these are the exact strings sent to the backend and, from
// there, into the booking_cancelled_ar WhatsApp template).
const CANCEL_REASON_OPTIONS = ['صيانة الملعب', 'ظروف جوية', 'تعارض بالحجز'] as const;

export interface BookingCancelPanelProps {
  booking: SheetBooking;
  title: string;
  pitchId?: number;
  submitting: boolean;
  setSubmitting: (v: boolean) => void;
  onRefetch: () => Promise<void>;
  onCancelled?: () => void;
  onClose: () => void;
  cancelStage: null | 'choose' | 'single' | 'series';
  setCancelStage: (v: null | 'choose' | 'single' | 'series') => void;
  upcoming: { count: number; tracked: boolean } | null;
  setUpcoming: (v: { count: number; tracked: boolean } | null) => void;
  loadingUpcoming: boolean;
  setLoadingUpcoming: (v: boolean) => void;
  cancelError: string | null;
  setCancelError: (v: string | null) => void;
  reasonOption: string | null;
  reasonText: string;
  selectReasonOption: (opt: string) => void;
  onReasonTextChange: (v: string) => void;
  effectiveReason: string | null;
  reasonValid: boolean;
  /** Same GENERIC_ERROR / copyFor BookingSheet.tsx uses — passed down so the
      error-code → Arabic copy mapping is not duplicated in two files. */
  copyFor: (code?: string) => string;
  genericError: string;
}

export default function BookingCancelPanel({
  booking,
  title,
  pitchId,
  submitting,
  setSubmitting,
  onRefetch,
  onCancelled,
  onClose,
  cancelStage,
  setCancelStage,
  upcoming,
  setUpcoming,
  loadingUpcoming,
  setLoadingUpcoming,
  cancelError,
  setCancelError,
  reasonOption,
  reasonText,
  selectReasonOption,
  onReasonTextChange,
  effectiveReason,
  reasonValid,
  copyFor,
  genericError: GENERIC_ERROR,
}: BookingCancelPanelProps) {
  // «إلغاء كل المواعيد القادمة» — lazy-fetch the count/tracked-money preview, then
  // show the series confirm. A 404 is treated as an empty group ({0,false}).
  const loadUpcomingThenConfirm = async () => {
    if (pitchId == null || booking.recurrence_group_id == null) return;
    setLoadingUpcoming(true);
    setCancelError(null);
    try {
      const { data } = await api.get(
        `/pitches/${pitchId}/bookings/group/${booking.recurrence_group_id}/upcoming`,
      );
      setUpcoming({ count: data.upcoming_count ?? 0, tracked: !!data.has_tracked_money });
      setCancelStage('series');
    } catch (err: any) {
      if (err?.response?.status === 404) {
        setUpcoming({ count: 0, tracked: false });
        setCancelStage('series');
      } else {
        setCancelError(GENERIC_ERROR);
      }
    } finally {
      setLoadingUpcoming(false);
    }
  };

  // On a successful cancel: refetch (the slot frees), notify the parent (toast),
  // then close. Day View also auto-closes when the booking vanishes post-refetch.
  const afterCancel = async () => {
    await onRefetch();
    onCancelled?.();
    onClose();
  };

  const cancelSingle = async () => {
    if (!reasonValid) return;
    setSubmitting(true);
    setCancelError(null);
    try {
      await api.patch(`/bookings/${booking.id}/cancel`, { reason: effectiveReason });
      await afterCancel();
    } catch (err: any) {
      setCancelError(copyFor(err?.response?.data?.error));
    } finally {
      setSubmitting(false);
    }
  };

  const cancelSeries = async () => {
    if (pitchId == null || booking.recurrence_group_id == null || !reasonValid) return;
    setSubmitting(true);
    setCancelError(null);
    try {
      await api.delete(`/pitches/${pitchId}/bookings/group/${booking.recurrence_group_id}`, {
        data: { reason: effectiveReason },
      });
      await afterCancel();
    } catch (err: any) {
      setCancelError(copyFor(err?.response?.data?.error));
    } finally {
      setSubmitting(false);
    }
  };

  // Reason selector shared by the single and series confirm panels: three fixed
  // buttons + one free-text box, mutually exclusive (WO-OWNER-NOTIFY-CANCEL-REASON-UI).
  const renderReasonSelector = () => (
    <div className="mb-1">
      <span className="block text-[11.5px] font-semibold text-white/45 mb-2">سبب الإلغاء</span>
      <div className="flex flex-wrap gap-2 mb-2">
        {CANCEL_REASON_OPTIONS.map(opt => (
          <button
            key={opt}
            type="button"
            onClick={() => selectReasonOption(opt)}
            disabled={submitting}
            aria-pressed={reasonOption === opt}
            className={[
              'min-h-[40px] px-3 rounded-xl text-[12px] font-bold border transition-all disabled:opacity-50',
              reasonOption === opt
                ? 'bg-emerald-500 text-[#08130d] border-emerald-400'
                : 'bg-white/[0.03] border-white/[0.1] text-white/70 hover:text-white hover:border-white/25',
            ].join(' ')}
          >
            {opt}
          </button>
        ))}
      </div>
      <input
        value={reasonText}
        onChange={e => onReasonTextChange(e.target.value)}
        dir="rtl"
        placeholder="سبب آخر…"
        disabled={submitting}
        className="w-full bg-white/[0.05] border border-white/[0.15] rounded-lg px-3 py-2 text-[13px] text-[#f0efe8] text-right placeholder:text-white/25 placeholder:text-[12px] focus:outline-none focus:border-emerald-500/50 disabled:opacity-50"
      />
    </div>
  );

  if (!cancelStage) return null;

  return (
    <div className="absolute inset-0 z-10 flex items-center justify-center p-4" dir="rtl">
      <div
        className="absolute inset-0 bg-black/60"
        onClick={() => { if (!submitting) setCancelStage(null); }}
        aria-hidden
      />
      <div role="dialog" aria-modal="true" className="relative w-full max-w-sm rounded-2xl bg-[#141715] border border-white/[0.1] shadow-2xl p-6">
        {/* Series two-option chooser */}
        {cancelStage === 'choose' && (
          <>
            <h3 className="text-[15px] font-bold text-[#f0efe8] mb-1">إلغاء الحجز المتكرر</h3>
            <p className="text-[12.5px] text-white/45 mb-4">{title || 'حجز'}</p>
            <div className="flex flex-col gap-2.5">
              <button
                type="button"
                onClick={() => setCancelStage('single')}
                disabled={submitting || loadingUpcoming}
                className="w-full min-h-[48px] inline-flex items-center justify-center gap-2 rounded-xl border border-white/[0.12] bg-white/[0.03] text-[12.5px] font-bold text-white/80 hover:text-white hover:border-white/25 disabled:opacity-50 transition-all"
              >
                <Trash2 size={14} aria-hidden /> إلغاء هذا الموعد فقط
              </button>
              <button
                type="button"
                onClick={loadUpcomingThenConfirm}
                disabled={submitting || loadingUpcoming}
                className="w-full min-h-[48px] inline-flex items-center justify-center gap-2 rounded-xl border border-red-500/25 bg-transparent text-[12.5px] font-bold text-red-400 hover:bg-red-500/[0.08] hover:border-red-500/40 disabled:opacity-50 transition-all"
              >
                {loadingUpcoming ? <Loader2 size={14} className="animate-spin" aria-hidden /> : <Repeat size={14} aria-hidden />}
                إلغاء كل المواعيد القادمة
              </button>
            </div>
            {cancelError && (
              <div className="flex items-center gap-2 mt-3 px-3 py-2 rounded-lg bg-red-500/[0.07] border border-red-500/20 text-[12px] text-red-300">
                <AlertTriangle size={13} aria-hidden className="shrink-0" /> {cancelError}
              </div>
            )}
            <button
              type="button"
              onClick={() => setCancelStage(null)}
              disabled={submitting || loadingUpcoming}
              className="w-full mt-2.5 min-h-[44px] rounded-xl text-[12px] font-semibold text-white/50 hover:text-white/80 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 transition-all"
            >
              تراجع
            </button>
          </>
        )}

        {/* Single-occurrence confirm */}
        {cancelStage === 'single' && (
          <>
            <h3 className="text-[15px] font-bold text-[#f0efe8] mb-2">إلغاء حجز {title || 'الضيف'}؟</h3>
            {booking.amount_paid !== null && (
              <div className="flex items-center gap-2 mb-3 px-3 py-2 rounded-lg bg-amber-500/[0.08] border border-amber-500/25 text-[12px] text-amber-300">
                <AlertTriangle size={13} aria-hidden className="shrink-0" /> هذا الحجز عليه مبلغ مدفوع مسجّل
              </div>
            )}
            {renderReasonSelector()}
            {cancelError && (
              <div className="flex items-center gap-2 mb-3 mt-3 px-3 py-2 rounded-lg bg-red-500/[0.07] border border-red-500/20 text-[12px] text-red-300">
                <AlertTriangle size={13} aria-hidden className="shrink-0" /> {cancelError}
              </div>
            )}
            <div className="flex items-center gap-3 mt-4">
              <button
                type="button"
                onClick={() => setCancelStage(null)}
                disabled={submitting}
                className="flex-1 min-h-[48px] rounded-xl text-[12.5px] font-bold text-[#08130d] bg-emerald-500 hover:bg-emerald-400 disabled:opacity-60 transition-all"
              >
                تراجع
              </button>
              <button
                type="button"
                onClick={cancelSingle}
                disabled={submitting || !reasonValid}
                className="flex-1 min-h-[48px] inline-flex items-center justify-center gap-2 rounded-xl border border-red-500/30 text-[12.5px] font-bold text-red-400 hover:bg-red-500/[0.08] disabled:opacity-40 disabled:cursor-not-allowed transition-all"
              >
                {submitting ? <Loader2 size={14} className="animate-spin" aria-hidden /> : <Trash2 size={14} aria-hidden />}
                إلغاء الحجز
              </button>
            </div>
          </>
        )}

        {/* Series cancel-all confirm */}
        {cancelStage === 'series' && upcoming && (
          <>
            <h3 className="text-[15px] font-bold text-[#f0efe8] mb-2">إلغاء المواعيد القادمة</h3>
            {upcoming.count === 0 ? (
              <p className="text-[12.5px] text-white/55 leading-relaxed mb-4">
                لا توجد مواعيد قادمة لإلغائها لـ {title || 'الضيف'}. الحجوزات السابقة لن تتأثر.
              </p>
            ) : (
              <p className="text-[12.5px] text-white/60 leading-relaxed mb-3">
                سيتم إلغاء <span className="font-bold text-[#f0efe8]">{upcoming.count}</span> حجوزات قادمة لـ {title || 'الضيف'}. الحجوزات السابقة لن تتأثر.
              </p>
            )}
            {upcoming.tracked && upcoming.count > 0 && (
              <div className="flex items-center gap-2 mb-3 px-3 py-2 rounded-lg bg-amber-500/[0.08] border border-amber-500/25 text-[12px] text-amber-300">
                <AlertTriangle size={13} aria-hidden className="shrink-0" /> بعض هذه الحجوزات عليها مبالغ مدفوعة مسجّلة
              </div>
            )}
            {upcoming.count > 0 && renderReasonSelector()}
            {cancelError && (
              <div className="flex items-center gap-2 mb-3 mt-3 px-3 py-2 rounded-lg bg-red-500/[0.07] border border-red-500/20 text-[12px] text-red-300">
                <AlertTriangle size={13} aria-hidden className="shrink-0" /> {cancelError}
              </div>
            )}
            <div className="flex items-center gap-3 mt-4">
              <button
                type="button"
                onClick={() => setCancelStage(null)}
                disabled={submitting}
                className="flex-1 min-h-[48px] rounded-xl text-[12.5px] font-bold text-[#08130d] bg-emerald-500 hover:bg-emerald-400 disabled:opacity-60 transition-all"
              >
                تراجع
              </button>
              <button
                type="button"
                onClick={cancelSeries}
                disabled={submitting || upcoming.count === 0 || !reasonValid}
                className="flex-1 min-h-[48px] inline-flex items-center justify-center gap-2 rounded-xl border border-red-500/30 text-[12.5px] font-bold text-red-400 hover:bg-red-500/[0.08] disabled:opacity-40 disabled:cursor-not-allowed transition-all"
              >
                {submitting ? <Loader2 size={14} className="animate-spin" aria-hidden /> : <Trash2 size={14} aria-hidden />}
                إلغاء {upcoming.count} حجوزات
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
