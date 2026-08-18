'use client';

// Series-reschedule result (WO-BOOKING-SHEET-REDESIGN). Presentational only —
// renders the three response buckets PATCH /bookings/:id/reschedule returns in
// series mode ({moved, conflicts, skipped}). No state, no API calls.
//
// conflicts is WARNING-styled (partial success) unless moved is empty, in which
// case nothing happened at all and it escalates to error styling — same weight
// as the single-booking slot_conflict error, per the WO. skipped is a muted
// note, never a warning (an already-started occurrence isn't a failure).

import { AlertTriangle, CheckCircle2, MinusCircle } from 'lucide-react';
import { formatDate } from '@/lib/format';

export interface SeriesRescheduleMovedItem {
  id: number;
  date: string;
  old_time: string;
  new_time: string;
}

export interface SeriesRescheduleBucketItem {
  id: number;
  date: string;
  reason: string;
}

export interface SeriesRescheduleResultProps {
  moved: SeriesRescheduleMovedItem[];
  conflicts: SeriesRescheduleBucketItem[];
  skipped: SeriesRescheduleBucketItem[];
}

// Dates arrive as plain "YYYY-MM-DD" (no time-of-day) — formatDate's own
// `new Date(iso)` + Amman-timezone rendering never rolls the day backward for
// a bare date string (UTC midnight + 3h stays inside the same calendar day).
const fmtConflictDate = (d: string) => formatDate(d, { day: 'numeric', month: 'short' });

export default function SeriesRescheduleResult({ moved, conflicts, skipped }: SeriesRescheduleResultProps) {
  if (moved.length === 0 && conflicts.length === 0 && skipped.length === 0) return null;

  // Nothing moved at all → same error weight as a single-booking slot_conflict,
  // not the softer partial-success warning.
  const allFailed = moved.length === 0 && conflicts.length > 0;

  return (
    <div className="flex flex-col gap-2">
      {moved.length > 0 && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-emerald-500/[0.08] border border-emerald-500/25 text-[12px] text-emerald-300">
          <CheckCircle2 size={13} aria-hidden className="shrink-0" />
          تم نقل {moved.length} {moved.length === 1 ? 'موعد' : 'مواعيد'}
        </div>
      )}

      {conflicts.length > 0 && (
        <div
          className={[
            'flex flex-col gap-1.5 px-3 py-2 rounded-lg border text-[12px]',
            allFailed
              ? 'bg-red-500/[0.07] border-red-500/20 text-red-300'
              : 'bg-amber-500/[0.08] border-amber-500/25 text-amber-300',
          ].join(' ')}
        >
          <div className="flex items-center gap-2">
            <AlertTriangle size={13} aria-hidden className="shrink-0" />
            {allFailed
              ? 'تعذّر نقل السلسلة — كل المواعيد متعارضة'
              : `تعذّر نقل ${conflicts.length} ${conflicts.length === 1 ? 'موعد' : 'مواعيد'}`}
          </div>
          <ul className="flex flex-col gap-0.5 pr-5 text-[11px] opacity-90">
            {conflicts.map(c => (
              <li key={c.id}>{fmtConflictDate(c.date)}</li>
            ))}
          </ul>
        </div>
      )}

      {skipped.length > 0 && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-white/[0.03] border border-white/[0.08] text-[11.5px] text-white/45">
          <MinusCircle size={12} aria-hidden className="shrink-0" />
          تم تجاهل {skipped.length} {skipped.length === 1 ? 'موعد بدأ بالفعل' : 'مواعيد بدأت بالفعل'}
        </div>
      )}
    </div>
  );
}
