// ─────────────────────────────────────────────────────────────────────────────
// Centralised number / currency / date formatting.
//
// HARD RULE: every user-facing number renders with Latin digits 0–9 (never
// Arabic-Indic ٠١٢…), while UI text stays Arabic RTL. This is enforced by pinning
// `numberingSystem: 'latn'` on every Intl formatter below. Do NOT call
// `toLocaleString` / `Intl.*` / `.toLocaleDateString('ar-…')` ad-hoc elsewhere —
// route through these helpers so the Latin-digit guarantee holds everywhere.
//
// We use the 'ar-JO' locale (Arabic month/weekday names, JOD conventions) but
// force the Latin numbering system, which gives Arabic words + Western digits.
// ─────────────────────────────────────────────────────────────────────────────

const LOCALE = 'ar-JO';
const LATN = 'latn' as const;

// Malaeb is a Jordan product: every user-facing timestamp is rendered in Amman
// civil time, NEVER the browser's local zone. The server is the source of truth
// (it sends UTC ISO-8601), and a player travelling abroad must still see their
// booking in Amman time. Pinning the IANA zone here guarantees that everywhere
// these helpers are used. Do not format dates/times outside these helpers.
const TIME_ZONE = 'Asia/Amman';

/** Format a plain number with Latin digits (e.g. 1,250 → "1,250"). */
export function formatNumber(
  value: number,
  options: Intl.NumberFormatOptions = {},
): string {
  if (!Number.isFinite(value)) return '0';
  return new Intl.NumberFormat(LOCALE, {
    numberingSystem: LATN,
    ...options,
  }).format(value);
}

/**
 * Format a JOD amount with Latin digits. By default shows up to 2 decimals but
 * trims trailing zeros (25 → "25", 25.5 → "25.5"). The currency symbol (د.أ) is
 * appended by the caller's markup, so this returns the bare number string.
 */
export function formatCurrency(
  value: number,
  options: Intl.NumberFormatOptions = {},
): string {
  return formatNumber(value, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
    ...options,
  });
}

/** Format an ISO date string as an Arabic date with Latin digits. */
export function formatDate(
  iso: string,
  options: Intl.DateTimeFormatOptions = { weekday: 'short', month: 'short', day: 'numeric' },
): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return new Intl.DateTimeFormat(LOCALE, {
    numberingSystem: LATN,
    timeZone: TIME_ZONE,
    ...options,
  }).format(d);
}

/** Format an ISO date string as an Arabic time with Latin digits, in Amman time. */
export function formatTime(
  iso: string,
  options: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', hour12: true },
): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return new Intl.DateTimeFormat(LOCALE, {
    numberingSystem: LATN,
    timeZone: TIME_ZONE,
    ...options,
  }).format(d);
}
