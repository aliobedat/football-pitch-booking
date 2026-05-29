'use client';

import { useState, useEffect, useMemo } from 'react';
import { useRouter } from 'next/navigation';
import { CalendarDays, Clock, CheckCircle2, ArrowLeft } from 'lucide-react';
import axios from 'axios';
import api from '@/lib/api';

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────────────────
// Constants & helpers
// ─────────────────────────────────────────────────────────────────────────────

const AR_WEEKDAY = ['أحد', 'اثن', 'ثلا', 'أرب', 'خمي', 'جمع', 'سبت'];
const AR_MONTH   = ['يناير','فبراير','مارس','أبريل','مايو','يونيو',
                    'يوليو','أغسطس','سبتمبر','أكتوبر','نوفمبر','ديسمبر'];

// Returns "HH:MM" strings for every 30-min step in [fromMins, toMins].
function genTimes(fromMins: number, toMins: number): string[] {
  const out: string[] = [];
  for (let m = fromMins; m <= toMins; m += 30) {
    out.push(`${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`);
  }
  return out;
}

// Minimum booking = 1 hour  →  last valid start = 21:00 so booking ends by 22:00
const START_TIMES = genTimes(7 * 60, 21 * 60);   // 07:00 – 21:00
const ALL_TIMES   = genTimes(7 * 60, 22 * 60);   // 07:00 – 22:00  (end-time pool)

function toMins(t: string): number {
  const [h, m] = t.split(':').map(Number);
  return h * 60 + m;
}

function durationLabel(totalMins: number): string {
  const h = totalMins / 60;
  if (h === 1)       return 'ساعة واحدة';
  if (h === 1.5)     return 'ساعة ونصف';
  if (h === 2)       return 'ساعتان';
  if (h % 1 === 0)   return `${h} ساعات`;
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

const DAYS = upcomingDays();

// ─────────────────────────────────────────────────────────────────────────────
// Component
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingForm({ pitchId, pricePerHour }: Props) {
  const router = useRouter();

  const [selDay,       setSelDay]       = useState<Date>(DAYS[0]);
  const [booked,       setBooked]       = useState<BookedSlot[]>([]);
  const [loadingSlots, setLoadingSlots] = useState(false);
  const [startTime,    setStartTime]    = useState('');
  const [endTime,      setEndTime]      = useState('');
  const [submitting,   setSubmitting]   = useState(false);
  const [apiError,     setApiError]     = useState<string | null>(null);
  const [success,      setSuccess]      = useState(false);

  // Fetch booked slots whenever the selected day changes
  useEffect(() => {
    setLoadingSlots(true);
    setStartTime('');
    setEndTime('');
    setApiError(null);
    api
      .get(`/pitches/${pitchId}/availability?date=${toDateStr(selDay)}`)
      .then(r  => setBooked(r.data.booked_slots ?? []))
      .catch(() => setBooked([]))
      .finally(() => setLoadingSlots(false));
  }, [pitchId, selDay]);

  // Reset end time whenever start changes
  function handleStartChange(t: string) {
    setStartTime(t);
    setEndTime('');
    setApiError(null);
  }

  // End-time options: every time ≥ startTime + 60 min, up to 22:00
  const validEndTimes = useMemo(() => {
    if (!startTime) return [];
    const minEnd = toMins(startTime) + 60;
    return ALL_TIMES.filter(t => toMins(t) >= minEnd);
  }, [startTime]);

  // Duration (minutes) and total price
  const durationMins = startTime && endTime ? toMins(endTime) - toMins(startTime) : 0;
  const total        = durationMins > 0
    ? Math.round((durationMins / 60) * pricePerHour * 100) / 100
    : 0;

  // True if the chosen range overlaps any existing booking
  const hasConflict = useMemo(() => {
    if (!startTime || !endTime) return false;
    const selStart = buildDateTime(selDay, startTime);
    const selEnd   = buildDateTime(selDay, endTime);
    return booked.some(
      b => new Date(b.start_time) < selEnd && new Date(b.end_time) > selStart,
    );
  }, [startTime, endTime, booked, selDay]);

  const canSubmit = !!startTime && !!endTime && !hasConflict && !submitting;

  // ── Submit ────────────────────────────────────────────────────────────────

  async function handleSubmit() {
    if (!canSubmit) return;

    const start = buildDateTime(selDay, startTime);
    const end   = buildDateTime(selDay, endTime);

    setSubmitting(true);
    setApiError(null);

    try {
      await api.post('/bookings', {
        pitch_id:    pitchId,
        start_time:  start.toISOString(),
        end_time:    end.toISOString(),
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

  // ── Main form ─────────────────────────────────────────────────────────────

  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.07] overflow-hidden">

      {/* Header */}
      <div className="px-6 pt-6 pb-5 border-b border-white/[0.05]">
        <p className="text-[10px] font-bold tracking-widest text-emerald-500 uppercase mb-1.5">
          احجز الملعب
        </p>
        <h2 className="text-[20px] font-bold text-[#f0efe8] tracking-tight leading-snug">
          اختر الموعد المناسب
        </h2>
      </div>

      <div className="px-6 py-5 flex flex-col gap-6">

        {/* ── Day strip ──────────────────────────────────────────────────────── */}
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
                  <span className={[
                    'text-[9px] font-bold tracking-wide',
                    active ? 'text-emerald-400' : 'text-white/30',
                  ].join(' ')}>
                    {i === 0 ? 'اليوم' : AR_WEEKDAY[day.getDay()]}
                  </span>
                  <span className={[
                    'text-[20px] font-bold leading-tight',
                    active ? 'text-emerald-300' : 'text-[#f0efe8]',
                  ].join(' ')}>
                    {day.getDate()}
                  </span>
                  <span className="text-[8px] text-white/20">
                    {AR_MONTH[day.getMonth()]}
                  </span>
                </button>
              );
            })}
          </div>
        </div>

        {/* ── Time dropdowns ──────────────────────────────────────────────────── */}
        {loadingSlots ? (
          <div className="h-20 flex items-center justify-center">
            <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
          </div>
        ) : (
          <div className="grid grid-cols-2 gap-3">

            {/* Start time */}
            <div className="flex flex-col gap-1.5">
              <label className="flex items-center gap-1.5 text-[10px] font-bold text-white/30 tracking-widest uppercase">
                <Clock size={10} className="text-emerald-500" aria-hidden />
                وقت البداية
              </label>
              <select
                value={startTime}
                onChange={e => handleStartChange(e.target.value)}
                style={{ colorScheme: 'dark' }}
                className={[
                  'w-full rounded-xl border px-3 py-2.5 appearance-none',
                  'bg-[#0d0f0e] text-[13px] font-mono font-bold',
                  'focus:outline-none focus:ring-1 focus:ring-emerald-500',
                  'transition-colors duration-150 cursor-pointer',
                  startTime
                    ? 'border-emerald-500/30 text-emerald-300'
                    : 'border-white/[0.08] text-white/30',
                ].join(' ')}
              >
                <option value="" disabled>— اختر —</option>
                {START_TIMES.map(t => (
                  <option key={t} value={t} className="bg-[#1a1c1b] text-[#f0efe8]">{t}</option>
                ))}
              </select>
            </div>

            {/* End time */}
            <div className="flex flex-col gap-1.5">
              <label className="flex items-center gap-1.5 text-[10px] font-bold text-white/30 tracking-widest uppercase">
                <Clock size={10} className="text-emerald-500" aria-hidden />
                وقت الانتهاء
              </label>
              <select
                value={endTime}
                onChange={e => { setEndTime(e.target.value); setApiError(null); }}
                disabled={!startTime}
                style={{ colorScheme: 'dark' }}
                className={[
                  'w-full rounded-xl border px-3 py-2.5 appearance-none',
                  'bg-[#0d0f0e] text-[13px] font-mono font-bold',
                  'focus:outline-none focus:ring-1 focus:ring-emerald-500',
                  'transition-colors duration-150',
                  !startTime
                    ? 'opacity-40 cursor-not-allowed border-white/[0.08] text-white/20'
                    : 'cursor-pointer',
                  endTime
                    ? 'border-emerald-500/30 text-emerald-300'
                    : 'border-white/[0.08] text-white/30',
                ].join(' ')}
              >
                <option value="" disabled>— اختر —</option>
                {validEndTimes.map(t => (
                  <option key={t} value={t} className="bg-[#1a1c1b] text-[#f0efe8]">{t}</option>
                ))}
              </select>
            </div>

          </div>
        )}

        {/* ── Conflict warning ───────────────────────────────────────────────── */}
        {hasConflict && (
          <div
            role="alert"
            className="rounded-xl px-4 py-3 text-[11px] text-amber-400 bg-amber-500/[0.07] border border-amber-500/[0.18] leading-relaxed"
          >
            هذا الوقت متعارض مع حجز موجود مسبقاً. يرجى اختيار وقت آخر.
          </div>
        )}

        {/* ── Price summary ───────────────────────────────────────────────────── */}
        {startTime && endTime && !hasConflict && (
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

        {/* ── API error ───────────────────────────────────────────────────────── */}
        {apiError && (
          <div
            role="alert"
            className="rounded-xl px-4 py-3 text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] leading-relaxed"
          >
            {apiError}
          </div>
        )}

        {/* ── Confirm button ───────────────────────────────────────────────────── */}
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
              ? [
                  'bg-gradient-to-r from-green-600 to-emerald-500 text-white',
                  'shadow-[0_4px_20px_rgba(16,185,129,0.22)]',
                  'hover:shadow-[0_4px_28px_rgba(16,185,129,0.38)]',
                ].join(' ')
              : 'bg-white/[0.04] text-white/20 border border-white/[0.05] cursor-not-allowed',
          ].join(' ')}
        >
          {submitting ? (
            <>
              <div className="w-4 h-4 rounded-full border-2 border-white/25 border-t-white animate-spin" />
              جاري الحجز...
            </>
          ) : (
            <>
              {canSubmit && <ArrowLeft size={14} className="rotate-180" aria-hidden />}
              تأكيد الحجز
            </>
          )}
        </button>

      </div>
    </div>
  );
}
