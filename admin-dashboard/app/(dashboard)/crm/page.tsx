'use client';

import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import api from '@/lib/api';

interface CRMRow {
  player_id: number;
  name: string;
  phone: string;
  visits: number;
  no_shows: number;
}

export default function CRMPage() {
  const [rows, setRows] = useState<CRMRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const { data } = await api.get<{ data: CRMRow[] }>('/owner/crm');
        setRows(data.data ?? []);
      } catch {
        setError('تعذّر تحميل قائمة اللاعبين.');
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-[20px] font-bold tracking-tight">اللاعبون (CRM)</h1>

      {loading ? (
        <div className="flex justify-center py-24"><Loader2 className="animate-spin text-white/30" /></div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>
      ) : rows.length === 0 ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center text-[13px] text-white/35">لا لاعبون بعد.</div>
      ) : (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] overflow-hidden">
          <table className="w-full text-right">
            <thead>
              <tr className="text-[11px] text-white/40 border-b border-white/[0.08]">
                <th className="font-semibold px-4 py-3">الاسم</th>
                <th className="font-semibold px-4 py-3">الهاتف</th>
                <th className="font-semibold px-4 py-3">الزيارات</th>
                <th className="font-semibold px-4 py-3">عدم الحضور</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.player_id} className="text-[12.5px] border-b border-white/[0.04] last:border-0">
                  <td className="px-4 py-3 text-white/85">{r.name || '—'}</td>
                  <td className="px-4 py-3 text-white/55 font-mono" dir="ltr">{r.phone || '—'}</td>
                  <td className="px-4 py-3 text-emerald-300 font-bold">{r.visits}</td>
                  <td className={`px-4 py-3 font-bold ${r.no_shows > 0 ? 'text-red-300' : 'text-white/40'}`}>{r.no_shows}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
