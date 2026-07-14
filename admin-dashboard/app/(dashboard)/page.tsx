'use client';

// Owner overview — headline KPI tiles (WO2 Part 3). Replaces the static dashes
// with live, owner-scoped, Amman-anchored figures from GET /owner/analytics/kpis.
// Revenue counts CONFIRMED bookings only (blocks are unpriced → 0); counts EXCLUDE
// maintenance blocks. Scoping is enforced server-side via the session actor — this
// page never filters tenancy client-side.

import { useEffect, useState } from 'react';
import { CalendarCheck2, Wallet, TrendingUp, TrendingDown, CalendarClock, Banknote } from 'lucide-react';
import api from '@/lib/api';
import { jod3, formatNumber } from '@/lib/format';

interface KPIs {
  today_revenue:          number;
  today_confirmed_count:  number;
  week_to_date_revenue:   number;
  upcoming_bookings:      number;
  today_collected:        number;
  week_to_date_collected: number;
}

type TileKind = 'currency' | 'count';

function KpiTile({
  icon: Icon, label, value, kind, loading, accent,
}: {
  icon: React.ElementType;
  label: string;
  value: number;
  kind: TileKind;
  loading: boolean;
  accent: string;
}) {
  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5 flex flex-col gap-3">
      <div className={`w-9 h-9 rounded-xl flex items-center justify-center ${accent}`}>
        <Icon size={16} aria-hidden />
      </div>
      <div className="flex flex-col gap-1">
        {loading ? (
          <span className="h-[30px] w-20 rounded-md bg-white/[0.06] animate-pulse" />
        ) : (
          <span className="text-[28px] font-bold tracking-tight leading-none text-[#f0efe8]">
            {kind === 'currency' ? (
              <>
                {jod3(value)}
                <span className="text-[12px] text-emerald-500/80 font-semibold ms-1.5">د.أ</span>
              </>
            ) : (
              formatNumber(value)
            )}
          </span>
        )}
        <span className="text-[12px] text-white/40">{label}</span>
      </div>
    </div>
  );
}

export default function OverviewPage() {
  const [kpis, setKpis]       = useState<KPIs | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState<string | null>(null);

  useEffect(() => {
    api.get('/owner/analytics/kpis')
      .then(res  => setKpis(res.data.data as KPIs))
      .catch(()  => setError('تعذّر تحميل المؤشرات. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, []);

  // Zero/empty is a valid state (a new owner with no bookings) — render zeros, not
  // an error. Only a failed request surfaces the error banner.
  const k: KPIs = kpis ?? {
    today_revenue: 0, today_confirmed_count: 0, week_to_date_revenue: 0, upcoming_bookings: 0,
    today_collected: 0, week_to_date_collected: 0,
  };
  const todayLeak = Math.max(0, k.today_revenue - k.today_collected);

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-[20px] font-bold tracking-tight">نظرة عامة</h1>

      {error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">
          {error}
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
            <KpiTile icon={CalendarCheck2} label="حجوزات اليوم المؤكدة" value={k.today_confirmed_count} kind="count"    loading={loading} accent="bg-emerald-500/10 text-emerald-400" />
            <KpiTile icon={Wallet}         label="متوقّع اليوم"          value={k.today_revenue}         kind="currency" loading={loading} accent="bg-white/[0.06] text-white/60" />
            <KpiTile icon={Banknote}       label="محصّل اليوم (نقداً)"   value={k.today_collected}       kind="currency" loading={loading} accent="bg-emerald-500/10 text-emerald-400" />
            <KpiTile icon={CalendarClock}  label="الحجوزات القادمة"     value={k.upcoming_bookings}     kind="count"    loading={loading} accent="bg-white/[0.06] text-white/60" />
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
            <KpiTile icon={TrendingUp}     label="متوقّع الأسبوع"        value={k.week_to_date_revenue}  kind="currency" loading={loading} accent="bg-white/[0.06] text-white/60" />
            <KpiTile icon={Banknote}       label="محصّل الأسبوع (نقداً)" value={k.week_to_date_collected} kind="currency" loading={loading} accent="bg-emerald-500/10 text-emerald-400" />
            <KpiTile icon={TrendingDown} label="فجوة التحصيل اليوم"  value={todayLeak}             kind="currency" loading={loading} accent={todayLeak > 0 ? 'bg-amber-500/10 text-amber-400' : 'bg-white/[0.06] text-white/60'} />
          </div>
        </div>
      )}

      <p className="text-[13px] text-white/35">
        أرقام مباشرة ضمن نطاق ملاعبك فقط، بتوقيت عمّان. «المتوقّع» يحتسب الحجوزات المؤكدة، و«المحصّل» يحتسب ما تم تحصيله نقداً. الفجوة بينهما هي ما لم يُحصّل بعد.
      </p>
    </div>
  );
}
