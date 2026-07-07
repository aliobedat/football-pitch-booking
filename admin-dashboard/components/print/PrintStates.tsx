// Shared loading / error blocks for the print statement routes. Neither state
// ever auto-prints (ruling A1) — useAutoPrint only arms on resolved data.

import { Loader2 } from 'lucide-react';

export function PrintLoading() {
  return (
    <div className="flex items-center justify-center py-24 text-[#888]">
      <Loader2 size={22} className="animate-spin" aria-hidden />
      <span className="ms-3 text-[13px]">جارٍ تجهيز الكشف…</span>
    </div>
  );
}

export function PrintError({ message }: { message: string }) {
  return (
    <div className="my-10 rounded-lg border border-red-300 bg-red-50 px-4 py-3 text-[13px] text-red-700">
      {message}
    </div>
  );
}

// Parse the statement window from the print URL. Returns null when malformed —
// the page surfaces an error and never fetches or prints.
export function parseWindow(sp: URLSearchParams): { from: string; to: string; pitchId: string | null } | null {
  const from = sp.get('from') ?? '';
  const to = sp.get('to') ?? '';
  const ymdRe = /^\d{4}-\d{2}-\d{2}$/;
  if (!ymdRe.test(from) || !ymdRe.test(to)) return null;
  const pitchId = sp.get('pitch_id');
  if (pitchId !== null && !/^\d+$/.test(pitchId)) return null;
  return { from, to, pitchId };
}
