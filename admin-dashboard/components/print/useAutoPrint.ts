'use client';

// Auto-print trigger for the /reports/print/* statement routes (WO-REPORTS-R4,
// ruling A1): window.print() fires ONCE, and only after the report data has
// fully resolved into the DOM (post-paint, fonts ready). Loading, error, and
// failed-fetch states never reach this hook with ready=true, so a skeleton or
// an error banner is never printed. A zeroed empty period is valid data and
// prints normally.

import { useEffect, useRef } from 'react';

export default function useAutoPrint(ready: boolean) {
  const fired = useRef(false);
  useEffect(() => {
    if (!ready || fired.current) return;
    fired.current = true;
    let cancelled = false;
    // Wait one frame (data is painted) + font readiness (Cairo webfont), so the
    // preview never captures fallback glyphs or a half-rendered table.
    const raf = requestAnimationFrame(() => {
      const fonts = typeof document !== 'undefined' && document.fonts
        ? document.fonts.ready
        : Promise.resolve();
      Promise.resolve(fonts).then(() => { if (!cancelled) window.print(); });
    });
    return () => { cancelled = true; cancelAnimationFrame(raf); };
  }, [ready]);
}
