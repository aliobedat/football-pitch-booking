'use client';

import { useState, useEffect, useMemo } from 'react';
import { useRouter } from 'next/navigation';
import { CalendarDays, RefreshCw, CheckCircle2 } from 'lucide-react';
import axios from 'axios';
import api from '@/lib/api';

// ─── Types ────────────────────────────────────────────────────────────────────

interface BookedSlot {
  booking_id: number;
  start_time: string;
  end_time:   string;
  status:     string;
}

interface Props {
  pitchId:      number;
  pricePerHour: number;
}

type SlotState = 'available' | 'booked' | 'selected-start' | 'in-range'
               | 'hover-range' | 'disabled';
type Phase = 'idle' | 'picking-end' | 'done';

// ─── Constants & helpers ──────────────────────────────────────────────────────

const AR_WEEKDAY = ['أحد', 'اثن', 'ثلا', 'أرب', 'خمي', 'جمع', 'سبت'];
const AR_MONTH   = ['يناير','فبراير','مارس','أبريل','مايو','يونيو',
                    'يوليو','أغسطس','سبتمبر','أكتوبر','نوفمبر','ديسمبر'];

function genTimes(fromMins: number, toMins: number): string[] {
  const out: string[] = [];
  for (let m = fromMins; m <= toMins; m += 30) {
    out.push(`${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`);
  }
  return out;
}

// 31 time labels: 07:00 → 22:00
// Each label is BOTH a possible start AND an exact end boundary.
// e.g. clicking "08:30" as start + "09:30" as end → 1-hour booking 08:30–09:30.
// The slot "22:00" can only appear as an end boundary, never as a start.
const SLOTS = genTimes(7 * 60, 22 * 60);

function toMins(t: string): number {
  const [h, m] = t.split(':').map(Number);
  return h * 60 + m;
}

function durationLabel(totalMins: number): string {
  const h = totalMins / 60;
  if (h === 1)     return 'ساعة واحدة';
  if (h === 1.5)   return 'ساعة ونصف';
  if (h === 2)     return 'ساعتان';
  if (h % 1 === 0) return `${h} ساعات`;
  return `${h} ساعة`;
}

function upcomingDays(): Date[] {
  return Array.from({ length: 7 }, (_, i) => {
    const d = new Date();
    d.setDate(d.getDate() + i);
    d.setHours(0, 0, 0, 0);
    return d;
  });
}

function toDateStr(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function buildDateTime(day: Date, timeStr: string): Date {
  const [h, m] = timeStr.split(':').map(Number);
  const d = new Date(day);
  d.setHours(h, m, 0, 0);
  return d;
}

// True if the 30-min period [slot, slot+30min) overlaps any existing booking.
// "22:00" is a boundary marker — its period 22:00–22:30 never overlaps real bookings.
function isSlotBooked(slot: string, booked: BookedSlot[], day: Date): boolean {
  const slotStart = buildDateTime(day, slot).getTime();
  const slotEnd   = slotStart + 30 * 60 * 1000;
  return booked.some(b => {
    const bStart = new Date(b.start_time).getTime();
    const bEnd   = new Date(b.end_time).getTime();
    return bStart < slotEnd && bEnd > slotStart;
  });
}

const DAYS = upcomingDays();

// ─── Slot visual appearance ───────────────────────────────────────────────────

function slotClasses(state: SlotState): string {
  const base =
    'flex items-center justify-center h-10 rounded-xl border ' +
    'text-[11px] font-mono font-semibold transition-all duration-150 select-none';

  switch (state) {
    case 'available':
      return `${base} bg-white/[0.03] border-white/[0.07] text-white/45 cursor-pointer ` +
             'hover:bg-emerald-500/10 hover:border-emerald-500/30 hover:text-emerald-300';
    case 'booked':
      return `${base} bg-red-950/20 border-red-900/[0.12] text-red-500/25 cursor-not-allowed ` +
             'line-through decoration-red-500/20 opacity-60';
    case 'selected-start':
      return `${base} bg-emerald-500/25 border-emerald-400/60 text-emerald-300 cursor-pointer ` +
             'shadow-[0_0_12px_rgba(52,211,153,0.14)]';
    case 'in-range':
      return `${base} bg-emerald-500/[0.13] border-emerald-500/25 text-emerald-400/80 cursor-pointer`;
    case 'hover-range':
      return `${base} bg-emerald-500/[0.07] border-emerald-500/[0.14] text-emerald-400/50 cursor-pointer`;
    case 'disabled':
      return `${base} bg-white/[0.01] border-white/[0.03] text-white/10 cursor-not-allowed`;
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Component
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingForm({ pitchId, pricePerHour }: Props) {
  const router = useRouter();

  const [selDay,       setSelDay]       = useState<Date>(DAYS[0]);
  const [booked,       setBooked]       = useState<BookedSlot[]>([]);
  const [loadingSlots, setLoadingSlots] = useState(false);
  // startSlot: the clicked start label, e.g. "08:30"
  // endSlot:   the exact end boundary, e.g. "09:30" → booking is 08:30–09:30
  const [startSlot,    setStartSlot]    = useState<string | null>(null);
  const [endSlot,      setEndSlot]      = useState<string | null>(null);
  const [hoverSlot,    setHoverSlot]    = useState<string | null>(null);
  const [submitting,   setSubmitting]   = useState(false);
  const [apiError,     setApiError]     = useState<string | null>(null);
  const [success,      setSuccess]      = useState(false);

  const phase: Phase = !startSlot ? 'idle' : !endSlot ? 'picking-end' : 'done';

  useEffect(() => {
    setLoadingSlots(true);
    setStartSlot(null);
    setEndSlot(null);
    setHoverSlot(null);
    setApiError(null);
    api
      .get(`/pitches/${pitchId}/availability?date=${toDateStr(selDay)}`)
      .then(r  => setBooked(r.data.booked_slots ?? []))
      .catch(() => setBooked([]))
      .finally(() => setLoadingSlots(false));
  }, [pitchId, selDay]);

  function resetSelection() {
    setStartSlot(null);
    setEndSlot(null);
    setHoverSlot(null);
    setApiError(null);
  }

  // ── Pre-computed values ───────────────────────────────────────────────────

  // Set of slot labels whose 30-min period overlaps an existing booking
  const bookedSet = useMemo(
    () => new Set(SLOTS.filter(s => isSlotBooked(s, booked, selDay))),
    [booked, selDay],
  );

  const startIdx  = startSlot ? SLOTS.indexOf(startSlot) : -1;
  const startMins = startSlot ? toMins(startSlot) : -1;

  // Minutes of the first booked period that starts AFTER startSlot.
  // Valid end times must be <= this value (so the new booking doesn't overlap it).
  const nextBookedStartMins = useMemo(() => {
    if (startMins < 0) return 22 * 60;
    // iterate only the 30 period slots (i < 30), not the "22:00" boundary
    for (let i = 0; i < 30; i++) {
      const m = toMins(SLOTS[i]);
      if (m > startMins && bookedSet.has(SLOTS[i])) return m;
    }
    return 22 * 60;
  }, [startMins, bookedSet]);

  // ── State map for every slot ──────────────────────────────────────────────

  const slotStateMap = useMemo<Map<string, SlotState>>(() => {
    const map = new Map<string, SlotState>();
    // Use confirmed endSlot, or hoverSlot when still picking
    const activeEnd     = endSlot ?? (phase === 'picking-end' ? hoverSlot : null);
    const activeEndMins = activeEnd ? toMins(activeEnd) : -1;

    for (let i = 0; i < SLOTS.length; i++) {
      const s     = SLOTS[i];
      const sMins = toMins(s);

      // Already occupied by an existing booking
      if (bookedSet.has(s)) { map.set(s, 'booked'); continue; }

      // ── Idle phase ──────────────────────────────────────────────────────
      if (phase === 'idle') {
        // A slot is only a valid START if a 1-hour end can fit before 22:00
        // i.e. startTime + 60min ≤ 22:00 → startTime ≤ 21:00
        map.set(s, sMins <= 21 * 60 ? 'available' : 'disabled');
        continue;
      }

      // ── Picking-end / done ──────────────────────────────────────────────

      // The selected start slot itself
      if (i === startIdx) { map.set(s, 'selected-start'); continue; }

      // Slots consumed by the active selection (strictly between start and end boundary)
      // These are highlighted regardless of the disabled checks below.
      if (activeEndMins > 0 && sMins > startMins && sMins < activeEndMins) {
        map.set(s, endSlot ? 'in-range' : 'hover-range');
        continue;
      }

      // Before the start: locked
      if (sMins < startMins) { map.set(s, 'disabled'); continue; }

      // Within the 1-hour minimum gap — can't be an end time
      // (startMins < sMins < startMins+60 → booking would be < 60 min)
      if (sMins > startMins && sMins < startMins + 60) { map.set(s, 'disabled'); continue; }

      // Past the next booked slot — can't extend the booking here
      if (sMins > nextBookedStartMins) { map.set(s, 'disabled'); continue; }

      // Everything else is an available (clickable) end boundary.
      // This includes the confirmed endSlot itself — it shows as 'available'
      // (unlit) because it is the exclusive boundary, not a consumed period.
      map.set(s, 'available');
    }

    return map;
  }, [phase, startIdx, startMins, nextBookedStartMins, bookedSet, endSlot, hoverSlot]);

  // ── Derived booking values ────────────────────────────────────────────────

  // endSlot IS the exact end time (no +30min offset)
  const durationMins = startMins >= 0 && endSlot
    ? toMins(endSlot) - startMins : 0;
  const total = durationMins > 0
    ? Math.round((durationMins / 60) * pricePerHour * 100) / 100 : 0;
  const canSubmit = !!startSlot && !!endSlot && !submitting;

  // ── Slot interaction ──────────────────────────────────────────────────────

  function handleSlotClick(s: string) {
    setApiError(null);
    const sMins = toMins(s);

    if (phase === 'idle') {
      // Only startable slots (available, not booked, sMins ≤ 21:00)
      if (!bookedSet.has(s) && sMins <= 21 * 60) setStartSlot(s);
      return;
    }

    if (phase === 'picking-end') {
      if (s === startSlot) { resetSelection(); return; }
      // Valid end: startMins+60 ≤ sMins ≤ nextBookedStartMins
      const isValidEnd = sMins >= startMins + 60 && sMins <= nextBookedStartMins;
      if (isValidEnd) {
        setEndSlot(s);
        setHoverSlot(null);
      }
      return;
    }
    // phase === 'done': locked until reset button
  }

  // Hover validity is checked directly (not from slotStateMap) to avoid
  // stale-state race when the mouse moves between slots quickly.
  function handleSlotHover(s: string) {
    if (phase !== 'picking-end') { setHoverSlot(null); return; }
    const sMins = toMins(s);
    const isValidEnd = sMins >= startMins + 60 && sMins <= nextBookedStartMins;
    setHoverSlot(isValidEnd ? s : null);
  }

  // ── Submit ────────────────────────────────────────────────────────────────

  async function handleSubmit() {
    if (!canSubmit || !startSlot || !endSlot) return;
    setSubmitting(true);
    setApiError(null);

    try {
      await api.post('/bookings', {
        pitch_id:    pitchId,
        start_time:  buildDateTime(selDay, startSlot).toISOString(),
        end_time:    buildDateTime(selDay, endSlot).toISOString(),
        total_price: total,
      });
      setSuccess(true);
      setTimeout(() => router.push('/bookings'), 1800);
    } catch (err) {
      if (axios.isAxiosError(err)) {
        const code = err.response?.data?.error as string | undefined;
        const msg  = err.response?.data?.message as string | undefined;
        if (code === 'slot_unavailable')
          setApiError('هذا الوقت محجوز بالفعل، اختر وقتاً آخر');
        else if (err.response?.status === 401)
          setApiError('يجب تسجيل الدخول أولاً للقيام بالحجز');
        else if (code === 'invalid_time' || code === 'invalid_duration')
          setApiError(msg ?? 'الوقت المحدد غير صالح');
        else
          setApiError(msg ?? 'حدث خطأ ما، يرجى المحاولة مرة أخرى');
      } else {
        setApiError('تعذّر الاتصال بالخادم، تحقق من اتصالك');
      }
    } finally {
      setSubmitting(false);
    }
  }

  // ── Success screen ────────────────────────────────────────────────────────

  if (success) {
    return (
      <div className="rounded-2xl bg-[#141715] border border-white/[0.07] p-8 flex flex-col items-center gap-5 text-center">
        <div className="w-14 h-14 rounded-full bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
          <CheckCircle2 size={28} className="text-emerald-500" aria-hidden />
        </div>
        <div>
          <h3 className="text-[18px] font-bold text-[#f0efe8] mb-1.5">تم الحجز بنجاح!</h3>
          <p className="text-[12px] text-white/35">جاري التحويل إلى صفحة حجوزاتك...</p>
        </div>
        <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  // ── Main render ───────────────────────────────────────────────────────────

  const phaseHint =
    phase === 'idle'        ? 'اختر وقت البداية'                  :
    phase === 'picking-end' ? 'اختر وقت الانتهاء (ساعة كحد أدنى)' :
                              `${startSlot} ← ${endSlot}`;

  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.07] overflow-hidden">

      {/* ── Header ──────────────────────────────────────────────────────── */}
      <div className="px-6 pt-6 pb-5 border-b border-white/[0.05]">
        <p className="text-[10px] font-bold tracking-widest text-emerald-500 uppercase mb-1.5">
          احجز الملعب
        </p>
        <h2 className="text-[20px] font-bold text-[#f0efe8] tracking-tight leading-snug">
          اختر الموعد المناسب
        </h2>
      </div>

      <div className="px-6 py-5 flex flex-col gap-6">

        {/* ── Day strip ───────────────────────────────────────────────── */}
        <div>
          <p className="flex items-center gap-1.5 text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
            <CalendarDays size={11} className="text-emerald-500" aria-hidden />
            التاريخ
          </p>
          <div className="flex gap-2 overflow-x-auto pb-0.5" style={{ scrollbarWidth: 'none' }}>
            {DAYS.map((day, i) => {
              const active = toDateStr(day) === toDateStr(selDay);
              return (
                <button
                  key={toDateStr(day)}
                  type="button"
                  onClick={() => setSelDay(day)}
                  className={[
                    'flex-shrink-0 flex flex-col items-center gap-0.5 px-3.5 py-2.5 rounded-xl border',
                    'text-center transition-all duration-150 focus-visible:outline-none',
                    'focus-visible:ring-1 focus-visible:ring-emerald-500',
                    active
                      ? 'bg-emerald-500/[0.14] border-emerald-500/40'
                      : 'bg-white/[0.025] border-white/[0.06] hover:border-white/[0.14] hover:bg-white/[0.04]',
                  ].join(' ')}
                >
                  <span className={['text-[9px] font-bold tracking-wide', active ? 'text-emerald-400' : 'text-white/30'].join(' ')}>
                    {i === 0 ? 'اليوم' : AR_WEEKDAY[day.getDay()]}
                  </span>
                  <span className={['text-[20px] font-bold leading-tight', active ? 'text-emerald-300' : 'text-[#f0efe8]'].join(' ')}>
                    {day.getDate()}
                  </span>
                  <span className="text-[8px] text-white/20">{AR_MONTH[day.getMonth()]}</span>
                </button>
              );
            })}
          </div>
        </div>

        {/* ── Slot grid ───────────────────────────────────────────────── */}
        {loadingSlots ? (
          <div className="h-44 flex items-center justify-center">
            <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
          </div>
        ) : (
          <>
            {/* Status / instruction bar */}
            <div className="flex items-center justify-between min-h-[20px]">
              <p className={[
                'text-[11px] transition-colors duration-200',
                phase === 'picking-end' ? 'text-emerald-400/70' :
                phase === 'done'        ? 'text-emerald-300 font-mono' : 'text-white/35',
              ].join(' ')}>
                {phaseHint}
              </p>
              {startSlot && (
                <button
                  type="button"
                  onClick={resetSelection}
                  className="flex items-center gap-1 text-[10px] text-white/20 hover:text-white/45 transition-colors duration-150"
                >
                  <RefreshCw size={10} aria-hidden />
                  إعادة
                </button>
              )}
            </div>

            {/* Grid */}
            <div
              role="group"
              aria-label="اختر الفترة الزمنية"
              className="grid grid-cols-3 sm:grid-cols-4 lg:grid-cols-6 gap-1.5"
              onMouseLeave={() => setHoverSlot(null)}
            >
              {SLOTS.map(s => (
                <button
                  key={s}
                  type="button"
                  onClick={() => handleSlotClick(s)}
                  onMouseEnter={() => handleSlotHover(s)}
                  aria-label={`${s}${slotStateMap.get(s) === 'booked' ? ' (محجوز)' : ''}`}
                  className={slotClasses(slotStateMap.get(s) ?? 'available')}
                >
                  {s}
                </button>
              ))}
            </div>

            {/* Legend */}
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[10px] text-white/25">
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-white/[0.05] border border-white/[0.08]" aria-hidden />
                متاح
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-red-950/20 border border-red-900/[0.12]" aria-hidden />
                محجوز
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-emerald-500/25 border border-emerald-400/60" aria-hidden />
                محدد
              </span>
            </div>
          </>
        )}

        {/* ── Price summary ────────────────────────────────────────────── */}
        {canSubmit && (
          <div className="flex items-center justify-between px-4 py-3.5 rounded-xl bg-[#0d0f0e] border border-white/[0.05]">
            <div>
              <p className="text-[9px] font-bold text-white/20 tracking-widest uppercase mb-0.5">
                إجمالي الحجز
              </p>
              <p className="text-[11px] text-white/30">
                {durationLabel(durationMins)} × {pricePerHour} دينار
              </p>
            </div>
            <div className="flex items-baseline gap-1">
              <span className="text-[30px] font-bold text-[#f0efe8] leading-none tracking-tight">
                {total.toFixed(2)}
              </span>
              <span className="text-[13px] font-bold text-emerald-500">د.أ</span>
            </div>
          </div>
        )}

        {/* ── API error ────────────────────────────────────────────────── */}
        {apiError && (
          <div
            role="alert"
            className="rounded-xl px-4 py-3 text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] leading-relaxed"
          >
            {apiError}
          </div>
        )}

        {/* ── Confirm button ───────────────────────────────────────────── */}
        <button
          type="button"
          onClick={handleSubmit}
          disabled={!canSubmit}
          className={[
            'flex items-center justify-center gap-2.5 w-full py-3.5 rounded-xl mb-1',
            'text-[13px] font-bold tracking-wide',
            'transition-all duration-200 active:scale-[0.98]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
            'focus-visible:ring-offset-2 focus-visible:ring-offset-[#141715]',
            canSubmit
              ? 'bg-gradient-to-r from-green-600 to-emerald-500 text-white ' +
                'shadow-[0_4px_20px_rgba(16,185,129,0.22)] hover:shadow-[0_4px_28px_rgba(16,185,129,0.38)]'
              : 'bg-white/[0.04] text-white/20 border border-white/[0.05] cursor-not-allowed',
          ].join(' ')}
        >
          {submitting ? (
            <>
              <div className="w-4 h-4 rounded-full border-2 border-white/25 border-t-white animate-spin" />
              جاري الحجز...
            </>
          ) : (
            'تأكيد الحجز'
          )}
        </button>

      </div>
    </div>
  );
}
