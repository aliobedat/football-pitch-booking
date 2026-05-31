'use client';

import { useState, useEffect, useMemo } from 'react';
import { useRouter } from 'next/navigation';
import { CalendarDays, CheckCircle2 } from 'lucide-react';
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

// ─── Helpers ──────────────────────────────────────────────────────────────────

function todayStr(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function parseDateInput(s: string): Date {
  const [y, mo, d] = s.split('-').map(Number);
  const date = new Date(y, mo - 1, d);
  date.setHours(0, 0, 0, 0);
  return date;
}

function minsToTime(mins: number): string {
  return `${String(Math.floor(mins / 60)).padStart(2, '0')}:${String(mins % 60).padStart(2, '0')}`;
}

function buildDateTime(day: Date, timeStr: string): Date {
  const [h, m] = timeStr.split(':').map(Number);
  const d = new Date(day);
  d.setHours(h, m, 0, 0); // setHours(24,0,0,0) rolls to next-day midnight — intentional for "24:00"
  return d;
}

// True if the 30-min block starting at slotMins overlaps any existing booking.
function isSlotBooked(slotMins: number, booked: BookedSlot[], day: Date): boolean {
  const slotStart = buildDateTime(day, minsToTime(slotMins)).getTime();
  const slotEnd   = slotStart + 30 * 60 * 1000;
  return booked.some(b => {
    const bStart = new Date(b.start_time).getTime();
    const bEnd   = new Date(b.end_time).getTime();
    return bStart < slotEnd && bEnd > slotStart;
  });
}

// True if the range [startMins, endMins) overlaps any existing booking.
function rangeOverlapsBookings(startMins: number, endMins: number, booked: BookedSlot[], day: Date): boolean {
  const rangeStart = buildDateTime(day, minsToTime(startMins)).getTime();
  const rangeEnd   = buildDateTime(day, minsToTime(endMins)).getTime();
  return booked.some(b => {
    const bStart = new Date(b.start_time).getTime();
    const bEnd   = new Date(b.end_time).getTime();
    return bStart < rangeEnd && bEnd > rangeStart;
  });
}

function durationLabel(mins: number): string {
  if (mins === 60)  return 'ساعة';
  if (mins === 90)  return 'ساعة ونصف';
  if (mins === 120) return 'ساعتان';
  return `${mins / 60} ساعة`;
}

// ─────────────────────────────────────────────────────────────────────────────
// Component
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingForm({ pitchId, pricePerHour }: Props) {
  const router = useRouter();

  const [selDayStr, setSelDayStr] = useState<string>(todayStr());
  const selDay = useMemo(() => parseDateInput(selDayStr), [selDayStr]);

  // Default AM/PM based on current hour
  const [amPm,     setAmPm]     = useState<'am' | 'pm'>(() => new Date().getHours() < 12 ? 'am' : 'pm');
  const [baseHour, setBaseHour] = useState<number | null>(null); // 0–23
  const [startMod, setStartMod] = useState<0 | 30>(0);           // :00 or :30
  const [duration, setDuration] = useState<60 | 90 | 120>(60);   // minutes

  const [booked,       setBooked]       = useState<BookedSlot[]>([]);
  const [loadingSlots, setLoadingSlots] = useState(false);
  const [submitting,   setSubmitting]   = useState(false);
  const [apiError,     setApiError]     = useState<string | null>(null);
  const [success,      setSuccess]      = useState(false);

  useEffect(() => {
    setLoadingSlots(true);
    setBaseHour(null);
    setApiError(null);
    api
      .get(`/pitches/${pitchId}/availability?date=${selDayStr}`)
      .then(r  => setBooked(r.data.booked_slots ?? []))
      .catch(() => setBooked([]))
      .finally(() => setLoadingSlots(false));
  }, [pitchId, selDayStr]);

  // Current local time in minutes from midnight; -1 when selDay is not today.
  const nowMins = useMemo(() => {
    if (selDayStr !== todayStr()) return -1;
    const now = new Date();
    return now.getHours() * 60 + now.getMinutes();
  }, [selDayStr]);

  // ── Slot availability helpers (30-min granularity) ────────────────────────

  function slotUnavailable(mins: number): boolean {
    if (nowMins >= 0 && mins <= nowMins) return true;
    return isSlotBooked(mins, booked, selDay);
  }

  function slotIsPast(mins: number): boolean {
    return nowMins >= 0 && mins <= nowMins;
  }

  // ── Grid helpers ──────────────────────────────────────────────────────────

  // Hours shown in the 12-slot grid
  const gridHours = amPm === 'am'
    ? [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]
    : [12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23];

  // An hour is disabled only when BOTH its :00 and :30 slots are unavailable.
  function isHourDisabled(h: number): boolean {
    return slotUnavailable(h * 60) && slotUnavailable(h * 60 + 30);
  }

  // ── Fine-tuning validation ────────────────────────────────────────────────

  function isModDisabled(mod: 0 | 30): boolean {
    if (baseHour === null) return true;
    return slotUnavailable(baseHour * 60 + mod);
  }

  function isDurationDisabled(d: 60 | 90 | 120): boolean {
    if (baseHour === null) return true;
    const startMs = baseHour * 60 + startMod;
    const endMs   = startMs + d;
    if (endMs > 24 * 60) return true;
    return rangeOverlapsBookings(startMs, endMs, booked, selDay);
  }

  // ── Derived booking values ────────────────────────────────────────────────

  const actualStartMins = baseHour !== null ? baseHour * 60 + startMod : -1;
  const actualEndMins   = actualStartMins >= 0 ? actualStartMins + duration : -1;
  const actualStartStr  = actualStartMins >= 0 ? minsToTime(actualStartMins) : null;
  const actualEndStr    = actualEndMins   >= 0 ? minsToTime(actualEndMins)   : null;
  const total           = actualStartMins >= 0
    ? Math.round((duration / 60) * pricePerHour * 100) / 100 : 0;

  const canSubmit =
    baseHour !== null &&
    !isModDisabled(startMod) &&
    !isDurationDisabled(duration) &&
    !submitting;

  // ── Interaction handlers ──────────────────────────────────────────────────

  function handleAmPm(val: 'am' | 'pm') {
    setAmPm(val);
    setBaseHour(null);
    setApiError(null);
  }

  function handleHourClick(h: number) {
    if (isHourDisabled(h)) return;
    setApiError(null);
    // Clicking the already-selected hour deselects it
    if (baseHour === h) { setBaseHour(null); return; }
    setBaseHour(h);
    // Auto-select first available modifier
    const newMod = (!slotUnavailable(h * 60) ? 0 : 30) as 0 | 30;
    setStartMod(newMod);
    setDuration(60);
  }

  function handleModClick(mod: 0 | 30) {
    if (isModDisabled(mod)) return;
    setStartMod(mod);
    setApiError(null);
  }

  function handleDurationClick(d: 60 | 90 | 120) {
    if (isDurationDisabled(d)) return;
    setDuration(d);
    setApiError(null);
  }

  // ── Submit ────────────────────────────────────────────────────────────────

  async function handleSubmit() {
    if (!canSubmit || !actualStartStr || !actualEndStr) return;
    setSubmitting(true);
    setApiError(null);

    try {
      await api.post('/bookings', {
        pitch_id:    pitchId,
        start_time:  buildDateTime(selDay, actualStartStr).toISOString(),
        end_time:    buildDateTime(selDay, actualEndStr).toISOString(),
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

  // ── Shared button style factories ─────────────────────────────────────────

  const optionBtn = (selected: boolean, disabled: boolean, extra = '') =>
    [
      'rounded-xl border transition-all duration-150 font-bold select-none',
      selected
        ? 'bg-emerald-500/20 border-emerald-500/40 text-emerald-300'
        : disabled
          ? 'bg-white/[0.01] border-white/[0.03] text-white/15 cursor-not-allowed opacity-40'
          : [
              'bg-white/[0.04] border-white/[0.08] text-white/50 cursor-pointer',
              'hover:bg-emerald-500/10 hover:border-emerald-500/25 hover:text-emerald-300',
            ].join(' '),
      extra,
    ].join(' ');

  // ── Main render ───────────────────────────────────────────────────────────

  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.07] overflow-hidden">

      {/* ── Header ── */}
      <div className="px-6 pt-6 pb-5 border-b border-white/[0.05]">
        <p className="text-[10px] font-bold tracking-widest text-emerald-500 uppercase mb-1.5">
          احجز الملعب
        </p>
        <h2 className="text-[20px] font-bold text-[#f0efe8] tracking-tight leading-snug">
          اختر الموعد المناسب
        </h2>
      </div>

      <div className="px-6 py-5 flex flex-col gap-6">

        {/* ── Date picker ── */}
        <div>
          <p className="flex items-center gap-1.5 text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
            <CalendarDays size={11} className="text-emerald-500" aria-hidden />
            التاريخ
          </p>
          <input
            type="date"
            value={selDayStr}
            min={todayStr()}
            onChange={e => { if (e.target.value) setSelDayStr(e.target.value); }}
            className={[
              'w-full rounded-xl border border-white/[0.09] px-4 py-2.5',
              'bg-[#0d0f0e] text-[13px] text-[#f0efe8]',
              'hover:border-white/[0.18] focus:outline-none',
              'focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/[0.12]',
              'transition-all duration-150 [color-scheme:dark]',
            ].join(' ')}
          />
        </div>

        {loadingSlots ? (
          <div className="h-44 flex items-center justify-center">
            <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
          </div>
        ) : (
          <>
            {/* ── AM / PM Toggle ── */}
            <div>
              <p className="text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
                الفترة
              </p>
              <div className="flex rounded-xl border border-white/[0.08] bg-[#0d0f0e] p-1 gap-1">
                {(['am', 'pm'] as const).map(val => (
                  <button
                    key={val}
                    type="button"
                    onClick={() => handleAmPm(val)}
                    className={[
                      'flex-1 py-2.5 rounded-lg text-[13px] font-bold tracking-wide',
                      'transition-all duration-150 border',
                      amPm === val
                        ? 'bg-emerald-500/20 border-emerald-500/30 text-emerald-300'
                        : 'border-transparent text-white/35 hover:text-white/60 hover:bg-white/[0.04]',
                    ].join(' ')}
                  >
                    {val === 'am' ? 'صباحاً' : 'مساءً'}
                  </button>
                ))}
              </div>
            </div>

            {/* ── 12-Hour Grid ── */}
            <div>
              <p className="text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
                الساعة
              </p>
              <div role="group" aria-label="اختر الساعة" className="grid grid-cols-4 gap-2">
                {gridHours.map(h => {
                  const disabled = isHourDisabled(h);
                  const selected = baseHour === h;
                  return (
                    <button
                      key={h}
                      type="button"
                      onClick={() => handleHourClick(h)}
                      disabled={disabled}
                      aria-pressed={selected}
                      title={disabled ? 'لا توجد أوقات متاحة في هذه الساعة' : undefined}
                      className={[
                        'h-14 rounded-xl border transition-all duration-150 select-none',
                        'flex flex-col items-center justify-center gap-0.5',
                        selected
                          ? 'bg-emerald-500/25 border-emerald-400/60 text-emerald-300 shadow-[0_0_14px_rgba(52,211,153,0.15)]'
                          : disabled
                            ? 'bg-white/[0.01] border-white/[0.03] text-white/10 cursor-not-allowed'
                            : [
                                'bg-white/[0.03] border-white/[0.07] text-white/55 cursor-pointer',
                                'hover:bg-emerald-500/10 hover:border-emerald-500/30 hover:text-emerald-300',
                              ].join(' '),
                      ].join(' ')}
                    >
                      <span className="text-[16px] font-mono font-bold leading-none">
                        {String(h).padStart(2, '0')}
                      </span>
                      <span className="text-[9px] font-semibold opacity-50 tracking-wider">:00</span>
                    </button>
                  );
                })}
              </div>
            </div>

            {/* ── Fine-Tuning Panel ── */}
            {baseHour !== null && (
              <div className="rounded-xl border border-white/[0.08] bg-[#0d0f0e] p-4 flex flex-col gap-5">

                {/* Start modifier */}
                <div>
                  <p className="text-[10px] font-bold text-white/25 tracking-widest uppercase mb-2.5">
                    وقت البداية بالضبط
                  </p>
                  <div className="grid grid-cols-2 gap-2">
                    {([0, 30] as const).map(mod => {
                      const dis = isModDisabled(mod);
                      const sel = startMod === mod;
                      const tip = !dis ? undefined
                        : slotIsPast(baseHour * 60 + mod) ? 'الوقت انقضى' : 'محجوز';
                      return (
                        <button
                          key={mod}
                          type="button"
                          onClick={() => handleModClick(mod)}
                          disabled={dis}
                          title={tip}
                          aria-pressed={sel}
                          className={optionBtn(sel, dis, 'py-3 text-[14px] font-mono')}
                        >
                          {minsToTime(baseHour * 60 + mod)}
                        </button>
                      );
                    })}
                  </div>
                </div>

                {/* Duration */}
                <div>
                  <p className="text-[10px] font-bold text-white/25 tracking-widest uppercase mb-2.5">
                    المدة
                  </p>
                  <div className="grid grid-cols-3 gap-2">
                    {([60, 90, 120] as const).map(d => {
                      const dis = isDurationDisabled(d);
                      const sel = duration === d;
                      return (
                        <button
                          key={d}
                          type="button"
                          onClick={() => handleDurationClick(d)}
                          disabled={dis}
                          title={dis ? 'يتعارض مع حجز موجود أو يتجاوز منتصف الليل' : undefined}
                          aria-pressed={sel}
                          className={optionBtn(sel, dis, 'py-3 text-[12px]')}
                        >
                          {durationLabel(d)}
                        </button>
                      );
                    })}
                  </div>
                </div>
              </div>
            )}

            {/* ── Legend ── */}
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[10px] text-white/25">
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-white/[0.05] border border-white/[0.08]" aria-hidden />
                متاح
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-white/[0.01] border border-white/[0.03]" aria-hidden />
                غير متاح
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-emerald-500/25 border border-emerald-400/60" aria-hidden />
                محدد
              </span>
            </div>
          </>
        )}

        {/* ── Price summary ── */}
        {canSubmit && actualStartStr && actualEndStr && (
          <div className="flex items-center justify-between px-4 py-3.5 rounded-xl bg-[#0d0f0e] border border-white/[0.05]">
            <div>
              <p className="text-[9px] font-bold text-white/20 tracking-widest uppercase mb-0.5">
                إجمالي الحجز
              </p>
              <p className="text-[11px] text-white/30 font-mono">
                {actualStartStr} ← {actualEndStr}
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

        {/* ── API error ── */}
        {apiError && (
          <div
            role="alert"
            className="rounded-xl px-4 py-3 text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] leading-relaxed"
          >
            {apiError}
          </div>
        )}

        {/* ── Confirm button ── */}
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
