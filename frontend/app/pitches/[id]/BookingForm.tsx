'use client';

import { useState, useEffect, useMemo, useRef } from 'react';
import { useRouter } from 'next/navigation';
import { CalendarDays, CheckCircle2, Users } from 'lucide-react';
import { IBM_Plex_Sans_Arabic } from 'next/font/google';
import axios from 'axios';
import api from '@/lib/api';
import { useAuth, type User } from '@/context/AuthContext';
import FullNameField, { isValidFullName, saveFullName } from '@/components/FullNameField';
import OtpModal from './OtpModal';

// Handoff design font (WO-CROSS-MIDNIGHT Gate 1B) — scoped to THIS card only.
// The global Cairo font in layout.tsx is intentionally untouched.
const plexArabic = IBM_Plex_Sans_Arabic({
  subsets: ['arabic', 'latin'],
  weight: ['400', '500', '600', '700'],
  display: 'swap',
});

// MVP no-OTP booking flag (mirrors backend BOOKING_OTP_REQUIRED, fail-open):
// only the exact string "true" requires OTP; unset / anything else → NOT required,
// so the booking flow skips the OTP modal and books with name + JO phone only.
const BOOKING_OTP_REQUIRED = process.env.NEXT_PUBLIC_BOOKING_OTP_REQUIRED === 'true';

// ─── Types ────────────────────────────────────────────────────────────────────

interface BookedSlot {
  booking_id: number;
  start_time: string;
  end_time:   string;
  status:     string;
}

// OpenWindow is one resolved operating-hours interval for the queried date, in
// absolute UTC [start, end) instants — the SAME referee the server's booking gate
// uses. `end` may fall on the next calendar day (cross-midnight window).
interface OpenWindow {
  start: string;
  end:   string;
}

// SessionRange is an OpenWindow projected onto the SESSION-day minute axis:
// minutes from the selected day's local midnight, UN-clamped — end (and, for the
// after-midnight tail, even start) may exceed 1440. A window that began on the
// PREVIOUS day (start < 0 on this axis) belongs to the previous session and is
// excluded here — that is the session-anchoring rule (a 00:30 slot belongs to
// yesterday's session, surfaced under yesterday's بعد منتصف الليل group).
interface SessionRange {
  start: number;
  end:   number;
}

// PitchOption is one selectable pitch inside a multi-pitch venue (WO-VENUES
// Gate 1c). subline is shown only when format/price differ across the venue.
export interface PitchOption {
  id:       number;
  label:    string;
  subline?: string;
}

interface Props {
  pitchId:      number;
  pricePerHour: number;
  // Multi-pitch venue selector (Gate 1c): when >1 option is passed, segmented
  // chips render above the date bar. Selection is controlled by the parent —
  // the availability effect below already refetches on pitchId and clears the
  // selected slot; the date stays sticky because it lives in state the pitch
  // switch never touches.
  pitchOptions?: PitchOption[];
  onPitchChange?: (id: number) => void;
}

// ─── Constants ────────────────────────────────────────────────────────────────

const AR_DAYS = ['الأحد', 'الاثنين', 'الثلاثاء', 'الأربعاء', 'الخميس', 'الجمعة', 'السبت'];
const MAX_BOOKING_DAYS = 365;

// Server-enforced lead window (Gate 1A: bookingLeadTime). The UI filters starts
// inside it so the server's insufficient_lead_time rejection is never provoked.
const LEAD_MINUTES = 15;

// Time groups per the handoff spec. `from`/`to` are session-axis minutes; the
// after-midnight group covers everything past 1440 (labeled "فجر <next weekday>").
const GROUP_DEFS = [
  { label: 'الصباح',          from: 0,    to: 720 },
  { label: 'الظهيرة والعصر',  from: 720,  to: 1020 },
  { label: 'المساء',          from: 1020, to: 1440 },
  { label: 'بعد منتصف الليل', from: 1440, to: 2880 },
] as const;

// ─── Helpers ──────────────────────────────────────────────────────────────────

function getAmmanParts(date: Date): { dateStr: string; hours: number; minutes: number } {
  const fmt = new Intl.DateTimeFormat('en-CA', {
    timeZone:  'Asia/Amman',
    year:      'numeric',
    month:     '2-digit',
    day:       '2-digit',
    hour:      '2-digit',
    minute:    '2-digit',
    hour12:    false,
  });
  const parts = fmt.formatToParts(date);
  const get = (t: string) => parts.find(p => p.type === t)?.value ?? '0';
  return {
    dateStr: `${get('year')}-${get('month')}-${get('day')}`,
    hours:   parseInt(get('hour'), 10) % 24, // guard: some engines emit "24" at midnight
    minutes: parseInt(get('minute'), 10),
  };
}

function addDays(dateStr: string, n: number): string {
  const [y, mo, d] = dateStr.split('-').map(Number);
  const date = new Date(y, mo - 1, d + n);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}

function parseDateStr(s: string): Date {
  const [y, mo, d] = s.split('-').map(Number);
  return new Date(y, mo - 1, d);
}

// buildAbs constructs the ABSOLUTE instant for `mins` minutes after dateStr's
// local midnight. Because JS Date normalises overflowing minute values, a
// cross-midnight value (e.g. 1500 = 25:00) lands on the CORRECT NEXT calendar
// day automatically — this replaces the old buildDateTime(dateStr, "HH:MM")
// which always resolved on dateStr's own day (the Gate 1B-0 trap).
function buildAbs(dateStr: string, mins: number): Date {
  const [y, mo, d] = dateStr.split('-').map(Number);
  return new Date(y, mo - 1, d, 0, mins, 0, 0);
}

// fmt24 renders session-axis minutes as a 24h wall clock, wrapping past-midnight
// values (1500 → "01:00") per the spec's `minutes % 1440` display rule. The
// underlying value stays absolute for all math.
function fmt24(mins: number): string {
  const m = ((mins % 1440) + 1440) % 1440;
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

// windowToSessionRange projects an absolute-UTC open window onto dayStr's
// session axis (minutes from local midnight), UN-clamped. Returns null for a
// window that began before this day's midnight — that is the previous session's
// after-midnight tail and must not appear under this day (session anchoring).
function windowToSessionRange(w: OpenWindow, dayStr: string): SessionRange | null {
  const base  = buildAbs(dayStr, 0).getTime();
  const start = Math.round((new Date(w.start).getTime() - base) / 60000);
  const end   = Math.round((new Date(w.end).getTime()   - base) / 60000);
  if (start < 0)     return null; // previous session's window
  if (end <= start)  return null;
  return { start, end };
}

// overlapsBooked: does the ABSOLUTE range [aStart, aEnd) touch any booked slot?
// All comparisons are epoch-ms of real Date objects, so cross-midnight ranges
// need no special casing.
function overlapsBooked(aStart: number, aEnd: number, booked: BookedSlot[]): boolean {
  return booked.some(b => {
    const bStart = new Date(b.start_time).getTime();
    const bEnd   = new Date(b.end_time).getTime();
    return bStart < aEnd && bEnd > aStart;
  });
}

// availableStartsFor computes the bookable 30-min start marks for one session
// day: inside a window, room for ≥60 min before window close, not overlapping a
// booked slot, and not inside the lead window (absolute-time comparison, so the
// after-midnight tail of today's session filters correctly too).
function availableStartsFor(
  dayStr:   string,
  windows:  SessionRange[],
  booked:   BookedSlot[],
  minStartMs: number,
): number[] {
  const out: number[] = [];
  for (const w of windows) {
    const first = Math.ceil(w.start / 30) * 30;
    for (let m = first; m <= w.end - 60; m += 30) {
      const absStart = buildAbs(dayStr, m).getTime();
      if (absStart < minStartMs) continue;
      if (overlapsBooked(absStart, absStart + 30 * 60000, booked)) continue;
      out.push(m);
    }
  }
  return [...new Set(out)].sort((a, b) => a - b);
}

// Accepts: 07XXXXXXXX | 7XXXXXXXX | +9627XXXXXXXX | 009627XXXXXXXX
// Outputs: { e164: '+9627XXXXXXXX' } or { error: <Arabic message> }
// Local-part regex: ^7[789]\d{7}$ — Jordanian mobile prefixes 77/78/79 only.
function normalizePhone(raw: string): { e164: string | null; error: string | null } {
  const cleaned = raw.replace(/[\s\-().]/g, '');
  let local: string;
  if      (cleaned.startsWith('+962'))  local = cleaned.slice(4);
  else if (cleaned.startsWith('00962')) local = cleaned.slice(5);
  else if (cleaned.startsWith('07'))    local = cleaned.slice(1);
  else if (cleaned.startsWith('7'))     local = cleaned;
  else return { e164: null, error: 'رقم هاتف غير صالح — أدخل رقماً أردنياً صحيحاً' };
  if (!/^7[789]\d{7}$/.test(local))
    return { e164: null, error: 'رقم هاتف غير صالح — يجب أن يبدأ بـ 077 أو 078 أو 079 ومكوّن من 10 أرقام' };
  return { e164: `+962${local}`, error: null };
}

function durationLabel(mins: number): string {
  if (mins === 60)  return 'ساعة';
  if (mins === 90)  return 'ساعة ونصف';
  if (mins === 120) return 'ساعتان';
  return `${mins / 60} ساعة`;
}

// Arabic month-day label ("24 يوليو") with latin digits, per the handoff.
function monthDayLabel(dateStr: string): string {
  return new Intl.DateTimeFormat('ar-u-nu-latn', { day: 'numeric', month: 'long' })
    .format(parseDateStr(dateStr));
}

// ─────────────────────────────────────────────────────────────────────────────
// Component
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingForm({ pitchId, pricePerHour, pitchOptions, onPitchChange }: Props) {
  const router = useRouter();
  const { user, login, refreshUser, isLoading: authLoading } = useAuth();

  // ── JIT name capture ──────────────────────────────────────────────────────
  // If the authed user has no full_name yet, we collect it inline in the confirm
  // step (no route change) and PATCH /me before the booking call.
  const needsName = !!user && !user.full_name?.trim();
  const [nameInput, setNameInput] = useState('');
  const [nameTouched, setNameTouched] = useState(false);
  const nameOK = !needsName || isValidFullName(nameInput);

  // ── Guest capture (unauthenticated path) ──────────────────────────────────
  // Shown only when !user. Mutually exclusive with the needsName block above.
  const isGuest = !user;
  // Seed from the session profile when present so a returning user never re-types
  // their name (the field is hidden for them anyway — see conditional render).
  const [guestName,         setGuestName]         = useState(user?.full_name ?? '');
  const [guestNameTouched,  setGuestNameTouched]  = useState(false);
  const [guestPhone,        setGuestPhone]        = useState('');
  const [guestPhoneTouched, setGuestPhoneTouched] = useState(false);
  const [guestPhoneError,   setGuestPhoneError]   = useState<string | null>(null);
  const [otpOpen,           setOtpOpen]           = useState(false);

  // ── Server time (Asia/Amman) — source of truth for all time logic ─────────
  const [serverNow,      setServerNow]      = useState<Date | null>(null);
  const [serverTodayStr, setServerTodayStr] = useState<string>('');

  useEffect(() => {
    fetch('/api/server-time')
      .then(r => r.json())
      .then((d: { iso: string }) => {
        const now = new Date(d.iso);
        const { dateStr } = getAmmanParts(now);
        setServerNow(now);
        setServerTodayStr(dateStr);
      })
      .catch(() => {
        // fallback to client time — display a console warning
        console.warn('[BookingForm] server-time fetch failed; falling back to client clock');
        const now = new Date();
        const { dateStr } = getAmmanParts(now);
        setServerNow(now);
        setServerTodayStr(dateStr);
      });
  }, []);

  // ── Selected date ─────────────────────────────────────────────────────────
  const [selDayStr,      setSelDayStr]      = useState<string>('');
  const [showDatePicker, setShowDatePicker] = useState(false);

  useEffect(() => {
    if (serverTodayStr && !selDayStr) setSelDayStr(serverTodayStr);
  }, [serverTodayStr, selDayStr]);

  // ── Time selection (handoff state model) ──────────────────────────────────
  // selectedStart = ABSOLUTE minutes from the selected session-day's midnight;
  // exceeds 1440 for after-midnight slots. Replaces the old baseHour+startMod.
  const [selectedStart, setSelectedStart] = useState<number | null>(null);
  const [duration,      setDuration]      = useState<60 | 90 | 120>(60);

  // ── Availability data ─────────────────────────────────────────────────────
  const [booked,       setBooked]       = useState<BookedSlot[]>([]);
  // Operating hours for the selected day. hasSchedule=false → the pitch is
  // unconfigured and OPEN 24/7 (the server's fail-open decision).
  const [openWindows,  setOpenWindows]  = useState<OpenWindow[]>([]);
  const [hasSchedule,  setHasSchedule]  = useState(false);
  const [loadingSlots, setLoadingSlots] = useState(false);
  const [submitting,   setSubmitting]   = useState(false);
  const [apiError,     setApiError]     = useState<string | null>(null);
  const [success,      setSuccess]      = useState(false);

  useEffect(() => {
    if (!selDayStr) return;
    setLoadingSlots(true);
    setSelectedStart(null);
    setApiError(null);
    api
      .get(`/pitches/${pitchId}/availability?date=${selDayStr}`)
      .then(r  => {
        setBooked(r.data.booked_slots ?? []);
        setOpenWindows(r.data.open_windows ?? []);
        setHasSchedule(!!r.data.has_schedule);
      })
      .catch(() => { setBooked([]); setOpenWindows([]); setHasSchedule(false); })
      .finally(() => setLoadingSlots(false));
  }, [pitchId, selDayStr]);

  // ── Session windows for the selected day (UN-clamped, session-anchored) ───
  // Unconfigured (24/7) → one full civil-day window [0, 1440).
  const sessionWindows = useMemo<SessionRange[]>(() => {
    if (!hasSchedule) return [{ start: 0, end: 1440 }];
    return openWindows
      .map(w => windowToSessionRange(w, selDayStr))
      .filter((r): r is SessionRange => r !== null);
  }, [openWindows, hasSchedule, selDayStr]);

  // Earliest bookable absolute instant (server lead window).
  const minStartMs = serverNow ? serverNow.getTime() + LEAD_MINUTES * 60000 : 0;

  // ── Available 30-min start marks for the selected session day ─────────────
  const starts = useMemo(
    () => selDayStr ? availableStartsFor(selDayStr, sessionWindows, booked, minStartMs) : [],
    [selDayStr, sessionWindows, booked, minStartMs],
  );

  // ── D3-a: still-live previous session ─────────────────────────────────────
  // After midnight, while YESTERDAY's cross-midnight session is still open, a
  // player at 00:30 must be able to book 01:00 — but those slots belong to the
  // previous session day (session anchoring), whose chip the strip would not
  // otherwise show. When the early hours + a live tail are detected, prepend a
  // special "yesterday, session ongoing" chip to the strip.
  const [prevSessionLive, setPrevSessionLive] = useState(false);
  useEffect(() => {
    if (!serverNow || !serverTodayStr) { setPrevSessionLive(false); return; }
    const { hours } = getAmmanParts(serverNow);
    if (hours >= 6) { setPrevSessionLive(false); return; } // only relevant in the early hours
    const yStr = addDays(serverTodayStr, -1);
    const cutoff = serverNow.getTime() + LEAD_MINUTES * 60000;
    api
      .get(`/pitches/${pitchId}/availability?date=${yStr}`, { _silent: true })
      .then(r => {
        const yWindows: SessionRange[] = (r.data.has_schedule
          ? (r.data.open_windows ?? [])
              .map((w: OpenWindow) => windowToSessionRange(w, yStr))
              .filter((x: SessionRange | null): x is SessionRange => x !== null)
          : []); // unconfigured 24/7 has no after-midnight tail — nothing to surface
        const tail = availableStartsFor(yStr, yWindows, r.data.booked_slots ?? [], cutoff)
          .filter(m => m >= 1440);
        setPrevSessionLive(tail.length > 0);
      })
      .catch(() => setPrevSessionLive(false));
  }, [pitchId, serverNow, serverTodayStr]);

  // ── 7-day rolling strip (+ optional live-previous-session chip) ───────────
  const sevenDays = useMemo(
    () => serverTodayStr ? Array.from({ length: 7 }, (_, i) => addDays(serverTodayStr, i)) : [],
    [serverTodayStr],
  );
  const stripDays = useMemo(
    () => (prevSessionLive && serverTodayStr)
      ? [addDays(serverTodayStr, -1), ...sevenDays]
      : sevenDays,
    [prevSessionLive, serverTodayStr, sevenDays],
  );

  const maxDateStr = useMemo(
    () => serverTodayStr ? addDays(serverTodayStr, MAX_BOOKING_DAYS) : '',
    [serverTodayStr],
  );

  // ── Duration validity + derived values ────────────────────────────────────

  // A duration fits iff the whole [start, start+dur) lies inside ONE session
  // window (containment — mirrors the server gate) and overlaps no booked slot.
  function validDur(start: number, dur: number): boolean {
    if (!sessionWindows.some(w => w.start <= start && start + dur <= w.end)) return false;
    const a = buildAbs(selDayStr, start).getTime();
    return !overlapsBooked(a, buildAbs(selDayStr, start + dur).getTime(), booked);
  }

  function pickStart(m: number) {
    setApiError(null);
    if (selectedStart === m) { setSelectedStart(null); return; }
    setSelectedStart(m);
    // Auto-fallback 60→90→120 when the current duration no longer fits.
    if (!validDur(m, duration)) {
      const fit = ([60, 90, 120] as const).find(d => validDur(m, d)) ?? 60;
      setDuration(fit);
    }
  }

  const started        = selectedStart !== null;
  const endMins        = started ? selectedStart! + duration : 0;
  const crossesMidnight = started && selectedStart! < 1440 && endMins > 1440;
  const startsAfterMid  = started && selectedStart! >= 1440;
  const total          = started ? Math.round((duration / 60) * pricePerHour * 100) / 100 : 0;
  const durationOK     = started && validDur(selectedStart!, duration);
  const nextDayName    = selDayStr ? AR_DAYS[parseDateStr(addDays(selDayStr, 1)).getDay()] : '';

  // A returning user's stored name satisfies validity without re-entry; a guest
  // (or a new account with no name yet) must supply a valid guestName.
  const nameValid = (user?.full_name?.trim() ?? guestName.trim()).length >= 2;
  // Keep the isGuest gate so the separate needsName/FullNameField path (an authed
  // user who never set a name) stays governed by nameOK above, not by nameValid.
  const guestNameOK  = !isGuest || nameValid;
  const guestPhoneOK = !isGuest || normalizePhone(guestPhone).e164 !== null;

  const canSubmit =
    started &&
    durationOK &&
    nameOK &&
    guestNameOK &&
    guestPhoneOK &&
    !submitting;

  // ── Handlers ─────────────────────────────────────────────────────────────

  function handleDaySelect(dayStr: string) {
    setSelDayStr(dayStr);
    setSelectedStart(null);
    setApiError(null);
    setShowDatePicker(false);
  }

  function handleDurationClick(d: 60 | 90 | 120) {
    if (selectedStart === null || !validDur(selectedStart, d)) return;
    setDuration(d);
    setApiError(null);
  }

  // Synchronous re-entry guard: blocks a double-tap from firing two POSTs before
  // React re-renders the disabled button. Declared here so createBooking and
  // handleSubmit both close over the same ref.
  const inFlightRef = useRef(false);

  // ── Booking POST — shared by the authenticated path and the post-OTP path ──
  // Owns its own inFlightRef acquisition so it is safe to call from both
  // handleSubmit (auth'd users) and handleOtpVerified (guest → verify → book).

  async function createBooking() {
    if (inFlightRef.current) return;
    inFlightRef.current = true;
    setSubmitting(true);
    setApiError(null);

    const idempotencyKey =
      (typeof crypto !== 'undefined' && 'randomUUID' in crypto)
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`;

    // Absolute instants for the selected range. buildAbs normalises minute
    // overflow, so a cross-midnight end (e.g. 1470 = 00:30) constructs on the
    // CORRECT NEXT calendar day — the Gate 1B buildDateTime fix.
    const absStart = buildAbs(selDayStr, selectedStart!);
    const absEnd   = buildAbs(selDayStr, selectedStart! + duration);

    // Fresh availability check — catches the race where another player booked
    // the same slot between selection and POST. Display-advisory only: the
    // server's GIST EXCLUDE constraint remains the sole conflict referee.
    try {
      const avail = await api.get(`/pitches/${pitchId}/availability?date=${selDayStr}`, { _silent: true });
      const freshBooked: BookedSlot[] = avail.data.booked_slots ?? [];
      setBooked(freshBooked);
      setOpenWindows(avail.data.open_windows ?? []);
      setHasSchedule(!!avail.data.has_schedule);
      if (overlapsBooked(absStart.getTime(), absEnd.getTime(), freshBooked)) {
        setApiError('هذا الموعد لم يعد متاحاً');
        setSelectedStart(null);
        setSubmitting(false);
        inFlightRef.current = false;
        return;
      }
    } catch {
      // Non-fatal — the server's GIST EXCLUDE constraint is the authoritative
      // conflict guard; proceed and let the 409 path below handle it if needed.
    }

    try {
      await api.post('/bookings', {
        pitch_id:    pitchId,
        start_time:  absStart.toISOString(),
        end_time:    absEnd.toISOString(),
        total_price: total,
      }, {
        _silent: true,
        headers: { 'Idempotency-Key': idempotencyKey },
      });
      setSuccess(true);
      setTimeout(() => router.push('/bookings'), 1800);
    } catch (err) {
      if (axios.isAxiosError(err)) {
        const code = err.response?.data?.error as string | undefined;
        const msg  = err.response?.data?.message as string | undefined;
        if (err.response?.status === 409 || code === 'slot_unavailable') {
          // GIST EXCLUDE fired — slot was taken concurrently.
          // Stay on screen, clear the selected slot, re-fetch the grid.
          setApiError('هذا الموعد لم يعد متاحاً');
          setSelectedStart(null);
          api.get(`/pitches/${pitchId}/availability?date=${selDayStr}`)
            .then(r => {
              setBooked(r.data.booked_slots ?? []);
              setOpenWindows(r.data.open_windows ?? []);
              setHasSchedule(!!r.data.has_schedule);
            })
            .catch(() => { /* stale grid is safe — user will retry */ });
        } else if (code === 'outside_operating_hours')
          setApiError('الوقت المطلوب خارج ساعات عمل الملعب، اختر وقتاً آخر');
        else if (err.response?.status === 401)
          // _silent suppresses the interceptor's redirect; surface here instead.
          setApiError('حدث خطأ، يرجى المحاولة مرة أخرى');
        else if (code === 'invalid_time' || code === 'invalid_duration' || code === 'insufficient_lead_time')
          setApiError(msg ?? 'الوقت المحدد غير صالح');
        else
          setApiError(msg ?? 'حدث خطأ ما، يرجى المحاولة مرة أخرى');
      } else {
        setApiError('تعذّر الاتصال بالخادم، تحقق من اتصالك');
      }
    } finally {
      setSubmitting(false);
      inFlightRef.current = false;
    }
  }

  // ── OTP modal callbacks ───────────────────────────────────────────────────

  function handleOtpDismiss() {
    // Fields (name, phone, slot) are intact — user can edit and retry.
    setOtpOpen(false);
  }

  async function handleOtpVerified(verifiedUser: User) {
    // /auth/verify-otp succeeded. httpOnly cookies + CSRF cookie are set.
    setOtpOpen(false);
    login(verifiedUser);

    // Step 1 — JIT name: write the guest-typed name only when the returned
    // profile has none. Never overwrite an existing verified user's name.
    if (!verifiedUser.full_name?.trim() && isValidFullName(guestName)) {
      try {
        await saveFullName(guestName);
      } catch {
        // Non-fatal: a failed name write doesn't block the booking.
      }
    }

    // Steps 2-4 — create the booking now that the session is live.
    // createBooking() handles: idempotency key, fresh avail check, POST,
    // 2xx → redirect, 409 → stay + re-fetch grid.
    await createBooking();
  }

  // ── Submit ────────────────────────────────────────────────────────────────

  async function handleSubmit() {
    if (!canSubmit) return;
    // Re-entry guard only — do NOT acquire the ref here. createBooking is the
    // SOLE acquirer/releaser (it sets true on entry, resets in its finally). If
    // handleSubmit set it true, the delegated createBooking call below would see
    // it already true and bail out instantly, hanging the button. The pre-steps
    // (saveFullName / OTP open) are synchronous-to-dispatch, so this read guard
    // is enough to block a double-tap before createBooking takes over.
    if (inFlightRef.current) return;
    setSubmitting(true);
    setApiError(null);

    // JIT name capture: persist the collected name BEFORE booking. The core
    // booking call is unchanged; this is a separate, prior PATCH /me.
    if (needsName) {
      try {
        await saveFullName(nameInput);
        await refreshUser();
      } catch {
        setApiError('تعذّر حفظ الاسم، تأكد من إدخال اسم صحيح وحاول مجدداً');
        setSubmitting(false);
        inFlightRef.current = false;
        return;
      }
    }

    // ── Guest field validation ─────────────────────────────────────────────
    if (isGuest) {
      const { e164, error: phoneErr } = normalizePhone(guestPhone);
      const nameValid = isValidFullName(guestName);
      setGuestNameTouched(true);
      setGuestPhoneTouched(true);
      if (!nameValid || !e164) {
        if (phoneErr) setGuestPhoneError(phoneErr);
        setSubmitting(false);
        inFlightRef.current = false;
        return;
      }
      setGuestPhoneError(null);

      if (BOOKING_OTP_REQUIRED) {
        // OTP flow: open the modal — it fires onVerified with the session-bearing
        // User after /auth/verify-otp succeeds; the booking POST then happens in
        // handleOtpVerified.
        setOtpOpen(true);
        setSubmitting(false);
        inFlightRef.current = false;
        return;
      }

      // MVP no-OTP flow: establish a session from name + JO phone (no code), then
      // book. ValidateJOMobile still runs server-side on this endpoint.
      try {
        const { data } = await api.post('/auth/booking-session', {
          phone: e164,
          full_name: guestName.trim(),
        }, { _silent: true });
        login(data.data.user);
      } catch (err) {
        if (axios.isAxiosError(err) && err.response?.data?.error === 'invalid_phone') {
          setGuestPhoneError('يرجى إدخال رقم هاتف أردني محمول صحيح');
        } else {
          setApiError('تعذّر بدء الحجز، حاول مجدداً');
        }
        setSubmitting(false);
        inFlightRef.current = false;
        return;
      }
      // Session is live — create the booking. createBooking owns inFlightRef, so
      // release it here first (handleSubmit only read-guarded it).
      setSubmitting(false);
      inFlightRef.current = false;
      await createBooking();
      return;
    }

    await createBooking();
  }

  // ── Success screen ────────────────────────────────────────────────────────

  if (success) {
    return (
      <div className={`${plexArabic.className} rounded-2xl bg-[#0a0c0b] border border-[#1a1f1c] p-8 flex flex-col items-center gap-5 text-center`}>
        <div className="w-14 h-14 rounded-full bg-[#124631]/40 border border-[#2fdc8f]/25 flex items-center justify-center">
          <CheckCircle2 size={28} className="text-[#2fdc8f]" aria-hidden />
        </div>
        <div>
          <h3 className="text-[18px] font-bold text-[#f2f5f3] mb-1.5">تم الحجز بنجاح!</h3>
          <p className="text-[12px] text-[#8a938e]">جاري التحويل إلى صفحة حجوزاتك...</p>
        </div>
        <div className="w-5 h-5 rounded-full border-2 border-[#2fdc8f] border-t-transparent animate-spin" />
      </div>
    );
  }

  // ── Loading screen (waiting for server time) ──────────────────────────────

  if (!serverTodayStr || !selDayStr) {
    return (
      <div className={`${plexArabic.className} rounded-2xl bg-[#0a0c0b] border border-[#1a1f1c] p-8 flex items-center justify-center h-44`}>
        <div className="w-5 h-5 rounded-full border-2 border-[#2fdc8f] border-t-transparent animate-spin" />
      </div>
    );
  }

  // ── Grouped slots for render ──────────────────────────────────────────────
  const groups = GROUP_DEFS
    .map(g => ({
      ...g,
      sub: g.from >= 1440 ? `فجر ${nextDayName}` : null,
      slots: starts.filter(m => m >= g.from && m < g.to),
    }))
    .filter(g => g.slots.length > 0);

  // ── Main render ───────────────────────────────────────────────────────────

  return (
    <>
    <div dir="rtl" className={`${plexArabic.className} rounded-2xl bg-[#0a0c0b] border border-[#1a1f1c] overflow-hidden max-w-[430px] mx-auto w-full relative`}>

      {/* ── Header ── */}
      <header className="px-5 pt-[22px] pb-3.5 border-b border-[#1a1f1c] flex flex-col gap-1">
        <span className="text-[12px] font-semibold text-[#2fdc8f] tracking-[.5px]">احجز الملعب</span>
        <h2 className="text-[22px] font-bold text-[#f2f5f3] leading-snug m-0">اختر الموعد المناسب</h2>
      </header>

      {/* ── Pitch selector — multi-pitch venues only (Gate 1c) ── */}
      {pitchOptions && pitchOptions.length > 1 && onPitchChange && (
        <div className="px-5 pt-4">
          <p className="flex items-center gap-1.5 text-[12px] font-semibold text-[#8a938e] mb-2.5">
            <Users size={11} className="text-[#2fdc8f]" aria-hidden />
            الملعب
          </p>
          <div
            className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
            role="group"
            aria-label="اختيار الملعب"
          >
            {pitchOptions.map(opt => {
              const selected = opt.id === pitchId;
              return (
                <button
                  key={opt.id}
                  type="button"
                  onClick={() => { if (!selected) onPitchChange(opt.id); }}
                  aria-pressed={selected}
                  className={[
                    'flex-shrink-0 flex flex-col items-center gap-0.5 px-4 py-2.5 min-w-[88px]',
                    'rounded-[14px] border-[1.5px] transition-all duration-150 select-none',
                    selected
                      ? 'bg-[#0f2a1e] border-[#2fdc8f] text-[#eafff4]'
                      : 'bg-[#141715] border-[#212823] text-[#aab3ad] cursor-pointer',
                  ].join(' ')}
                >
                  <span className="text-[12px] leading-tight whitespace-nowrap font-semibold">{opt.label}</span>
                  {opt.subline && (
                    <span className={`text-[9px] font-semibold whitespace-nowrap ${selected ? 'text-[#5ceaa8]' : 'text-[#8a938e]'}`}>
                      {opt.subline}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        </div>
      )}

      {/* ── Date row ── */}
      <div className="px-5 pt-4 pb-2 flex items-center justify-between">
        <span className="text-[12px] font-semibold text-[#8a938e] whitespace-nowrap">التاريخ</span>
        <div className="flex items-center gap-3">
          <span className="text-[11px] text-[#5c655f] whitespace-nowrap">
            {monthDayLabel(sevenDays[0])} — {monthDayLabel(sevenDays[6])}
          </span>
          <button
            type="button"
            onClick={() => setShowDatePicker(v => !v)}
            aria-expanded={showDatePicker}
            className="flex items-center gap-1 text-[11px] text-[#5c655f] hover:text-[#2fdc8f] transition-colors duration-150"
          >
            <CalendarDays size={12} />
          </button>
        </div>
      </div>

      {/* ── Date strip ── */}
      <div className="flex gap-2 overflow-x-auto px-5 pt-2.5 pb-3.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {stripDays.map(dayStr => {
          const date       = parseDateStr(dayStr);
          const isSelected = selDayStr === dayStr;
          const isToday    = dayStr === serverTodayStr;
          const isTomorrow = dayStr === addDays(serverTodayStr, 1);
          const isPrevLive = prevSessionLive && dayStr === addDays(serverTodayStr, -1);
          const badge = isPrevLive ? 'مستمرة الآن' : isToday ? 'اليوم' : isTomorrow ? 'غداً' : null;

          return (
            <button
              key={dayStr}
              type="button"
              onClick={() => handleDaySelect(dayStr)}
              aria-pressed={isSelected}
              className={[
                'relative flex-shrink-0 flex flex-col items-center gap-0.5',
                'rounded-[14px] border-[1.5px] min-w-[64px] px-2 pt-2.5 pb-[9px]',
                'transition-all duration-150 select-none cursor-pointer',
                isSelected
                  ? 'bg-[#0f2a1e] border-[#2fdc8f] text-[#eafff4]'
                  : 'bg-[#141715] border-[#212823] text-[#aab3ad]',
              ].join(' ')}
            >
              {badge && (
                <span className={[
                  'absolute -top-[7px] left-1/2 -translate-x-1/2',
                  'px-2 py-px rounded-full text-[9px] font-bold whitespace-nowrap',
                  isSelected ? 'bg-[#2fdc8f] text-[#06231a]' : 'bg-[#263029] text-[#c9d2cc]',
                ].join(' ')}>
                  {badge}
                </span>
              )}
              <span className="text-[11px] font-medium opacity-85">{AR_DAYS[date.getDay()]}</span>
              <span className="text-[18px] font-bold leading-none">{date.getDate()}</span>
            </button>
          );
        })}
      </div>

      {/* Expandable date picker for dates beyond the 7-day strip */}
      <div className={[
        'overflow-hidden transition-all duration-300 ease-in-out px-5',
        showDatePicker ? 'max-h-24 opacity-100 pb-2' : 'max-h-0 opacity-0',
      ].join(' ')}>
        <input
          type="date"
          value={selDayStr}
          min={serverTodayStr}
          max={maxDateStr}
          onChange={e => { if (e.target.value) handleDaySelect(e.target.value); }}
          className={[
            'w-full rounded-xl border border-[#212823] px-4 py-2.5',
            'bg-[#111412] text-[13px] text-[#f2f5f3]',
            'hover:border-[#2fdc8f]/40 focus:outline-none focus:border-[#2fdc8f]/60',
            'transition-all duration-150 [color-scheme:dark]',
          ].join(' ')}
        />
      </div>

      {/* Selected date badge when outside the strip */}
      {!stripDays.includes(selDayStr) && (
        <div className="mx-5 mb-2 px-3 py-2 rounded-xl bg-[#0f2a1e] border border-[#2fdc8f]/30 text-[11px] text-[#5ceaa8] text-center">
          {parseDateStr(selDayStr).toLocaleDateString('ar-JO', {
            weekday: 'long', year: 'numeric', month: 'long', day: 'numeric',
          })}
        </div>
      )}

      {/* ── Start-time section label ── */}
      <div className="px-5 pt-2.5 pb-1.5 flex items-baseline justify-between gap-2.5">
        <span className="text-[12px] font-semibold text-[#8a938e] whitespace-nowrap">وقت البداية</span>
        <span className="text-[11px] text-[#5c655f] whitespace-nowrap">تُعرض الأوقات المتاحة فقط</span>
      </div>

      {loadingSlots ? (
        <div className="h-44 flex items-center justify-center">
          <div className="w-5 h-5 rounded-full border-2 border-[#2fdc8f] border-t-transparent animate-spin" />
        </div>
      ) : groups.length === 0 ? (
        /* ── Empty state: zero available starts this day ── */
        <div className="mx-5 my-2.5 px-5 py-[26px] border border-dashed border-[#263029] rounded-[14px] text-[#8a938e] text-[13px] text-center leading-[1.8]">
          لا توجد أوقات متاحة في هذا اليوم.<br />جرّب يوماً آخر من شريط التاريخ.
        </div>
      ) : (
        /* ── Time groups ── */
        <div className="flex flex-col gap-[18px] px-5 pt-1.5 pb-4">
          {groups.map(g => (
            <div key={g.label} className="flex flex-col gap-2.5">
              <div className="flex items-center gap-2.5">
                <span className="text-[13px] font-semibold text-[#c9d2cc] whitespace-nowrap">{g.label}</span>
                {g.sub && (
                  <span className="text-[11px] text-[#2fdc8f] bg-[#10231a] border border-[#1d3a2c] rounded-full px-2.5 py-0.5 whitespace-nowrap">
                    {g.sub}
                  </span>
                )}
                <span className="flex-1 h-px bg-[#1a1f1c]" aria-hidden />
              </div>
              <div className="grid grid-cols-4 gap-[9px]" role="group" aria-label={g.label}>
                {g.slots.map(m => {
                  const sel = selectedStart === m;
                  return (
                    <button
                      key={m}
                      type="button"
                      dir="ltr"
                      onClick={() => pickStart(m)}
                      aria-pressed={sel}
                      className={[
                        'text-[14px] font-semibold py-3 px-1 rounded-xl cursor-pointer',
                        'border-[1.5px] transition-all duration-[120ms] select-none',
                        sel
                          ? 'bg-[#0f2a1e] border-[#2fdc8f] text-[#5ceaa8] shadow-[0_0_0_3px_rgba(47,220,143,0.12)]'
                          : 'bg-[#141715] border-[#212823] text-[#dfe6e1]',
                      ].join(' ')}
                    >
                      {fmt24(m)}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* ── Duration section (after start selection) ── */}
      {started && (
        <div className="px-5 pb-4 flex flex-col gap-2.5">
          <span className="text-[12px] font-semibold text-[#8a938e]">المدة</span>
          <div className="grid grid-cols-3 gap-[9px]">
            {([60, 90, 120] as const).map(d => {
              const ok    = validDur(selectedStart!, d);
              const sel   = duration === d;
              const price = Math.round((d / 60) * pricePerHour * 100) / 100;
              return (
                <button
                  key={d}
                  type="button"
                  onClick={() => handleDurationClick(d)}
                  disabled={!ok}
                  aria-pressed={sel && ok}
                  className={[
                    'flex flex-col items-center gap-[3px] py-3 px-1.5 rounded-xl',
                    'border-[1.5px] transition-all duration-[120ms] select-none',
                    !ok
                      ? 'bg-[#101312] border-[#1a1f1c] text-[#5c655f] cursor-not-allowed opacity-60'
                      : sel
                        ? 'bg-[#0f2a1e] border-[#2fdc8f] text-[#eafff4] cursor-pointer'
                        : 'bg-[#141715] border-[#212823] text-[#dfe6e1] cursor-pointer',
                  ].join(' ')}
                >
                  <span className="text-[14px] font-bold">{durationLabel(d)}</span>
                  <span dir="rtl" className={[
                    'text-[10.5px]',
                    sel && ok ? 'text-[#5ceaa8]' : ok ? 'text-[#8a938e]' : 'text-[#5c655f]',
                  ].join(' ')}>
                    {ok ? `ينتهي ${fmt24(selectedStart! + d)} · ${price} د.أ` : 'غير متاح'}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}

      {/* ── JIT full-name capture (only when missing) ── */}
      {needsName && (
        <div className="mx-5 mb-4 rounded-xl border border-[#1a1f1c] bg-[#111412] px-4 py-3.5">
          <p className="text-[11px] text-[#8a938e] mb-2.5 leading-relaxed">
            أدخل اسمك مرة واحدة لإتمام الحجز
          </p>
          <FullNameField
            value={nameInput}
            onChange={(v) => { setNameInput(v); setNameTouched(true); }}
            disabled={submitting}
            showError={nameTouched}
            id="booking-full-name"
          />
        </div>
      )}

      {/* ── Guest fields — name / phone ── */}
      {/* Gate on !authLoading so the block never flashes during the /auth/me
          in-flight window: a returning user is null until the probe resolves. */}
      {!authLoading && isGuest && (
        <div className="mx-5 mb-4 rounded-xl border border-[#212823] bg-[#111412] px-4 py-4 flex flex-col gap-4">
          <p className="text-[11px] font-bold text-[#8a938e] tracking-wide">بياناتك</p>

          {/* Name. This block renders only inside the guest path (outer
              `{isGuest && …}` gate), so a returning user with a stored name
              never reaches it — the field is already hidden for them. TS narrows
              `user` to null here, which is exactly the "(!user || !full_name)"
              contract: the field shows for a guest / new-no-name account only. */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="guest-name" className="text-[11px] font-bold text-[#c9d2cc]">
              الاسم الكامل <span className="text-[#2fdc8f]">*</span>
            </label>
            <input
              id="guest-name"
              type="text"
              dir="rtl"
              value={guestName}
              disabled={submitting}
              onChange={(e) => { setGuestName(e.target.value); setGuestNameTouched(true); }}
              onBlur={() => setGuestNameTouched(true)}
              placeholder="مثال: أحمد خالد"
              aria-invalid={guestNameTouched && !isValidFullName(guestName) || undefined}
              className={[
                'w-full rounded-xl px-4 py-2.5 bg-[#141715] text-[13px] text-[#f2f5f3]',
                'border transition-all duration-150 focus:outline-none',
                'placeholder:text-[#5c655f] disabled:opacity-50',
                guestNameTouched && !isValidFullName(guestName)
                  ? 'border-red-500/50 focus:ring-1 focus:ring-red-500/30'
                  : 'border-[#212823] hover:border-[#2fdc8f]/30 focus:border-[#2fdc8f]/60',
              ].join(' ')}
            />
            {guestNameTouched && !isValidFullName(guestName) && (
              <span className="text-[11px] text-red-400/80">الاسم يجب أن يكون بين حرفين و100 حرف</span>
            )}
          </div>

          {/* Phone */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="guest-phone" className="text-[11px] font-bold text-[#c9d2cc]">
              رقم الجوال <span className="text-[#2fdc8f]">*</span>
            </label>
            <input
              id="guest-phone"
              type="tel"
              dir="ltr"
              value={guestPhone}
              disabled={submitting}
              onChange={(e) => {
                setGuestPhone(e.target.value);
                setGuestPhoneTouched(true);
                const { error } = normalizePhone(e.target.value);
                setGuestPhoneError(e.target.value.trim() ? error : null);
              }}
              onBlur={() => {
                if (guestPhone.trim()) {
                  setGuestPhoneTouched(true);
                  setGuestPhoneError(normalizePhone(guestPhone).error);
                }
              }}
              placeholder="07XXXXXXXX"
              aria-invalid={!!guestPhoneError || undefined}
              className={[
                'w-full rounded-xl px-4 py-2.5 bg-[#141715] text-[13px] text-[#f2f5f3] font-mono',
                'border transition-all duration-150 focus:outline-none',
                'placeholder:text-[#5c655f] disabled:opacity-50',
                guestPhoneTouched && guestPhoneError
                  ? 'border-red-500/50 focus:ring-1 focus:ring-red-500/30'
                  : 'border-[#212823] hover:border-[#2fdc8f]/30 focus:border-[#2fdc8f]/60',
              ].join(' ')}
            />
            {guestPhoneTouched && guestPhoneError && (
              <span className="text-[11px] text-red-400/80">{guestPhoneError}</span>
            )}
            {guestPhoneTouched && !guestPhoneError && guestPhone.trim() && (
              <span className="text-[11px] text-[#5ceaa8]/70 font-mono">
                {normalizePhone(guestPhone).e164}
              </span>
            )}
          </div>
        </div>
      )}

      {/* ── API error ── */}
      {apiError && (
        <div
          role="alert"
          className="mx-5 mb-3 rounded-xl px-4 py-3 text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] leading-relaxed"
        >
          {apiError}
        </div>
      )}

      {/* ── Sticky summary bar ── */}
      <div className="sticky bottom-0 bg-gradient-to-t from-[#0a0c0b] from-75% to-transparent px-5 pt-[18px] pb-5">
        <div className="bg-[#111412] border border-[#212823] rounded-[18px] px-[18px] py-4 flex flex-col gap-3 shadow-[0_-8px_30px_rgba(0,0,0,0.45)]">
          {started ? (
            <div className="flex items-center justify-between gap-3">
              <div className="flex flex-col gap-[3px]">
                <span className="text-[11px] text-[#8a938e]">
                  {AR_DAYS[parseDateStr(selDayStr).getDay()]} {monthDayLabel(selDayStr)}
                </span>
                <span dir="ltr" className="text-[17px] font-bold text-[#f2f5f3] text-right">
                  {fmt24(selectedStart!)} → {fmt24(endMins)}
                </span>
                {crossesMidnight && (
                  <span className="text-[11px] text-[#2fdc8f]">يمتد الحجز إلى فجر {nextDayName}</span>
                )}
                {startsAfterMid && (
                  <span className="text-[11px] text-[#2fdc8f]">هذا الوقت فجر {nextDayName}</span>
                )}
              </div>
              <div className="flex flex-col items-start gap-0.5">
                <span className="text-[11px] text-[#8a938e]">إجمالي الحجز</span>
                <span className="text-[22px] font-bold text-[#2fdc8f] whitespace-nowrap">{total} د.أ</span>
              </div>
            </div>
          ) : (
            <span className="text-[13px] text-[#8a938e] text-center">اختر وقت البداية لعرض ملخص الحجز</span>
          )}
          <button
            type="button"
            onClick={handleSubmit}
            disabled={authLoading || !canSubmit}
            className={[
              'flex items-center justify-center gap-2.5 w-full py-3.5 rounded-[13px]',
              'text-[15px] font-bold transition-all duration-150',
              !authLoading && canSubmit
                ? 'bg-[#2fdc8f] text-[#06231a] cursor-pointer hover:bg-[#5ceaa8]'
                : 'bg-[#1a1f1c] text-[#5c655f] cursor-not-allowed',
            ].join(' ')}
          >
            {authLoading ? (
              <div className="w-4 h-4 rounded-full border-2 border-[#5c655f]/40 border-t-[#5c655f] animate-spin" />
            ) : submitting ? (
              <>
                <div className="w-4 h-4 rounded-full border-2 border-[#06231a]/25 border-t-[#06231a] animate-spin" />
                جاري الحجز...
              </>
            ) : canSubmit ? (
              'تأكيد الحجز'
            ) : started && durationOK ? (
              'أكمل بياناتك للمتابعة'
            ) : (
              'اختر الوقت أولاً'
            )}
          </button>
        </div>
      </div>

    </div>

    {/* OTP modal — rendered outside the booking card via portal-like fixed positioning.
        Only mounts for unauthenticated guests; already-authed users never reach this. */}
    {otpOpen && (
      <OtpModal
        phone={normalizePhone(guestPhone).e164 ?? guestPhone}
        onVerified={handleOtpVerified}
        onDismiss={handleOtpDismiss}
      />
    )}
    </>
  );
}
