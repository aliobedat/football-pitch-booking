'use client';

// Analytics & Financials (WO2 Part 3, charts). Reachable only by owner/admin: the
// sidebar hides it for staff, the route guard redirects a staff deep-link, and the
// Go backend independently 403s staff at the analytics endpoints. Data is live,
// owner-scoped server-side, and Amman-anchored: GET /owner/analytics/timeseries.
// Revenue = confirmed SUM(total_price) (blocks contribute 0); volume = confirmed
// bookings EXCLUDING maintenance blocks; bucketed by occupancy date.

import { useEffect, useMemo, useState } from 'react';
import {
  ResponsiveContainer, BarChart, Bar, LineChart, Line,
  XAxis, YAxis, Tooltip, CartesianGrid,
} from 'recharts';
import { BarChart3, Loader2 } from 'lucide-react';
import api from '@/lib/api';
import { formatCurrency, formatNumber, formatDate } from '@/lib/format';
import FinancialsSection from '@/components/FinancialsSection';

type Granularity = 'day' | 'week' | 'month';

interface TimeBucket {
  bucket:    string; // YYYY-MM-DD (Amman)
  revenue:   number; // Expected (confirmed)
  collected: number; // Collected (paid_cash)
  volume:    number;
}

const GRANULARITY_OPTIONS: { value: Granularity; label: string }[] = [
  { value: 'day',   label: 'يومي'  },
  { value: 'week',  label: 'أسبوعي' },
  { value: 'month', label: 'شهري'  },
];

// Bucket labels arrive as ISO dates → render Arabic month/day with Latin digits.
const fmtBucket = (iso: string) => formatDate(iso, { month: 'short', day: 'numeric' });

const AXIS_TICK = { fill: 'rgba(255,255,255,0.4)', fontSize: 11 };
const TOOLTIP_STYLE = { background: '#141715', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 12, fontSize: 12 };

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

export default function AnalyticsPage() {
  const [granularity, setGranularity] = useState<Granularity>('day');
  const [series, setSeries]   = useState<TimeBucket[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    api.get('/owner/analytics/timeseries', { params: { granularity } })
      .then(res  => setSeries(res.data.data ?? []))
      .catch(()  => setError('تعذّر تحميل التحليلات. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, [granularity]);

  const hasData = useMemo(
    () => series.some(b => b.revenue > 0 || b.volume > 0),
    [series],
  );

  return (
    <div className="flex flex-col gap-6" dir="rtl">
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <h1 className="text-[20px] font-bold tracking-tight">التحليلات والمالية</h1>
        <div className="inline-flex rounded-xl border border-white/[0.09] bg-[#141715] p-1 gap-1">
          {GRANULARITY_OPTIONS.map(o => (
            <button
              key={o.value}
              type="button"
              onClick={() => setGranularity(o.value)}
              className={[
                'px-3.5 py-1.5 rounded-lg text-[12px] font-semibold transition-all',
                granularity === o.value
                  ? 'bg-emerald-500/15 text-emerald-400 border border-emerald-500/25'
                  : 'text-white/45 hover:text-white/70 border border-transparent',
              ].join(' ')}
            >
              {o.label}
            </button>
          ))}
        </div>
      </div>

      {error && (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">
          {error}
        </div>
      )}

      <ChartCard title="المتوقّع مقابل المحصّل عبر الزمن (د.أ)" loading={loading} hasData={hasData}>
        <LineChart data={series}>
          <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
          <XAxis dataKey="bucket" tick={AXIS_TICK} tickFormatter={fmtBucket} reversed />
          <YAxis tick={AXIS_TICK} tickFormatter={(v: number) => formatNumber(v)} width={48} orientation="right" />
          <Tooltip
            contentStyle={TOOLTIP_STYLE}
            labelFormatter={(l: string) => fmtBucket(l)}
            formatter={(v: number, name) => [
              `${formatCurrency(v, { minimumFractionDigits: 2 })} د.أ`,
              name === 'collected' ? 'محصّل' : 'متوقّع',
            ]}
          />
          {/* Expected = dashed/muted; Collected = solid emerald. The visible gap is leakage. */}
          <Line type="monotone" dataKey="revenue"   stroke="rgba(255,255,255,0.35)" strokeWidth={2} strokeDasharray="4 3" dot={false} />
          <Line type="monotone" dataKey="collected" stroke="#3dba8a" strokeWidth={2} dot={false} />
        </LineChart>
      </ChartCard>

      <ChartCard title="عدد الحجوزات عبر الزمن" loading={loading} hasData={hasData}>
        <BarChart data={series}>
          <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
          <XAxis dataKey="bucket" tick={AXIS_TICK} tickFormatter={fmtBucket} reversed />
          <YAxis tick={AXIS_TICK} tickFormatter={(v: number) => formatNumber(v)} width={36} orientation="right" allowDecimals={false} />
          <Tooltip
            contentStyle={TOOLTIP_STYLE}
            labelFormatter={(l: string) => fmtBucket(l)}
            formatter={(v: number) => [formatNumber(v), 'حجوزات']}
          />
          <Bar dataKey="volume" fill="#3b82f6" radius={[4, 4, 0, 0]} />
        </BarChart>
      </ChartCard>

      {/* WO-F2: Net Profit + Expense Ledger, sharing the same period granularity. */}
      <FinancialsSection granularity={granularity} />

      <p className="text-[13px] text-white/35">
        أرقام مباشرة ضمن نطاق ملاعبك فقط، بتوقيت عمّان. تُحتسب الحجوزات المؤكدة، وتُستثنى أوقات الصيانة من العدد.
      </p>
    </div>
  );
}
