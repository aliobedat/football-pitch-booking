'use client';

// Printable A4 financial statement (WO-REPORTS-R4). Reads from&to[&pitch_id]
// from the URL, fetches GET /owner/reports/financial ONCE and renders the
// statement: header, summary block, by_day table, by_pitch table. The MoM
// comparison strip is structurally absent — this route never fetches the prior
// month (locked G0 decision), unlike the on-screen /reports financial tab.
// window.print() auto-fires only on fully-resolved success (ruling A1); error
// and loading states never print. A zeroed empty period prints normally.
// Staff never reach this route (FINANCE_ROUTES prefix match in proxy.ts) and
// the backend 403s them regardless — the error banner renders, nothing prints.

import { Suspense, useEffect, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import api from '@/lib/api';
import { formatCurrency, formatNumber, formatDate } from '@/lib/format';
import { useAuth } from '@/context/AuthContext';
import StatementHeader from '@/components/print/StatementHeader';
import { PrintLoading, PrintError, parseWindow } from '@/components/print/PrintStates';
import useAutoPrint from '@/components/print/useAutoPrint';

// Payload types mirror the ratified R1 financial contract (reports/page.tsx).
interface ReportSummary {
  gross_revenue: number;
  collected: number;
  outstanding: number;
  booking_count: number;
  cancelled_count: number;
}
interface ReportDay { date: string; booking_count: number; gross_revenue: number; collected: number }
interface ReportPitch { pitch_id: number; pitch_name: string; booking_count: number; gross_revenue: number; collected: number }
interface FinancialReport {
  from: string;
  to: string;
  pitch_id?: number;
  pitch_name?: string;
  summary: ReportSummary;
  by_day: ReportDay[];
  by_pitch?: ReportPitch[];
}

// Local strict-3dp JOD wrapper — 3rd copy by ruling B1; consolidation tracked
// in docs/followups/jod3-consolidation.md.
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });

const TH = 'text-right font-bold text-[10.5px] text-[#555] border-b border-[#1a1a1a] pb-1.5 pe-3';
const TD = 'py-1.5 pe-3 border-b border-[#ddd] tabular-nums';

function FinancialPrintInner() {
  const { user } = useAuth();
  const searchParams = useSearchParams();
  const win = parseWindow(new URLSearchParams(searchParams.toString()));

  const [data, setData] = useState<FinancialReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!win) return;
    let stale = false;
    const params: Record<string, string> = { from: win.from, to: win.to };
    if (win.pitchId !== null) params.pitch_id = win.pitchId;
    api.get('/owner/reports/financial', { params })
      .then(res => { if (!stale) setData(res.data.data as FinancialReport); })
      .catch(err => {
        if (stale) return;
        const status = err?.response?.status;
        const serverMsg = err?.response?.data?.message;
        setError(status === 404
          ? (serverMsg ?? 'الملعب غير موجود أو لا تملك صلاحية عرضه')
          : status === 400
            ? (serverMsg ?? 'طلب غير صالح')
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
        title="كشف مالي"
        ownerName={user?.full_name}
        pitchLabel={data.pitch_name ?? 'كل الملاعب'}
        from={data.from}
        to={data.to}
      />

      {/* ── Summary block ── */}
      <section className="mb-6">
        <h2 className="text-[13px] font-bold mb-2">الملخّص</h2>
        <div className="grid grid-cols-5 border border-[#1a1a1a]">
          {([
            ['إجمالي الإيرادات (د.أ)', jod3(data.summary.gross_revenue)],
            ['المحصّل (د.أ)', jod3(data.summary.collected)],
            ['غير المحصّل (د.أ)', jod3(data.summary.outstanding)],
            ['الحجوزات', formatNumber(data.summary.booking_count)],
            ['الملغاة', formatNumber(data.summary.cancelled_count)],
          ] as [string, string][]).map(([label, value], i) => (
            <div key={label} className={`px-3 py-2 ${i > 0 ? 'border-e border-[#ccc]' : ''}`}>
              <p className="text-[10px] text-[#555]">{label}</p>
              <p className="text-[14px] font-bold tabular-nums mt-0.5">{value}</p>
            </div>
          ))}
        </div>
      </section>

      {/* ── Daily breakdown ── */}
      <section className="mb-6">
        <h2 className="text-[13px] font-bold mb-2">التفصيل اليومي</h2>
        {data.by_day.length === 0 ? (
          <p className="text-[12px] text-[#777]">لا توجد بيانات في هذه الفترة.</p>
        ) : (
          <table className="text-[11.5px]">
            <thead>
              <tr>
                <th className={TH}>التاريخ</th>
                <th className={TH}>الحجوزات</th>
                <th className={TH}>الإيراد (د.أ)</th>
                <th className={`${TH} pe-0`}>المحصّل (د.أ)</th>
              </tr>
            </thead>
            <tbody>
              {data.by_day.map(d => (
                <tr key={d.date}>
                  <td className={TD}>{formatDate(d.date, { weekday: 'short', day: 'numeric', month: 'long' })}</td>
                  <td className={TD}>{formatNumber(d.booking_count)}</td>
                  <td className={TD}>{jod3(d.gross_revenue)}</td>
                  <td className={`${TD} pe-0`}>{jod3(d.collected)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {/* ── Per-pitch — present only on the unfiltered report (R1 contract) ── */}
      {data.by_pitch && (
        <section className="mb-6">
          <h2 className="text-[13px] font-bold mb-2">حسب الملعب</h2>
          {data.by_pitch.length === 0 ? (
            <p className="text-[12px] text-[#777]">لا توجد بيانات في هذه الفترة.</p>
          ) : (
            <table className="text-[11.5px]">
              <thead>
                <tr>
                  <th className={TH}>الملعب</th>
                  <th className={TH}>الحجوزات</th>
                  <th className={TH}>الإيراد (د.أ)</th>
                  <th className={`${TH} pe-0`}>المحصّل (د.أ)</th>
                </tr>
              </thead>
              <tbody>
                {data.by_pitch.map(p => (
                  <tr key={p.pitch_id}>
                    <td className={`${TD} font-semibold`}>{p.pitch_name}</td>
                    <td className={TD}>{formatNumber(p.booking_count)}</td>
                    <td className={TD}>{jod3(p.gross_revenue)}</td>
                    <td className={`${TD} pe-0`}>{jod3(p.collected)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}

      <p className="text-[10.5px] text-[#777]">
        أرقام ضمن نطاق ملاعبك فقط، بتوقيت عمّان. الإيراد يشمل الحجوزات المؤكدة، وتُستثنى أوقات الصيانة من العدد.
      </p>
    </div>
  );
}

// useSearchParams requires a Suspense boundary at build (reports/page.tsx precedent).
export default function FinancialPrintPage() {
  return (
    <Suspense fallback={<PrintLoading />}>
      <FinancialPrintInner />
    </Suspense>
  );
}
