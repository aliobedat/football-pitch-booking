'use client';

// Month-over-month comparison strip (WO-REPORTS-R2). Renders ONLY when the
// selected period is a full calendar month — the parent gates mounting; this
// component never does partial-month math. Both summaries come from two
// parallel client-side fetches of GET /owner/reports/financial (the backend
// computes no diffs). Deltas are display arithmetic on server aggregates.

import { TrendingUp, TrendingDown, Minus } from 'lucide-react';
import { formatCurrency, formatNumber } from '@/lib/format';

export interface ReportSummary {
  booking_count: number;
  gross_revenue: number;
  collected: number;
  outstanding: number;
  cancelled_count: number;
}

// Local strict-3dp JOD wrapper (amendment C1) — lib/format untouched.
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });

interface Metric {
  key: 'gross_revenue' | 'collected' | 'booking_count';
  label: string;
  money: boolean;
}

const METRICS: Metric[] = [
  { key: 'gross_revenue', label: 'إجمالي الإيرادات', money: true },
  { key: 'collected',     label: 'المحصّل',          money: true },
  { key: 'booking_count', label: 'الحجوزات',         money: false },
];

function Delta({ current, prior }: { current: number; prior: number }) {
  if (prior === 0 && current === 0) {
    return <span className="inline-flex items-center gap-1 text-[11px] font-semibold text-white/30"><Minus size={12} aria-hidden />—</span>;
  }
  if (prior === 0) {
    return <span className="inline-flex items-center gap-1 text-[11px] font-bold text-emerald-400"><TrendingUp size={12} aria-hidden />جديد</span>;
  }
  const pct = ((current - prior) / prior) * 100;
  if (pct === 0) {
    return <span className="inline-flex items-center gap-1 text-[11px] font-semibold text-white/30"><Minus size={12} aria-hidden />بدون تغيير</span>;
  }
  const up = pct > 0;
  return (
    <span className={[
      'inline-flex items-center gap-1 text-[11px] font-bold',
      up ? 'text-emerald-400' : 'text-red-400',
    ].join(' ')}>
      {up ? <TrendingUp size={12} aria-hidden /> : <TrendingDown size={12} aria-hidden />}
      {formatNumber(Math.abs(pct), { maximumFractionDigits: 1 })}٪
    </span>
  );
}

export default function ComparisonStrip({
  current,
  prior,
  priorLabel,
}: {
  current: ReportSummary;
  prior: ReportSummary;
  priorLabel: string; // e.g. "حزيران 2026" — the preceding full month
}) {
  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
      <p className="text-[12px] text-white/40 mb-4">مقارنة مع {priorLabel}</p>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        {METRICS.map(({ key, label, money }) => {
          const cur = current[key];
          const prev = prior[key];
          return (
            <div key={key} className="rounded-xl bg-white/[0.02] border border-white/[0.06] px-4 py-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-[11px] font-semibold text-white/45">{label}</span>
                <Delta current={cur} prior={prev} />
              </div>
              <p className="mt-1.5 text-[16px] font-bold text-[#f0efe8] tabular-nums">
                {money ? <>{jod3(cur)}<span className="text-[10.5px] text-emerald-500/70 ms-1">د.أ</span></> : formatNumber(cur)}
              </p>
              <p className="text-[11px] text-white/30 tabular-nums">
                {priorLabel}: {money ? `${jod3(prev)} د.أ` : formatNumber(prev)}
              </p>
            </div>
          );
        })}
      </div>
    </div>
  );
}
