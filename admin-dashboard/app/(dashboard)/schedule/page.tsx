'use client';

// جدول اليوم — the operator's daily list (staff/owner/admin).
//
// WO-BOOKING-SHEET / PR-B.2b: each booking row opens the shared BookingSheet
// (payment tracking + extension). The legacy one-tap مدفوع toggle is REPLACED
// by the sheet (first executed third of
// docs/followups/legacy-setpayment-callers-migration.md); attendance buttons
// stay optimistic-with-rollback and stopPropagation so a حضر tap never opens
// the sheet. Blocks keep their inert row. Money state on the row comes from
// the PR-B.2a additive payload and is never mutated client-side.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Loader2, Check, X, Clock, Lock, ChevronLeft } from 'lucide-react';
import api from '@/lib/api';
import { useAuth } from '@/context/AuthContext';
import BookingSheet, { type SheetBooking, paymentDisplayBadge } from '@/components/BookingSheet';
import { formatCurrency } from '@/lib/format';

type Attendance = 'pending' | 'checked_in' | 'no_show';

interface Row {
  id: number;
  pitch_id: number;
  pitch_name: string;
  start_time: string;
  end_time: string;
  source: 'player' | 'manual' | 'academy' | 'block';
  status: string;
  attendance: Attendance;
  payment_status: string; // legacy (display no longer reads it)
  attendee_name: string;
  // PR-B.2a additive money payload (server-derived; authoritative).
  total_price: number;
  amount_paid: number | null;
  payment_display: 'untracked' | 'unpaid' | 'partial' | 'paid';
  remaining: number | null;
  price_per_hour: number; // per-row: the schedule spans pitches with different rates
  recurrence_group_id: string | null; // carried by the payload; satisfies SheetBooking (cancel gated off here)
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString('ar-JO', { hour: '2-digit', minute: '2-digit', hour12: true });
}

const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });

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
  const { user } = useAuth();
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pitchFilter, setPitchFilter] = useState<number>(0); // 0 = all in scope
  const [sheetId, setSheetId] = useState<number | null>(null);

  // Staff can settle payment but not extend or reprice — the backend enforces
  // this (extend route is owner/admin; total_price 403s for staff); these
  // props just hide what the server would refuse.
  const canManage = user != null && user.role !== 'staff';

  const load = useCallback(async (silent = false): Promise<void> => {
    if (!silent) setLoading(true);
    setError(null);
    try {
      const { data } = await api.get<{ data: Row[] }>('/schedule', {
        params: pitchFilter ? { pitch_id: pitchFilter } : {},
      });
      setRows(data.data ?? []);
    } catch {
      if (!silent) setError('تعذّر تحميل الجدول.');
    } finally {
      if (!silent) setLoading(false);
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

  // The open sheet's booking, re-derived from the freshest payload by id —
  // refetch is authoritative (Day View pattern).
  const sheetRow = useMemo<Row | null>(
    () => (sheetId == null ? null : rows.find((r) => r.id === sheetId) ?? null),
    [sheetId, rows],
  );
  useEffect(() => {
    if (sheetId != null && !rows.some((r) => r.id === sheetId)) setSheetId(null);
  }, [sheetId, rows]);

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
            const ended = new Date(r.end_time).getTime() < Date.now();
            const badge = paymentDisplayBadge(r.payment_display);
            const tappable = !isBlock;
            const rowInner = (
              <>
                <div className="flex items-center gap-1.5 text-white/55 min-w-[110px] shrink-0">
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
                {/* Attendance on player/manual/academy rows only — never blocks.
                    stopPropagation: a حضر tap must never open the sheet. */}
                {!isBlock && (
                  <div className="flex items-center gap-1.5 shrink-0" onClick={(e) => e.stopPropagation()}>
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
                  </div>
                )}
                {/* Payment state at a glance (untracked = deliberately silent) +
                    the tap affordance. Replaces the legacy مدفوع toggle. */}
                {!isBlock && (
                  <div className="flex items-center gap-2 shrink-0 ms-auto sm:ms-0">
                    {badge && (
                      <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold border ${badge.cls}`}>
                        {badge.label}
                      </span>
                    )}
                    {r.payment_display === 'partial' && r.remaining != null && (
                      <span className="text-[11px] font-bold text-red-400 tabular-nums whitespace-nowrap">
                        باقي {jod3(r.remaining)}
                      </span>
                    )}
                    <ChevronLeft size={15} className="text-white/25" aria-hidden />
                  </div>
                )}
              </>
            );

            return tappable ? (
              <button
                key={r.id}
                type="button"
                onClick={() => setSheetId(r.id)}
                aria-label={`تفاصيل حجز ${r.attendee_name || ''}`}
                className={`flex flex-wrap items-center gap-x-4 gap-y-2 text-start w-full min-h-[48px] rounded-xl bg-[#141715] border border-white/[0.08] px-4 py-3 transition-colors hover:border-white/[0.16] active:bg-white/[0.04] ${ended ? 'opacity-60' : ''}`}
              >
                {rowInner}
              </button>
            ) : (
              <div
                key={r.id}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 min-h-[48px] rounded-xl bg-[#141715] border border-white/[0.08] px-4 py-3"
              >
                {rowInner}
              </div>
            );
          })}
        </div>
      )}

      {sheetRow && (
        <BookingSheet
          booking={sheetRow as SheetBooking}
          title={sheetRow.attendee_name}
          pricePerHour={sheetRow.price_per_hour}
          canExtend={canManage}
          canEditTotal={canManage}
          canCancel={false}
          onClose={() => setSheetId(null)}
          onRefetch={() => load(true)}
        />
      )}
    </div>
  );
}
