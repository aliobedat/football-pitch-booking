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
  CalendarClock, ChevronRight, ChevronLeft, RotateCcw, Ban, Plus, Repeat, Check,
} from 'lucide-react';
import api from '@/lib/api';
import { formatDate, formatTime, formatNumber, formatCurrency } from '@/lib/format';
import {
  type CivilDate, ammanCivilDate, ammanInstant, sameCivilDate, addDays, ymd, parseYmd,
} from '@/lib/amman';
import { useAmmanToday } from '@/lib/useAmmanToday';
import DayViewDatePicker from '@/components/DayViewDatePicker';
import DayViewManualSheet from '@/components/DayViewManualSheet';
import BookingSheet, { paymentDisplayBadge } from '@/components/BookingSheet';

// ── Payload types (mirror the PR-1 DayView JSON) ─────────────────────────────
type SlotStatus = 'available' | 'booked' | 'blocked' | 'closed';

interface DVBooking {
  id: number;
  source: 'player' | 'manual' | 'academy' | 'block';
  status: string;
  attendance: string;
  payment_status: string; // legacy (unchanged)
  title: string;
  start_time: string;
  end_time: string;
  // Booking-sheet money fields (PR-A backend; additive). amount_paid/remaining
  // are nullable (null = untracked); payment_display is server-derived.
  total_price: number;
  amount_paid: number | null;
  payment_display: 'untracked' | 'unpaid' | 'partial' | 'paid';
  remaining: number | null;
  recurrence_group_id: string | null; // WO-SERIES-CANCEL (additive; null = one-off)
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
  price_per_hour: number; // whole-JOD hourly rate (PR-A; for extension projection)
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
//
// A booked/blocked row may span SEVERAL 30-minute cells of the same booking
// (WO-OWNER-SLOTS). It keeps its FIRST cell — so the React key stays the unique
// cell start — and carries the label bounds taken from the BOOKING itself.
// `labelStart`/`labelEnd` are absent on available/closed rows, where the cell's
// own bounds are the truth.
//
// A booking may extend beyond this civil day at EITHER edge. Each bound that
// falls outside the day is clamped to it and flagged, because the time labels
// carry no date: an unclamped bound silently names a different day.
type Row =
  | {
      kind: 'slot';
      slot: DVSlot;
      labelStart?: string;
      labelEnd?: string;
      continuesFromPrevDay?: boolean;
      continuesIntoNextDay?: boolean;
    }
  | { kind: 'closed'; start: string; end: string };

function DayViewInner() {
  const router = useRouter();
  const sp = useSearchParams();

  // Reactive: a dashboard left open across Amman midnight must roll over.
  const today = useAmmanToday();
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

  // Date picker + manual booking sheet + booking-details sheet (by booking id).
  const [pickerOpen, setPickerOpen] = useState(false);
  const dateBtnRef = useRef<HTMLButtonElement>(null);
  const [anchorRect, setAnchorRect] = useState<DOMRect | null>(null);
  const [manual, setManual] = useState<{ prefill: string | null } | null>(null);
  const [sheetId, setSheetId] = useState<number | null>(null);
  // Transient confirmation toast (WO-SERIES-CANCEL: shown after a cancel).
  const [toast, setToast] = useState<string | null>(null);
  const showToast = useCallback((msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 2500);
  }, []);

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
  //    no refetch loop from object identity (dateStr is a string). `silent` skips
  //    the timeline skeleton so a sheet action refetches without flashing the grid.
  //    Returns the promise so a sheet can await the refetch before clearing its
  //    in-flight state (ruling 7: authoritative money, never optimistic). A stale
  //    guard drops an out-of-order response (the reports-race fix pattern). ──────
  const reqSeq = useRef(0);
  const fetchDay = useCallback((silent = false): Promise<void> => {
    if (pitchId == null) return Promise.resolve();
    const seq = ++reqSeq.current;
    if (!silent) setLoading(true);
    setError(null);
    return api.get('/owner/day-view', { params: { pitch_id: pitchId, date: dateStr } })
      .then(res => { if (seq === reqSeq.current) setData(res.data.data as DayViewData); })
      .catch(() => { if (seq === reqSeq.current) setError('تعذّر تحميل جدول الملعب. تأكد من صلاحيات الحساب.'); })
      .finally(() => { if (!silent && seq === reqSeq.current) setLoading(false); });
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

  // Rows for a given filter. Extracted so the filter CHIP COUNTS can be the
  // length of the list each filter would actually render — counting raw cells
  // made the chip read "محجوز ١٤" above four rows, which is the same "why does
  // it say 3?" confusion this WO set out to remove, just relocated.
  const rowsFor = useCallback((f: Filter): Row[] => {
    const all = data?.slots ?? [];
    const vis = f === 'booked'
      ? all.filter(x => x.status === 'booked' || x.status === 'blocked')
      : f === 'available'
        ? all.filter(x => x.status === 'available')
        : all;
    // Local midnight of the day these SLOTS belong to — the clamp reference for
    // a booking that spilled in from last night.
    //
    // Taken from `data.date` (the payload the rows are built from), NOT the
    // `date` state. Tapping "next day" re-renders with the new date before the
    // fetch resolves, so for one paint the OLD day's slots would be measured
    // against the NEW day's midnight — printing a spurious "مستمر من ليلة أمس"
    // on a row whose start is then ~23 hours wrong. Sourcing both from the same
    // payload makes that mismatch structurally impossible instead of a race.
    // If the payload's own date is missing or unparseable we do NOT fall back to
    // the `date` state: that is precisely the mismatch this is here to prevent,
    // and clamping against an unrelated day would print a confidently wrong
    // time. Skipping the clamp under-explains a spill; clamping wrongly lies.
    const payloadDate = data?.date ? parseYmd(data.date) : null;
    const dayStartMs = payloadDate ? ammanInstant(payloadDate, 0).getTime() : null;
    const dayEndMs = payloadDate ? ammanInstant(payloadDate, 24).getTime() : null; // exclusive end

    const out: Row[] = [];
    for (let i = 0; i < vis.length; i++) {
      const slot = vis[i];

      if (slot.status === 'closed') {
        let end = slot.end;
        let j = i;
        while (j + 1 < vis.length && vis[j + 1].status === 'closed') { j++; end = vis[j].end; }
        out.push({ kind: 'closed', start: slot.start, end });
        i = j;
        continue;
      }

      // Collapse the consecutive cells of ONE booking into a single row: a
      // 90-minute booking occupies three cells and used to render as three
      // identical-looking rows.
      //
      // The `id != null` guard is LOAD-BEARING, not defensive. available and
      // closed cells carry no booking at all, so a naive
      // `next.booking?.id === slot.booking?.id` compares undefined to undefined,
      // returns TRUE, and silently collapses every run of free cells into one
      // row — destroying the availability grid and tap-to-book's per-cell
      // prefill, which is the whole point of this screen. Merge only where a
      // real booking id exists.
      const id = slot.booking?.id;
      if (id != null && (slot.status === 'booked' || slot.status === 'blocked')) {
        let j = i;
        // Matches on id alone, never on adjacency: the GIST EXCLUDE constraint
        // guarantees at most ONE occupancy row per instant, so a booking's cells
        // can never be interleaved with another's and are always one run.
        while (j + 1 < vis.length && vis[j + 1].booking?.id === id) j++;
        const b = slot.booking!;
        // Label from the BOOKING's own instants, not the cell grid — a booking
        // that starts at 10:15 must read 10:15, not the 10:00 cell it sits in.
        //
        // EXCEPT where it falls outside this day. The payload returns any
        // booking OVERLAPPING the day, so a 23:30→01:30 booking appears on BOTH
        // days, and an owner block (no max-duration gate) can span several.
        // `hm()` prints wall-clock with no date, so an unclamped bound silently
        // names another day: a Fri 20:00 → Mon 02:00 block read "00:00 – 02:00"
        // on Saturday — a full-day block looking like a two-hour one.
        //
        // Clamp BOTH edges to the day and mark whichever was clamped. A booking
        // covering the whole day therefore reads "00:00 – 00:00" carrying both
        // markers, which is the same end-of-day convention an ordinary
        // 22:00→00:00 block already uses on this screen.
        const continuesFromPrevDay = dayStartMs != null && new Date(b.start_time).getTime() < dayStartMs;
        const continuesIntoNextDay = dayEndMs != null && new Date(b.end_time).getTime() > dayEndMs;
        out.push({
          kind: 'slot',
          slot,
          labelStart: continuesFromPrevDay ? new Date(dayStartMs).toISOString() : b.start_time,
          labelEnd: continuesIntoNextDay ? new Date(dayEndMs).toISOString() : b.end_time,
          continuesFromPrevDay,
          continuesIntoNextDay,
        });
        i = j;
        continue;
      }

      out.push({ kind: 'slot', slot });
    }
    return out;
  }, [data, date]);

  const rows = useMemo<Row[]>(() => rowsFor(filter), [rowsFor, filter]);

  const counts = useMemo(() => ({
    all: rowsFor('all').length,
    booked: rowsFor('booked').length,
    available: rowsFor('available').length,
  }), [rowsFor]);

  // Ordered available cells of the loaded day — the manual sheet's start-time set.
  const availableSlots = useMemo(
    () => (data?.slots ?? []).filter(s => s.status === 'available').map(s => ({ start: s.start, end: s.end })),
    [data],
  );

  // The open booking-details sheet's booking, re-derived from the freshest day
  // payload by id (never held as its own copy — refetch is authoritative).
  const sheetBooking = useMemo<DVBooking | null>(() => {
    if (sheetId == null || !data) return null;
    for (const s of data.slots) {
      if (s.booking && s.booking.id === sheetId) return s.booking;
    }
    return null;
  }, [sheetId, data]);

  // If the open booking vanished after a refetch (cancelled/removed elsewhere),
  // close the sheet rather than strand it.
  useEffect(() => {
    if (sheetId != null && data && !data.slots.some(s => s.booking?.id === sheetId)) {
      setSheetId(null);
    }
  }, [sheetId, data]);

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
            onClick={() => fetchDay()}
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
            {([['all', 'الكل', counts.all], ['booked', 'مشغول', counts.booked], ['available', 'متاح', counts.available]] as [Filter, string, number][]).map(([val, label, n]) => {
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
                : <SlotRow
                    key={row.slot.start}
                    slot={row.slot}
                    labelStart={row.labelStart}
                    labelEnd={row.labelEnd}
                    continuesFromPrevDay={row.continuesFromPrevDay}
                    continuesIntoNextDay={row.continuesIntoNextDay}
                    onPick={iso => setManual({ prefill: iso })}
                    onOpen={b => setSheetId(b.id)}
                  />)}
            </div>
          )}
        </>
      ) : null}

      {/* FAB — add a manual booking. Bottom-left (RTL), above the thumb zone,
          hidden while any sheet is open. */}
      {data && pitchId != null && !manual && !pickerOpen && sheetId == null && (
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

      {sheetBooking && data && (
        <BookingSheet
          booking={sheetBooking}
          title={sheetBooking.title}
          pricePerHour={data.price_per_hour}
          canExtend={true}
          canEditTotal={true}
          canCancel={true}
          pitchId={data.pitch_id}
          onClose={() => setSheetId(null)}
          onRefetch={() => fetchDay(true)}
          onCancelled={() => showToast('تم الإلغاء')}
        />
      )}

      {/* Confirmation toast (WO-SERIES-CANCEL). */}
      {toast && (
        <div
          role="status"
          className="fixed bottom-6 left-1/2 -translate-x-1/2 z-[70] inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-[#141715] border border-emerald-500/30 text-[13px] font-bold text-emerald-300 shadow-2xl"
          dir="rtl"
        >
          <Check size={15} aria-hidden />
          {toast}
        </div>
      )}

      <style jsx>{`
        .chip-scroll { scrollbar-width: none; }
        .chip-scroll::-webkit-scrollbar { display: none; }
      `}</style>
    </div>
  );
}

// ── Row renderers ────────────────────────────────────────────────────────────

function SlotRow({ slot, labelStart, labelEnd, continuesFromPrevDay, continuesIntoNextDay, onPick, onOpen }: {
  slot: DVSlot;
  labelStart?: string;
  labelEnd?: string;
  continuesFromPrevDay?: boolean;
  continuesIntoNextDay?: boolean;
  onPick?: (startIso: string) => void;
  onOpen?: (booking: DVBooking) => void;
}) {
  // A booking row labels itself with the booking's real bounds; every other row
  // is exactly its own cell.
  const fromIso = labelStart ?? slot.start;
  const toIso   = labelEnd ?? slot.end;
  const range = (
    <span className="font-mono text-[11px] tabular-nums text-white/45 shrink-0" dir="ltr">
      {hm(fromIso)}<span className="mx-1 text-white/20">–</span>{hm(toIso)}
    </span>
  );
  // One note for whichever edges were clamped. Rendered on BOTH the blocked and
  // booked branches: an owner block is exactly the row that can span days (no
  // max-duration gate), so clamping its label without explaining it would leave
  // a whole-day block reading a bare "00:00 – 00:00".
  const continuationNote =
    continuesFromPrevDay && continuesIntoNextDay ? 'مستمر طوال اليوم'
    : continuesFromPrevDay ? 'مستمر من ليلة أمس'
    : continuesIntoNextDay ? 'يمتد إلى الغد'
    : null;

  if (slot.status === 'available') {
    // Tap an available cell → open the manual sheet pre-filled to this start.
    return (
      <button
        type="button"
        onClick={() => onPick?.(slot.start)}
        className={`w-full min-h-[44px] flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-emerald-500/15 bg-emerald-500/[0.04] hover:bg-emerald-500/[0.09] hover:border-emerald-500/30 transition-all active:scale-[0.99] text-start`}
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
      <div className={`flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-amber-500/25 bg-amber-500/[0.08]`}>
        <div className="min-w-0 flex flex-col gap-0.5">
          {range}
          {continuationNote && (
            <span className="text-[10px] text-sky-300/80 truncate">{continuationNote}</span>
          )}
        </div>
        <span className="inline-flex shrink-0 items-center gap-1.5 text-[12px] font-semibold text-amber-300">
          <Ban size={12} aria-hidden />
          محجوز يدويًا / صيانة
        </span>
      </div>
    );
  }

  // booked — tappable (opens the details sheet) for real bookings; blocks never
  // reach here (they render as 'blocked'). Badge is driven by payment_display:
  // untracked → no badge (ruling 2), so the board doesn't scream "unpaid".
  const b = slot.booking;
  const srcBadge = b ? SOURCE_BADGE[b.source] : undefined;
  const payBadge = b ? paymentDisplayBadge(b.payment_display) : null;
  const tappable = !!b && b.source !== 'block' && !!onOpen;

  const inner = (
    <>
      <div className="min-w-0 flex items-center gap-2.5">
        {range}
        <div className="min-w-0">
          <p className="text-[13px] font-semibold text-[#f0efe8] truncate">{b?.title || 'حجز'}</p>
          {continuationNote && (
            <p className="text-[10px] text-sky-300/80 truncate">{continuationNote}</p>
          )}
        </div>
      </div>
      <div className="flex items-center gap-1.5 shrink-0">
        {b?.recurrence_group_id && (
          <Repeat size={11} className="text-sky-400/80 flex-shrink-0" aria-label="حجز متكرر" />
        )}
        {srcBadge && (
          <span className={`inline-flex items-center px-1.5 py-0.5 rounded-md text-[9px] font-bold border ${srcBadge.cls}`}>
            {srcBadge.label}
          </span>
        )}
        {payBadge && (
          <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-[9px] font-bold border ${payBadge.cls}`}>
            {payBadge.label}
          </span>
        )}
      </div>
    </>
  );

  if (tappable) {
    return (
      <button
        type="button"
        onClick={() => onOpen!(b!)}
        className={`w-full flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-white/[0.09] bg-white/[0.03] hover:bg-white/[0.055] hover:border-white/20 transition-all active:scale-[0.99] text-start`}
        aria-label={`تفاصيل حجز ${b!.title || ''} ${hm(fromIso)} إلى ${hm(toIso)}${continuationNote ? ` — ${continuationNote}` : ''}`}
      >
        {inner}
      </button>
    );
  }
  return (
    <div className={`flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl border border-white/[0.09] bg-white/[0.03]`}>
      {inner}
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
