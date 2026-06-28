'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Loader2, Check, X, Clock, Lock, Banknote } from 'lucide-react';
import api from '@/lib/api';

type Attendance = 'pending' | 'checked_in' | 'no_show';
type Payment = 'unpaid' | 'paid_cash';

interface Row {
  id: number;
  pitch_id: number;
  pitch_name: string;
  start_time: string;
  end_time: string;
  source: 'player' | 'manual' | 'block';
  status: string;
  attendance: Attendance;
  payment_status: Payment;
  attendee_name: string;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString('ar-JO', { hour: '2-digit', minute: '2-digit', hour12: true });
}

const STATE_STYLE: Record<Attendance, string> = {
  pending: 'text-white/45 bg-white/[0.05] border-white/[0.09]',
  checked_in: 'text-emerald-300 bg-emerald-500/15 border-emerald-500/30',
  no_show: 'text-red-300 bg-red-500/15 border-red-500/30',
};
const STATE_LABEL: Record<Attendance, string> = {
  pending: 'بانتظار',
  checked_in: 'حضر',
  no_show: 'لم يحضر',
};

export default function SchedulePage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pitchFilter, setPitchFilter] = useState<number>(0); // 0 = all in scope

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await api.get<{ data: Row[] }>('/schedule', {
        params: pitchFilter ? { pitch_id: pitchFilter } : {},
      });
      setRows(data.data ?? []);
    } catch {
      setError('تعذّر تحميل الجدول.');
    } finally {
      setLoading(false);
    }
  }, [pitchFilter]);

  useEffect(() => {
    load();
  }, [load]);

  // Pitch selector only when scope spans >1 pitch (multi-pitch owners).
  const pitches = useMemo(() => {
    const m = new Map<number, string>();
    rows.forEach((r) => m.set(r.pitch_id, r.pitch_name));
    return [...m.entries()].map(([id, name]) => ({ id, name }));
  }, [rows]);
  const showSelector = pitchFilter !== 0 || pitches.length > 1;

  // Reversible/idempotent: tapping the active state again reverts to 'pending'.
  async function setAttendance(row: Row, next: Attendance) {
    const target = row.attendance === next ? 'pending' : next;
    const prev = row.attendance;
    setRows((rs) => rs.map((r) => (r.id === row.id ? { ...r, attendance: target } : r))); // optimistic
    try {
      await api.patch(`/bookings/${row.id}/attendance`, { attendance: target });
    } catch {
      setRows((rs) => rs.map((r) => (r.id === row.id ? { ...r, attendance: prev } : r))); // rollback
    }
  }

  // One-click cash settlement (WO-F1 endpoint). Optimistic flip with rollback so
  // the floor operator never waits on the network mid-shift.
  async function togglePayment(row: Row) {
    const target: Payment = row.payment_status === 'paid_cash' ? 'unpaid' : 'paid_cash';
    const prev = row.payment_status;
    setRows((rs) => rs.map((r) => (r.id === row.id ? { ...r, payment_status: target } : r))); // optimistic
    try {
      await api.patch(`/bookings/${row.id}/payment`, { payment_status: target });
    } catch {
      setRows((rs) => rs.map((r) => (r.id === row.id ? { ...r, payment_status: prev } : r))); // rollback
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-[20px] font-bold tracking-tight">جدول اليوم</h1>
        {showSelector && (
          <select
            value={pitchFilter}
            onChange={(e) => setPitchFilter(Number(e.target.value))}
            className="rounded-lg bg-[#111312] border border-white/10 px-3 py-2 text-[12.5px] text-white/80"
          >
            <option value={0}>كل الملاعب</option>
            {pitches.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </select>
        )}
      </div>

      {loading ? (
        <div className="flex justify-center py-24"><Loader2 className="animate-spin text-white/30" /></div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>
      ) : rows.length === 0 ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center text-[13px] text-white/35">
          لا حجوزات اليوم.
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {rows.map((r) => {
            const isBlock = r.source === 'block';
            return (
              <div key={r.id} className="flex flex-wrap items-center gap-4 rounded-xl bg-[#141715] border border-white/[0.08] px-4 py-3">
                <div className="flex items-center gap-1.5 text-white/55 min-w-[120px]">
                  <Clock size={13} aria-hidden />
                  <span className="text-[12.5px] font-mono">{fmtTime(r.start_time)} – {fmtTime(r.end_time)}</span>
                </div>
                <div className="flex items-center gap-2 flex-1 min-w-0">
                  {isBlock && <Lock size={12} className="text-white/30 shrink-0" aria-hidden />}
                  <span className="text-[13px] truncate">{r.attendee_name || '—'}</span>
                </div>
                <span className={`shrink-0 rounded-full border px-2.5 py-0.5 text-[10.5px] font-bold ${STATE_STYLE[r.attendance]}`}>
                  {STATE_LABEL[r.attendance]}
                </span>
                {/* Actions on player/manual rows only — never blocks. */}
                {!isBlock && (
                  <div className="flex items-center gap-1.5 shrink-0">
                    <button
                      type="button"
                      onClick={() => setAttendance(r, 'checked_in')}
                      className={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-[11px] font-bold border transition-colors ${r.attendance === 'checked_in' ? 'bg-emerald-500/20 border-emerald-500/40 text-emerald-300' : 'border-white/10 text-white/55 hover:text-emerald-300 hover:border-emerald-500/30'}`}
                    >
                      <Check size={12} aria-hidden /> حضر
                    </button>
                    <button
                      type="button"
                      onClick={() => setAttendance(r, 'no_show')}
                      className={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-[11px] font-bold border transition-colors ${r.attendance === 'no_show' ? 'bg-red-500/20 border-red-500/40 text-red-300' : 'border-white/10 text-white/55 hover:text-red-300 hover:border-red-500/30'}`}
                    >
                      <X size={12} aria-hidden /> لم يحضر
                    </button>
                    {/* Divider then the one-click cash-settlement toggle. Emerald =
                        paid, amber = unpaid (consistent with the calendar view). */}
                    <span className="w-px h-5 bg-white/10 mx-0.5" aria-hidden />
                    <button
                      type="button"
                      onClick={() => togglePayment(r)}
                      aria-pressed={r.payment_status === 'paid_cash'}
                      className={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-[11px] font-bold border transition-colors ${r.payment_status === 'paid_cash' ? 'bg-emerald-500/20 border-emerald-500/40 text-emerald-300' : 'border-amber-500/25 text-amber-300/80 hover:text-amber-300 hover:border-amber-500/40'}`}
                    >
                      <Banknote size={12} aria-hidden /> {r.payment_status === 'paid_cash' ? 'مدفوع' : 'تحصيل'}
                    </button>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
