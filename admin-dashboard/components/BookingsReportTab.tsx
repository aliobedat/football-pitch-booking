'use client';

// الحجوزات tab of التقارير (WO-REPORTS-R3). Mounted only when the tab is
// active, so the fetch is lazy; the shared period/pitch selection is owned by
// reports/page.tsx and passed down. Consumes GET /owner/reports/bookings
// exclusively — rows exclude blocks and cancelled server-side (all rows are
// confirmed, hence no status column), ordered by start time. Summary buckets:
// total / attended / no_show / cancelled; a pending-attendance card is
// deliberately absent (docs/followups/reports-pending-attendance-card.md) —
// pending rows render the neutral — pill in the table.

import { useEffect, useState } from 'react';
import { Loader2, CalendarDays } from 'lucide-react';
import api from '@/lib/api';
import { formatCurrency, formatNumber, formatDate, formatTime } from '@/lib/format';
import ReportSummaryCard from '@/components/ReportSummaryCard';
import AttendancePill from '@/components/AttendancePill';
import PaymentStatusPill from '@/components/PaymentStatusPill';

interface BookingsSummary {
  total: number;
  attended: number;
  no_show: number;
  cancelled: number;
}
interface ReportBookingRow {
  id: number;
  pitch_id: number;
  pitch_name: string;
  start_time: string;
  end_time: string;
  source: string;
  status: string;
  attendance: string;
  customer_name: string;
  customer_phone: string;
  total_price: number;
  payment_status: string;
}
interface BookingsReport {
  from: string;
  to: string;
  pitch_id?: number;
  pitch_name?: string;
  summary: BookingsSummary;
  rows: ReportBookingRow[];
}

// Local strict-3dp JOD wrapper (amendment C1) — duplicated from the financial
// tab by ruling D1; lib/format untouched.
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });

// Manual rows carry an inline tag, mirroring the operational الحجوزات SOURCE_TAG
// colour language. Player/academy rows are untagged (operational precedent);
// blocks never reach this statement.
const MANUAL_TAG_CLS = 'bg-sky-500/15 border-sky-500/30 text-sky-300';

export default function BookingsReportTab({
  from,
  to,
  pitchId,
  rangeIssue,
}: {
  from: string;
  to: string;
  pitchId: number | '';
  rangeIssue: string | null;
}) {
  const [data, setData] = useState<BookingsReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (rangeIssue) return;
    // Stale-response guard: a slow response for a superseded selection must not
    // overwrite the current one (out-of-order resolution observed in QA).
    let stale = false;
    setLoading(true);
    setError(null);

    const params: Record<string, string | number> = { from, to };
    if (pitchId !== '') params.pitch_id = pitchId;

    api.get('/owner/reports/bookings', { params })
      .then(res => { if (!stale) setData(res.data.data as BookingsReport); })
      .catch(err => {
        if (stale) return;
        setData(null);
        const status = err?.response?.status;
        const serverMsg = err?.response?.data?.message;
        // 404 = out-of-scope/unknown pitch — surface the server's Arabic message
        // (same banner path as the financial tab).
        setError(status === 404
          ? (serverMsg ?? 'الملعب غير موجود أو لا تملك صلاحية عرضه')
          : status === 400
            ? (serverMsg ?? 'طلب غير صالح')
            : status === 422
              ? 'الفترة المطلوبة تحتوي عدداً كبيراً جداً من الحجوزات — ضيّق نطاق التاريخ'
              : 'تعذّر تحميل التقرير. تأكد من صلاحيات الحساب.');
      })
      .finally(() => { if (!stale) setLoading(false); });

    return () => { stale = true; };
  }, [from, to, pitchId, rangeIssue]);

  if (rangeIssue) return null; // the page renders the shared range banner

  if (error) {
    return (
      <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">
        {error}
      </div>
    );
  }

  if (loading || !data) {
    return (
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 flex items-center justify-center">
        <Loader2 size={22} className="text-emerald-500 animate-spin" aria-hidden />
      </div>
    );
  }

  return (
    <>
      {/* ── Summary cards (ruling A1) ── */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <ReportSummaryCard label="إجمالي الحجوزات" value={formatNumber(data.summary.total)} />
        <ReportSummaryCard label="الحضور" value={formatNumber(data.summary.attended)} tone="green" />
        <ReportSummaryCard label="الغياب" value={formatNumber(data.summary.no_show)} tone={data.summary.no_show > 0 ? 'red' : undefined} />
        <ReportSummaryCard label="الملغاة" value={formatNumber(data.summary.cancelled)} tone={data.summary.cancelled > 0 ? 'red' : undefined} />
      </div>

      {/* ── Rows table — render-all (probe ruling: ≤~24 rows per 92-day window) ── */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
        <p className="text-[12px] text-white/40 mb-4">سجل الحجوزات</p>
        {data.rows.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-10 gap-3 text-center">
            <div className="w-14 h-14 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
              <CalendarDays size={22} className="text-white/15" aria-hidden />
            </div>
            <p className="text-[13px] text-white/30">لا توجد حجوزات في هذه الفترة</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-[12.5px]">
              <thead>
                <tr className="text-white/40 text-[11px]">
                  <th className="text-right font-semibold pb-3 pe-4"># الحجز</th>
                  <th className="text-right font-semibold pb-3 pe-4">العميل</th>
                  <th className="text-right font-semibold pb-3 pe-4">الملعب</th>
                  <th className="text-right font-semibold pb-3 pe-4">التاريخ</th>
                  <th className="text-right font-semibold pb-3 pe-4">الوقت</th>
                  <th className="text-right font-semibold pb-3 pe-4">المبلغ (د.أ)</th>
                  <th className="text-right font-semibold pb-3 pe-4">الحضور</th>
                  <th className="text-right font-semibold pb-3">الدفع</th>
                </tr>
              </thead>
              <tbody>
                {data.rows.map(row => (
                  <tr key={row.id} className="border-t border-white/[0.05] text-[#f0efe8]">
                    <td className="py-3 pe-4">
                      <span className="text-[12px] font-bold text-white/45 font-mono">
                        #{String(row.id).padStart(4, '0')}
                      </span>
                    </td>
                    <td className="py-3 pe-4">
                      <p className="font-semibold leading-snug flex items-center gap-1.5">
                        <span className="truncate max-w-[160px]">{row.customer_name || '—'}</span>
                        {row.source === 'manual' && (
                          <span className={`inline-flex flex-shrink-0 items-center px-1.5 py-0.5 rounded-md text-[9px] font-bold border ${MANUAL_TAG_CLS}`}>
                            يدوي
                          </span>
                        )}
                      </p>
                      {row.customer_phone && (
                        <p className="text-[11.5px] text-emerald-400/80 font-mono mt-0.5" dir="ltr">
                          {row.customer_phone}
                        </p>
                      )}
                    </td>
                    <td className="py-3 pe-4 text-white/65">{row.pitch_name || `ملعب #${row.pitch_id}`}</td>
                    <td className="py-3 pe-4 whitespace-nowrap text-white/50 text-[12px]">{formatDate(row.start_time)}</td>
                    <td className="py-3 pe-4 whitespace-nowrap text-white/50 text-[12px]">
                      {formatTime(row.start_time)}
                      <span className="mx-1 text-white/20">—</span>
                      {formatTime(row.end_time)}
                    </td>
                    <td className="py-3 pe-4 tabular-nums whitespace-nowrap">{jod3(row.total_price)}</td>
                    <td className="py-3 pe-4"><AttendancePill attendance={row.attendance} /></td>
                    <td className="py-3"><PaymentStatusPill status={row.payment_status} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}
