// Pure geometry for the Visual Calendar (Cockpit WO2). The timeline is rendered
// as an absolute-positioned track: events are detached from the discrete grid and
// placed by converting an absolute instant to a percentage offset along the
// windowed day. All inputs are absolute UTC ISO strings; positioning is purely
// relative (instant − windowStart) / windowSpan, so NO timezone arithmetic is
// needed here — Amman wall-clock labels are produced separately via lib/format
// (which pins Asia/Amman). The time axis flows LTR (earliest → latest) even inside
// the RTL chrome, matching the Western-numeral mental model.

export interface Interval {
  start: string; // ISO UTC, inclusive
  end: string;   // ISO UTC, exclusive
}

export const MIN_MS = 60_000;
export const SLOT_MIN = 30; // 30-minute granularity
const SLOT_MS = SLOT_MIN * MIN_MS;

/** Parse an ISO instant to epoch ms. */
export const msOf = (iso: string): number => new Date(iso).getTime();

/** Floor an epoch-ms value to the previous 30-minute boundary. */
export const floorSlot = (ms: number): number => ms - (((ms % SLOT_MS) + SLOT_MS) % SLOT_MS);

/** Ceil an epoch-ms value to the next 30-minute boundary. */
export const ceilSlot = (ms: number): number => {
  const r = ((ms % SLOT_MS) + SLOT_MS) % SLOT_MS;
  return r === 0 ? ms : ms + (SLOT_MS - r);
};

export interface DayWindow {
  startMs: number;
  endMs: number;
}

/**
 * Compute the visible [start,end) window in epoch ms. It tightly bounds the union
 * of the day's open windows (so the timeline isn't a flat 00:00–24:00), adds a
 * buffer on each side so edges don't feel cramped, and snaps to 30-min
 * boundaries. When there are NO open windows at all (every pitch unconfigured →
 * 24/7), it falls back to the supplied [fallbackStart, fallbackEnd] (the page
 * derives these from the Amman day, e.g. 08:00–23:00). Events outside the window
 * still get clamped by clampPct, never dropped.
 */
export function computeWindow(
  openWindows: Interval[],
  bufferMin: number,
  fallbackStartMs: number,
  fallbackEndMs: number,
): DayWindow {
  let lo = Infinity;
  let hi = -Infinity;
  for (const w of openWindows) {
    lo = Math.min(lo, msOf(w.start));
    hi = Math.max(hi, msOf(w.end));
  }
  if (!isFinite(lo) || !isFinite(hi) || hi <= lo) {
    lo = fallbackStartMs;
    hi = fallbackEndMs;
  } else {
    lo -= bufferMin * MIN_MS;
    hi += bufferMin * MIN_MS;
  }
  return { startMs: floorSlot(lo), endMs: ceilSlot(hi) };
}

/** Percentage offset (0–100) of an instant along the window, unclamped. */
export function offsetPct(ms: number, win: DayWindow): number {
  const span = win.endMs - win.startMs;
  if (span <= 0) return 0;
  return ((ms - win.startMs) / span) * 100;
}

/** Left% + width% for an event band, clamped to [0,100] so it never overflows. */
export function bandStyle(startIso: string, endIso: string, win: DayWindow): { left: number; width: number } {
  const a = Math.max(0, offsetPct(msOf(startIso), win));
  const b = Math.min(100, offsetPct(msOf(endIso), win));
  const left = Math.min(a, 100);
  const width = Math.max(0, b - left);
  return { left, width };
}

/** 30-minute tick marks (epoch ms) spanning the window, inclusive of both ends. */
export function buildTicks(win: DayWindow): number[] {
  const ticks: number[] = [];
  for (let t = win.startMs; t <= win.endMs; t += SLOT_MS) ticks.push(t);
  return ticks;
}

/**
 * Snap an arbitrary instant (e.g. derived from a click x-fraction) to the nearest
 * 30-min boundary within the window — the start time for tap-to-create-manual.
 */
export function snapToSlot(ms: number): number {
  const r = ((ms % SLOT_MS) + SLOT_MS) % SLOT_MS;
  return r < SLOT_MS / 2 ? ms - r : ms + (SLOT_MS - r);
}

/** Convert a click fraction (0–1 along the LTR track) to an epoch-ms instant. */
export function fractionToMs(fraction: number, win: DayWindow): number {
  const clamped = Math.min(1, Math.max(0, fraction));
  return win.startMs + clamped * (win.endMs - win.startMs);
}
