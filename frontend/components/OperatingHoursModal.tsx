'use client';

import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { Clock, Plus, Trash2, Copy, X, AlertTriangle, Info } from 'lucide-react';
import api from '@/lib/api';

// ─────────────────────────────────────────────────────────────────────────────
// Operating Hours editor — owner weekly open-window schedule
//
// Submits the FULL grid as a single PUT /pitches/:id/operating-hours (the server
// wholesale-replaces). Weekday convention 0=Sunday … 6=Saturday, matching the
// backend. A day with no windows is CLOSED. A window whose close ≤ open crosses
// midnight (rendered "16:00 – 02:00 +1"). Overlap validation — including
// cross-midnight spillover and the Sat→Sun wrap — mirrors the server validator so
// the owner sees the error before submit; the server is still the final referee.
// ─────────────────────────────────────────────────────────────────────────────

// Levantine Arabic weekday names, indexed 0=Sun … 6=Sat.
const AR_DAYS = ['الأحد', 'الإثنين', 'الثلاثاء', 'الأربعاء', 'الخميس', 'الجمعة', 'السبت'];
const WEEK_MINUTES = 7 * 24 * 60;
const DEFAULT_WINDOW = { open: '09:00', close: '17:00' };

interface Window {
  open:  string; // "HH:MM"
  close: string; // "HH:MM"
}

// One window per weekday-row; the grid is 7 arrays of windows.
type Grid = Window[][];

interface ServerWindow {
  weekday:    number;
  open_time:  string;
  close_time: string;
}

function emptyGrid(): Grid {
  return Array.from({ length: 7 }, () => []);
}

function hhmmToMin(s: string): number {
  const [h, m] = s.split(':').map(Number);
  return h * 60 + m;
}

// Project a window onto the circular week-minute line. A cross-midnight window
// (close ≤ open) extends its end by a day; comparison handles the Sat→Sun wrap.
function toInterval(weekday: number, w: Window): { start: number; end: number } {
  const o = hhmmToMin(w.open);
  const c = hhmmToMin(w.close);
  let end = weekday * 1440 + c;
  if (c <= o) end += 1440;
  return { start: weekday * 1440 + o, end };
}

function intervalsOverlap(a: { start: number; end: number }, b: { start: number; end: number }): boolean {
  for (const shift of [-WEEK_MINUTES, 0, WEEK_MINUTES]) {
    if (a.start < b.end + shift && b.start + shift < a.end) return true;
  }
  return false;
}

function crossesMidnight(w: Window): boolean {
  return hhmmToMin(w.close) <= hhmmToMin(w.open);
}

// validateGrid mirrors the server's ValidateSchedule. Returns an Arabic error
// string for the first problem found, or null when the whole week is valid.
function validateGrid(grid: Grid): string | null {
  const flat: { weekday: number; w: Window }[] = [];
  for (let d = 0; d < 7; d++) {
    for (const w of grid[d]) {
      if (hhmmToMin(w.open) === hhmmToMin(w.close)) {
        return `يوم ${AR_DAYS[d]}: وقت البداية والنهاية متطابقان`;
      }
      flat.push({ weekday: d, w });
    }
  }
  const intervals = flat.map(f => ({ ...toInterval(f.weekday, f.w), day: f.weekday }));
  for (let i = 0; i < intervals.length; i++) {
    for (let j = i + 1; j < intervals.length; j++) {
      if (intervalsOverlap(intervals[i], intervals[j])) {
        const d1 = AR_DAYS[intervals[i].day];
        const d2 = AR_DAYS[intervals[j].day];
        return d1 === d2
          ? `يوم ${d1}: فترتان متداخلتان`
          : `تداخل بين فترات ${d1} و ${d2} (قد يكون بسبب امتداد فترة بعد منتصف الليل)`;
      }
    }
  }
  return null;
}

export default function OperatingHoursModal({
  pitchId,
  pitchName,
  onClose,
}: {
  pitchId:   number;
  pitchName: string;
  onClose:   () => void;
}) {
  const [grid, setGrid]       = useState<Grid>(emptyGrid());
  const [loading, setLoading] = useState(true);
  const [saving, setSaving]   = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const dialogRef = useRef<HTMLDivElement>(null);

  // Load the current schedule into the grid on open.
  useEffect(() => {
    let cancelled = false;
    api.get(`/pitches/${pitchId}/operating-hours`)
      .then(res => {
        if (cancelled) return;
        const g = emptyGrid();
        for (const sw of (res.data.data ?? []) as ServerWindow[]) {
          if (sw.weekday >= 0 && sw.weekday <= 6) {
            g[sw.weekday].push({ open: sw.open_time, close: sw.close_time });
          }
        }
        setGrid(g);
      })
      .catch(() => { if (!cancelled) setLoadError('تعذّر تحميل أوقات العمل، حاول مجدداً'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [pitchId]);

  // Esc to close (unless saving).
  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape' && !saving) onClose();
  }, [saving, onClose]);

  const validationError = useMemo(() => validateGrid(grid), [grid]);
  const hasAnyWindow = useMemo(() => grid.some(d => d.length > 0), [grid]);

  // ── Grid mutations ─────────────────────────────────────────────────────────
  const addWindow = (d: number) => {
    setGrid(prev => prev.map((day, i) => i === d ? [...day, { ...DEFAULT_WINDOW }] : day));
    setSaveError(null);
  };
  const removeWindow = (d: number, idx: number) => {
    setGrid(prev => prev.map((day, i) => i === d ? day.filter((_, j) => j !== idx) : day));
    setSaveError(null);
  };
  const changeWindow = (d: number, idx: number, field: keyof Window, val: string) => {
    setGrid(prev => prev.map((day, i) =>
      i === d ? day.map((w, j) => j === idx ? { ...w, [field]: val } : w) : day));
    setSaveError(null);
  };
  // "Closed" toggle: a closed day has zero windows. Re-opening seeds one default.
  const toggleClosed = (d: number) => {
    setGrid(prev => prev.map((day, i) =>
      i === d ? (day.length > 0 ? [] : [{ ...DEFAULT_WINDOW }]) : day));
    setSaveError(null);
  };
  // Copy this day's windows to every other day.
  const copyToAll = (d: number) => {
    setGrid(prev => prev.map(() => prev[d].map(w => ({ ...w }))));
    setSaveError(null);
  };

  // ── Submit — single PUT of the full schedule ────────────────────────────────
  const handleSave = async () => {
    if (validationError) return;
    setSaving(true);
    setSaveError(null);
    const windows: ServerWindow[] = [];
    for (let d = 0; d < 7; d++) {
      for (const w of grid[d]) {
        windows.push({ weekday: d, open_time: w.open, close_time: w.close });
      }
    }
    try {
      await api.put(`/pitches/${pitchId}/operating-hours`, { windows });
      onClose();
    } catch (err: any) {
      setSaveError(err?.response?.data?.message ?? 'تعذّر حفظ أوقات العمل، حاول مجدداً');
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" onKeyDown={handleKeyDown}>
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={() => { if (!saving) onClose(); }}
        aria-hidden
      />

      <div
        ref={dialogRef}
        dir="rtl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="oh-title"
        className="relative w-full max-w-2xl max-h-[88vh] flex flex-col rounded-2xl bg-[#141715] border border-white/[0.10] shadow-2xl overflow-hidden"
      >
        {/* Header */}
        <div className="flex items-start gap-3.5 px-6 pt-6 pb-4 border-b border-white/[0.06]">
          <div className="w-10 h-10 rounded-xl bg-emerald-500/[0.10] border border-emerald-500/20 flex items-center justify-center flex-shrink-0">
            <Clock size={18} className="text-emerald-400" aria-hidden />
          </div>
          <div className="min-w-0 flex-1">
            <h2 id="oh-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">أوقات العمل</h2>
            <p className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
              حدّد فترات فتح ملعب <span className="text-white/60 font-semibold">{pitchName}</span> لكل يوم من أيام الأسبوع
            </p>
          </div>
          <button
            type="button"
            onClick={() => { if (!saving) onClose(); }}
            className="text-white/25 hover:text-white/55 transition-colors duration-150 flex-shrink-0"
            aria-label="إغلاق"
          >
            <X size={18} aria-hidden />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-6 py-5">
          {loading ? (
            <div className="h-40 flex items-center justify-center">
              <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
            </div>
          ) : loadError ? (
            <p className="text-[13px] text-red-400 text-center py-10">{loadError}</p>
          ) : (
            <div className="flex flex-col gap-3">
              {/* Consequence note: configured hours close everything outside them. */}
              {hasAnyWindow && (
                <div className="flex items-start gap-2.5 px-4 py-3 rounded-xl bg-amber-500/[0.06] border border-amber-500/20">
                  <Info size={15} className="text-amber-400 flex-shrink-0 mt-0.5" aria-hidden />
                  <p className="text-[11.5px] text-amber-200/80 leading-relaxed">
                    الأوقات خارج الفترات المحددة ستظهر <span className="font-bold">كمغلقة</span> للاعبين.
                    الأيام التي بلا فترات تُعتبر مغلقة بالكامل.
                  </p>
                </div>
              )}

              {AR_DAYS.map((dayName, d) => {
                const windows = grid[d];
                const isOpen = windows.length > 0;
                return (
                  <div key={d} className="rounded-xl border border-white/[0.07] bg-[#0f1110] overflow-hidden">
                    <div className="flex items-center justify-between px-4 py-3">
                      <div className="flex items-center gap-3">
                        <span className="text-[13px] font-bold text-[#f0efe8] w-16">{dayName}</span>
                        {/* Closed toggle */}
                        <button
                          type="button"
                          role="switch"
                          aria-checked={isOpen}
                          onClick={() => toggleClosed(d)}
                          dir="ltr"
                          className={[
                            'relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full px-0.5 transition-colors duration-200',
                            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40',
                            isOpen ? 'bg-emerald-500/80' : 'bg-white/[0.15]',
                          ].join(' ')}
                          aria-label={isOpen ? `إغلاق يوم ${dayName}` : `فتح يوم ${dayName}`}
                        >
                          <span className={[
                            'h-4 w-4 transform rounded-full bg-white shadow transition-transform duration-200',
                            isOpen ? 'translate-x-[16px]' : 'translate-x-0',
                          ].join(' ')} />
                        </button>
                        <span className={`text-[11px] font-semibold ${isOpen ? 'text-emerald-400' : 'text-white/35'}`}>
                          {isOpen ? 'مفتوح' : 'مغلق'}
                        </span>
                      </div>

                      {isOpen && (
                        <div className="flex items-center gap-2">
                          <button
                            type="button"
                            onClick={() => copyToAll(d)}
                            className="inline-flex items-center gap-1 text-[10.5px] text-white/40 hover:text-emerald-400 transition-colors duration-150"
                            title="نسخ هذه الفترات إلى كل الأيام"
                          >
                            <Copy size={11} aria-hidden />
                            نسخ للكل
                          </button>
                          <button
                            type="button"
                            onClick={() => addWindow(d)}
                            className="inline-flex items-center gap-1 text-[10.5px] text-emerald-400 hover:text-emerald-300 transition-colors duration-150"
                          >
                            <Plus size={12} aria-hidden />
                            فترة
                          </button>
                        </div>
                      )}
                    </div>

                    {isOpen && (
                      <div className="px-4 pb-3 flex flex-col gap-2">
                        {windows.map((w, idx) => (
                          <div key={idx} className="flex items-center gap-2">
                            <input
                              type="time"
                              value={w.open}
                              onChange={e => changeWindow(d, idx, 'open', e.target.value)}
                              dir="ltr"
                              className="bg-white/[0.04] border border-white/[0.13] rounded-lg px-2.5 py-2 text-[12px] text-[#f0efe8] [color-scheme:dark] focus:outline-none focus:border-emerald-500/60"
                              aria-label={`بداية الفترة ${idx + 1} ليوم ${dayName}`}
                            />
                            <span className="text-white/30 text-[12px]">—</span>
                            <input
                              type="time"
                              value={w.close}
                              onChange={e => changeWindow(d, idx, 'close', e.target.value)}
                              dir="ltr"
                              className="bg-white/[0.04] border border-white/[0.13] rounded-lg px-2.5 py-2 text-[12px] text-[#f0efe8] [color-scheme:dark] focus:outline-none focus:border-emerald-500/60"
                              aria-label={`نهاية الفترة ${idx + 1} ليوم ${dayName}`}
                            />
                            {crossesMidnight(w) && (
                              <span
                                className="px-1.5 py-0.5 rounded-md text-[9px] font-bold bg-sky-500/15 border border-sky-500/30 text-sky-300"
                                title="تمتد هذه الفترة إلى اليوم التالي بعد منتصف الليل"
                              >
                                +1 اليوم التالي
                              </span>
                            )}
                            <button
                              type="button"
                              onClick={() => removeWindow(d, idx)}
                              className="ms-auto text-red-400/60 hover:text-red-400 transition-colors duration-150"
                              aria-label={`حذف الفترة ${idx + 1} ليوم ${dayName}`}
                            >
                              <Trash2 size={14} aria-hidden />
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="border-t border-white/[0.06] bg-[#111312] px-6 py-4">
          {/* Inline validation (overlap incl. spillover) */}
          {validationError && (
            <div className="flex items-center gap-2 mb-3 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
              <AlertTriangle size={14} className="flex-shrink-0" aria-hidden />
              {validationError}
            </div>
          )}
          {saveError && (
            <div className="flex items-center gap-2 mb-3 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
              <AlertTriangle size={14} className="flex-shrink-0" aria-hidden />
              {saveError}
            </div>
          )}
          <div className="flex items-center justify-end gap-3">
            <button
              type="button"
              onClick={() => { if (!saving) onClose(); }}
              disabled={saving}
              className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
            >
              إلغاء
            </button>
            <button
              type="button"
              onClick={handleSave}
              disabled={saving || loading || !!validationError || !!loadError}
              className={[
                'flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold',
                'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
                'hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40',
                'disabled:opacity-50 disabled:cursor-not-allowed',
                'transition-all duration-200 active:scale-[0.97]',
              ].join(' ')}
            >
              {saving ? (
                <>
                  <span className="w-3.5 h-3.5 rounded-full border-2 border-emerald-400/50 border-t-transparent animate-spin" aria-hidden />
                  جاري الحفظ...
                </>
              ) : 'حفظ أوقات العمل'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
