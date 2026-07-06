// Attendance pill — the shared read-only rendering of a booking's attendance
// state. Strings and colours mirror the inline ATTENDANCE_AR trio already used
// on customers/[id], schedule, and calendar (extraction is ADDITIVE ONLY — the
// three inline copies stay untouched; consolidation is tracked in
// docs/followups/attendance-pill-consolidation.md). Values mirror the backend
// attendance column: 'checked_in' | 'no_show' | 'pending' (anything else
// renders as the neutral pending pill).

const ATTENDANCE_AR: Record<string, { label: string; cls: string }> = {
  checked_in: { label: 'حضر',     cls: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400' },
  no_show:    { label: 'لم يحضر', cls: 'bg-red-500/15 border-red-500/30 text-red-400' },
  pending:    { label: '—',       cls: 'bg-white/[0.04] border-white/[0.08] text-white/35' },
};

export default function AttendancePill({
  attendance,
  className = '',
}: {
  attendance: string;
  className?: string;
}) {
  const { label, cls } = ATTENDANCE_AR[attendance] ?? ATTENDANCE_AR.pending;
  return (
    <span
      className={`inline-flex items-center px-2.5 py-1 rounded-lg text-[10px] font-bold border ${cls} ${className}`.trim()}
    >
      {label}
    </span>
  );
}
