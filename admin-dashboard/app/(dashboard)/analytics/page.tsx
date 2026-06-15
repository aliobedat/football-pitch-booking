'use client';

import { useEffect, useState } from 'react';
import {
  ResponsiveContainer, BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid,
} from 'recharts';
import { Loader2 } from 'lucide-react';
import api from '@/lib/api';

interface Totals { revenue: number; bookings: number; no_shows: number; no_show_rate: number }
interface Overview {
  from: string; to: string;
  revenue_by_day: { date: string; revenue: number }[];
  revenue_by_month: { month: string; revenue: number }[];
  heatmap: { weekday: number; hour: number; count: number }[];
  current: Totals; previous: Totals;
}

const WD = ['الأحد', 'الإثنين', 'الثلاثاء', 'الأربعاء', 'الخميس', 'الجمعة', 'السبت'];
const HOURS = Array.from({ length: 24 }, (_, h) => h);

function delta(cur: number, prev: number): string {
  if (prev === 0) return cur > 0 ? '+∞' : '—';
  const p = Math.round(((cur - prev) / prev) * 100);
  return `${p >= 0 ? '+' : ''}${p}%`;
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5 flex flex-col gap-1">
      <span className="text-[12px] text-white/40">{label}</span>
      <span className="text-[22px] font-bold">{value}</span>
      {sub && <span className="text-[11px] text-white/35">{sub}</span>}
    </div>
  );
}

export default function AnalyticsPage() {
  const [d, setD] = useState<Overview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const { data } = await api.get<{ data: Overview }>('/owner/analytics/overview');
        setD(data.data);
      } catch {
        setError('تعذّر تحميل التحليلات.');
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  if (loading) return <div className="flex justify-center py-24"><Loader2 className="animate-spin text-white/30" /></div>;
  if (error || !d) return <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>;

  const maxHeat = Math.max(1, ...d.heatmap.map((c) => c.count));
  const heatAt = (wd: number, h: number) => d.heatmap.find((c) => c.weekday === wd && c.hour === h)?.count ?? 0;

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-[20px] font-bold tracking-tight">التحليلات والمالية</h1>

      {/* Period comparison */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-5">
        <Stat label="الإيرادات (الفترة الحالية)" value={`${d.current.revenue} د.أ`} sub={`مقارنة بالسابقة ${delta(d.current.revenue, d.previous.revenue)}`} />
        <Stat label="الحجوزات المحققة" value={`${d.current.bookings}`} sub={`السابقة ${d.previous.bookings}`} />
        <Stat label="نسبة عدم الحضور" value={`${Math.round(d.current.no_show_rate * 100)}%`} sub={`${d.current.no_shows} حالة عدم حضور`} />
      </div>

      {/* Revenue by day */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
        <p className="text-[12px] text-white/40 mb-4">الإيرادات المحققة يومياً (تستثني عدم الحضور)</p>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={d.revenue_by_day}>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
              <XAxis dataKey="date" tick={{ fill: 'rgba(255,255,255,0.4)', fontSize: 10 }} />
              <YAxis tick={{ fill: 'rgba(255,255,255,0.4)', fontSize: 11 }} />
              <Tooltip contentStyle={{ background: '#141715', border: '1px solid rgba(255,255,255,0.1)' }} />
              <Bar dataKey="revenue" fill="#3dba8a" radius={[4, 4, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Peak-hours heatmap */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5 overflow-x-auto">
        <p className="text-[12px] text-white/40 mb-4">ساعات الذروة (يوم × ساعة)</p>
        <div className="min-w-[640px]">
          <div className="grid" style={{ gridTemplateColumns: `64px repeat(24, 1fr)` }}>
            <div />
            {HOURS.map((h) => <div key={h} className="text-[9px] text-white/30 text-center">{h}</div>)}
            {WD.map((name, wd) => (
              <div key={wd} className="contents">
                <div className="text-[10px] text-white/45 py-1 pe-2 text-left">{name}</div>
                {HOURS.map((h) => {
                  const v = heatAt(wd, h);
                  const a = v === 0 ? 0 : 0.15 + 0.85 * (v / maxHeat);
                  return <div key={h} title={`${v}`} className="m-[1px] rounded-sm h-4" style={{ background: `rgba(61,186,138,${a})` }} />;
                })}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
