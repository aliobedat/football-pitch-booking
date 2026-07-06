'use client';

// التقارير — Financial report (WO-REPORTS-R2). Aggregates only: summary cards,
// daily breakdown chart, per-pitch table, and a month-over-month comparison
// strip. NO line items, NO transactions table (R1's bookings statement gets its
// own tab later; printing is R4).
//
// Period selector: month mode (default — always a full calendar month, enables
// the comparison via a SECOND parallel fetch of the immediately preceding
// month) or custom range mode (two DayViewDatePicker invocations; comparison
// absent entirely — no partial-month math, per the locked WO decision).
// Consumes GET /owner/reports/financial?from&to[&pitch_id] exclusively; the
// backend enforces OwnerScopeFilter and 404s a foreign pitch_id — surfaced
// here, never swallowed. Date math via lib/amman; rendering via lib/format
// (Arabic words, Latin digits); money strict 3-dp JOD (amendment C1).

import { useEffect, useMemo, useRef, useState } from 'react';
import {
  ResponsiveContainer, BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid,
} from 'recharts';
import { FileText, CalendarRange, Calendar, BarChart3, Loader2 } from 'lucide-react';
import api from '@/lib/api';
import { formatCurrency, formatNumber, formatDate } from '@/lib/format';
import {
  type CivilDate, pad, ammanCivilDate, ammanInstant, ymd, daysInMonth,
} from '@/lib/amman';
import DayViewDatePicker from '@/components/DayViewDatePicker';
import MonthPicker, { type CivilMonth } from '@/components/MonthPicker';
import ComparisonStrip, { type ReportSummary } from '@/components/ComparisonStrip';

// ── Payload types (mirror the ratified R1 financial contract) ────────────────
interface ReportDay {
  date: string; // YYYY-MM-DD (Amman)
  booking_count: number;
  gross_revenue: number;
  collected: number;
}
interface ReportPitch {
  pitch_id: number;
  pitch_name: string;
  booking_count: number;
  gross_revenue: number;
  collected: number;
}
interface FinancialReport {
  from: string;
  to: string;
  pitch_id?: number;
  pitch_name?: string;
  summary: ReportSummary;
  by_day: ReportDay[];
  by_pitch?: ReportPitch[];
}
interface OwnerPitch { id: number; name: string }

type Mode = 'month' | 'range';

// Local strict-3dp JOD wrapper (amendment C1) — lib/format untouched.
const jod3 = (v: number) => formatCurrency(v, { minimumFractionDigits: 3, maximumFractionDigits: 3 });

// Chart tokens — mirror التحليلات والمالية exactly (analytics/page.tsx).
const AXIS_TICK = { fill: 'rgba(255,255,255,0.4)', fontSize: 11 };
const TOOLTIP_STYLE = { background: '#141715', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 12, fontSize: 12 };
const fmtBucket = (iso: string) => formatDate(iso, { month: 'short', day: 'numeric' });

// The immediately preceding calendar month.
const prevMonth = (m: CivilMonth): CivilMonth =>
  m.m === 1 ? { y: m.y - 1, m: 12 } : { y: m.y, m: m.m - 1 };

// Full-month window strings for a CivilMonth.
const monthWindow = (m: CivilMonth) => ({
  from: `${m.y}-${pad(m.m)}-01`,
  to: `${m.y}-${pad(m.m)}-${pad(daysInMonth(m.y, m.m))}`,
});

// Inclusive day span of two YYYY-MM-DD strings (both parse as midnight UTC).
const spanDays = (from: string, to: string) =>
  Math.round((Date.parse(to) - Date.parse(from)) / 86_400_000) + 1;

// Arabic "month year" label, Latin digits (e.g. حزيران 2026).
const monthYearLabel = (m: CivilMonth) =>
  formatDate(ammanInstant({ y: m.y, m: m.m, d: 1 }, 12).toISOString(), { month: 'long', year: 'numeric' });

function ChartCard({
  title, loading, hasData, children,
}: {
  title: string; loading: boolean; hasData: boolean; children: React.ReactElement;
}) {
  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
      <p className="text-[12px] text-white/40 mb-4">{title}</p>
      <div className="h-64">
        {loading ? (
          <div className="h-full flex items-center justify-center">
            <Loader2 size={22} className="text-emerald-500 animate-spin" aria-hidden />
          </div>
        ) : !hasData ? (
          <div className="h-full flex flex-col items-center justify-center gap-3 text-center">
            <div className="w-14 h-14 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
              <BarChart3 size={22} className="text-white/15" aria-hidden />
            </div>
            <p className="text-[13px] text-white/30">لا توجد بيانات في هذه الفترة</p>
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">{children}</ResponsiveContainer>
        )}
      </div>
    </div>
  );
}

function SummaryCard({ label, value, unit, tone }: {
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

export default function ReportsPage() {
  const todayAmman = useMemo(() => ammanCivilDate(new Date()), []);

  const [mode, setMode] = useState<Mode>('month');
  const [month, setMonth] = useState<CivilMonth>({ y: todayAmman.y, m: todayAmman.m });
  const [rangeFrom, setRangeFrom] = useState<CivilDate>({ ...todayAmman, d: 1 });
  const [rangeTo, setRangeTo] = useState<CivilDate>(todayAmman);
  const [pitchId, setPitchId] = useState<number | ''>('');

  const [pitches, setPitches] = useState<OwnerPitch[]>([]);
  const [data, setData] = useState<FinancialReport | null>(null);
  const [prior, setPrior] = useState<FinancialReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Picker overlays + their desktop anchors.
  const [openPicker, setOpenPicker] = useState<null | 'month' | 'from' | 'to'>(null);
  const [anchorRect, setAnchorRect] = useState<DOMRect | null>(null);
  const monthBtnRef = useRef<HTMLButtonElement>(null);
  const fromBtnRef = useRef<HTMLButtonElement>(null);
  const toBtnRef = useRef<HTMLButtonElement>(null);

  // ── Selected window. Month mode is a full calendar month by construction —
  //    the ONLY condition under which the comparison renders. ────────────────
  const window_ = useMemo(() => {
    if (mode === 'month') return { ...monthWindow(month), fullMonth: true };
    return { from: ymd(rangeFrom), to: ymd(rangeTo), fullMonth: false };
  }, [mode, month, rangeFrom, rangeTo]);

  // Client-side pre-validation mirroring R1's guards (the server still enforces).
  const rangeIssue = useMemo(() => {
    if (spanDays(window_.from, window_.to) < 1) return 'نهاية الفترة يجب أن تكون بعد بدايتها';
    if (spanDays(window_.from, window_.to) > 92) return 'الفترة القصوى للتقرير 92 يوماً';
    return null;
  }, [window_]);

  // ── Pitch list (once) — the optional filter's options. ────────────────────
  useEffect(() => {
    api.get('/owner/pitches')
      .then(res => setPitches((res.data.data ?? []) as OwnerPitch[]))
      .catch(() => { /* filter stays "all pitches"; the report itself still loads */ });
  }, []);

  // ── Report fetch: one request for the period; a SECOND PARALLEL request for
  //    the preceding month only in full-month mode. Prior failure degrades to
  //    "no comparison" — never blocks the report. ─────────────────────────────
  useEffect(() => {
    if (rangeIssue) return;
    setLoading(true);
    setError(null);
    setPrior(null);

    const params: Record<string, string | number> = { from: window_.from, to: window_.to };
    if (pitchId !== '') params.pitch_id = pitchId;

    api.get('/owner/reports/financial', { params })
      .then(res => setData(res.data.data as FinancialReport))
      .catch(err => {
        setData(null);
        const status = err?.response?.status;
        const serverMsg = err?.response?.data?.message;
        // 404 = out-of-scope/unknown pitch — surface the server's message.
        setError(status === 404
          ? (serverMsg ?? 'الملعب غير موجود أو لا تملك صلاحية عرضه')
          : status === 400
            ? (serverMsg ?? 'طلب غير صالح')
            : 'تعذّر تحميل التقرير. تأكد من صلاحيات الحساب.');
      })
      .finally(() => setLoading(false));

    if (window_.fullMonth) {
      const pm = prevMonth(month);
      const pw = monthWindow(pm);
      const pParams: Record<string, string | number> = { from: pw.from, to: pw.to };
      if (pitchId !== '') pParams.pitch_id = pitchId;
      api.get('/owner/reports/financial', { params: pParams })
        .then(res => setPrior(res.data.data as FinancialReport))
        .catch(() => setPrior(null)); // comparison silently absent; report unaffected
    }
    // month is only read when fullMonth (derived from it) — safe.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [window_.from, window_.to, window_.fullMonth, pitchId, rangeIssue]);

  const hasChartData = useMemo(
    () => (data?.by_day ?? []).some(d => d.gross_revenue > 0 || d.booking_count > 0),
    [data],
  );

  const openAnchored = (which: 'month' | 'from' | 'to', ref: React.RefObject<HTMLButtonElement | null>) => {
    setAnchorRect(ref.current?.getBoundingClientRect() ?? null);
    setOpenPicker(which);
  };

  const periodLabel = mode === 'month'
    ? monthYearLabel(month)
    : `${formatDate(ammanInstant(rangeFrom, 12).toISOString(), { day: 'numeric', month: 'short' })} – ${formatDate(ammanInstant(rangeTo, 12).toISOString(), { day: 'numeric', month: 'short', year: 'numeric' })}`;

  return (
    <div className="flex flex-col gap-6" dir="rtl">
      {/* ── Header ── */}
      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-xl bg-emerald-500/[0.08] border border-emerald-500/20 flex items-center justify-center">
          <FileText size={18} className="text-emerald-400" aria-hidden />
        </div>
        <div>
          <h1 className="text-[20px] font-bold tracking-tight">التقارير</h1>
          <p className="text-[12px] text-white/35">الملخّص المالي — {periodLabel}</p>
        </div>
      </div>

      {/* ── Period selector + pitch filter ── */}
      <div className="flex items-center gap-3 flex-wrap">
        {/* Mode toggle */}
        <div className="inline-flex rounded-xl border border-white/[0.09] bg-[#141715] p-1 gap-1">
          {([['month', 'شهر'], ['range', 'فترة مخصصة']] as [Mode, string][]).map(([m, label]) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              aria-pressed={mode === m}
              className={[
                'px-3.5 py-1.5 rounded-lg text-[12px] font-semibold transition-all min-h-[36px]',
                mode === m
                  ? 'bg-emerald-500/15 text-emerald-400 border border-emerald-500/25'
                  : 'text-white/45 hover:text-white/70 border border-transparent',
              ].join(' ')}
            >
              {label}
            </button>
          ))}
        </div>

        {mode === 'month' ? (
          <button
            ref={monthBtnRef}
            type="button"
            onClick={() => openAnchored('month', monthBtnRef)}
            className="inline-flex items-center gap-2 min-h-[44px] px-4 rounded-xl border border-white/[0.09] bg-[#141715] text-[12.5px] font-semibold text-[#f0efe8] hover:border-white/20 transition-all"
          >
            <Calendar size={14} className="text-emerald-400" aria-hidden />
            {monthYearLabel(month)}
          </button>
        ) : (
          <>
            <button
              ref={fromBtnRef}
              type="button"
              onClick={() => openAnchored('from', fromBtnRef)}
              className="inline-flex items-center gap-2 min-h-[44px] px-4 rounded-xl border border-white/[0.09] bg-[#141715] text-[12.5px] font-semibold text-[#f0efe8] hover:border-white/20 transition-all"
            >
              <CalendarRange size={14} className="text-emerald-400" aria-hidden />
              من: {formatDate(ammanInstant(rangeFrom, 12).toISOString(), { day: 'numeric', month: 'short', year: 'numeric' })}
            </button>
            <button
              ref={toBtnRef}
              type="button"
              onClick={() => openAnchored('to', toBtnRef)}
              className="inline-flex items-center gap-2 min-h-[44px] px-4 rounded-xl border border-white/[0.09] bg-[#141715] text-[12.5px] font-semibold text-[#f0efe8] hover:border-white/20 transition-all"
            >
              <CalendarRange size={14} className="text-emerald-400" aria-hidden />
              إلى: {formatDate(ammanInstant(rangeTo, 12).toISOString(), { day: 'numeric', month: 'short', year: 'numeric' })}
            </button>
          </>
        )}

        {/* Optional pitch filter — passthrough only; the server owns scoping/404. */}
        <select
          value={pitchId}
          onChange={e => setPitchId(e.target.value === '' ? '' : Number(e.target.value))}
          aria-label="تصفية حسب الملعب"
          className="min-h-[44px] bg-[#141715] border border-white/[0.09] rounded-xl px-4 text-[12.5px] font-semibold text-[#f0efe8] [color-scheme:dark] focus:outline-none focus:border-emerald-500/50 transition-all"
        >
          <option value="">كل الملاعب</option>
          {pitches.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
        </select>
      </div>

      {(rangeIssue || error) && (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">
          {rangeIssue ?? error}
        </div>
      )}

      {/* ── Summary cards ── */}
      {data && !rangeIssue && (
        <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
          <SummaryCard label="إجمالي الإيرادات" value={jod3(data.summary.gross_revenue)} unit="د.أ" />
          <SummaryCard label="المحصّل" value={jod3(data.summary.collected)} unit="د.أ" tone="green" />
          <SummaryCard label="غير المحصّل" value={jod3(data.summary.outstanding)} unit="د.أ" tone="amber" />
          <SummaryCard label="الحجوزات" value={formatNumber(data.summary.booking_count)} />
          <SummaryCard label="الملغاة" value={formatNumber(data.summary.cancelled_count)} tone={data.summary.cancelled_count > 0 ? 'red' : undefined} />
        </div>
      )}

      {/* ── MoM comparison — mounted ONLY for a full calendar month with a loaded
             prior; custom ranges never mount it (locked WO decision). ── */}
      {window_.fullMonth && data && prior && !rangeIssue && (
        <ComparisonStrip
          current={data.summary}
          prior={prior.summary}
          priorLabel={monthYearLabel(prevMonth(month))}
        />
      )}

      {/* ── Daily breakdown ── */}
      {!rangeIssue && (
        <ChartCard title="الإيرادات اليومية — الإجمالي مقابل المحصّل (د.أ)" loading={loading} hasData={hasChartData}>
          <BarChart data={data?.by_day ?? []}>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
            <XAxis dataKey="date" tick={AXIS_TICK} tickFormatter={fmtBucket} reversed />
            <YAxis tick={AXIS_TICK} tickFormatter={(v: number) => formatNumber(v)} width={48} orientation="right" />
            <Tooltip
              contentStyle={TOOLTIP_STYLE}
              labelFormatter={(l: string) => fmtBucket(l)}
              formatter={(v: number, name) => [
                `${jod3(v)} د.أ`,
                name === 'collected' ? 'محصّل' : 'إجمالي',
              ]}
            />
            <Bar dataKey="gross_revenue" fill="rgba(255,255,255,0.25)" radius={[4, 4, 0, 0]} />
            <Bar dataKey="collected" fill="#3dba8a" radius={[4, 4, 0, 0]} />
          </BarChart>
        </ChartCard>
      )}

      {/* ── Per-pitch table — present only on the unfiltered report ── */}
      {data?.by_pitch && !rangeIssue && (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
          <p className="text-[12px] text-white/40 mb-4">حسب الملعب</p>
          {data.by_pitch.length === 0 ? (
            <p className="text-[13px] text-white/30 py-6 text-center">لا توجد بيانات في هذه الفترة</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-[12.5px]">
                <thead>
                  <tr className="text-white/40 text-[11px]">
                    <th className="text-right font-semibold pb-3 pe-4">الملعب</th>
                    <th className="text-right font-semibold pb-3 pe-4">الحجوزات</th>
                    <th className="text-right font-semibold pb-3 pe-4">الإيراد (د.أ)</th>
                    <th className="text-right font-semibold pb-3">المحصّل (د.أ)</th>
                  </tr>
                </thead>
                <tbody>
                  {data.by_pitch.map(p => (
                    <tr key={p.pitch_id} className="border-t border-white/[0.05] text-[#f0efe8]">
                      <td className="py-3 pe-4 font-semibold">{p.pitch_name}</td>
                      <td className="py-3 pe-4 tabular-nums">{formatNumber(p.booking_count)}</td>
                      <td className="py-3 pe-4 tabular-nums">{jod3(p.gross_revenue)}</td>
                      <td className="py-3 tabular-nums text-emerald-300">{jod3(p.collected)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <p className="text-[13px] text-white/35">
        أرقام مباشرة ضمن نطاق ملاعبك فقط، بتوقيت عمّان. الإيراد يشمل الحجوزات المؤكدة، وتُستثنى أوقات الصيانة من العدد.
      </p>

      {/* ── Pickers ── */}
      {openPicker === 'month' && (
        <MonthPicker
          value={month}
          anchorRect={anchorRect}
          onSelect={setMonth}
          onClose={() => setOpenPicker(null)}
        />
      )}
      {openPicker === 'from' && (
        <DayViewDatePicker
          value={rangeFrom}
          anchorRect={anchorRect}
          onSelect={setRangeFrom}
          onClose={() => setOpenPicker(null)}
        />
      )}
      {openPicker === 'to' && (
        <DayViewDatePicker
          value={rangeTo}
          anchorRect={anchorRect}
          onSelect={setRangeTo}
          onClose={() => setOpenPicker(null)}
        />
      )}
    </div>
  );
}
