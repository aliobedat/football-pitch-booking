'use client';

// جدول الملعب — the Day View. One pitch, one Amman day, answered in three
// glances: what's booked, what's free, how's the day going. Consumes
// GET /owner/day-view?pitch_id&date exclusively (PR-1 backend); zero writes.
//
// Amman semantics come from lib/amman (CivilDate/pad/addDays — shared with the
// Blocks tool); rendering from lib/format (Amman-pinned, Latin digits). No date
// library, no new Date() local-tz math. Selection (?pitch=&date=) lives in the URL
// so the view is shareable and refresh-proof — no localStorage.

import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import {
  CalendarClock, ChevronRight, ChevronLeft, RotateCcw, Ban, Plus,
} from 'lucide-react';
import api from '@/lib/api';
import { formatDate, formatTime, formatNumber, formatCurrency } from '@/lib/format';
import {
  type CivilDate, ammanCivilDate, ammanInstant, sameCivilDate, addDays, ymd, parseYmd,
} from '@/lib/amman';
import PaymentStatusPill from '@/components/PaymentStatusPill';
import DayViewDatePicker from '@/components/DayViewDatePicker';
import DayViewManualSheet from '@/components/DayViewManualSheet';

// ── Payload types (mirror the PR-1 DayView JSON) ─────────────────────────────
type SlotStatus = 'available' | 'booked' | 'blocked' | 'closed';

interface DVBooking {
  id: number;
  source: 'player' | 'manual' | 'academy' | 'block';
  status: string;
  attendance: string;
  payment_status: string;
  title: string;
  start_time: string;
  end_time: string;
}
interface DVSlot {
  start: string; // UTC RFC3339
  end: string;
  status: SlotStatus;
  partial: boolean;
  booking?: DVBooking | null;
}
interface DVSummary {
  total_bookings: number;
  booked_slots: number;
  booked_hours: number;
  available_slots: number;
  available_hours: number;
  confirmed_revenue: number;
}
interface DayViewData {
  pitch_id: number;
  pitch_name: string;
  is_active: boolean;
  date: string;
  timezone: string;
  slot_minutes: number;
  has_schedule: boolean;
  slots: DVSlot[];
  summary: DVSummary;
}
interface OwnerPitch { id: number; name: string; isActive: boolean }

type Filter = 'all' | 'booked' | 'available';

// Source badge for booked rows — matches the dashboard's existing colour language
// (sky=manual, violet=academy) and adds "أونلاين" for player/online bookings.
const SOURCE_BADGE: Record<string, { label: string; cls: string }> = {
  manual:  { label: 'يدوي',    cls: 'bg-sky-500/15 border-sky-500/30 text-sky-300' },
  player:  { label: 'أونلاين', cls: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-300' },
  academy: { label: 'أكاديمية', cls: 'bg-violet-500/15 border-violet-500/30 text-violet-300' },
};

// HH:MM (24h, Latin digits, Amman) — compact timeline labels.
const hm = (iso: string) => formatTime(iso, { hour: '2-digit', minute: '2-digit', hour12: false });

// One rendered timeline row: a real slot, or a collapsed run of closed slots.
type Row =
  | { kind: 'slot'; slot: DVSlot }
  | { kind: 'closed'; start: string; end: string };

function DayViewInner() {
  const router = useRouter();
  const sp = useSearchParams();

  const today = useMemo(() => ammanCivilDate(new Date()), []);
  const [date, setDate] = useState<CivilDate>(() => parseYmd(sp.get('date') ?? '') ?? ammanCivilDate(new Date()));
  const [pitchId, setPitchId] = useState<number | null>(() => {
    const p = sp.get('pitch');
    return p && /^\d+$/.test(p) ? Number(p) : null;
  });

  const [pitches, setPitches] = useState<OwnerPitch[]>([]);
  const [pitchesLoading, setPitchesLoading] = useState(true);
  const [pitchesError, setPitchesError] = useState<string | null>(null);

  const [data, setData] = useState<DayViewData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [filter, setFilter] = useState<Filter>('all');

  // Date picker + manual booking sheet.
  const [pickerOpen, setPickerOpen] = useState(false);
  const dateBtnRef = useRef<HTMLButtonElement>(null);
  const [anchorRect, setAnchorRect] = useState<DOMRect | null>(null);
  const [manual, setManual] = useState<{ prefill: string | null } | null>(null);

  const dateStr = useMemo(() => ymd(date), [date]);
  const isToday = sameCivilDate(date, today);

  // ── Pitch list (once). Auto-select the first pitch when the URL names none (or
  //    an unknown one). ─────────────────────────────────────────────────────────
  useEffect(() => {
    setPitchesLoading(true);
    api.get('/owner/pitches')
      .then(res => {
        const list = (res.data.data ?? []) as OwnerPitch[];
        setPitches(list);
        setPitchId(prev => (prev != null && list.some(p => p.id === prev) ? prev : (list[0]?.id ?? null)));
      })
      .catch(() => setPitchesError('تعذّر تحميل الملاعب.'))
      .finally(() => setPitchesLoading(false));
  }, []);

  // ── Day fetch — memoised on (pitchId, dateStr): exactly one request per change,
  //    no refetch loop from object identity (dateStr is a string). ──────────────
  const fetchDay = useCallback(() => {
    if (pitchId == null) return;
    setLoading(true);
    setError(null);
    api.get('/owner/day-view', { params: { pitch_id: pitchId, date: dateStr } })
      .then(res => setData(res.data.data as DayViewData))
      .catch(() => setError('تعذّر تحميل جدول الملعب. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, [pitchId, dateStr]);
  useEffect(() => { fetchDay(); }, [fetchDay]);

  // ── URL sync (shareable, refresh-proof). Does not drive the fetch, so it never
  //    causes an extra request. ──────────────────────────────────────────────────
  useEffect(() => {
    const params = new URLSearchParams();
    if (pitchId != null) params.set('pitch', String(pitchId));
    params.set('date', dateStr);
    router.replace(`/day-view?${params.toString()}`, { scroll: false });
  }, [pitchId, dateStr, router]);

  const counts = useMemo(() => {
    const s = data?.slots ?? [];
    return {
      all: s.length,
      booked: s.filter(x => x.status === 'booked' || x.status === 'blocked').length,
      available: s.filter(x => x.status === 'available').length,
    };
  }, [data]);

  // Filter client-side, then collapse consecutive closed cells into one row so a
  // dead night doesn't drown the list. Closed cells only appear under "الكل".
  const rows = useMemo<Row[]>(() => {
    const all = data?.slots ?? [];
    const vis = filter === 'booked'
      ? all.filter(x => x.status === 'booked' || x.status === 'blocked')
      : filter === 'available'
        ? all.filter(x => x.status === 'available')
        : all;
    const out: Row[] = [];
    for (let i = 0; i < vis.length; i++) {
      const slot = vis[i];
      if (slot.status === 'closed') {
        let end = slot.end;
        let j = i;
        while (j + 1 < vis.length && vis[j + 1].status === 'closed') { j++; end = vis[j].end; }
        out.push({ kind: 'closed', start: slot.start, end });
        i = j;
      } else {
        out.push({ kind: 'slot', slot });
      }
    }
    return out;
  }, [data, filter]);

  // Ordered available cells of the loaded day — the manual sheet's start-time set.
  const availableSlots = useMemo(
    () => (data?.slots ?? []).filter(s => s.status === 'available').map(s => ({ start: s.start, end: s.end })),
    [data],
  );

  const dateLabel = (isToday ? 'اليوم، ' : '')
    + formatDate(ammanInstant(date, 12).toISOString(), { weekday: 'long', day: 'numeric', month: 'long' });

  const openPicker = () => {
    setAnchorRect(dateBtnRef.current?.getBoundingClientRect() ?? null);
    setPickerOpen(true);
  };

  return (
    <div className="flex flex-col gap-4" dir="rtl">
      <div className="flex items-center gap-2">
        <CalendarClock size={20} className="text-emerald-400" aria-hidden />
        <h1 className="text-[20px] font-bold tracking-tight">جدول الملعب</h1>
      </div>

      {/* 1 ── Pitch chips ───────────────────────────────────────────────────── */}
      {pitchesLoading ? (
        <div className="flex gap-2">
          {[0, 1, 2].map(i => <div key={i} className="h-11 w-28 rounded-xl bg-white/[0.04] animate-pulse" />)}
        </div>
      ) : pitchesError ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{pitchesError}</div>
      ) : pitches.length === 0 ? (
        <div className="rounded-xl border border-white/[0.07] bg-[#141715] px-4 py-6 text-center text-[13px] text-white/40">
          لا توجد ملاعب لعرضها
        </div>
      ) : (
        <div className="chip-scroll flex gap-2 overflow-x-auto pb-1">
          {pitches.map(p => {
            const selected = p.id === pitchId;
            return (
              <button
                key={p.id}
                type="button"
                onClick={() => setPitchId(p.id)}
                aria-pressed={selected}
                className={[
                  'flex-shrink-0 inline-flex items-center gap-1.5 min-h-[44px] px-4 rounded-xl text-[13px] font-semibold border whitespace-nowrap transition-all active:scale-[0.98]',
                  selected
                    ? 'bg-emerald-500/15 border-emerald-500/45 text-emerald-300'
                    : 'bg-white/[0.03] border-white/[0.08] text-white/60 hover:text-white/85 hover:border-white/[0.16]',
                ].join(' ')}
              >
                {p.name}
                {!p.isActive && <span className="text-[10px] text-white/30">(غير نشط)</span>}
              </button>
            );
          })}
        </div>
      )}

      {/* 2 ── Date bar ──────────────────────────────────────────────────────── */}
      {pitches.length > 0 && (
        <div className="flex items-center justify-between gap-2">
          {/* right = previous, left = next (RTL) */}
          <button
            type="button"
            onClick={() => setDate(d => addDays(d, -1))}
            className="inline-flex items-center justify-center w-11 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] text-white/55 hover:text-white hover:border-white/20 transition-all active:scale-[0.97]"
            aria-label="اليوم السابق"
          >
            <ChevronRight size={18} aria-hidden />
          </button>

          <div className="flex items-center gap-2 flex-1 justify-center">
            {/* Tapping the label opens the hand-rolled RTL month grid (no native
                date input — its LTR chrome fights the Arabic page). */}
            <button
              ref={dateBtnRef}
              type="button"
              onClick={openPicker}
              className="inline-flex items-center justify-center px-3 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] hover:border-white/[0.16] transition-all"
              aria-haspopup="dialog"
              aria-expanded={pickerOpen}
            >
              <span className="text-[13px] font-bold text-[#f0efe8]">{dateLabel}</span>
            </button>
            {!isToday && (
              <button
                type="button"
                onClick={() => setDate(today)}
                className="inline-flex items-center gap-1 h-11 px-3 rounded-xl text-[12px] font-semibold text-emerald-300 border border-emerald-500/25 bg-emerald-500/[0.08] hover:bg-emerald-500/[0.14] transition-all"
              >
                <RotateCcw size={13} aria-hidden />
                اليوم
              </button>
            )}
          </div>

          <button
            type="button"
            onClick={() => setDate(d => addDays(d, 1))}
            className="inline-flex items-center justify-center w-11 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] text-white/55 hover:text-white hover:border-white/20 transition-all active:scale-[0.97]"
            aria-label="اليوم التالي"
          >
            <ChevronLeft size={18} aria-hidden />
          </button>
        </div>
      )}

      {/* 3–5 ── Summary + filters + timeline, or the loading/error states ─────── */}
      {pitchId == null && !pitchesLoading ? null : loading ? (
        <TimelineSkeleton />
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-4 flex items-center justify-between gap-3">
          <span className="text-[12.5px] text-red-400">{error}</span>
          <button
            type="button"
            onClick={fetchDay}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-semibold text-white/70 border border-white/[0.1] hover:border-white/25 transition-all"
          >
            <RotateCcw size={13} aria-hidden />
            إعادة المحاولة
          </button>
        </div>
      ) : data ? (
        <>
          {/* Summary strip — three items on mobile; revenue as a 4th on md+ only. */}
          <div className="flex items-center gap-3 flex-wrap rounded-xl border border-white/[0.07] bg-[#141715] px-4 py-2.5 text-[12.5px]">
            <SummaryItem value={formatNumber(data.summary.total_bookings)} label="حجوزات" tone="text-[#f0efe8]" />
            <Dot />
            <SummaryItem value={formatNumber(data.summary.booked_hours)} label="س محجوزة" tone="text-sky-300" />
            <Dot />
            <SummaryItem value={formatNumber(data.summary.available_hours)} label="س متاحة" tone="text-emerald-300" />
            <span className="hidden md:inline-flex items-center gap-3">
              <Dot />
              <SummaryItem value={`${formatCurrency(data.summary.confirmed_revenue, { minimumFractionDigits: 2 })} د.أ`} label="محصّل مؤكد" tone="text-emerald-300" />
            </span>
          </div>

          {/* Filter chips */}
          <div className="flex items-center gap-2">
            {([['all', 'الكل', counts.all], ['booked', 'محجوز', counts.booked], ['available', 'متاح', counts.available]] as [Filter, string, number][]).map(([val, label, n]) => {
              const on = filter === val;
              return (
                <button
                  key={val}
                  type="button"
                  onClick={() => setFilter(val)}
                  aria-pressed={on}
                  className={[
                    'inline-flex items-center gap-1.5 min-h-[44px] px-3.5 rounded-xl text-[12.5px] font-semibold border transition-all',
                    on ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300'
                       : 'bg-white/[0.03] border-white/[0.08] text-white/55 hover:text-white/80',
                  ].join(' ')}
                >
                  {label}
                  <span className={`text-[10px] font-mono ${on ? 'text-emerald-300/70' : 'text-white/30'}`}>{formatNumber(n)}</span>
                </button>
              );
            })}
          </div>

          {/* 5 ── Timeline ────────────────────────────────────────────────────── */}
          {rows.length === 0 ? (
            <div className="rounded-xl border border-white/[0.07] bg-[#141715] px-4 py-10 text-center text-[13px] text-white/35">
              لا توجد فترات مطابقة
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              {rows.map((row, i) => row.kind === 'closed'
                ? <ClosedRow key={`c-${i}`} start={row.start} end={row.end} />
                : <SlotRow key={row.slot.start} slot={row.slot} onPick={iso => setManual({ prefill: iso })} />)}
            </div>
          )}
        </>
      ) : null}

      {/* FAB — add a manual booking. Bottom-left (RTL), above the thumb zone,
          hidden while any sheet is open. */}
      {data && pitchId != null && !manual && !pickerOpen && (
        <button
          type="button"
          onClick={() => setManual({ prefill: null })}
          className="fixed bottom-6 left-4 z-40 inline-flex items-center gap-2 h-12 px-4 rounded-2xl bg-emerald-500 text-[#08130d] font-bold text-[13px] shadow-lg shadow-emerald-500/25 hover:bg-emerald-400 active:scale-[0.97] transition-all"
          aria-label="إضافة حجز يدوي"
        >
          <Plus size={18} aria-hidden />
          إضافة حجز
        </button>
      )}

      {pickerOpen && (
        <DayViewDatePicker
          value={date}
          anchorRect={anchorRect}
          onSelect={setDate}
          onClose={() => setPickerOpen(false)}
        />
      )}

      {manual && data && pitchId != null && (
        <DayViewManualSheet
          pitchId={pitchId}
          pitchName={data.pitch_name}
          availableSlots={availableSlots}
          prefillStart={manual.prefill}
          onClose={() => setManual(null)}
          onBooked={() => { setManual(null); fetchDay(); }}
          onRefetch={fetchDay}
        />
      )}

      <style jsx>{`
        .chip-scroll { scrollbar-width: none; }
        .chip-scroll::-webkit-scrollbar { display: none; }
      `}</style>
    </div>
  );
}

// ── Row renderers ────────────────────────────────────────────────────────────

function SlotRow({ slot, onPick }: { slot: DVSlot; onPick?: (startIso: string) => void }) {
  const range = (
    <span className="font-mono text-[11px] tabular-nums text-white/45 shrink-0" dir="ltr">
      {hm(slot.start)}<span className="mx-1 text-white/20">–</span>{hm(slot.end)}
    </span>
  );
  // Thin edge indicator for a partially-covered cell (no further treatment in PR-2).
  const partialEdge = slot.partial ? 'relative before:absolute before:inset-y-0 before:start-0 before:w-[3px] before:rounded-s-xl before:bg-white/40' : '';

  if (slot.status === 'available') {
    // Tap an available cell → open the manual sheet pre-filled to this start.
    return (
      <button
        type="button"
        onClick={() => onPick?.(slot.start)}
        className={`w-full min-h-[44px] flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-emerald-500/15 bg-emerald-500/[0.04] hover:bg-emerald-500/[0.09] hover:border-emerald-500/30 transition-all active:scale-[0.99] text-start ${partialEdge}`}
        aria-label={`متاح ${hm(slot.start)} — اضغط لإضافة حجز`}
      >
        {range}
        <span className="inline-flex items-center gap-1.5 text-[12px] font-semibold text-emerald-300/70">
          <Plus size={12} aria-hidden />
          متاح
        </span>
      </button>
    );
  }

  if (slot.status === 'blocked') {
    return (
      <div className={`flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-amber-500/25 bg-amber-500/[0.08] ${partialEdge}`}>
        {range}
        <span className="inline-flex items-center gap-1.5 text-[12px] font-semibold text-amber-300">
          <Ban size={12} aria-hidden />
          محجوز يدويًا / صيانة
        </span>
      </div>
    );
  }

  // booked
  const b = slot.booking;
  const badge = b ? SOURCE_BADGE[b.source] : undefined;
  return (
    <div className={`flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-white/[0.09] bg-white/[0.03] ${partialEdge}`}>
      <div className="min-w-0 flex items-center gap-2.5">
        {range}
        <div className="min-w-0">
          <p className="text-[13px] font-semibold text-[#f0efe8] truncate">{b?.title || 'حجز'}</p>
        </div>
      </div>
      <div className="flex items-center gap-1.5 shrink-0">
        {badge && (
          <span className={`inline-flex items-center px-1.5 py-0.5 rounded-md text-[9px] font-bold border ${badge.cls}`}>
            {badge.label}
          </span>
        )}
        {b && b.source !== 'block' && <PaymentStatusPill status={b.payment_status} />}
      </div>
    </div>
  );
}

function ClosedRow({ start, end }: { start: string; end: string }) {
  return (
    <div className="flex items-center justify-between gap-3 px-3.5 py-1.5 rounded-lg border border-white/[0.04] bg-white/[0.012]">
      <span className="font-mono text-[10px] tabular-nums text-white/25" dir="ltr">
        {hm(start)}<span className="mx-1">–</span>{hm(end)}
      </span>
      <span className="text-[11px] text-white/25">مغلق</span>
    </div>
  );
}

// ── Small pieces ─────────────────────────────────────────────────────────────

function SummaryItem({ value, label, tone }: { value: string; label: string; tone: string }) {
  return (
    <span className="inline-flex items-baseline gap-1">
      <span className={`font-bold ${tone}`}>{value}</span>
      <span className="text-white/40">{label}</span>
    </span>
  );
}

const Dot = () => <span className="text-white/15">·</span>;

function TimelineSkeleton() {
  return (
    <div className="flex flex-col gap-3">
      <div className="h-10 rounded-xl bg-white/[0.04] animate-pulse" />
      <div className="flex gap-2">
        {[0, 1, 2].map(i => <div key={i} className="h-10 w-20 rounded-xl bg-white/[0.04] animate-pulse" />)}
      </div>
      <div className="flex flex-col gap-1.5">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-12 rounded-xl bg-white/[0.03] animate-pulse" />
        ))}
      </div>
    </div>
  );
}

export default function DayViewPage() {
  return (
    <Suspense fallback={<TimelineSkeleton />}>
      <DayViewInner />
    </Suspense>
  );
}
