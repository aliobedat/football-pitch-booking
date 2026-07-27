'use client';

// The current Amman civil date, RE-EVALUATED as the day rolls over.
//
// Both جدول اليوم and جدول الملعب previously did:
//   const today = useMemo(() => ammanCivilDate(new Date()), []);
// which samples the date ONCE at mount and never again. A dashboard left open
// across Amman midnight — the normal case for a venue operating late — then
// keeps yesterday as "today": the "اليوم، " prefix lies and the "اليوم" reset
// button points at the wrong day (or hides when it should show).
//
// SCOPE, precisely: this fixes the LABELLING and the reset button's TARGET. It
// does NOT move the day the tab is currently showing — the viewed `date` is
// seeded once and stays put by design. Auto-advancing it would yank an operator
// mid-task, which is worse than a stale label; manual next-day navigation is the
// answer to rollover, and it is already there.
//
// The value is polled once a minute rather than scheduled precisely at
// midnight: a single timer is cheap, and a long setTimeout is unreliable
// anyway because browsers throttle background tabs and suspend on sleep. The
// visibility/focus listeners are what actually make it feel instant — a phone
// picked up in the morning re-checks the moment its tab is shown, without
// waiting out the interval.
//
// The SAME CivilDate object is returned while the day is unchanged, so
// downstream useMemo/useCallback dependencies stay referentially stable and no
// consumer refetches on a tick.

import { useEffect, useState } from 'react';
import { type CivilDate, ammanCivilDate, sameCivilDate } from './amman';

export function useAmmanToday(): CivilDate {
  const [today, setToday] = useState<CivilDate>(() => ammanCivilDate(new Date()));

  useEffect(() => {
    const tick = () => {
      const now = ammanCivilDate(new Date());
      setToday(prev => (sameCivilDate(prev, now) ? prev : now));
    };
    const onVisible = () => { if (document.visibilityState === 'visible') tick(); };

    const id = window.setInterval(tick, 60_000);
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('focus', tick);
    return () => {
      window.clearInterval(id);
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('focus', tick);
    };
  }, []);

  return today;
}
