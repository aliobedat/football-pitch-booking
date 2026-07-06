// Report summary card — extracted verbatim from reports/page.tsx (R2's
// SummaryCard) so both التقارير tabs (المالي / الحجوزات) share one card.

export default function ReportSummaryCard({ label, value, unit, tone }: {
  label: string; value: string; unit?: string; tone?: 'green' | 'amber' | 'red';
}) {
  const valueColor =
    tone === 'green' ? 'text-emerald-300' :
    tone === 'amber' ? 'text-amber-300' :
    tone === 'red'   ? 'text-red-300'   : 'text-[#f0efe8]';
  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-4">
      <p className="text-[11px] font-semibold text-white/40">{label}</p>
      <p className={`mt-1.5 text-[18px] font-bold tabular-nums ${valueColor}`}>
        {value}
        {unit && <span className="text-[10.5px] text-emerald-500/70 ms-1 font-semibold">{unit}</span>}
      </p>
    </div>
  );
}
