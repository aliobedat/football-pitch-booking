'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { CalendarDays, ArrowLeft, CheckCircle2 } from 'lucide-react';
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

type SlotState = 'selected' | 'booked' | 'past' | 'available';

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

// Hourly slots 07:00 – 22:00  (16 slots → 4 × 4 grid)
const SLOT_HOURS = Array.from({ length: 16 }, (_, i) => i + 7);

const AR_WEEKDAY = ['أحد', 'اثن', 'ثلا', 'أرب', 'خمي', 'جمع', 'سبت'];
const AR_MONTH   = ['يناير','فبراير','مارس','أبريل','مايو','يونيو',
                    'يوليو','أغسطس','سبتمبر','أكتوبر','نوفمبر','ديسمبر'];

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

function upcomingDays(): Date[] {
  return Array.from({ length: 7 }, (_, i) => {
    const d = new Date();
    d.setDate(d.getDate() + i);
    d.setHours(0, 0, 0, 0);
    return d;
  });
}

function toDateStr(d: Date): string {
  const y   = d.getFullYear();
  const m   = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

function fmt(h: number): string {
  return `${String(h).padStart(2, '0')}:00`;
}

function durationLabel(d: number): string {
  if (d === 1) return 'ساعة واحدة';
  if (d === 2) return 'ساعتان';
  return `${d} ساعات`;
}

// Computed once per module load — fresh for each new page visit in the browser
const DAYS = upcomingDays();

// ─────────────────────────────────────────────────────────────────────────────
// Component
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingForm({ pitchId, pricePerHour }: Props) {
  const router = useRouter();

  const [selDay,  setSelDay]  = useState<Date>(DAYS[0]);
  const [booked,  setBooked]  = useState<BookedSlot[]>([]);
  const [loading, setLoading] = useState(false);

  // startH inclusive, endH exclusive  (endH = startH + 1 when single slot)
  const [startH, setStartH] = useState<number | null>(null);
  const [endH,   setEndH]   = useState<number | null>(null);

  const [submitting, setSubmitting] = useState(false);
  const [apiError,   setApiError]   = useState<string | null>(null);
  const [success,    setSuccess]    = useState(false);

  // Fetch booked slots whenever the selected day changes
  useEffect(() => {
    setLoading(true);
    setStartH(null);
    setEndH(null);
    setApiError(null);
    api
      .get(`/pitches/${pitchId}/availability?date=${toDateStr(selDay)}`)
      .then(r  => setBooked(r.data.booked_slots ?? []))
      .catch(() => setBooked([]))
      .finally(() => setLoading(false));
  }, [pitchId, selDay]);

  // ── Slot state helpers ──────────────────────────────────────────────────────

  function slotBooked(h: number): boolean {
    const s = new Date(selDay); s.setHours(h, 0, 0, 0);
    const e = new Date(selDay); e.setHours(h + 1, 0, 0, 0);
    return booked.some(b => new Date(b.start_time) < e && new Date(b.end_time) > s);
  }

  function slotPast(h: number): boolean {
    const s = new Date(selDay); s.setHours(h, 0, 0, 0);
    return s <= new Date();
  }

  function slotDisabled(h: number): boolean { return slotBooked(h) || slotPast(h); }

  function slotState(h: number): SlotState {
    if (slotBooked(h)) return 'booked';
    if (slotPast(h))   return 'past';
    if (startH !== null) {
      const end = endH ?? startH + 1;
      if (h >= startH && h < end) return 'selected';
    }
    return 'available';
  }

  // ── Selection logic ─────────────────────────────────────────────────────────

  function handleSlot(h: number) {
    if (slotDisabled(h)) return;
    setApiError(null);

    // No start yet, or a complete range already exists → begin fresh
    if (startH === null || endH !== null) {
      setStartH(h); setEndH(null); return;
    }
    // Same slot → deselect
    if (h === startH) { setStartH(null); return; }
    // Earlier slot → reset start
    if (h < startH)  { setStartH(h); setEndH(null); return; }

    // Later slot → extend if no blocked slots in between
    const gap = Array.from({ length: h - startH - 1 }, (_, i) => startH + 1 + i);
    if (gap.some(slotDisabled)) {
      setStartH(h); setEndH(null); return;
    }
    setEndH(h + 1); // exclusive
  }

  // ── Derived values ──────────────────────────────────────────────────────────

  const effectiveEnd = startH !== null ? (endH ?? startH + 1) : null;
  const duration     = startH !== null && effectiveEnd !== null ? effectiveEnd - startH : 0;
  const total        = duration * pricePerHour;

  // ── Submit ──────────────────────────────────────────────────────────────────

  async function handleSubmit() {
    if (startH === null || submitting) return;

    const start = new Date(selDay); start.setHours(startH, 0, 0, 0);
    const end   = new Date(selDay); end.setHours(effectiveEnd!, 0, 0, 0);

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
        if      (code === 'slot_unavailable')                             setApiError('هذا الوقت محجوز بالفعل، اختر وقتاً آخر');
        else if (err.response?.status === 401)                            setApiError('يجب تسجيل الدخول أولاً للقيام بالحجز');
        else if (code === 'invalid_time' || code === 'invalid_duration')  setApiError(msg ?? 'الوقت المحدد غير صالح');
        else                                                               setApiError(msg ?? 'حدث خطأ ما، يرجى المحاولة مرة أخرى');
      } else {
        setApiError('تعذّر الاتصال بالخادم، تحقق من اتصالك');
      }
    } finally {
      setSubmitting(false);
    }
  }

  // ── Success screen ──────────────────────────────────────────────────────────

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

  // ── Main form ───────────────────────────────────────────────────────────────

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

        {/* ── Slot grid ──────────────────────────────────────────────────────── */}
        <div>
          <p className="text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
            الأوقات المتاحة
          </p>

          {loading ? (
            <div className="h-28 flex items-center justify-center">
              <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
            </div>
          ) : (
            <div className="grid grid-cols-4 gap-1.5">
              {SLOT_HOURS.map(h => {
                const state    = slotState(h);
                const disabled = state === 'booked' || state === 'past';
                return (
                  <button
                    key={h}
                    type="button"
                    disabled={disabled}
                    onClick={() => handleSlot(h)}
                    className={[
                      'relative py-2.5 rounded-xl border text-[11px] font-mono font-bold tracking-wide',
                      'transition-all duration-100',
                      'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-emerald-500',
                      state === 'selected'
                        ? 'bg-emerald-500/20 border-emerald-500/50 text-emerald-300 scale-[1.04]'
                        : state === 'booked'
                        ? 'bg-red-500/[0.05] border-red-500/[0.10] text-red-400/25 cursor-not-allowed overflow-hidden'
                        : state === 'past'
                        ? 'bg-transparent border-white/[0.03] text-white/[0.12] cursor-not-allowed'
                        : [
                            'bg-white/[0.025] border-white/[0.06] text-white/50 cursor-pointer',
                            'hover:bg-emerald-500/[0.07] hover:border-emerald-500/25 hover:text-emerald-300/80',
                            'active:scale-[0.96]',
                          ].join(' '),
                    ].join(' ')}
                  >
                    {fmt(h)}
                    {state === 'booked' && (
                      <span aria-hidden className="absolute inset-0 flex items-center justify-center pointer-events-none">
                        <span className="block w-[65%] h-px bg-red-400/20 -rotate-6" />
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          )}

          {/* Selection hint */}
          <div className="h-5 mt-2.5 flex items-center justify-center">
            {startH === null ? (
              <p className="text-[10px] text-white/20">اضغط على وقت البداية</p>
            ) : endH === null ? (
              <p className="text-[10px] text-emerald-400/60">
                يبدأ {fmt(startH)} · اضغط للانتهاء أو احجز ساعة واحدة
              </p>
            ) : (
              <p className="text-[10px] font-semibold text-emerald-400">
                {fmt(startH)} – {fmt(endH)} · {durationLabel(duration)}
              </p>
            )}
          </div>
        </div>

        {/* ── Price summary ───────────────────────────────────────────────────── */}
        {startH !== null && (
          <div className="flex items-center justify-between px-4 py-3.5 rounded-xl bg-[#0d0f0e] border border-white/[0.05]">
            <div>
              <p className="text-[9px] font-bold text-white/20 tracking-widest uppercase mb-0.5">
                إجمالي الحجز
              </p>
              <p className="text-[11px] text-white/30">
                {duration} {duration === 1 ? 'ساعة' : 'ساعات'} × {pricePerHour} دينار
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

        {/* ── Error banner ─────────────────────────────────────────────────────── */}
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
          disabled={startH === null || submitting}
          className={[
            'flex items-center justify-center gap-2.5 w-full py-3.5 rounded-xl mb-1',
            'text-[13px] font-bold tracking-wide',
            'transition-all duration-200 active:scale-[0.98]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
            'focus-visible:ring-offset-2 focus-visible:ring-offset-[#141715]',
            startH !== null && !submitting
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
              {startH !== null && <ArrowLeft size={14} className="rotate-180" aria-hidden />}
              تأكيد الحجز
            </>
          )}
        </button>

      </div>
    </div>
  );
}
