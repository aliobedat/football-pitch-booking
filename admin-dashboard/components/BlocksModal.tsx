'use client';

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Ban, ChevronRight, ChevronLeft, X, AlertTriangle, Info, CalendarDays, Loader2, Sparkles, UserPlus, Clock, Repeat, Trash2 } from 'lucide-react';
import api from '@/lib/api';
import { formatDate, formatTime } from '@/lib/format';

// ─────────────────────────────────────────────────────────────────────────────
// Blocks tool — owner/admin "held time" (صيانة/مغلق) manager, one pitch, one day.
//
// A BLOCK is an owner-initiated occupancy with no player (source='block') that
// sits on the SAME anti-double-booking constraint as player bookings: it makes a
// slot unbookable (maintenance, a private game, …). This modal renders the
// selected day's occupancy inline — player bookings and blocks side by side but
// visually + lexically distinct — and lets the owner block free hours or lift an
// existing block.
//
// Data source: GET /admin/bookings (already owner-scoped server-side) filtered to
// THIS pitch + the viewed Amman civil day. Unlike the public /availability feed it
// carries each row's source, id, and player_name — exactly what the owner view and
// the unblock action need. Writes go to POST/DELETE /pitches/:id/blocks (Phase 3).
//
// Jordan has no DST (permanent UTC+3 since 2022), so an Amman wall-clock instant is
// the literal "...T HH:00:00+03:00" — this is the SAME instant the backend resolves
// the Amman civil day against, with no offset drift.
// ─────────────────────────────────────────────────────────────────────────────

const AMMAN_OFFSET = '+03:00';
const HOURS = Array.from({ length: 24 }, (_, h) => h); // 00:00 … 23:00

type BookingSource = 'player' | 'academy' | 'block' | 'manual';

interface DayBooking {
  id:          number;
  pitch_id:    number;
  player_id:   number | null;
  user_name:   string;
  guest_name?:  string | null; // manual (walk-in) rows
  guest_phone?: string | null;
  recurrence_group_id?: string | null; // recurring walk-in grouping
  start_time:  string; // ISO UTC
  end_time:    string; // ISO UTC
  status:      'pending' | 'confirmed' | 'cancelled';
  source:      BookingSource;
}

// One server conflict entry (Phase 3 409 payload).
interface BlockConflict {
  booking_id:  number;
  source:      BookingSource;
  start:       string;
  end:         string;
  player_name: string | null;
}

// ── Amman civil-date helpers (Latin-digit, fixed +03:00) ─────────────────────

interface CivilDate { y: number; m: number; d: number } // m: 1-12

// The Amman civil Y/M/D of an absolute instant.
function ammanCivilDate(at: Date): CivilDate {
  // en-CA → "YYYY-MM-DD"; pinned to Amman so the day boundary is civil, not UTC.
  const s = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Amman', year: 'numeric', month: '2-digit', day: '2-digit',
  }).format(at);
  const [y, m, d] = s.split('-').map(Number);
  return { y, m, d };
}

// The Amman civil hour (0-23) of an absolute instant.
function ammanHour(at: Date): number {
  return Number(new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Asia/Amman', hour: '2-digit', hour12: false,
  }).format(at)) % 24;
}

const pad = (n: number) => String(n).padStart(2, '0');

// Build the absolute instant for an Amman wall-clock hour on a civil date.
// hour may be 24 → midnight starting the next day (the day's exclusive end).
function ammanInstant(date: CivilDate, hour: number): Date {
  return new Date(`${date.y}-${pad(date.m)}-${pad(date.d)}T${pad(hour)}:00:00${AMMAN_OFFSET}`);
}

function sameCivilDate(a: CivilDate, b: CivilDate): boolean {
  return a.y === b.y && a.m === b.m && a.d === b.d;
}

function addDays(date: CivilDate, delta: number): CivilDate {
  // Anchor at Amman noon to stay clear of any boundary, then re-read the civil date.
  const at = new Date(`${date.y}-${pad(date.m)}-${pad(date.d)}T12:00:00${AMMAN_OFFSET}`);
  at.setUTCDate(at.getUTCDate() + delta);
  return ammanCivilDate(at);
}

// Per-hour occupancy cell for the grid.
type CellKind = 'free' | 'player' | 'block' | 'manual' | 'past';
interface HourCell {
  hour:    number;
  kind:    CellKind;
  booking?: DayBooking; // the occupying row (player or block), if any
}

export default function BlocksModal({
  pitchId,
  pitchName,
  onClose,
}: {
  pitchId:   number;
  pitchName: string;
  onClose:   () => void;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);

  // `now` is sampled once on open for the past-hour guard (the server stays the
  // final referee — it rejects a block whose end ≤ now with 422).
  const nowRef = useRef<Date>(new Date());
  const todayCivil = useMemo(() => ammanCivilDate(nowRef.current), []);

  const [viewDate, setViewDate] = useState<CivilDate>(todayCivil);
  const [rows, setRows]       = useState<DayBooking[]>([]); // all of this pitch's non-cancelled rows
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // hour-range selection (anchor + head, inclusive); null when nothing selected.
  const [sel, setSel] = useState<{ a: number; b: number } | null>(null);

  const [submitting, setSubmitting] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [conflict, setConflict] = useState<BlockConflict | null>(null);
  const [conflictWeek, setConflictWeek] = useState<number | null>(null); // failing week (recurring)

  // unblock confirmation target.
  const [unblockTarget, setUnblockTarget] = useState<DayBooking | null>(null);

  // walk-in (manual) guest form: open over the selected range.
  const [manualOpen, setManualOpen] = useState(false);
  const [guestName, setGuestName]   = useState('');
  const [guestPhone, setGuestPhone] = useState('');
  const [guestPhoneError, setGuestPhoneError] = useState<string | null>(null);
  const [repeatWeeks, setRepeatWeeks] = useState(1); // 1 = one-off; >1 = recurring

  // Recurrence UUID lifecycle: generated client-side, kept STABLE across a 409/422
  // rollback (so the owner can fix the conflict and retry the same group), and
  // regenerated ONLY after a successful create. Sent only for a recurring (>1) series.
  const [groupId, setGroupId] = useState<string>(() => crypto.randomUUID());

  // Soft override: holds the pending walk-in payload after a 422 out-of-hours
  // rejection, awaiting the owner's confirmation to resubmit with force_bypass_hours.
  const [overridePending, setOverridePending] = useState<{ start: number; end: number } | null>(null);

  // Manual-booking cancel: the clicked walk-in awaiting a cancel decision. A
  // recurring row (recurrence_group_id set) offers single-vs-all; a one-off offers
  // only the standard single cancel.
  const [cancelManualTarget, setCancelManualTarget] = useState<DayBooking | null>(null);

  // ── Load this pitch's bookings (owner-scoped server-side) ──────────────────
  const reload = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    try {
      const res = await api.get('/admin/bookings');
      const all = (res.data.data ?? []) as DayBooking[];
      setRows(all.filter(b => b.pitch_id === pitchId && b.status !== 'cancelled'));
    } catch {
      setLoadError('تعذّر تحميل الحجوزات، حاول مجدداً');
    } finally {
      setLoading(false);
    }
  }, [pitchId]);

  useEffect(() => { reload(); }, [reload]);

  // Changing the day clears any in-progress selection / form / error.
  useEffect(() => {
    setSel(null); setConflict(null); setConflictWeek(null); setActionError(null);
    setManualOpen(false); setGuestName(''); setGuestPhone(''); setGuestPhoneError(null);
    setRepeatWeeks(1); setOverridePending(null); setCancelManualTarget(null);
  }, [viewDate]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape' && !submitting && !unblockTarget && !overridePending && !cancelManualTarget) onClose();
  }, [submitting, unblockTarget, overridePending, cancelManualTarget, onClose]);

  const isToday = sameCivilDate(viewDate, todayCivil);

  // ── Build the 24-hour occupancy grid for the viewed day ────────────────────
  const cells: HourCell[] = useMemo(() => {
    const dayStart = ammanInstant(viewDate, 0).getTime();
    const dayEnd   = ammanInstant(viewDate, 24).getTime();
    const nowHour  = isToday ? ammanHour(nowRef.current) : -1;

    // Map each hour to the row covering it (a row covers hour h if it overlaps
    // [h:00, h+1:00) on this civil day).
    const cover: (DayBooking | undefined)[] = Array(24).fill(undefined);
    for (const b of rows) {
      const s = new Date(b.start_time).getTime();
      const e = new Date(b.end_time).getTime();
      if (e <= dayStart || s >= dayEnd) continue; // not on this day
      for (const h of HOURS) {
        const hs = dayStart + h * 3_600_000;
        const he = hs + 3_600_000;
        if (s < he && hs < e) cover[h] = b; // overlaps this hour
      }
    }

    return HOURS.map(h => {
      const b = cover[h];
      let kind: CellKind;
      if (b) kind = b.source === 'block' ? 'block' : b.source === 'manual' ? 'manual' : 'player';
      else if (h < nowHour) kind = 'past';
      else kind = 'free';
      return { hour: h, kind, booking: b };
    });
  }, [rows, viewDate, isToday]);

  // ── Selection ──────────────────────────────────────────────────────────────
  // Clicking a free hour starts or extends a CONTIGUOUS run of free hours. Any
  // occupied hour in the span resets selection to the single clicked hour.
  const onCellClick = (cell: HourCell) => {
    if (cell.kind === 'block') { setUnblockTarget(cell.booking!); return; }
    if (cell.kind === 'manual') { setActionError(null); setCancelManualTarget(cell.booking!); return; }
    if (cell.kind !== 'free') return; // player booking / past → not selectable
    setConflict(null);
    setActionError(null);
    setSel(prev => {
      if (!prev) return { a: cell.hour, b: cell.hour };
      const lo = Math.min(prev.a, cell.hour);
      const hi = Math.max(prev.a, cell.hour);
      // every hour in [lo, hi] must be free to form a contiguous block
      for (let h = lo; h <= hi; h++) {
        if (cells[h].kind !== 'free') return { a: cell.hour, b: cell.hour };
      }
      return { a: prev.a, b: cell.hour };
    });
  };

  const selRange = sel ? { start: Math.min(sel.a, sel.b), end: Math.max(sel.a, sel.b) + 1 } : null;

  const blockWholeDay = () => {
    // From the first not-yet-passed free hour through end of day.
    const firstFree = cells.find(c => c.kind === 'free');
    if (!firstFree) { setActionError('لا توجد ساعات متاحة للحجب في هذا اليوم'); return; }
    setConflict(null);
    setActionError(null);
    setSel({ a: firstFree.hour, b: 23 });
  };

  // ── Create a block over the selected range ─────────────────────────────────
  const submitBlock = async () => {
    if (!selRange) return;
    setSubmitting(true);
    setActionError(null);
    setConflict(null);
    try {
      await api.post(`/pitches/${pitchId}/blocks`, {
        start_time: ammanInstant(viewDate, selRange.start).toISOString(),
        end_time:   ammanInstant(viewDate, selRange.end).toISOString(),
      });
      setSel(null);
      await reload();
    } catch (err: any) {
      const status = err?.response?.status;
      const data = err?.response?.data;
      if (status === 409 && Array.isArray(data?.conflicts) && data.conflicts.length > 0) {
        setConflict(data.conflicts[0] as BlockConflict);
      } else {
        setActionError(data?.message ?? 'تعذّر حجب الموعد، حاول مجدداً');
      }
    } finally {
      setSubmitting(false);
    }
  };

  // ── Log a walk-in (manual) booking over the selected range ─────────────────
  // Jordanian mobile: +9627######## or 07########. Optional — only validated when
  // the owner actually types a number.
  const phoneValid = (raw: string) => /^(\+962|0)7\d{8}$/.test(raw.replace(/[\s-]/g, ''));

  const openManualForm = () => {
    setConflict(null);
    setActionError(null);
    setGuestPhoneError(null);
    setManualOpen(true);
  };

  const closeManualForm = () => {
    setManualOpen(false);
    setGuestName('');
    setGuestPhone('');
    setGuestPhoneError(null);
    setRepeatWeeks(1);
    setConflictWeek(null);
  };

  // Core POST. `bypass` is set on the override-confirmed resubmit. A recurring (>1)
  // series carries repeat_weeks + the STABLE client UUID (recurrence_group_id); a
  // one-off sends neither, so its row keeps a NULL group (standard cancel only).
  const postManual = async (range: { start: number; end: number }, name: string, phone: string, bypass: boolean) => {
    const body: Record<string, unknown> = {
      start_time: ammanInstant(viewDate, range.start).toISOString(),
      end_time:   ammanInstant(viewDate, range.end).toISOString(),
      guest_name: name,
    };
    if (phone) body.guest_phone = phone;
    if (bypass) body.force_bypass_hours = true;
    if (repeatWeeks > 1) {
      body.repeat_weeks = repeatWeeks;
      body.recurrence_group_id = groupId;
    }
    await api.post(`/pitches/${pitchId}/bookings/manual`, body);
  };

  const submitManual = async (bypass = false) => {
    if (!selRange) return;
    const name = guestName.trim();
    if (!name) { setActionError('اسم الضيف مطلوب'); return; }
    const phone = guestPhone.trim();
    if (phone && !phoneValid(phone)) {
      setGuestPhoneError('رقم هاتف غير صالح (مثال: 0791234567 أو ‎+962791234567)');
      return;
    }
    setSubmitting(true);
    setActionError(null);
    setConflict(null);
    setConflictWeek(null);
    setGuestPhoneError(null);
    try {
      await postManual(selRange, name, phone, bypass);
      // SUCCESS → only now regenerate the recurrence UUID (a fresh series next time);
      // reset the form + selection and refresh occupancy.
      setGroupId(crypto.randomUUID());
      setSel(null);
      setOverridePending(null);
      closeManualForm();
      await reload();
    } catch (err: any) {
      // NB: the group UUID is intentionally NOT regenerated here — it stays stable so
      // the owner can fix the conflict and retry the SAME series (idempotent).
      const status = err?.response?.status;
      const data = err?.response?.data;
      if (status === 409 && Array.isArray(data?.conflicts) && data.conflicts.length > 0) {
        setConflict(data.conflicts[0] as BlockConflict);
        setConflictWeek(data?.occurrence?.week ?? null);
      } else if (status === 422 && data?.error === 'outside_operating_hours' && !bypass) {
        // Soft override: stage the confirmation dialog instead of erroring.
        setOverridePending({ start: selRange.start, end: selRange.end });
      } else {
        setActionError(data?.message ?? 'تعذّر تسجيل الحجز، حاول مجدداً');
      }
    } finally {
      setSubmitting(false);
    }
  };

  // ── Cancel a manual walk-in from the grid ──────────────────────────────────
  const cancelSingleOccurrence = async () => {
    if (!cancelManualTarget) return;
    setSubmitting(true);
    setActionError(null);
    try {
      await api.patch(`/bookings/${cancelManualTarget.id}/cancel`);
      setCancelManualTarget(null);
      await reload();
    } catch (err: any) {
      setActionError(err?.response?.data?.message ?? 'تعذّر إلغاء الحجز، حاول مجدداً');
    } finally {
      setSubmitting(false);
    }
  };

  const cancelFutureGroup = async () => {
    if (!cancelManualTarget?.recurrence_group_id) return;
    setSubmitting(true);
    setActionError(null);
    try {
      await api.delete(`/pitches/${pitchId}/bookings/group/${cancelManualTarget.recurrence_group_id}`);
      setCancelManualTarget(null);
      await reload();
    } catch (err: any) {
      setActionError(err?.response?.data?.message ?? 'تعذّر إلغاء الحجوزات، حاول مجدداً');
    } finally {
      setSubmitting(false);
    }
  };

  // ── Lift a block ───────────────────────────────────────────────────────────
  const confirmUnblock = async () => {
    if (!unblockTarget) return;
    setSubmitting(true);
    setActionError(null);
    try {
      await api.delete(`/pitches/${pitchId}/blocks/${unblockTarget.id}`);
      setUnblockTarget(null);
      await reload();
    } catch (err: any) {
      setActionError(err?.response?.data?.message ?? 'تعذّر رفع الحجب، حاول مجدداً');
    } finally {
      setSubmitting(false);
    }
  };

  // Arabic time label for an Amman wall-clock hour (00-24).
  const hourLabel = (h: number) => formatTime(ammanInstant(viewDate, h % 24).toISOString());
  const dayLabel = formatDate(ammanInstant(viewDate, 12).toISOString(),
    { weekday: 'long', day: 'numeric', month: 'long' });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" onKeyDown={handleKeyDown}>
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={() => { if (!submitting && !unblockTarget && !overridePending && !cancelManualTarget) onClose(); }}
        aria-hidden
      />

      <div
        ref={dialogRef}
        dir="rtl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="blk-title"
        className="relative w-full max-w-2xl max-h-[90vh] flex flex-col rounded-2xl bg-[#141715] border border-white/[0.10] shadow-2xl overflow-hidden"
      >
        {/* Header */}
        <div className="flex items-start gap-3.5 px-6 pt-6 pb-4 border-b border-white/[0.06]">
          <div className="w-10 h-10 rounded-xl bg-amber-500/[0.10] border border-amber-500/25 flex items-center justify-center flex-shrink-0">
            <Ban size={18} className="text-amber-400" aria-hidden />
          </div>
          <div className="min-w-0 flex-1">
            <h2 id="blk-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">الحجب والصيانة</h2>
            <p className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
              احجب فترات على ملعب <span className="text-white/60 font-semibold">{pitchName}</span> (صيانة، مباراة خاصة…) فتصبح غير متاحة للحجز
            </p>
          </div>
          <button
            type="button"
            onClick={() => { if (!submitting && !unblockTarget) onClose(); }}
            className="text-white/25 hover:text-white/55 transition-colors duration-150 flex-shrink-0"
            aria-label="إغلاق"
          >
            <X size={18} aria-hidden />
          </button>
        </div>

        {/* Day navigator */}
        <div className="flex items-center justify-between px-6 py-3 border-b border-white/[0.05] bg-[#0f1110]">
          <button
            type="button"
            onClick={() => setViewDate(d => addDays(d, -1))}
            disabled={isToday}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-[12px] font-semibold text-white/55 border border-white/[0.08] hover:text-white/80 hover:border-white/[0.18] disabled:opacity-30 disabled:cursor-not-allowed transition-all duration-150"
            aria-label="اليوم السابق"
          >
            <ChevronRight size={14} aria-hidden />
            السابق
          </button>
          <div className="flex items-center gap-2 text-[13px] font-bold text-[#f0efe8]">
            <CalendarDays size={14} className="text-amber-400" aria-hidden />
            {dayLabel}
            {isToday && <span className="text-[10px] font-semibold text-amber-400 bg-amber-500/15 border border-amber-500/25 rounded-md px-1.5 py-0.5">اليوم</span>}
          </div>
          <button
            type="button"
            onClick={() => setViewDate(d => addDays(d, 1))}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-[12px] font-semibold text-white/55 border border-white/[0.08] hover:text-white/80 hover:border-white/[0.18] transition-all duration-150"
            aria-label="اليوم التالي"
          >
            التالي
            <ChevronLeft size={14} aria-hidden />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-6 py-5">
          {loading ? (
            <div className="h-40 flex items-center justify-center">
              <Loader2 size={22} className="text-amber-400 animate-spin" aria-hidden />
            </div>
          ) : loadError ? (
            <p className="text-[13px] text-red-400 text-center py-10">{loadError}</p>
          ) : (
            <>
              {/* Legend */}
              <div className="flex flex-wrap items-center gap-4 mb-4 text-[11px]">
                <span className="inline-flex items-center gap-1.5 text-white/45">
                  <span className="w-3 h-3 rounded bg-white/[0.05] border border-white/[0.12]" /> متاح
                </span>
                <span className="inline-flex items-center gap-1.5 text-emerald-300/80">
                  <span className="w-3 h-3 rounded bg-emerald-500/15 border border-emerald-500/30" /> حجز لاعب
                </span>
                <span className="inline-flex items-center gap-1.5 text-sky-300/90">
                  <span className="w-3 h-3 rounded bg-sky-500/15 border border-sky-500/35" /> حجز يدوي
                </span>
                <span className="inline-flex items-center gap-1.5 text-amber-300/90">
                  <span className="w-3 h-3 rounded bg-amber-500/15 border border-amber-500/35" /> محظور / صيانة
                </span>
              </div>

              {/* Hour grid */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
                {cells.map(cell => {
                  const inSel = selRange && cell.hour >= selRange.start && cell.hour < selRange.end;
                  const base = 'flex items-center justify-between gap-2 px-3 py-2 rounded-lg border text-[12px] transition-all duration-150';
                  let cls: string;
                  let label: React.ReactNode;
                  if (cell.kind === 'block') {
                    cls = 'bg-amber-500/[0.10] border-amber-500/30 text-amber-300 hover:bg-amber-500/[0.16] cursor-pointer';
                    label = <span className="font-semibold">صيانة / مغلق</span>;
                  } else if (cell.kind === 'player') {
                    cls = 'bg-emerald-500/[0.08] border-emerald-500/25 text-emerald-300/90 cursor-default';
                    label = <span className="truncate">{cell.booking?.user_name || 'حجز لاعب'}</span>;
                  } else if (cell.kind === 'manual') {
                    cls = 'bg-sky-500/[0.10] border-sky-500/30 text-sky-300/90 hover:bg-sky-500/[0.16] cursor-pointer';
                    label = (
                      <span className="inline-flex items-center gap-1 truncate">
                        <span className="text-[9px] font-bold text-sky-400/70 bg-sky-500/15 rounded px-1 py-px flex-shrink-0">يدوي</span>
                        {cell.booking?.recurrence_group_id && (
                          <Repeat size={11} className="text-sky-400/80 flex-shrink-0" aria-hidden />
                        )}
                        <span className="truncate">{cell.booking?.guest_name || 'حجز يدوي'}</span>
                      </span>
                    );
                  } else if (cell.kind === 'past') {
                    cls = 'bg-white/[0.01] border-white/[0.05] text-white/20 cursor-not-allowed';
                    label = <span className="text-white/20">—</span>;
                  } else if (inSel) {
                    cls = 'bg-white/[0.10] border-white/40 text-[#f0efe8] cursor-pointer';
                    label = <span className="font-semibold">محدّد</span>;
                  } else {
                    cls = 'bg-white/[0.025] border-white/[0.08] text-white/45 hover:bg-white/[0.05] hover:border-white/[0.16] cursor-pointer';
                    label = <span className="text-white/30">متاح</span>;
                  }
                  const clickable = cell.kind === 'free' || cell.kind === 'block' || cell.kind === 'manual';
                  return (
                    <button
                      key={cell.hour}
                      type="button"
                      onClick={() => onCellClick(cell)}
                      disabled={!clickable || submitting}
                      className={`${base} ${cls} disabled:opacity-100`}
                      aria-label={`${hourLabel(cell.hour)} — ${
                        cell.kind === 'block' ? 'محظور، اضغط لرفع الحجب'
                        : cell.kind === 'player' ? 'حجز لاعب'
                        : cell.kind === 'manual' ? (cell.booking?.recurrence_group_id ? 'حجز يدوي متكرر، اضغط للإلغاء' : 'حجز يدوي، اضغط للإلغاء')
                        : cell.kind === 'past' ? 'وقت منقضٍ'
                        : 'متاح، اضغط للتحديد'}`}
                    >
                      <span className="font-mono text-[11px] tabular-nums text-white/50" dir="ltr">{pad(cell.hour)}:00</span>
                      {label}
                      {cell.kind === 'block' && <Ban size={12} className="flex-shrink-0" aria-hidden />}
                    </button>
                  );
                })}
              </div>
            </>
          )}
        </div>

        {/* Footer — selection summary + actions */}
        {!loading && !loadError && (
          <div className="border-t border-white/[0.06] bg-[#111312] px-6 py-4">
            {/* 409 conflict, surfaced in Arabic */}
            {conflict && (
              <div className="flex items-start gap-2 mb-3 text-[12px] text-red-300 bg-red-500/[0.07] border border-red-500/20 rounded-xl px-4 py-2.5 leading-relaxed">
                <AlertTriangle size={14} className="flex-shrink-0 mt-0.5 text-red-400" aria-hidden />
                <span>
                  {conflictWeek ? <>الأسبوع {conflictWeek}: </> : null}
                  هذا الموعد يتعارض مع{' '}
                  {conflict.source === 'block' ? 'حجب قائم' : conflict.source === 'manual' ? 'حجز يدوي' : 'حجز'}{' '}
                  الساعة{' '}
                  <span className="font-mono" dir="ltr">{formatTime(conflict.start)}</span>
                  {conflict.player_name ? <> — <span className="font-semibold">{conflict.player_name}</span></> : null}
                  ؛ يجب إلغاؤه أولاً.
                </span>
              </div>
            )}
            {actionError && (
              <div className="flex items-center gap-2 mb-3 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
                <AlertTriangle size={14} className="flex-shrink-0" aria-hidden />
                {actionError}
              </div>
            )}

            {selRange ? (
              <div className="flex items-center justify-between gap-3 mb-3 px-3.5 py-2.5 rounded-xl bg-white/[0.03] border border-white/[0.10]">
                <span className="text-[12px] text-white/70">
                  الفترة المحددة{' '}
                  <span className="font-mono font-bold text-[#f0efe8]" dir="ltr">{hourLabel(selRange.start)}</span>
                  {' '}—{' '}
                  <span className="font-mono font-bold text-[#f0efe8]" dir="ltr">{hourLabel(selRange.end)}</span>
                </span>
                <button
                  type="button"
                  onClick={() => { setSel(null); closeManualForm(); }}
                  className="text-[11px] font-semibold text-white/40 hover:text-white/70 transition-colors"
                >
                  مسح التحديد
                </button>
              </div>
            ) : (
              <div className="flex items-start gap-2 mb-3 px-3.5 py-2.5 rounded-xl bg-white/[0.02] border border-white/[0.06]">
                <Info size={13} className="text-white/30 flex-shrink-0 mt-0.5" aria-hidden />
                <p className="text-[11.5px] text-white/35 leading-relaxed">
                  اضغط على الساعات المتاحة لتحديد فترة، ثم احجبها أو سجّل حجزاً يدوياً. «حجب اليوم بالكامل» متاح أيضاً، واضغط على فترة محظورة لرفعها.
                </p>
              </div>
            )}

            {/* Walk-in guest form (opens over the selected range) */}
            {manualOpen && selRange ? (
              <div className="flex flex-col gap-3">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div>
                    <label className="block text-[11px] font-semibold text-white/40 mb-1.5">
                      اسم الضيف <span className="text-red-400/60">*</span>
                    </label>
                    <input
                      type="text"
                      value={guestName}
                      onChange={e => { setGuestName(e.target.value); setActionError(null); }}
                      placeholder="مثال: أبو محمد"
                      autoFocus
                      className="w-full bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-2.5 text-[13px] text-[#f0efe8] placeholder:text-white/25 focus:outline-none focus:border-sky-500/60 focus:ring-2 focus:ring-sky-500/20 transition-all duration-150"
                    />
                  </div>
                  <div>
                    <label className="block text-[11px] font-semibold text-white/40 mb-1.5">
                      رقم الهاتف <span className="text-white/25">(اختياري)</span>
                    </label>
                    <input
                      type="tel"
                      dir="ltr"
                      value={guestPhone}
                      onChange={e => { setGuestPhone(e.target.value); setGuestPhoneError(null); }}
                      placeholder="07XXXXXXXX"
                      className="w-full bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-2.5 text-[13px] text-[#f0efe8] placeholder:text-white/25 text-right focus:outline-none focus:border-sky-500/60 focus:ring-2 focus:ring-sky-500/20 transition-all duration-150"
                    />
                    {guestPhoneError && <p className="text-[10.5px] text-red-400 mt-1">{guestPhoneError}</p>}
                  </div>
                </div>
                {/* Weekly recurrence — same slot, advanced 7 days per occurrence */}
                <div className="flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl bg-sky-500/[0.04] border border-sky-500/15">
                  <label htmlFor="repeat-weeks" className="inline-flex items-center gap-1.5 text-[11.5px] font-semibold text-sky-200/80">
                    <Repeat size={13} aria-hidden />
                    تكرار أسبوعي
                  </label>
                  <select
                    id="repeat-weeks"
                    value={repeatWeeks}
                    onChange={e => { setRepeatWeeks(Number(e.target.value)); setConflict(null); setConflictWeek(null); }}
                    className="bg-white/[0.05] border border-white/[0.13] rounded-lg px-3 py-1.5 text-[12px] text-[#f0efe8] [color-scheme:dark] focus:outline-none focus:border-sky-500/60"
                  >
                    <option value={1}>بدون تكرار</option>
                    <option value={4}>كل أسبوع × 4</option>
                    <option value={8}>كل أسبوع × 8</option>
                    <option value={12}>كل أسبوع × 12</option>
                    <option value={24}>كل أسبوع × 24</option>
                  </select>
                </div>
                {repeatWeeks > 1 && (
                  <p className="text-[10.5px] text-white/35 -mt-1">
                    سيتم إنشاء {repeatWeeks} حجوزات، واحدة كل أسبوع في نفس التوقيت.
                  </p>
                )}
                <div className="flex items-center justify-end gap-3">
                  <button
                    type="button"
                    onClick={closeManualForm}
                    disabled={submitting}
                    className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
                  >
                    رجوع
                  </button>
                  <button
                    type="button"
                    onClick={() => submitManual(false)}
                    disabled={submitting || !guestName.trim()}
                    className={[
                      'flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold',
                      'bg-sky-500/[0.14] text-sky-300 border border-sky-500/30',
                      'hover:bg-sky-500/[0.20] hover:text-sky-200 hover:border-sky-500/45',
                      'disabled:opacity-50 disabled:cursor-not-allowed',
                      'transition-all duration-200 active:scale-[0.97]',
                    ].join(' ')}
                  >
                    {submitting && !overridePending ? (
                      <><Loader2 size={14} className="animate-spin" aria-hidden /> جاري التسجيل...</>
                    ) : (
                      <><UserPlus size={13} aria-hidden /> تأكيد الحجز اليدوي</>
                    )}
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex items-center justify-between gap-3">
                <button
                  type="button"
                  onClick={blockWholeDay}
                  disabled={submitting}
                  className="inline-flex items-center gap-1.5 px-4 py-2.5 rounded-xl text-[12px] font-semibold text-white/55 border border-white/[0.08] hover:text-amber-300 hover:border-amber-500/30 hover:bg-amber-500/[0.05] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
                >
                  <Sparkles size={13} aria-hidden />
                  حجب اليوم بالكامل
                </button>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={openManualForm}
                    disabled={submitting || !selRange}
                    className={[
                      'flex items-center gap-2 px-4 py-2.5 rounded-xl text-[12px] font-bold',
                      'bg-sky-500/[0.12] text-sky-300 border border-sky-500/30',
                      'hover:bg-sky-500/[0.18] hover:text-sky-200 hover:border-sky-500/45',
                      'disabled:opacity-50 disabled:cursor-not-allowed',
                      'transition-all duration-200 active:scale-[0.97]',
                    ].join(' ')}
                  >
                    <UserPlus size={13} aria-hidden />
                    حجز يدوي
                  </button>
                  <button
                    type="button"
                    onClick={submitBlock}
                    disabled={submitting || !selRange}
                    className={[
                      'flex items-center gap-2 px-5 py-2.5 rounded-xl text-[12px] font-bold',
                      'bg-amber-500/[0.12] text-amber-300 border border-amber-500/30',
                      'hover:bg-amber-500/[0.18] hover:text-amber-200 hover:border-amber-500/45',
                      'disabled:opacity-50 disabled:cursor-not-allowed',
                      'transition-all duration-200 active:scale-[0.97]',
                    ].join(' ')}
                  >
                    {submitting && !unblockTarget && !overridePending ? (
                      <><Loader2 size={14} className="animate-spin" aria-hidden /> جاري الحجب...</>
                    ) : (
                      <><Ban size={13} aria-hidden /> حجب الفترة</>
                    )}
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Soft-override confirmation: the slot is outside operating hours */}
      {overridePending && (
        <div className="absolute inset-0 z-20 flex items-center justify-center p-4">
          <div
            className="absolute inset-0 bg-black/60"
            onClick={() => { if (!submitting) setOverridePending(null); }}
            aria-hidden
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="override-title"
            dir="rtl"
            className="relative w-full max-w-sm rounded-2xl bg-[#141715] border border-white/[0.10] shadow-2xl overflow-hidden"
          >
            <div className="flex items-start gap-3.5 px-6 pt-6 pb-4">
              <div className="w-10 h-10 rounded-xl bg-amber-500/[0.10] border border-amber-500/25 flex items-center justify-center flex-shrink-0">
                <Clock size={18} className="text-amber-400" aria-hidden />
              </div>
              <div className="min-w-0">
                <h2 id="override-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">خارج أوقات العمل</h2>
                <p className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
                  هذا الوقت خارج أوقات العمل المحددة، هل تود تأكيد الحجز؟
                </p>
              </div>
            </div>
            <div className="flex items-center justify-end gap-3 px-6 py-5 mt-1 border-t border-white/[0.05] bg-[#111312]">
              <button
                type="button"
                onClick={() => { if (!submitting) setOverridePending(null); }}
                disabled={submitting}
                className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
              >
                تراجع
              </button>
              <button
                type="button"
                onClick={() => submitManual(true)}
                disabled={submitting}
                className="flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold bg-amber-500/[0.12] text-amber-300 border border-amber-500/30 hover:bg-amber-500/[0.18] hover:text-amber-200 hover:border-amber-500/45 disabled:opacity-60 disabled:cursor-not-allowed transition-all duration-200 active:scale-[0.97]"
              >
                {submitting ? (
                  <><Loader2 size={13} className="animate-spin" aria-hidden /> جاري التسجيل...</>
                ) : (
                  <><UserPlus size={13} aria-hidden /> تأكيد رغم ذلك</>
                )}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Unblock confirmation */}
      {unblockTarget && (
        <div className="absolute inset-0 z-10 flex items-center justify-center p-4">
          <div
            className="absolute inset-0 bg-black/60"
            onClick={() => { if (!submitting) setUnblockTarget(null); }}
            aria-hidden
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="unblock-title"
            dir="rtl"
            className="relative w-full max-w-sm rounded-2xl bg-[#141715] border border-white/[0.10] shadow-2xl overflow-hidden"
          >
            <div className="flex items-start gap-3.5 px-6 pt-6 pb-4">
              <div className="w-10 h-10 rounded-xl bg-amber-500/[0.10] border border-amber-500/25 flex items-center justify-center flex-shrink-0">
                <Ban size={18} className="text-amber-400" aria-hidden />
              </div>
              <div className="min-w-0">
                <h2 id="unblock-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">رفع الحجب</h2>
                <p className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
                  سيُرفع حجب الفترة{' '}
                  <span className="font-mono font-bold text-white/60" dir="ltr">{formatTime(unblockTarget.start_time)}</span>
                  {' '}—{' '}
                  <span className="font-mono font-bold text-white/60" dir="ltr">{formatTime(unblockTarget.end_time)}</span>
                  {' '}وتعود الفترة متاحة للحجز.
                </p>
              </div>
            </div>
            {actionError && (
              <div className="mx-6 mb-1 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
                {actionError}
              </div>
            )}
            <div className="flex items-center justify-end gap-3 px-6 py-5 mt-1 border-t border-white/[0.05] bg-[#111312]">
              <button
                type="button"
                onClick={() => { if (!submitting) setUnblockTarget(null); }}
                disabled={submitting}
                className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
              >
                تراجع
              </button>
              <button
                type="button"
                onClick={confirmUnblock}
                disabled={submitting}
                className="flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold bg-amber-500/[0.12] text-amber-300 border border-amber-500/30 hover:bg-amber-500/[0.18] hover:text-amber-200 hover:border-amber-500/45 disabled:opacity-60 disabled:cursor-not-allowed transition-all duration-200 active:scale-[0.97]"
              >
                {submitting ? (
                  <>
                    <Loader2 size={13} className="animate-spin" aria-hidden />
                    جاري الرفع...
                  </>
                ) : (
                  <>
                    <Ban size={13} aria-hidden />
                    تأكيد رفع الحجب
                  </>
                )}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Manual walk-in cancel: a recurring row offers single-vs-all; a one-off
          offers only the standard single cancel. */}
      {cancelManualTarget && (
        <div className="absolute inset-0 z-20 flex items-center justify-center p-4">
          <div
            className="absolute inset-0 bg-black/60"
            onClick={() => { if (!submitting) setCancelManualTarget(null); }}
            aria-hidden
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="cancel-manual-title"
            dir="rtl"
            className="relative w-full max-w-sm rounded-2xl bg-[#141715] border border-white/[0.10] shadow-2xl overflow-hidden"
          >
            <div className="flex items-start gap-3.5 px-6 pt-6 pb-4">
              <div className="w-10 h-10 rounded-xl bg-sky-500/[0.10] border border-sky-500/25 flex items-center justify-center flex-shrink-0">
                {cancelManualTarget.recurrence_group_id
                  ? <Repeat size={18} className="text-sky-400" aria-hidden />
                  : <Trash2 size={18} className="text-sky-400" aria-hidden />}
              </div>
              <div className="min-w-0">
                <h2 id="cancel-manual-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">
                  إلغاء الحجز اليدوي
                </h2>
                <p className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
                  <span className="text-white/60 font-semibold">{cancelManualTarget.guest_name || 'ضيف'}</span>
                  {' '}—{' '}
                  <span className="font-mono text-white/60" dir="ltr">{formatTime(cancelManualTarget.start_time)}</span>
                  {cancelManualTarget.recurrence_group_id
                    ? '. هذا الحجز جزء من سلسلة متكررة — اختر نطاق الإلغاء.'
                    : '. سيُلغى هذا الحجز وتعود الفترة متاحة.'}
                </p>
              </div>
            </div>
            {actionError && (
              <div className="mx-6 mb-1 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
                {actionError}
              </div>
            )}
            <div className="flex flex-col gap-2.5 px-6 py-5 mt-1 border-t border-white/[0.05] bg-[#111312]">
              {/* Standard single-occurrence cancel — always available */}
              <button
                type="button"
                onClick={cancelSingleOccurrence}
                disabled={submitting}
                className="flex items-center justify-center gap-2 px-5 py-2.5 rounded-xl text-[12px] font-bold bg-sky-500/[0.12] text-sky-300 border border-sky-500/30 hover:bg-sky-500/[0.18] hover:text-sky-200 hover:border-sky-500/45 disabled:opacity-60 disabled:cursor-not-allowed transition-all duration-200 active:scale-[0.97]"
              >
                {submitting ? <Loader2 size={13} className="animate-spin" aria-hidden /> : <Trash2 size={13} aria-hidden />}
                {cancelManualTarget.recurrence_group_id ? 'إلغاء هذا الموعد فقط' : 'تأكيد الإلغاء'}
              </button>
              {/* Bulk future cancel — only for a recurring occurrence */}
              {cancelManualTarget.recurrence_group_id && (
                <button
                  type="button"
                  onClick={cancelFutureGroup}
                  disabled={submitting}
                  className="flex items-center justify-center gap-2 px-5 py-2.5 rounded-xl text-[12px] font-bold bg-red-500/[0.10] text-red-300 border border-red-500/25 hover:bg-red-500/[0.16] hover:text-red-200 hover:border-red-500/40 disabled:opacity-60 disabled:cursor-not-allowed transition-all duration-200 active:scale-[0.97]"
                >
                  {submitting ? <Loader2 size={13} className="animate-spin" aria-hidden /> : <Repeat size={13} aria-hidden />}
                  إلغاء كل المواعيد القادمة
                </button>
              )}
              <button
                type="button"
                onClick={() => { if (!submitting) setCancelManualTarget(null); }}
                disabled={submitting}
                className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
              >
                تراجع
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
