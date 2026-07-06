'use client';

// Manual (walk-in) booking sheet for the Day View. Posts to the existing
// POST /pitches/:id/bookings/manual (Gate 0 contract) — no new endpoint, no
// backend change. Start times are constrained to the currently-loaded day's
// AVAILABLE cells (the grid already knows what's taken); duration is 30-min
// snapped with a 60-min floor mirroring the DB. Mobile: bottom sheet. Desktop:
// centred modal. repeat_weeks/recurrence is intentionally NOT surfaced here.

import { useEffect, useMemo, useState } from 'react';
import { X, UserPlus, Loader2, AlertTriangle, Clock, Minus, Plus } from 'lucide-react';
import api from '@/lib/api';
import { formatDate, formatTime } from '@/lib/format';

interface AvailSlot { start: string; end: string } // UTC RFC3339

// Duration chips + stepper. floor mirrors DB chk_min_duration (≥60min) — if that
// constraint ever changes, update here.
const DURATION_FLOOR = 60;
const DURATION_STEP = 30;
const BASE_DURATIONS = [60, 90, 120];

const hm = (iso: string) => formatTime(iso, { hour: '2-digit', minute: '2-digit', hour12: false });

// Jordanian mobile, matching the existing manual-booking validation:
// +9627######## or 07########. Only checked when the owner actually types one.
const phoneValid = (raw: string) => /^(\+962|0)7\d{8}$/.test(raw.replace(/[\s-]/g, ''));

// Contiguous available minutes starting at `startIso` (each cell is 30 min).
function contiguousMinutes(slots: AvailSlot[], startIso: string): number {
  const idx = slots.findIndex(s => s.start === startIso);
  if (idx < 0) return 0;
  let count = 1;
  for (let i = idx; i + 1 < slots.length && slots[i].end === slots[i + 1].start; i++) count++;
  return count * DURATION_STEP;
}

export default function DayViewManualSheet({
  pitchId,
  pitchName,
  availableSlots,
  prefillStart,
  onClose,
  onBooked,
  onRefetch,
}: {
  pitchId: number;
  pitchName: string;
  availableSlots: AvailSlot[]; // ordered available cells of the loaded day
  prefillStart: string | null;
  onClose: () => void;
  onBooked: () => void;   // success → close + refetch
  onRefetch: () => void;  // 409 → refetch, keep the sheet open
}) {
  // Only starts that can host the 60-min minimum are offerable.
  const starts = useMemo(
    () => availableSlots.filter(s => contiguousMinutes(availableSlots, s.start) >= DURATION_FLOOR),
    [availableSlots],
  );

  const initialStart = useMemo(() => {
    if (prefillStart && starts.some(s => s.start === prefillStart)) return prefillStart;
    return starts[0]?.start ?? '';
  }, [prefillStart, starts]);

  const [start, setStart] = useState(initialStart);
  const [name, setName] = useState('');
  const [phone, setPhone] = useState('');
  const [duration, setDuration] = useState(DURATION_FLOOR);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [phoneError, setPhoneError] = useState<string | null>(null);
  const [override, setOverride] = useState(false); // 422 soft-override pending

  // If a refetch (post-409) drops the selected start, fall back to the first valid one.
  useEffect(() => {
    if (start && !starts.some(s => s.start === start)) setStart(starts[0]?.start ?? '');
  }, [starts, start]);

  const maxMinutes = useMemo(() => (start ? contiguousMinutes(availableSlots, start) : 0), [availableSlots, start]);

  // Keep duration within the contiguous run from the selected start.
  useEffect(() => {
    if (duration > maxMinutes && maxMinutes >= DURATION_FLOOR) setDuration(maxMinutes);
  }, [maxMinutes, duration]);

  // Lock body scroll while open.
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => { document.body.style.overflow = prev; };
  }, []);

  // start_time comes straight from the slot's authoritative UTC instant; end_time
  // is that instant + duration (absolute-instant arithmetic, not local-tz math).
  const endIso = useMemo(
    () => (start ? new Date(new Date(start).getTime() + duration * 60_000).toISOString() : ''),
    [start, duration],
  );

  const summary = start
    ? `${formatDate(start, { weekday: 'long', day: 'numeric', month: 'long' })}، ${hm(start)} – ${hm(endIso)}`
    : '';

  const durationChoices = BASE_DURATIONS.filter(d => d <= maxMinutes);

  const submit = async (bypass: boolean) => {
    const guestName = name.trim();
    if (!guestName) { setError('اسم اللاعب مطلوب'); return; }
    if (!start) { setError('اختر وقت البداية'); return; }
    const p = phone.trim();
    if (p && !phoneValid(p)) {
      setPhoneError('رقم هاتف غير صالح (مثال: 0791234567 أو ‎+962791234567)');
      return;
    }
    setSubmitting(true);
    setError(null);
    setPhoneError(null);
    try {
      const body: Record<string, unknown> = { start_time: start, end_time: endIso, guest_name: guestName };
      if (p) body.guest_phone = p;
      if (bypass) body.force_bypass_hours = true;
      await api.post(`/pitches/${pitchId}/bookings/manual`, body);
      onBooked(); // success → parent closes + refetches; the new cell IS the confirmation
    } catch (err: any) {
      const status = err?.response?.status;
      const code = err?.response?.data?.error;
      if (status === 409) {
        // The GIST referee: someone booked this slot first. Not an error to hide.
        setError('تم حجز هذا الوقت للتو');
        onRefetch();
      } else if (status === 422 && code === 'outside_operating_hours' && !bypass) {
        setOverride(true); // mirror the BlocksModal soft-override
      } else {
        setError(err?.response?.data?.message ?? 'تعذّر تسجيل الحجز، حاول مجدداً');
      }
    } finally {
      setSubmitting(false);
    }
  };

  const canSubmit = !submitting && !!name.trim() && !!start && durationChoices.length >= 0 && duration >= DURATION_FLOOR;

  const body = (
    <>
      <div className="flex items-start justify-between gap-3 mb-4">
        <div>
          <h2 className="text-[15px] font-bold text-[#f0efe8]">حجز يدوي جديد</h2>
          <p className="text-[12px] text-white/40 mt-0.5">{pitchName}</p>
        </div>
        <button type="button" onClick={onClose} aria-label="إغلاق" className="text-white/40 hover:text-white/80">
          <X size={18} aria-hidden />
        </button>
      </div>

      {starts.length === 0 ? (
        <p className="text-[13px] text-white/45 py-8 text-center">لا توجد فترات متاحة في هذا اليوم</p>
      ) : (
        <div className="flex flex-col gap-4">
          {/* Name */}
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px] font-semibold text-white/45">اسم اللاعب <span className="text-red-400/70">*</span></span>
            <input
              value={name}
              onChange={e => { setName(e.target.value); setError(null); }}
              placeholder="مثال: أحمد"
              autoFocus
              className="bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-3 text-[13px] text-[#f0efe8] placeholder:text-white/25 focus:outline-none focus:border-emerald-500/50 focus:ring-2 focus:ring-emerald-500/15 transition-all"
            />
          </label>

          {/* Phone (optional) */}
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px] font-semibold text-white/45">رقم الهاتف <span className="text-white/25">(اختياري)</span></span>
            <input
              value={phone}
              onChange={e => { setPhone(e.target.value); setPhoneError(null); }}
              placeholder="07XXXXXXXX"
              dir="ltr"
              inputMode="tel"
              className="bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-3 text-[13px] text-[#f0efe8] placeholder:text-white/25 text-right focus:outline-none focus:border-emerald-500/50 focus:ring-2 focus:ring-emerald-500/15 transition-all"
            />
            {phoneError && <span className="text-[10.5px] text-red-400">{phoneError}</span>}
          </label>

          {/* Start time — only available cells that fit the 60-min minimum */}
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px] font-semibold text-white/45">وقت البداية</span>
            <select
              value={start}
              onChange={e => setStart(e.target.value)}
              className="bg-[#0f1110] border border-white/[0.13] rounded-xl px-4 py-3 text-[13px] text-[#f0efe8] [color-scheme:dark] focus:outline-none focus:border-emerald-500/50 transition-all"
            >
              {starts.map(s => <option key={s.start} value={s.start} className="bg-[#0f1110]">{hm(s.start)}</option>)}
            </select>
          </label>

          {/* Duration — chips (≤ contiguous run) + 30-min stepper */}
          <div className="flex flex-col gap-1.5">
            <span className="text-[11px] font-semibold text-white/45">المدة</span>
            <div className="flex items-center gap-2 flex-wrap">
              {durationChoices.map(d => {
                const on = duration === d;
                return (
                  <button
                    key={d}
                    type="button"
                    onClick={() => setDuration(d)}
                    aria-pressed={on}
                    className={[
                      'min-h-[44px] px-4 rounded-xl text-[12.5px] font-bold border transition-all',
                      on ? 'bg-emerald-500/15 border-emerald-500/45 text-emerald-300'
                         : 'bg-white/[0.03] border-white/[0.09] text-white/60 hover:text-white/85',
                    ].join(' ')}
                  >
                    {d} د
                  </button>
                );
              })}
              {/* Stepper for longer durations (30-min increments, capped at the run). */}
              <div className="inline-flex items-center rounded-xl border border-white/[0.09] bg-white/[0.03] overflow-hidden">
                <button
                  type="button"
                  aria-label="إنقاص المدة"
                  disabled={duration <= DURATION_FLOOR}
                  onClick={() => setDuration(d => Math.max(DURATION_FLOOR, d - DURATION_STEP))}
                  className="w-11 h-11 inline-flex items-center justify-center text-white/60 hover:text-white disabled:opacity-30 transition-colors"
                >
                  <Minus size={15} aria-hidden />
                </button>
                <span className="px-2 text-[12.5px] font-bold text-[#f0efe8] tabular-nums min-w-[52px] text-center">{duration} د</span>
                <button
                  type="button"
                  aria-label="زيادة المدة"
                  disabled={duration + DURATION_STEP > maxMinutes}
                  onClick={() => setDuration(d => Math.min(maxMinutes, d + DURATION_STEP))}
                  className="w-11 h-11 inline-flex items-center justify-center text-white/60 hover:text-white disabled:opacity-30 transition-colors"
                >
                  <Plus size={15} aria-hidden />
                </button>
              </div>
            </div>
          </div>

          {/* Live summary */}
          {summary && (
            <div className="flex items-center gap-2 px-3.5 py-2.5 rounded-xl bg-emerald-500/[0.06] border border-emerald-500/20 text-[12.5px] text-emerald-200/90">
              <Clock size={13} aria-hidden className="shrink-0" />
              <span className="font-semibold" dir="rtl">{summary}</span>
            </div>
          )}

          {error && (
            <div className="flex items-center gap-2 px-3.5 py-2.5 rounded-xl bg-red-500/[0.07] border border-red-500/20 text-[12px] text-red-300">
              <AlertTriangle size={13} aria-hidden className="shrink-0" />
              {error}
            </div>
          )}

          {/* Confirm — in-flow (not a fixed footer) so the mobile keyboard can't bury it */}
          <button
            type="button"
            onClick={() => submit(false)}
            disabled={!canSubmit}
            className="mt-1 inline-flex items-center justify-center gap-2 px-6 py-3 rounded-xl text-[13px] font-bold bg-emerald-500/15 text-emerald-300 border border-emerald-500/35 hover:bg-emerald-500/20 disabled:opacity-50 disabled:cursor-not-allowed transition-all active:scale-[0.98]"
          >
            {submitting && !override ? <Loader2 size={15} className="animate-spin" aria-hidden /> : <UserPlus size={14} aria-hidden />}
            تأكيد الحجز
          </button>
        </div>
      )}
    </>
  );

  return (
    <div className="fixed inset-0 z-[60] flex md:items-center md:justify-center" dir="rtl">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={() => { if (!submitting) onClose(); }} aria-hidden />

      <div className="relative w-full md:max-w-md md:rounded-2xl rounded-t-2xl bg-[#141715] border border-white/[0.1] shadow-2xl mt-auto md:mt-0 max-h-[92vh] overflow-y-auto p-5 pb-8">
        {body}
      </div>

      {/* Soft-override: the chosen range spills outside operating hours */}
      {override && (
        <div className="absolute inset-0 z-10 flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-black/60" onClick={() => { if (!submitting) setOverride(false); }} aria-hidden />
          <div role="dialog" aria-modal="true" dir="rtl" className="relative w-full max-w-sm rounded-2xl bg-[#141715] border border-white/[0.1] shadow-2xl p-6">
            <div className="flex items-start gap-3 mb-4">
              <div className="w-10 h-10 rounded-xl bg-amber-500/[0.1] border border-amber-500/25 flex items-center justify-center flex-shrink-0">
                <Clock size={18} className="text-amber-400" aria-hidden />
              </div>
              <div>
                <h3 className="text-[15px] font-bold text-[#f0efe8]">خارج أوقات العمل</h3>
                <p className="text-[12.5px] text-white/40 mt-1 leading-relaxed">هذا الوقت خارج أوقات عمل الملعب المحددة، هل تود تأكيد الحجز؟</p>
              </div>
            </div>
            <div className="flex items-center justify-end gap-3">
              <button type="button" onClick={() => { if (!submitting) setOverride(false); }} disabled={submitting}
                className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 transition-all">
                تراجع
              </button>
              <button type="button" onClick={() => { setOverride(false); submit(true); }} disabled={submitting}
                className="inline-flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold bg-amber-500/[0.12] text-amber-300 border border-amber-500/30 hover:bg-amber-500/[0.18] disabled:opacity-60 transition-all">
                {submitting ? <Loader2 size={13} className="animate-spin" aria-hidden /> : <UserPlus size={13} aria-hidden />}
                تأكيد رغم ذلك
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
