'use client';

// Visual Calendar Command Center — Read Calendar (Cockpit WO2 Part 2). A per-day
// resource timeline: the owner's pitches as ROWS, time as a LTR axis (earliest →
// latest, Western numerals) inside the RTL chrome. Events are absolutely
// positioned via lib/calendar geometry (detached from the discrete grid). The
// visible window is tightly bound to operating hours (+buffer); out-of-hours
// regions are dimmed. Tap an event → details; tap an empty slot → create a manual
// (walk-in) booking prefilled with that pitch + snapped 30-min start.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import {
  ChevronRight, ChevronLeft, CalendarDays, X, Loader2, Plus,
} from 'lucide-react';
import api from '@/lib/api';
import { formatTime, formatDate, formatNumber } from '@/lib/format';
import {
  computeWindow, bandStyle, buildTicks, offsetPct, fractionToMs, snapToSlot,
  SLOT_MIN, type DayWindow, type Interval,
} from '@/lib/calendar';

const AMMAN_TZ = 'Asia/Amman';
// Jordan observes a fixed UTC+3 (DST abolished); a literal offset is safe for the
// fallback-window math when no operating hours are configured.
const AMMAN_OFFSET = '+03:00';
const BUFFER_MIN = 60;
const PX_PER_HOUR = 88;
const ROW_LABEL_W = 150;

type Source = 'player' | 'manual' | 'block' | 'academy';

interface CalEvent {
  id: number; pitch_id: number; start_time: string; end_time: string;
  source: Source; status: string; attendance: string; payment_status: string; title: string;
  customer_id: number | null;
}
interface PitchRow {
  pitch_id: number; pitch_name: string; is_active: boolean;
  open_windows: Interval[]; has_schedule: boolean; events: CalEvent[];
}
interface CalendarDay { date: string; pitches: PitchRow[]; }

// today (Amman) as YYYY-MM-DD.
const ammanToday = () => new Intl.DateTimeFormat('en-CA', { timeZone: AMMAN_TZ }).format(new Date());
const shiftDate = (date: string, days: number) =>
  new Date(Date.parse(`${date}T12:00:00Z`) + days * 86400000).toISOString().slice(0, 10);

const SOURCE_STYLE: Record<Source, { bg: string; border: string; text: string; label: string }> = {
  player:  { bg: 'bg-emerald-500/20', border: 'border-emerald-500/40', text: 'text-emerald-100', label: 'لاعب' },
  manual:  { bg: 'bg-sky-500/20',     border: 'border-sky-500/40',     text: 'text-sky-100',     label: 'يدوي' },
  block:   { bg: 'bg-amber-500/15',   border: 'border-amber-500/40',   text: 'text-amber-100',   label: 'محظور' },
  academy: { bg: 'bg-violet-500/20',  border: 'border-violet-500/40',  text: 'text-violet-100',  label: 'أكاديمية' },
};

export default function CalendarPage() {
  const [date, setDate] = useState<string>(ammanToday);
  const [day, setDay]   = useState<CalendarDay | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState<string | null>(null);

  const [detail, setDetail]   = useState<CalEvent | null>(null);
  const [create, setCreate]   = useState<{ pitch: PitchRow; startMs: number } | null>(null);

  const fetchDay = useCallback(() => {
    setLoading(true);
    setError(null);
    api.get('/owner/calendar', { params: { date } })
      .then(res => setDay(res.data.data as CalendarDay))
      .catch(() => setError('تعذّر تحميل التقويم. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, [date]);

  useEffect(() => { fetchDay(); }, [fetchDay]);

  // The visible window: union of all open windows across pitches (+buffer), else a
  // sensible Amman fallback (08:00–23:00) when nothing is configured.
  const win: DayWindow = useMemo(() => {
    const allWindows = (day?.pitches ?? []).flatMap(p => p.open_windows);
    const fStart = Date.parse(`${date}T08:00:00${AMMAN_OFFSET}`);
    const fEnd   = Date.parse(`${date}T23:00:00${AMMAN_OFFSET}`);
    return computeWindow(allWindows, BUFFER_MIN, fStart, fEnd);
  }, [day, date]);

  const ticks = useMemo(() => buildTicks(win), [win]);
  const hours = (win.endMs - win.startMs) / 3_600_000;
  const trackWidth = Math.max(720, Math.round(hours * PX_PER_HOUR));
  const slotsCount = Math.max(1, Math.round((win.endMs - win.startMs) / (SLOT_MIN * 60_000)));

  const isToday = date === ammanToday();
  const nowPct = isToday ? offsetPct(Date.now(), win) : -1;

  return (
    <div className="flex flex-col gap-5">
      {/* Header / date nav */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <h1 className="text-[20px] font-bold tracking-tight flex items-center gap-2">
          <CalendarDays size={20} className="text-emerald-400" aria-hidden /> التقويم
        </h1>
        <div className="flex items-center gap-2">
          {/* RTL chrome: chevron-right = previous (newer to the right feels wrong; we
              map right→earlier day to match the RTL reading flow of controls). */}
          <button onClick={() => setDate(d => shiftDate(d, -1))} className="nav-btn" aria-label="اليوم السابق">
            <ChevronRight size={16} aria-hidden />
          </button>
          <button
            onClick={() => setDate(ammanToday())}
            className="px-3 py-1.5 rounded-lg text-[12px] font-semibold border border-white/[0.09] bg-white/[0.03] text-white/70 hover:text-white hover:border-white/20 transition-all"
          >
            اليوم
          </button>
          <button onClick={() => setDate(d => shiftDate(d, 1))} className="nav-btn" aria-label="اليوم التالي">
            <ChevronLeft size={16} aria-hidden />
          </button>
          <span className="text-[13px] text-white/60 font-semibold ms-1 min-w-[120px]">{formatDate(`${date}T12:00:00Z`, { weekday: 'long', day: 'numeric', month: 'long' })}</span>
        </div>
      </div>

      {/* Legend */}
      <div className="flex items-center gap-4 flex-wrap text-[11px] text-white/45">
        {(['player', 'manual', 'block', 'academy'] as Source[]).map(s => (
          <span key={s} className="inline-flex items-center gap-1.5">
            <span className={`w-3 h-3 rounded ${SOURCE_STYLE[s].bg} border ${SOURCE_STYLE[s].border}`} />
            {SOURCE_STYLE[s].label}
          </span>
        ))}
        <span className="inline-flex items-center gap-1.5"><span className="w-3 h-3 rounded bg-white/[0.02] border border-white/10" /> خارج الدوام</span>
      </div>

      {loading ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center">
          <Loader2 size={22} className="inline text-emerald-500 animate-spin" aria-hidden />
        </div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>
      ) : !day || day.pitches.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 gap-4">
          <CalendarDays size={28} className="text-white/15" aria-hidden />
          <p className="text-[14px] text-white/40">لا توجد ملاعب لعرضها</p>
        </div>
      ) : (
        // The timeline scroll region flows LTR (axis earliest→latest); the pitch
        // label column is sticky on the left.
        <div className="rounded-2xl border border-white/[0.07] bg-[#141715] overflow-x-auto" dir="ltr">
          <div style={{ width: ROW_LABEL_W + trackWidth }}>
            {/* Axis header (sticky top) */}
            <div className="flex sticky top-0 z-20 bg-[#111312] border-b border-white/[0.08]">
              <div style={{ width: ROW_LABEL_W }} className="sticky left-0 z-10 bg-[#111312] border-e border-white/[0.06]" />
              <div className="relative h-9" style={{ width: trackWidth }}>
                {ticks.map((t, i) => {
                  const onHour = new Date(t).getMinutes() === 0;
                  if (!onHour) return null;
                  return (
                    <span
                      key={i}
                      className="absolute top-0 h-full flex items-center text-[10px] font-mono text-white/40 -translate-x-1/2"
                      style={{ left: `${offsetPct(t, win)}%` }}
                    >
                      {formatTime(new Date(t).toISOString(), { hour: '2-digit', minute: '2-digit', hour12: false })}
                    </span>
                  );
                })}
              </div>
            </div>

            {/* Pitch rows */}
            {day.pitches.map(p => (
              <PitchLane
                key={p.pitch_id}
                pitch={p}
                win={win}
                trackWidth={trackWidth}
                slotsCount={slotsCount}
                nowPct={nowPct}
                onEvent={setDetail}
                onEmpty={(startMs) => setCreate({ pitch: p, startMs })}
              />
            ))}
          </div>
        </div>
      )}

      {detail && (
        <EventDetailModal
          event={detail}
          onClose={() => setDetail(null)}
          onChanged={() => { setDetail(null); fetchDay(); }}
        />
      )}
      {create && (
        <CreateManualModal
          pitch={create.pitch}
          startMs={create.startMs}
          onClose={() => setCreate(null)}
          onCreated={() => { setCreate(null); fetchDay(); }}
        />
      )}

      <style jsx>{`
        .nav-btn {
          display: inline-flex; align-items: center; justify-content: center;
          width: 32px; height: 32px; border-radius: 10px;
          border: 1px solid rgba(255,255,255,0.09); background: rgba(255,255,255,0.03);
          color: rgba(255,255,255,0.6); transition: all .15s;
        }
        .nav-btn:hover { color: #fff; border-color: rgba(255,255,255,0.2); }
      `}</style>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// One pitch row: dimmed base + lit operating-hour bands + gridlines + events.
// ─────────────────────────────────────────────────────────────────────────────
function PitchLane({
  pitch, win, trackWidth, slotsCount, nowPct, onEvent, onEmpty,
}: {
  pitch: PitchRow; win: DayWindow; trackWidth: number; slotsCount: number;
  nowPct: number; onEvent: (e: CalEvent) => void; onEmpty: (startMs: number) => void;
}) {
  const trackRef = useRef<HTMLDivElement>(null);

  const handleTrackClick = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const el = trackRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const fraction = (e.clientX - rect.left) / rect.width; // LTR track
    const startMs = snapToSlot(fractionToMs(fraction, win));
    onEmpty(startMs);
  }, [win, onEvent, onEmpty]);

  // 30-minute gridlines via a repeating gradient (perf: one paint, no per-tick DOM).
  const gridBg = `repeating-linear-gradient(to right, rgba(255,255,255,0.05) 0 1px, transparent 1px calc(100% / ${slotsCount}))`;

  return (
    <div className="flex border-b border-white/[0.04] last:border-0">
      {/* Sticky pitch label (left). Arabic text inside the LTR chrome. */}
      <div
        style={{ width: ROW_LABEL_W }}
        className="sticky left-0 z-10 bg-[#141715] border-e border-white/[0.06] px-3 py-3 flex flex-col justify-center"
        dir="rtl"
      >
        <span className="text-[13px] font-bold text-[#f0efe8] truncate leading-tight">{pitch.pitch_name}</span>
        <span className="text-[10px] text-white/30 mt-0.5">
          {pitch.is_active ? `${formatNumber(pitch.events.length)} حجز` : 'غير نشط'}
        </span>
      </div>

      {/* Track */}
      <div
        ref={trackRef}
        onClick={handleTrackClick}
        className="relative h-16 cursor-copy bg-white/[0.012]"
        style={{ width: trackWidth, backgroundImage: gridBg }}
        title="انقر لإضافة حجز يدوي"
      >
        {/* Lit operating-hour bands. A pitch with NO schedule is open 24/7 → fully
            lit (no dim). With a schedule, only open windows are lit (rest dimmed). */}
        {pitch.has_schedule
          ? pitch.open_windows.map((w, i) => {
              const b = bandStyle(w.start, w.end, win);
              return (
                <div
                  key={i}
                  className="absolute top-0 bottom-0 bg-white/[0.025] pointer-events-none"
                  style={{ left: `${b.left}%`, width: `${b.width}%` }}
                />
              );
            })
          : <div className="absolute inset-0 bg-white/[0.02] pointer-events-none" />}

        {/* Events */}
        {pitch.events.map(ev => {
          const b = bandStyle(ev.start_time, ev.end_time, win);
          const s = SOURCE_STYLE[ev.source] ?? SOURCE_STYLE.player;
          const noShow = ev.attendance === 'no_show';
          return (
            <button
              key={ev.id}
              onClick={(e) => { e.stopPropagation(); onEvent(ev); }}
              className={`absolute top-1.5 bottom-1.5 rounded-lg border px-2 overflow-hidden text-start ${s.bg} ${noShow ? 'border-red-500/60' : s.border} hover:brightness-125 transition-all`}
              style={{ left: `${b.left}%`, width: `${b.width}%`, minWidth: 6 }}
              title={ev.title}
            >
              {ev.payment_status === 'paid_cash' && (
                <span className="absolute top-1 end-1 w-1.5 h-1.5 rounded-full bg-emerald-400 ring-1 ring-emerald-300/50" title="مدفوع نقداً" />
              )}
              <span className={`block text-[11px] font-bold truncate ${s.text}`} dir="rtl">{ev.title || s.label}</span>
              <span className="block text-[9px] font-mono text-white/60" dir="ltr">
                {formatTime(ev.start_time, { hour: '2-digit', minute: '2-digit', hour12: false })}
                {noShow && <span className="text-red-300"> · لم يحضر</span>}
              </span>
            </button>
          );
        })}

        {/* Now indicator */}
        {nowPct >= 0 && nowPct <= 100 && (
          <div className="absolute top-0 bottom-0 w-px bg-red-500/70 pointer-events-none z-10" style={{ left: `${nowPct}%` }}>
            <span className="absolute -top-0.5 -translate-x-1/2 w-1.5 h-1.5 rounded-full bg-red-500" />
          </div>
        )}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Tap-to-detail
// ─────────────────────────────────────────────────────────────────────────────
function EventDetailModal({ event, onClose, onChanged }: { event: CalEvent; onClose: () => void; onChanged: () => void }) {
  const s = SOURCE_STYLE[event.source] ?? SOURCE_STYLE.player;
  const [saving, setSaving] = useState(false);
  const [error, setError]   = useState<string | null>(null);
  const isPaid = event.payment_status === 'paid_cash';
  // Blocks are unpriced maintenance holds — no settlement concept.
  const settleable = event.source !== 'block';

  const togglePaid = useCallback(async () => {
    setSaving(true);
    setError(null);
    try {
      await api.patch(`/bookings/${event.id}/payment`, {
        payment_status: isPaid ? 'unpaid' : 'paid_cash',
      });
      onChanged();
    } catch {
      setError('تعذّر تحديث حالة الدفع.');
      setSaving(false);
    }
  }, [event.id, isPaid, onChanged]);

  return (
    <ModalShell onClose={onClose} title={event.title || s.label}>
      <div className="flex flex-col gap-3 text-[13px]">
        <Row label="النوع" value={s.label} />
        <Row label="الوقت" value={`${formatTime(event.start_time)} — ${formatTime(event.end_time)}`} ltr />
        <Row label="التاريخ" value={formatDate(event.start_time, { weekday: 'long', day: 'numeric', month: 'long' })} />
        <Row label="الحالة" value={event.status === 'cancelled' ? 'ملغى' : 'مؤكد'} />
        <Row label="الحضور" value={
          event.attendance === 'checked_in' ? 'حضر' : event.attendance === 'no_show' ? 'لم يحضر' : '—'
        } />

        {settleable && (
          <div className="flex items-center justify-between gap-4 pt-1">
            <span className="text-white/40">حالة الدفع</span>
            <button
              type="button"
              onClick={togglePaid}
              disabled={saving}
              className={[
                'inline-flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-[12px] font-bold border transition-all disabled:opacity-50',
                isPaid
                  ? 'bg-emerald-500/15 border-emerald-500/30 text-emerald-300 hover:bg-emerald-500/20'
                  : 'bg-amber-500/10 border-amber-500/30 text-amber-300 hover:bg-amber-500/15',
              ].join(' ')}
            >
              {saving ? <Loader2 size={13} className="animate-spin" aria-hidden /> : null}
              {isPaid ? 'مدفوع نقداً ✓ — اضغط لإلغاء' : 'غير مدفوع — تحصيل نقدي'}
            </button>
          </div>
        )}
        {error && <p className="text-[12px] text-red-400">{error}</p>}

        {event.customer_id && (
          <Link
            href={`/customers/${event.customer_id}`}
            onClick={onClose}
            className="mt-1 inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl text-[12px] font-semibold border border-emerald-500/25 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/15 transition-all"
          >
            عرض ملف الزبون
          </Link>
        )}
      </div>
    </ModalShell>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Tap-to-create-manual
// ─────────────────────────────────────────────────────────────────────────────
const DURATIONS = [
  { min: 60, label: 'ساعة' },
  { min: 90, label: 'ساعة ونصف' },
  { min: 120, label: 'ساعتان' },
  { min: 30, label: 'نصف ساعة' },
];

function CreateManualModal({
  pitch, startMs, onClose, onCreated,
}: {
  pitch: PitchRow; startMs: number; onClose: () => void; onCreated: () => void;
}) {
  const [name, setName]       = useState('');
  const [phone, setPhone]     = useState('');
  const [duration, setDur]    = useState(60);
  const [saving, setSaving]   = useState(false);
  const [error, setError]     = useState<string | null>(null);
  const [bypassPrompt, setBypass] = useState(false);

  const startISO = new Date(startMs).toISOString();
  const endISO   = new Date(startMs + duration * 60_000).toISOString();

  const submit = useCallback(async (forceBypassHours: boolean) => {
    if (!name.trim()) { setError('اسم الضيف مطلوب'); return; }
    setSaving(true);
    setError(null);
    try {
      await api.post(`/pitches/${pitch.pitch_id}/bookings/manual`, {
        start_time: startISO,
        end_time: endISO,
        guest_name: name.trim(),
        guest_phone: phone.trim(),
        force_bypass_hours: forceBypassHours,
      });
      onCreated();
    } catch (err: any) {
      const code = err?.response?.data?.error;
      if (code === 'outside_operating_hours') {
        setBypass(true); // offer the soft override
        setError('الوقت خارج ساعات عمل الملعب. تأكيد التسجيل رغم ذلك؟');
      } else if (code === 'slot_conflict') {
        setError('هذا الوقت يتعارض مع حجز قائم.');
      } else {
        setError(err?.response?.data?.message ?? 'تعذّر تسجيل الحجز.');
      }
    } finally {
      setSaving(false);
    }
  }, [name, phone, startISO, endISO, pitch.pitch_id, onCreated]);

  return (
    <ModalShell onClose={onClose} title="حجز يدوي جديد">
      <div className="flex flex-col gap-3.5">
        <p className="text-[12px] text-white/45">
          {pitch.pitch_name} · <span className="font-mono text-white/60" dir="ltr">{formatTime(startISO)} — {formatTime(endISO)}</span>
        </p>
        <Field label="اسم الضيف *">
          <input value={name} onChange={e => setName(e.target.value)} autoFocus
            className="modal-input" placeholder="مثال: أحمد" />
        </Field>
        <Field label="رقم الهاتف (اختياري)">
          <input value={phone} onChange={e => setPhone(e.target.value)} dir="ltr"
            className="modal-input font-mono" placeholder="+9627…" />
        </Field>
        <Field label="المدة">
          <select value={duration} onChange={e => setDur(Number(e.target.value))} className="modal-input">
            {DURATIONS.map(d => <option key={d.min} value={d.min} className="bg-[#0f1110]">{d.label}</option>)}
          </select>
        </Field>

        {error && <p className="text-[12px] text-amber-400">{error}</p>}

        <div className="flex items-center justify-end gap-3 mt-1">
          <button onClick={onClose} disabled={saving} className="px-4 py-2 rounded-xl text-[12px] font-semibold text-white/50 hover:text-white/80 border border-white/[0.08] disabled:opacity-50 transition-all">إلغاء</button>
          {bypassPrompt ? (
            <button onClick={() => submit(true)} disabled={saving}
              className="inline-flex items-center gap-2 px-5 py-2 rounded-xl text-[12px] font-bold bg-amber-500/15 text-amber-300 border border-amber-500/30 hover:bg-amber-500/20 disabled:opacity-50 transition-all">
              {saving && <Loader2 size={13} className="animate-spin" aria-hidden />} تأكيد رغم خارج الدوام
            </button>
          ) : (
            <button onClick={() => submit(false)} disabled={saving}
              className="inline-flex items-center gap-2 px-5 py-2 rounded-xl text-[12px] font-bold bg-emerald-500/[0.12] text-emerald-400 border border-emerald-500/25 hover:bg-emerald-500/[0.18] disabled:opacity-50 transition-all">
              {saving ? <Loader2 size={13} className="animate-spin" aria-hidden /> : <Plus size={13} aria-hidden />} تسجيل الحجز
            </button>
          )}
        </div>
      </div>
      <style jsx>{`
        .modal-input {
          width: 100%; background: #0f1110; border: 1px solid rgba(255,255,255,0.09);
          border-radius: 12px; padding: 10px 12px; font-size: 13px; color: rgba(255,255,255,0.85);
          outline: none;
        }
        .modal-input:focus { border-color: rgba(16,185,129,0.4); }
      `}</style>
    </ModalShell>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared bits
// ─────────────────────────────────────────────────────────────────────────────
function ModalShell({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={onClose} aria-hidden />
      <div role="dialog" aria-modal="true" className="relative w-full max-w-md rounded-2xl bg-[#141715] border border-white/[0.09] shadow-2xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
          <h2 className="text-[15px] font-bold text-[#f0efe8]">{title}</h2>
          <button onClick={onClose} className="text-white/40 hover:text-white/80 transition-colors" aria-label="إغلاق"><X size={18} /></button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  );
}

function Row({ label, value, ltr }: { label: string; value: string; ltr?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-white/40">{label}</span>
      <span className={`text-white/80 font-semibold ${ltr ? 'font-mono' : ''}`} dir={ltr ? 'ltr' : undefined}>{value}</span>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-[12px] text-white/45">{label}</span>
      {children}
    </label>
  );
}
