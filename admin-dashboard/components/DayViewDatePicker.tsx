'use client';

// Hand-rolled RTL month-grid date picker for the Day View — deliberately NOT a
// native <input type="date"> (its LTR mm/dd/yyyy chrome fights the Arabic RTL
// page) and no date library. Pure lib/amman civil-date math. Mobile: bottom
// sheet. Desktop (md+): popover anchored under the trigger.

import { useEffect, useMemo, useState } from 'react';
import { ChevronRight, ChevronLeft, X } from 'lucide-react';
import { formatDate } from '@/lib/format';
import {
  type CivilDate, ammanCivilDate, ammanInstant, sameCivilDate, daysInMonth, ammanWeekday,
} from '@/lib/amman';

// Sunday-first columns (weekday 0-6); under dir="rtl" أحد renders right-most.
const DAY_HEADERS = ['أحد', 'إثنين', 'ثلاثاء', 'أربعاء', 'خميس', 'جمعة', 'سبت'];

export default function DayViewDatePicker({
  value,
  anchorRect,
  onSelect,
  onClose,
}: {
  value: CivilDate;
  anchorRect: DOMRect | null;
  onSelect: (d: CivilDate) => void;
  onClose: () => void;
}) {
  const today = useMemo(() => ammanCivilDate(new Date()), []);
  const [view, setView] = useState<{ y: number; m: number }>({ y: value.y, m: value.m });

  // Desktop popover vs mobile bottom sheet. Read once + track resize.
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

  const prevMonth = () => setView(v => (v.m === 1 ? { y: v.y - 1, m: 12 } : { y: v.y, m: v.m - 1 }));
  const nextMonth = () => setView(v => (v.m === 12 ? { y: v.y + 1, m: 1 } : { y: v.y, m: v.m + 1 }));

  const monthLabel = formatDate(ammanInstant({ y: view.y, m: view.m, d: 1 }, 12).toISOString(),
    { month: 'long', year: 'numeric' });
  const lead = ammanWeekday({ y: view.y, m: view.m, d: 1 }); // blank cells before day 1
  const count = daysInMonth(view.y, view.m);

  const grid = (
    <>
      {/* Month nav — right = previous, left = next (RTL). */}
      <div className="flex items-center justify-between mb-3">
        <button type="button" onClick={prevMonth} aria-label="الشهر السابق"
          className="inline-flex items-center justify-center w-11 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] text-white/55 hover:text-white hover:border-white/20 transition-all">
          <ChevronRight size={18} aria-hidden />
        </button>
        <span className="text-[14px] font-bold text-[#f0efe8]">{monthLabel}</span>
        <button type="button" onClick={nextMonth} aria-label="الشهر التالي"
          className="inline-flex items-center justify-center w-11 h-11 rounded-xl border border-white/[0.08] bg-white/[0.03] text-white/55 hover:text-white hover:border-white/20 transition-all">
          <ChevronLeft size={18} aria-hidden />
        </button>
      </div>

      {/* Weekday headers */}
      <div className="grid grid-cols-7 mb-1">
        {DAY_HEADERS.map(h => (
          <span key={h} className="text-center text-[10px] font-semibold text-white/30 py-1">{h}</span>
        ))}
      </div>

      {/* Days */}
      <div className="grid grid-cols-7 gap-1">
        {Array.from({ length: lead }).map((_, i) => <span key={`b-${i}`} />)}
        {Array.from({ length: count }).map((_, i) => {
          const d = i + 1;
          const cell: CivilDate = { y: view.y, m: view.m, d };
          const selected = sameCivilDate(cell, value);
          const isToday = sameCivilDate(cell, today);
          return (
            <button
              key={d}
              type="button"
              onClick={() => { onSelect(cell); onClose(); }}
              className={[
                'min-h-[44px] rounded-xl text-[13px] font-semibold border transition-all active:scale-[0.95]',
                selected
                  ? 'bg-emerald-500/20 border-emerald-500/50 text-emerald-200'
                  : isToday
                    ? 'bg-white/[0.04] border-white/25 text-[#f0efe8]'
                    : 'bg-transparent border-transparent text-white/70 hover:bg-white/[0.05]',
              ].join(' ')}
              aria-pressed={selected}
              aria-label={`${d} ${monthLabel}`}
            >
              {d}
            </button>
          );
        })}
      </div>
    </>
  );

  // Desktop: anchored popover (clamped to the viewport). Mobile: bottom sheet.
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
        <div style={popStyle} role="dialog" aria-modal="true" aria-label="اختر التاريخ"
          className="rounded-2xl bg-[#141715] border border-white/[0.1] shadow-2xl p-4">
          {grid}
        </div>
      ) : (
        <div role="dialog" aria-modal="true" aria-label="اختر التاريخ"
          className="absolute bottom-0 inset-x-0 rounded-t-2xl bg-[#141715] border-t border-white/[0.1] shadow-2xl p-5 pb-8">
          <div className="flex items-center justify-between mb-3">
            <span className="text-[13px] font-bold text-white/70">اختر التاريخ</span>
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
