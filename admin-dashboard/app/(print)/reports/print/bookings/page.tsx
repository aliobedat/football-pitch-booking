'use client';

// Printable A4 bookings statement (WO-REPORTS-R4). Reads from&to[&pitch_id]
// from the URL, fetches GET /owner/reports/bookings ONCE and renders: header,
// attendance summary block, rows table with the R3 column set. Pills are
// flattened to plain text — no colored spans on paper. Rows arrive ordered
// lower(booking_range) ASC, id ASC server-side (deterministic output).
// window.print() auto-fires only on fully-resolved success (ruling A1).

import { Suspense, useEffect, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import api from '@/lib/api';
import { formatCurrency, formatNumber, formatDate, formatTime } from '@/lib/format';
import { useAuth } from '@/context/AuthContext';
import { paymentPillLabel } from '@/components/PaymentStatusPill';
import StatementHeader from '@/components/print/StatementHeader';
import { PrintLoading, PrintError, parseWindow } from '@/components/print/PrintStates';
import useAutoPrint from '@/components/print/useAutoPrint';

// Payload types mirror the ratified R1 bookings contract (BookingsReportTab).
interface BookingsSummary { total: number; attended: number; no_show: number; cancelled: number }
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
  collected_amount: number; // PR-C amended collected semantics (server-derived)
  remaining_amount: number;
}
interface BookingsReport {
  from: string;
  to: string;
  pitch_id?: number;
  pitch_name?: string;
  summary: BookingsSummary;
  rows: ReportBookingRow[];
}

// Local strict-3dp JOD wrapper — 3rd copy by ruling B1; consolidation tracked
// in docs/followups/jod3-consolidation.md.
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });

// AttendancePill's wording flattened to plain text (paper carries no color).
const ATTENDANCE_TEXT: Record<string, string> = {
  checked_in: 'حضر',
  no_show: 'لم يحضر',
  pending: '—',
};

const TH = 'text-right font-bold text-[10px] text-[#555] border-b border-[#1a1a1a] pb-1.5 pe-2.5';
const TD = 'py-1.5 pe-2.5 border-b border-[#ddd] align-top';

function BookingsPrintInner() {
  const { user } = useAuth();
  const searchParams = useSearchParams();
  const win = parseWindow(new URLSearchParams(searchParams.toString()));

  const [data, setData] = useState<BookingsReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!win) return;
    let stale = false;
    const params: Record<string, string> = { from: win.from, to: win.to };
    if (win.pitchId !== null) params.pitch_id = win.pitchId;
    api.get('/owner/reports/bookings', { params })
      .then(res => { if (!stale) setData(res.data.data as BookingsReport); })
      .catch(err => {
        if (stale) return;
        const status = err?.response?.status;
        const serverMsg = err?.response?.data?.message;
        setError(status === 404
          ? (serverMsg ?? 'الملعب غير موجود أو لا تملك صلاحية عرضه')
          : status === 400
            ? (serverMsg ?? 'طلب غير صالح')
            : status === 422
              ? 'الفترة المطلوبة تحتوي عدداً كبيراً جداً من الحجوزات — ضيّق نطاق التاريخ'
              : 'تعذّر تحميل التقرير. تأكد من صلاحيات الحساب.');
      });
    return () => { stale = true; };
    // win is derived from the URL — its parts are the real deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [win?.from, win?.to, win?.pitchId]);

  useAutoPrint(!!data && !error);

  if (!win) return <PrintError message="رابط الطباعة غير صالح — الفترة مفقودة أو غير مكتملة." />;
  if (error) return <PrintError message={error} />;
  if (!data) return <PrintLoading />;

  return (
    <div dir="rtl">
      <StatementHeader
        title="كشف الحجوزات"
        ownerName={user?.full_name}
        pitchLabel={data.pitch_name ?? 'كل الملاعب'}
        from={data.from}
        to={data.to}
      />

      {/* ── Summary block ── */}
      <section className="mb-6">
        <h2 className="text-[13px] font-bold mb-2">الملخّص</h2>
        <div className="grid grid-cols-4 border border-[#1a1a1a]">
          {([
            ['إجمالي الحجوزات', formatNumber(data.summary.total)],
            ['الحضور', formatNumber(data.summary.attended)],
            ['الغياب', formatNumber(data.summary.no_show)],
            ['الملغاة', formatNumber(data.summary.cancelled)],
          ] as [string, string][]).map(([label, value], i) => (
            <div key={label} className={`px-3 py-2 ${i > 0 ? 'border-e border-[#ccc]' : ''}`}>
              <p className="text-[10px] text-[#555]">{label}</p>
              <p className="text-[14px] font-bold tabular-nums mt-0.5">{value}</p>
            </div>
          ))}
        </div>
      </section>

      {/* ── Rows table — R3 column set, pills as plain text ── */}
      <section className="mb-6">
        <h2 className="text-[13px] font-bold mb-2">سجل الحجوزات</h2>
        {data.rows.length === 0 ? (
          <p className="text-[12px] text-[#777]">لا توجد حجوزات في هذه الفترة.</p>
        ) : (
          <table className="text-[10.5px]">
            <thead>
              <tr>
                <th className={TH}># الحجز</th>
                <th className={TH}>العميل</th>
                <th className={TH}>الملعب</th>
                <th className={TH}>التاريخ</th>
                <th className={TH}>الوقت</th>
                <th className={TH}>المبلغ (د.أ)</th>
                <th className={TH}>المحصّل (د.أ)</th>
                <th className={TH}>المتبقي (د.أ)</th>
                <th className={TH}>الحضور</th>
                <th className={`${TH} pe-0`}>الدفع</th>
              </tr>
            </thead>
            <tbody>
              {data.rows.map(row => (
                <tr key={row.id}>
                  <td className={`${TD} font-mono whitespace-nowrap`}>#{String(row.id).padStart(4, '0')}</td>
                  <td className={TD}>
                    <p className="font-semibold leading-snug">
                      {row.customer_name || '—'}
                      {row.source === 'manual' && <span className="text-[#555] font-normal"> (يدوي)</span>}
                    </p>
                    {row.customer_phone && (
                      <p className="font-mono text-[#555] mt-0.5" dir="ltr">{row.customer_phone}</p>
                    )}
                  </td>
                  <td className={TD}>{row.pitch_name || `ملعب #${row.pitch_id}`}</td>
                  <td className={`${TD} whitespace-nowrap`}>{formatDate(row.start_time)}</td>
                  <td className={`${TD} whitespace-nowrap`}>
                    {formatTime(row.start_time)} — {formatTime(row.end_time)}
                  </td>
                  <td className={`${TD} tabular-nums whitespace-nowrap`}>{jod3(row.total_price)}</td>
                  <td className={`${TD} tabular-nums whitespace-nowrap`}>{jod3(row.collected_amount)}</td>
                  <td className={`${TD} tabular-nums whitespace-nowrap`}>{jod3(row.remaining_amount)}</td>
                  <td className={`${TD} whitespace-nowrap`}>{ATTENDANCE_TEXT[row.attendance] ?? '—'}</td>
                  <td className={`${TD} pe-0 whitespace-nowrap`}>{paymentPillLabel(row.payment_status)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <p className="text-[10.5px] text-[#777]">
        تشمل القائمة الحجوزات المؤكدة فقط ضمن نطاق ملاعبك، بتوقيت عمّان، مرتبةً حسب وقت البدء.
      </p>
    </div>
  );
}

// useSearchParams requires a Suspense boundary at build (reports/page.tsx precedent).
export default function BookingsPrintPage() {
  return (
    <Suspense fallback={<PrintLoading />}>
      <BookingsPrintInner />
    </Suspense>
  );
}
