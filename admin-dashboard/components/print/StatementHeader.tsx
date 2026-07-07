// Statement header shared by both A4 print routes (WO-REPORTS-R4): brand,
// statement title, owner/pitch context, the reported period, and the
// generation timestamp. Light-theme only — used exclusively under
// app/(print)/reports/print/.

import { formatDate, formatTime } from '@/lib/format';

export default function StatementHeader({
  title,
  ownerName,
  pitchLabel,
  from,
  to,
}: {
  title: string;
  ownerName?: string;
  pitchLabel: string;
  from: string;
  to: string;
}) {
  const now = new Date().toISOString();
  const fullDate = (iso: string) =>
    formatDate(iso, { day: 'numeric', month: 'long', year: 'numeric' });
  return (
    <header className="mb-6 border-b-2 border-[#1a1a1a] pb-4">
      <div className="flex items-baseline justify-between gap-4">
        <div>
          <p className="text-[22px] font-bold leading-tight">مرمى</p>
          <h1 className="text-[15px] font-bold mt-1">{title}</h1>
        </div>
        <div className="text-left text-[11px] text-[#555] leading-relaxed">
          {ownerName && <p>الحساب: <span className="text-[#1a1a1a] font-semibold">{ownerName}</span></p>}
          <p>تاريخ الإصدار: <span className="text-[#1a1a1a]">{fullDate(now)} — {formatTime(now)}</span></p>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap gap-x-8 gap-y-1 text-[12px]">
        <p>الفترة: <span className="font-semibold">{fullDate(`${from}T12:00:00Z`)} – {fullDate(`${to}T12:00:00Z`)}</span></p>
        <p>النطاق: <span className="font-semibold">{pitchLabel}</span></p>
      </div>
    </header>
  );
}
