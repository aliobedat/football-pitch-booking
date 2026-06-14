'use client';

import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
} from 'recharts';

// Analytics & Financials. Reachable only by owner/admin/super_admin: the
// sidebar hides it for staff and the route guard redirects a staff deep-link.
// The Go backend independently 403s staff at the finance/analytics endpoints.
const SAMPLE = [
  { day: 'السبت', revenue: 0 },
  { day: 'الأحد', revenue: 0 },
  { day: 'الاثنين', revenue: 0 },
  { day: 'الثلاثاء', revenue: 0 },
  { day: 'الأربعاء', revenue: 0 },
  { day: 'الخميس', revenue: 0 },
  { day: 'الجمعة', revenue: 0 },
];

export default function AnalyticsPage() {
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-[20px] font-bold tracking-tight">التحليلات والمالية</h1>
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
        <p className="text-[12px] text-white/40 mb-4">الإيرادات الأسبوعية (عيّنة)</p>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={SAMPLE}>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
              <XAxis dataKey="day" tick={{ fill: 'rgba(255,255,255,0.4)', fontSize: 11 }} />
              <YAxis tick={{ fill: 'rgba(255,255,255,0.4)', fontSize: 11 }} />
              <Tooltip
                contentStyle={{ background: '#141715', border: '1px solid rgba(255,255,255,0.1)' }}
              />
              <Bar dataKey="revenue" fill="#3dba8a" radius={[4, 4, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>
      <p className="text-[13px] text-white/35">
        أرقام حقيقية تُربط بالواجهة الخلفية في PR 2 (مع حارس الصلاحيات المعتمِد على قاعدة البيانات).
      </p>
    </div>
  );
}
