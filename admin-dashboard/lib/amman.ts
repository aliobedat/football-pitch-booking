// Amman civil-date helpers — the single source of truth for the dashboard's
// day-scoped date arithmetic (previous/next day, "is this today", building the
// absolute instant for an Amman wall-clock hour). Extracted verbatim from
// BlocksModal so the Day View and the Blocks tool share ONE implementation
// instead of two copies.
//
// Jordan has no DST (permanent UTC+3 since 2022), so an Amman wall-clock instant
// is the literal "…T HH:00:00+03:00" — the SAME instant the backend resolves the
// Amman civil day against, with no offset drift. Rendering (Arabic month/weekday
// names, Latin digits) stays in lib/format; this module is pure date math.

export const AMMAN_OFFSET = '+03:00';

export interface CivilDate {
  y: number;
  m: number; // 1-12
  d: number;
}

export const pad = (n: number) => String(n).padStart(2, '0');

// The Amman civil Y/M/D of an absolute instant.
export function ammanCivilDate(at: Date): CivilDate {
  // en-CA → "YYYY-MM-DD"; pinned to Amman so the day boundary is civil, not UTC.
  const s = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Amman', year: 'numeric', month: '2-digit', day: '2-digit',
  }).format(at);
  const [y, m, d] = s.split('-').map(Number);
  return { y, m, d };
}

// The Amman civil hour (0-23) of an absolute instant.
export function ammanHour(at: Date): number {
  return Number(new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Asia/Amman', hour: '2-digit', hour12: false,
  }).format(at)) % 24;
}

// Build the absolute instant for an Amman wall-clock hour on a civil date.
// hour may be 24 → midnight starting the next day (the day's exclusive end).
export function ammanInstant(date: CivilDate, hour: number): Date {
  return new Date(`${date.y}-${pad(date.m)}-${pad(date.d)}T${pad(hour)}:00:00${AMMAN_OFFSET}`);
}

export function sameCivilDate(a: CivilDate, b: CivilDate): boolean {
  return a.y === b.y && a.m === b.m && a.d === b.d;
}

export function addDays(date: CivilDate, delta: number): CivilDate {
  // Anchor at Amman noon to stay clear of any boundary, then re-read the civil date.
  const at = new Date(`${date.y}-${pad(date.m)}-${pad(date.d)}T12:00:00${AMMAN_OFFSET}`);
  at.setUTCDate(at.getUTCDate() + delta);
  return ammanCivilDate(at);
}

// "YYYY-MM-DD" for a CivilDate — the query-param / URL form the backend expects.
export function ymd(date: CivilDate): string {
  return `${date.y}-${pad(date.m)}-${pad(date.d)}`;
}

// Parse a "YYYY-MM-DD" string (URL param / native date input) back to a CivilDate.
// Returns null for a malformed value so callers can fall back to today.
export function parseYmd(s: string): CivilDate | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return null;
  return { y: Number(m[1]), m: Number(m[2]), d: Number(m[3]) };
}

// Number of days in a civil month (m: 1-12). Day 0 of the next month = last day of m.
export function daysInMonth(y: number, m: number): number {
  return new Date(Date.UTC(y, m, 0)).getUTCDate();
}

// Amman civil weekday of a date: 0 = Sunday … 6 = Saturday (matches the backend's
// PG DOW). Anchored at Amman noon and read back in Amman, so it never drifts.
export function ammanWeekday(date: CivilDate): number {
  const wd = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Amman', weekday: 'short',
  }).format(ammanInstant(date, 12));
  return ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(wd);
}
