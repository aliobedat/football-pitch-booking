'use client';

// Hand-rolled RTL month picker for the Reports period selector (WO-REPORTS-R2
// amendment A1): a 12-month grid + year nav — the month-granularity sibling of
// DayViewDatePicker, mirroring its shell exactly (mobile bottom sheet, desktop
// anchored popover, body scroll lock). All date math via lib/amman; month/year
// labels via lib/format (Arabic words, Latin digits). No date library.

import { useEffect, useMemo, useState } from 'react';
import { ChevronRight, ChevronLeft, X } from 'lucide-react';
import { formatDate, formatNumber } from '@/lib/format';
import { ammanCivilDate, ammanInstant } from '@/lib/amman';

export interface CivilMonth {
  y: number;
  m: number; // 1-12
}

// Arabic month name of month m (rendered from Intl, never hand-listed) — anchored
// at Amman noon of day 1 so the label can't drift across the month boundary.
const monthLabel = (y: number, m: number) =>
  formatDate(ammanInstant({ y, m, d: 1 }, 12).toISOString(), { month: 'long' });

export default function MonthPicker({
  value,
  anchorRect,
  onSelect,
  onClose,
}: {
  value: CivilMonth;
  anchorRect: DOMRect | null;
  onSelect: (m: CivilMonth) => void;
  onClose: () => void;
}) {
  const today = useMemo(() => ammanCivilDate(new Date()), []);
  const [year, setYear] = useState(value.y);

  // Desktop popover vs mobile bottom sheet (same breakpoint as DayViewDatePicker).
  const [isDesktop, setIsDesktop] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 768px)');
    const sync = () => setIsDesktop(mq.matches);
    sync();
    mq.addEventListener('change', sync);
    return () => mq.removeEventListener('change', sync);
  }, []);

  // Lock body scroll while open.
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => { document.body.style.overflow = prev; };
  }, []);

  const grid = (
    <>
      {/* Year nav — right = previous, left = next (RTL). */}
      <div className="flex items-center justify-between mb-3">
        <button type="button" onClick={() => setYear(y => y - 1)} aria-label="السنة السابقة"
          className="inline-flex items-center justify-center w-11 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] text-white/55 hover:text-white hover:border-white/20 transition-all">
          <ChevronRight size={18} aria-hidden />
        </button>
        <span className="text-[14px] font-bold text-[#f0efe8]">{formatNumber(year, { useGrouping: false })}</span>
        <button type="button" onClick={() => setYear(y => y + 1)} aria-label="السنة التالية"
          className="inline-flex items-center justify-center w-11 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] text-white/55 hover:text-white hover:border-white/20 transition-all">
          <ChevronLeft size={18} aria-hidden />
        </button>
      </div>

      {/* 12 months, 3 per row */}
      <div className="grid grid-cols-3 gap-1.5">
        {Array.from({ length: 12 }).map((_, i) => {
          const m = i + 1;
          const selected = value.y === year && value.m === m;
          const isCurrent = today.y === year && today.m === m;
          return (
            <button
              key={m}
              type="button"
              onClick={() => { onSelect({ y: year, m }); onClose(); }}
              aria-pressed={selected}
              className={[
                'min-h-[44px] rounded-xl text-[13px] font-semibold border transition-all active:scale-[0.95]',
                selected
                  ? 'bg-emerald-500/20 border-emerald-500/50 text-emerald-200'
                  : isCurrent
                    ? 'bg-white/[0.04] border-white/25 text-[#f0efe8]'
                    : 'bg-transparent border-transparent text-white/70 hover:bg-white/[0.05]',
              ].join(' ')}
            >
              {monthLabel(year, m)}
            </button>
          );
        })}
      </div>
    </>
  );

  // Desktop: anchored popover (viewport-clamped). Mobile: bottom sheet.
  const popStyle: React.CSSProperties | undefined = isDesktop && anchorRect
    ? {
        position: 'fixed',
        top: Math.round(anchorRect.bottom + 8),
        left: Math.round(Math.min(Math.max(8, anchorRect.left + anchorRect.width / 2 - 160), (typeof window !== 'undefined' ? window.innerWidth : 360) - 328)),
        width: 320,
      }
    : undefined;

  return (
    <div className="fixed inset-0 z-[60]" dir="rtl">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} aria-hidden />
      {isDesktop ? (
        <div style={popStyle} role="dialog" aria-modal="true" aria-label="اختر الشهر"
          className="rounded-2xl bg-[#141715] border border-white/[0.1] shadow-2xl p-4">
          {grid}
        </div>
      ) : (
        <div role="dialog" aria-modal="true" aria-label="اختر الشهر"
          className="absolute bottom-0 inset-x-0 rounded-t-2xl bg-[#141715] border-t border-white/[0.1] shadow-2xl p-5 pb-8">
          <div className="flex items-center justify-between mb-3">
            <span className="text-[13px] font-bold text-white/70">اختر الشهر</span>
            <button type="button" onClick={onClose} aria-label="إغلاق" className="text-white/40 hover:text-white/80">
              <X size={18} aria-hidden />
            </button>
          </div>
          {grid}
        </div>
      )}
    </div>
  );
}
