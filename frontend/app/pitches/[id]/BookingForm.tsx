'use client';

import { useState, useEffect, useMemo, useRef } from 'react';
import { useRouter } from 'next/navigation';
import { CalendarDays, CheckCircle2 } from 'lucide-react';
import axios from 'axios';
import api from '@/lib/api';
import { useAuth, type User } from '@/context/AuthContext';
import FullNameField, { isValidFullName, saveFullName } from '@/components/FullNameField';
import OtpModal from './OtpModal';

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

// LocalRange is an OpenWindow projected onto the selected civil day's minute axis
// (minutes from that day's Amman midnight), clamped to [0, 1440] so the half-hour
// grid can test containment without any timezone math of its own.
interface LocalRange {
  start: number;
  end:   number;
}

interface Props {
  pitchId:      number;
  pricePerHour: number;
}

// ─── Constants ────────────────────────────────────────────────────────────────

const AR_DAYS = ['الأحد', 'الاثنين', 'الثلاثاء', 'الأربعاء', 'الخميس', 'الجمعة', 'السبت'];
const MAX_BOOKING_DAYS = 365;

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

function minsToTime(mins: number): string {
  return `${String(Math.floor(mins / 60)).padStart(2, '0')}:${String(mins % 60).padStart(2, '0')}`;
}

function buildDateTime(dateStr: string, timeStr: string): Date {
  const [y, mo, d] = dateStr.split('-').map(Number);
  const [h, m] = timeStr.split(':').map(Number);
  return new Date(y, mo - 1, d, h, m, 0, 0);
}

function isSlotBooked(slotMins: number, booked: BookedSlot[], dateStr: string): boolean {
  const slotStart = buildDateTime(dateStr, minsToTime(slotMins)).getTime();
  const slotEnd   = slotStart + 30 * 60 * 1000;
  return booked.some(b => {
    const bStart = new Date(b.start_time).getTime();
    const bEnd   = new Date(b.end_time).getTime();
    return bStart < slotEnd && bEnd > slotStart;
  });
}

function rangeOverlapsBookings(
  startMins: number,
  endMins:   number,
  booked:    BookedSlot[],
  dateStr:   string,
): boolean {
  const rangeStart = buildDateTime(dateStr, minsToTime(startMins)).getTime();
  const rangeEnd   = buildDateTime(dateStr, minsToTime(endMins)).getTime();
  return booked.some(b => {
    const bStart = new Date(b.start_time).getTime();
    const bEnd   = new Date(b.end_time).getTime();
    return bStart < rangeEnd && bEnd > rangeStart;
  });
}

// projectWindowToDay maps an absolute-UTC open window onto the civil day `dayStr`
// (Amman), returning minutes-from-midnight clamped to [0, 1440], or null if the
// window does not touch that day. A window that started the day BEFORE clamps its
// start to 0 (open from midnight); one that ends the day AFTER clamps its end to
// 1440 (open through midnight) — so the early-hours tail of a cross-midnight
// window and a window that runs past midnight are both represented correctly on
// the day's axis. Date strings are YYYY-MM-DD, so lexical comparison is date order.
function projectWindowToDay(w: OpenWindow, dayStr: string): LocalRange | null {
  const s = getAmmanParts(new Date(w.start));
  const e = getAmmanParts(new Date(w.end));

  let start: number;
  if (s.dateStr < dayStr) start = 0;                       // began before today → from midnight
  else if (s.dateStr === dayStr) start = s.hours * 60 + s.minutes;
  else return null;                                        // begins after today

  let end: number;
  if (e.dateStr > dayStr) end = 24 * 60;                   // ends after today → through midnight
  else if (e.dateStr === dayStr) end = e.hours * 60 + e.minutes;
  else return null;                                        // ended before today

  if (end <= start) return null;
  return { start, end };
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

// Returns 12-hour display string: 0→"12", 1→"01" … 11→"11", 12→"12", 13→"01" …
function displayHour(h: number): string {
  const d = h % 12 === 0 ? 12 : h % 12;
  return String(d).padStart(2, '0');
}

// Formats minutes-from-midnight as "02:00 م" / "11:30 ص" etc.
function formatTime12(totalMins: number): string {
  const h    = Math.floor(totalMins / 60) % 24;
  const m    = totalMins % 60;
  const hr12 = (h === 0 || h === 12) ? 12 : h > 12 ? h - 12 : h;
  return `${String(hr12).padStart(2, '0')}:${String(m).padStart(2, '0')} ${h < 12 ? 'ص' : 'م'}`;
}

// ─────────────────────────────────────────────────────────────────────────────
// Component
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingForm({ pitchId, pricePerHour }: Props) {
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
  const [smsConsent,        setSmsConsent]        = useState(false);
  const [consentAt,         setConsentAt]         = useState<string | null>(null);
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
  const [selDayStr,     setSelDayStr]     = useState<string>('');
  const [showDatePicker, setShowDatePicker] = useState(false);

  useEffect(() => {
    if (serverTodayStr && !selDayStr) setSelDayStr(serverTodayStr);
  }, [serverTodayStr, selDayStr]);

  // ── AM/PM — smart default tied to server time ─────────────────────────────
  const [amPm, setAmPm] = useState<'am' | 'pm'>('am');

  useEffect(() => {
    if (!serverNow || !selDayStr || !serverTodayStr) return;
    if (selDayStr === serverTodayStr) {
      const { hours } = getAmmanParts(serverNow);
      setAmPm(hours < 12 ? 'am' : 'pm');
    } else {
      setAmPm('am');
    }
  }, [selDayStr, serverNow, serverTodayStr]);

  // ── Time selection ────────────────────────────────────────────────────────
  const [baseHour, setBaseHour] = useState<number | null>(null);
  const [startMod, setStartMod] = useState<0 | 30>(0);
  const [duration, setDuration] = useState<60 | 90 | 120>(60);

  // ── Availability data ─────────────────────────────────────────────────────
  const [booked,       setBooked]       = useState<BookedSlot[]>([]);
  // Operating hours for the selected day. hasSchedule=false → the pitch is
  // unconfigured and OPEN 24/7 (the server's fail-open decision); openWindows is
  // then irrelevant. hasSchedule=true with no covering window for a slot → CLOSED
  // (rendered distinctly from "booked").
  const [openWindows,  setOpenWindows]  = useState<OpenWindow[]>([]);
  const [hasSchedule,  setHasSchedule]  = useState(false);
  const [loadingSlots, setLoadingSlots] = useState(false);
  const [submitting,   setSubmitting]   = useState(false);
  const [apiError,     setApiError]     = useState<string | null>(null);
  const [success,      setSuccess]      = useState(false);

  useEffect(() => {
    if (!selDayStr) return;
    setLoadingSlots(true);
    setBaseHour(null);
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

  // ── Open-hours projection for the selected day ─────────────────────────────
  // null = open 24/7 (unconfigured pitch); otherwise the day's covering ranges.
  const openRanges = useMemo<LocalRange[] | null>(() => {
    if (!hasSchedule) return null;
    return openWindows
      .map(w => projectWindowToDay(w, selDayStr))
      .filter((r): r is LocalRange => r !== null);
  }, [openWindows, hasSchedule, selDayStr]);

  // ── Server "now" in minutes (Amman time, today only) ──────────────────────
  const nowMins = useMemo(() => {
    if (!serverNow || !selDayStr || selDayStr !== serverTodayStr) return -1;
    const { hours, minutes } = getAmmanParts(serverNow);
    return hours * 60 + minutes;
  }, [serverNow, selDayStr, serverTodayStr]);

  // ── 7-day rolling strip ───────────────────────────────────────────────────
  const sevenDays = useMemo(
    () => serverTodayStr ? Array.from({ length: 7 }, (_, i) => addDays(serverTodayStr, i)) : [],
    [serverTodayStr],
  );

  const maxDateStr = useMemo(
    () => serverTodayStr ? addDays(serverTodayStr, MAX_BOOKING_DAYS) : '',
    [serverTodayStr],
  );

  // ── Grid hours for selected half ──────────────────────────────────────────
  const gridHours = amPm === 'am'
    ? [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]
    : [12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23];

  // ── Slot availability predicates ──────────────────────────────────────────

  function slotIsPast(mins: number): boolean {
    return nowMins >= 0 && mins <= nowMins;
  }

  function slotIsBooked(mins: number): boolean {
    return isSlotBooked(mins, booked, selDayStr);
  }

  // rangeIsOpen: is [startMins, endMins] fully inside a single open window?
  // (containment, not overlap — a range straddling a split-shift gap is closed.)
  // null openRanges = open 24/7.
  function rangeIsOpen(startMins: number, endMins: number): boolean {
    if (openRanges === null) return true;
    return openRanges.some(r => r.start <= startMins && endMins <= r.end);
  }

  // A 30-min slot is closed when it is not contained by any open window.
  function slotIsClosed(mins: number): boolean {
    return !rangeIsOpen(mins, mins + 30);
  }

  function slotUnavailable(mins: number): boolean {
    return slotIsPast(mins) || slotIsBooked(mins) || slotIsClosed(mins);
  }

  function isHourDisabled(h: number): boolean {
    return slotUnavailable(h * 60) && slotUnavailable(h * 60 + 30);
  }

  function isHourFullyBooked(h: number): boolean {
    return slotIsBooked(h * 60) && slotIsBooked(h * 60 + 30);
  }

  function isHourFullyPast(h: number): boolean {
    return nowMins >= 0 && h * 60 + 30 <= nowMins;
  }

  // Both half-slots of the hour fall outside operating hours (and aren't booked) →
  // render as "مغلق" (closed), visually distinct from "محجوز" (booked).
  function isHourFullyClosed(h: number): boolean {
    return slotIsClosed(h * 60) && slotIsClosed(h * 60 + 30)
      && !slotIsBooked(h * 60) && !slotIsBooked(h * 60 + 30);
  }

  // ── Fine-tuning validation ────────────────────────────────────────────────

  function isModDisabled(mod: 0 | 30): boolean {
    if (baseHour === null) return true;
    return slotUnavailable(baseHour * 60 + mod);
  }

  function isDurationDisabled(d: 60 | 90 | 120): boolean {
    if (baseHour === null) return true;
    const startMs = baseHour * 60 + startMod;
    const endMs   = startMs + d;
    if (endMs > 24 * 60) return true;
    if (rangeOverlapsBookings(startMs, endMs, booked, selDayStr)) return true;
    // The whole range must lie inside one open window (out-of-hours → disabled).
    return !rangeIsOpen(startMs, endMs);
  }

  // ── Empty state check for current AM/PM half ──────────────────────────────
  const hasAvailableHours = gridHours.some(h => !isHourDisabled(h));

  // The pitch is configured but has no open window covering ANY part of the
  // selected day → it is closed all day (distinct from "fully booked").
  const dayClosed = openRanges !== null && openRanges.length === 0;

  // ── Derived booking values ────────────────────────────────────────────────
  const actualStartMins = baseHour !== null ? baseHour * 60 + startMod : -1;
  const actualEndMins   = actualStartMins >= 0 ? actualStartMins + duration : -1;
  const actualStartStr  = actualStartMins >= 0 ? minsToTime(actualStartMins) : null;
  const actualEndStr    = actualEndMins   >= 0 ? minsToTime(actualEndMins)   : null;
  const total           = actualStartMins >= 0
    ? Math.round((duration / 60) * pricePerHour * 100) / 100 : 0;

  // A returning user's stored name satisfies validity without re-entry; a guest
  // (or a new account with no name yet) must supply a valid guestName.
  const nameValid = (user?.full_name?.trim() ?? guestName.trim()).length >= 2;
  // Keep the isGuest gate so the separate needsName/FullNameField path (an authed
  // user who never set a name) stays governed by nameOK above, not by nameValid.
  const guestNameOK  = !isGuest || nameValid;
  const guestPhoneOK = !isGuest || normalizePhone(guestPhone).e164 !== null;

  const canSubmit =
    baseHour !== null &&
    !isModDisabled(startMod) &&
    !isDurationDisabled(duration) &&
    nameOK &&
    guestNameOK &&
    guestPhoneOK &&
    !submitting;

  // ── Handlers ─────────────────────────────────────────────────────────────

  function handleDaySelect(dayStr: string) {
    setSelDayStr(dayStr);
    setBaseHour(null);
    setApiError(null);
    setShowDatePicker(false);
  }

  function handleAmPm(val: 'am' | 'pm') {
    setAmPm(val);
    setBaseHour(null);
    setApiError(null);
  }

  function handleHourClick(h: number) {
    if (isHourDisabled(h)) return;
    setApiError(null);
    if (baseHour === h) { setBaseHour(null); return; }
    setBaseHour(h);
    const newMod = (!slotUnavailable(h * 60) ? 0 : 30) as 0 | 30;
    setStartMod(newMod);
    setDuration(60);
  }

  function handleModClick(mod: 0 | 30) {
    if (isModDisabled(mod)) return;
    setStartMod(mod);
    setApiError(null);
  }

  function handleDurationClick(d: 60 | 90 | 120) {
    if (isDurationDisabled(d)) return;
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

    // Fresh availability check — catches the race where another player booked
    // the same slot while the OTP window was open (or between submit and POST).
    try {
      const avail = await api.get(`/pitches/${pitchId}/availability?date=${selDayStr}`, { _silent: true });
      const freshBooked: BookedSlot[] = avail.data.booked_slots ?? [];
      setBooked(freshBooked);
      setOpenWindows(avail.data.open_windows ?? []);
      setHasSchedule(!!avail.data.has_schedule);
      if (rangeOverlapsBookings(actualStartMins, actualEndMins, freshBooked, selDayStr)) {
        setApiError('هذا الموعد لم يعد متاحاً');
        setBaseHour(null);
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
        start_time:  buildDateTime(selDayStr, actualStartStr!).toISOString(),
        end_time:    buildDateTime(selDayStr, actualEndStr!).toISOString(),
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
          // GIST EXCLUDE fired — slot was taken during the OTP window.
          // Stay on screen, clear the selected slot, re-fetch the grid.
          setApiError('هذا الموعد لم يعد متاحاً');
          setBaseHour(null);
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
        else if (code === 'invalid_time' || code === 'invalid_duration')
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
    if (!canSubmit || !actualStartStr || !actualEndStr) return;
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

      // Guest fields are valid. Open the OTP modal — the modal fires onVerified
      // with the session-bearing User after /auth/verify-otp succeeds. The booking
      // POST happens in handleOtpVerified once the caller confirms the chain.
      setOtpOpen(true);
      setSubmitting(false);
      inFlightRef.current = false;
      return;
    }

    await createBooking();
  }

  // ── Success screen ────────────────────────────────────────────────────────

  if (success) {
    return (
      <div className="rounded-2xl bg-[#141715] border border-white/[0.07] p-8 flex flex-col items-center gap-5 text-center">
        <div className="w-14 h-14 rounded-full bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
          <CheckCircle2 size={28} className="text-emerald-500" aria-hidden />
        </div>
        <div>
          <h3 className="text-[18px] font-bold text-[#f0efe8] mb-1.5">تم الحجز بنجاح!</h3>
          <p className="text-[12px] text-white/35">جاري التحويل إلى صفحة حجوزاتك...</p>
        </div>
        <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  // ── Shared option-button style factory ────────────────────────────────────

  const optionBtn = (selected: boolean, disabled: boolean, extra = '') =>
    [
      'rounded-xl border transition-all duration-150 font-bold select-none',
      'flex items-center justify-center',
      selected
        ? 'bg-emerald-500/20 border-emerald-500/40 text-emerald-300'
        : disabled
          ? 'bg-white/[0.01] border-white/[0.03] text-white/15 cursor-not-allowed opacity-40'
          : [
              'bg-white/[0.04] border-white/[0.08] text-white/50 cursor-pointer',
              'hover:bg-emerald-500/10 hover:border-emerald-500/25 hover:text-emerald-300',
            ].join(' '),
      extra,
    ].join(' ');

  // ── Loading screen (waiting for server time) ──────────────────────────────

  if (!serverTodayStr || !selDayStr) {
    return (
      <div className="rounded-2xl bg-[#141715] border border-white/[0.07] p-8 flex items-center justify-center h-44">
        <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  // ── Main render ───────────────────────────────────────────────────────────

  return (
    <>
    <div className="rounded-2xl bg-[#141715] border border-white/[0.07] overflow-hidden">

      {/* ── Header ── */}
      <div className="px-6 pt-6 pb-5 border-b border-white/[0.05]">
        <p className="text-[10px] font-bold tracking-widest text-emerald-500 uppercase mb-1.5">
          احجز الملعب
        </p>
        <h2 className="text-[20px] font-bold text-[#f0efe8] tracking-tight leading-snug">
          اختر الموعد المناسب
        </h2>
      </div>

      <div className="px-6 py-5 flex flex-col gap-6">

        {/* ── 7-day rolling strip + date picker ── */}
        <div>
          <div className="flex items-center justify-between mb-3">
            <p className="flex items-center gap-1.5 text-[10px] font-bold text-white/30 tracking-widest uppercase">
              <CalendarDays size={11} className="text-emerald-500" aria-hidden />
              التاريخ
            </p>
            <button
              type="button"
              onClick={() => setShowDatePicker(v => !v)}
              aria-expanded={showDatePicker}
              className="flex items-center gap-1 text-[10px] text-white/30 hover:text-emerald-400 transition-colors duration-150"
            >
              <CalendarDays size={12} />
              <span>تاريخ آخر</span>
            </button>
          </div>

          {/* Horizontally scrollable 7-day strip */}
          <div className="flex gap-2 overflow-x-auto pt-3 pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {sevenDays.map(dayStr => {
              const date      = parseDateStr(dayStr);
              const dow       = date.getDay();
              const dd        = date.getDate();
              const isSelected = selDayStr === dayStr;
              const isToday    = dayStr === serverTodayStr;
              const isTomorrow = dayStr === addDays(serverTodayStr, 1);

              return (
                <button
                  key={dayStr}
                  type="button"
                  onClick={() => handleDaySelect(dayStr)}
                  aria-pressed={isSelected}
                  className={[
                    'relative flex-shrink-0 flex flex-col items-center gap-1',
                    'rounded-xl border px-3 py-2.5 min-w-[56px]',
                    'transition-all duration-150 select-none',
                    isSelected
                      ? 'bg-emerald-500/20 border-emerald-500/40 text-emerald-300'
                      : 'bg-white/[0.03] border-white/[0.07] text-white/50 hover:bg-white/[0.06] hover:border-white/[0.14]',
                  ].join(' ')}
                >
                  {(isToday || isTomorrow) && (
                    <span className={[
                      'absolute -top-2.5 left-1/2 -translate-x-1/2',
                      'px-1.5 py-px rounded-full text-[8px] font-bold whitespace-nowrap',
                      isSelected ? 'bg-emerald-500 text-white' : 'bg-white/10 text-white/50',
                    ].join(' ')}>
                      {isToday ? 'اليوم' : 'غداً'}
                    </span>
                  )}
                  <span className="text-[9px] font-bold tracking-wide">{AR_DAYS[dow]}</span>
                  <span className="text-[18px] font-bold leading-none font-mono">{dd}</span>
                </button>
              );
            })}
          </div>

          {/* Expandable date picker for dates beyond the 7-day strip */}
          <div className={[
            'overflow-hidden transition-all duration-300 ease-in-out',
            showDatePicker ? 'max-h-24 opacity-100 mt-2' : 'max-h-0 opacity-0',
          ].join(' ')}>
            <input
              type="date"
              value={selDayStr}
              min={serverTodayStr}
              max={maxDateStr}
              onChange={e => { if (e.target.value) handleDaySelect(e.target.value); }}
              className={[
                'w-full rounded-xl border border-white/[0.09] px-4 py-2.5',
                'bg-[#0d0f0e] text-[13px] text-[#f0efe8]',
                'hover:border-white/[0.18] focus:outline-none',
                'focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/[0.12]',
                'transition-all duration-150 [color-scheme:dark]',
              ].join(' ')}
            />
          </div>

          {/* Selected date badge when outside the 7-day strip */}
          {!sevenDays.includes(selDayStr) && (
            <div className="mt-2 px-3 py-2 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-[11px] text-emerald-400 text-center">
              {parseDateStr(selDayStr).toLocaleDateString('ar-JO', {
                weekday: 'long',
                year:    'numeric',
                month:   'long',
                day:     'numeric',
              })}
            </div>
          )}
        </div>

        {loadingSlots ? (
          <div className="h-44 flex items-center justify-center">
            <div className="w-5 h-5 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
          </div>
        ) : (
          <>
            {/* ── AM / PM Toggle ── */}
            <div>
              <p className="text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
                الفترة
              </p>
              <div className="flex rounded-xl border border-white/[0.08] bg-[#0d0f0e] p-1 gap-1">
                {(['am', 'pm'] as const).map(val => (
                  <button
                    key={val}
                    type="button"
                    onClick={() => handleAmPm(val)}
                    className={[
                      'flex-1 py-2.5 rounded-lg text-[13px] font-bold tracking-wide',
                      'transition-all duration-150 border',
                      amPm === val
                        ? 'bg-emerald-500/20 border-emerald-500/30 text-emerald-300'
                        : 'border-transparent text-white/35 hover:text-white/60 hover:bg-white/[0.04]',
                    ].join(' ')}
                  >
                    {val === 'am' ? 'صباحاً' : 'مساءً'}
                  </button>
                ))}
              </div>
            </div>

            {/* ── 12-Hour Grid or Empty State ── */}
            {dayClosed ? (
              <div className="flex flex-col items-center gap-3 py-8 text-center">
                <div className="w-10 h-10 rounded-full bg-white/[0.04] border border-white/[0.07] flex items-center justify-center [background-image:repeating-linear-gradient(45deg,transparent,transparent_4px,rgba(255,255,255,0.03)_4px,rgba(255,255,255,0.03)_8px)]">
                  <CalendarDays size={18} className="text-white/20" aria-hidden />
                </div>
                <div>
                  <p className="text-[13px] font-bold text-white/40 mb-1">الملعب مغلق هذا اليوم</p>
                  <p className="text-[11px] text-white/20">اختر يوماً آخر لعرض المواعيد المتاحة</p>
                </div>
              </div>
            ) : !hasAvailableHours ? (
              <div className="flex flex-col items-center gap-3 py-8 text-center">
                <div className="w-10 h-10 rounded-full bg-white/[0.04] border border-white/[0.07] flex items-center justify-center">
                  <CalendarDays size={18} className="text-white/20" aria-hidden />
                </div>
                <div>
                  <p className="text-[13px] font-bold text-white/40 mb-1">لا مواعيد متاحة لهذا اليوم</p>
                  <p className="text-[11px] text-white/20">
                    {amPm === 'am' ? 'جرب فترة المساء أو اختر يوماً آخر' : 'جرب فترة الصباح أو اختر يوماً آخر'}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => handleAmPm(amPm === 'am' ? 'pm' : 'am')}
                  className="px-4 py-2 rounded-xl text-[11px] font-bold text-emerald-400 bg-emerald-500/10 border border-emerald-500/20 hover:bg-emerald-500/20 transition-all duration-150"
                >
                  {amPm === 'am' ? 'تبديل إلى المساء' : 'تبديل إلى الصباح'}
                </button>
              </div>
            ) : (
              <div>
                <p className="text-[10px] font-bold text-white/30 tracking-widest uppercase mb-3">
                  الساعة
                </p>
                <div role="group" aria-label="اختر الساعة" className="grid grid-cols-4 gap-2">
                  {gridHours.map(h => {
                    const fullyPast     = isHourFullyPast(h);
                    const fullyBooked   = isHourFullyBooked(h);
                    const fullyClosed   = isHourFullyClosed(h);
                    const disabled      = isHourDisabled(h);
                    const selected      = baseHour === h;
                    const partialBooked = !fullyBooked && (slotIsBooked(h * 60) || slotIsBooked(h * 60 + 30));

                    // Strict 12-hour mapping — never shows raw 24-hr values
                    // 0→12, 1-11→01-11, 12→12, 13-23→01-11
                    const hr12  = (h === 0 || h === 12) ? 12 : h > 12 ? h - 12 : h;
                    const label = String(hr12).padStart(2, '0') + ':00';

                    const edgeNote = h === 0 ? ' منتصف الليل' : h === 12 ? ' الظهر' : '';

                    return (
                      <button
                        key={h}
                        type="button"
                        onClick={() => handleHourClick(h)}
                        disabled={disabled}
                        aria-pressed={selected}
                        aria-label={label + edgeNote}
                        title={
                          fullyBooked ? 'محجوز'
                          : fullyClosed ? 'خارج ساعات العمل'
                          : fullyPast  ? 'انقضى الوقت'
                          : disabled   ? 'لا توجد أوقات متاحة في هذه الساعة'
                          : undefined
                        }
                        className={[
                          'h-14 rounded-xl border transition-all duration-150 select-none',
                          'flex flex-col items-center justify-center gap-0.5',
                          fullyPast
                            ? 'opacity-30 pointer-events-none bg-white/[0.02] border-white/[0.04] text-white/20'
                            : fullyBooked
                              ? 'bg-red-950/40 text-red-400 border-red-800/50 cursor-not-allowed'
                              : fullyClosed
                                ? 'bg-white/[0.015] border-white/[0.05] text-white/20 cursor-not-allowed [background-image:repeating-linear-gradient(45deg,transparent,transparent_5px,rgba(255,255,255,0.025)_5px,rgba(255,255,255,0.025)_10px)]'
                                : selected
                                  ? 'bg-emerald-500/25 border-emerald-400/60 text-emerald-300 shadow-[0_0_14px_rgba(52,211,153,0.15)]'
                                  : disabled
                                    ? 'bg-white/[0.01] border-white/[0.03] text-white/10 cursor-not-allowed'
                                    : [
                                        'bg-white/[0.03] border-white/[0.07] text-white/55 cursor-pointer',
                                        'hover:bg-emerald-500/10 hover:border-emerald-500/30 hover:text-emerald-300',
                                        partialBooked ? 'border-dashed border-white/[0.12]' : '',
                                      ].join(' '),
                        ].join(' ')}
                      >
                        {/* Single inline string — hour and :00 are never split across elements */}
                        <span className="text-[14px] font-mono font-bold leading-none tracking-wide">
                          {label}
                        </span>
                        {fullyBooked && (
                          <span className="text-[8px] font-bold tracking-wider">محجوز</span>
                        )}
                        {!fullyBooked && fullyClosed && (
                          <span className="text-[8px] font-bold tracking-wider">مغلق</span>
                        )}
                      </button>
                    );
                  })}
                </div>
              </div>
            )}

            {/* ── Fine-Tuning Panel — slides in smoothly via max-height transition ── */}
            <div
              className={[
                'overflow-hidden transition-all duration-300 ease-in-out',
                baseHour !== null ? 'max-h-[420px] opacity-100' : 'max-h-0 opacity-0',
              ].join(' ')}
            >
              <div className="rounded-xl border border-white/[0.08] bg-[#0d0f0e] p-4 flex flex-col gap-5">

                {/* Start modifier :00 / :30 */}
                <div>
                  <p className="text-[10px] font-bold text-white/25 tracking-widest uppercase mb-2.5">
                    وقت البداية بالضبط
                  </p>
                  <div className="grid grid-cols-2 gap-2">
                    {([0, 30] as const).map(mod => {
                      const dis = isModDisabled(mod);
                      const sel = startMod === mod;
                      const tip = !dis ? undefined
                        : slotIsPast(baseHour! * 60 + mod) ? 'الوقت انقضى' : 'محجوز';
                      return (
                        <button
                          key={mod}
                          type="button"
                          onClick={() => handleModClick(mod)}
                          disabled={dis}
                          title={tip}
                          aria-pressed={sel}
                          className={optionBtn(sel, dis, 'py-3 text-[14px] font-mono')}
                        >
                          {minsToTime(baseHour! * 60 + mod)}
                        </button>
                      );
                    })}
                  </div>
                </div>

                {/* Duration — shows live price per option */}
                <div>
                  <p className="text-[10px] font-bold text-white/25 tracking-widest uppercase mb-2.5">
                    المدة
                  </p>
                  <div className="grid grid-cols-3 gap-2">
                    {([60, 90, 120] as const).map(d => {
                      const dis   = isDurationDisabled(d);
                      const sel   = duration === d;
                      const price = Math.round((d / 60) * pricePerHour * 100) / 100;
                      return (
                        <button
                          key={d}
                          type="button"
                          onClick={() => handleDurationClick(d)}
                          disabled={dis}
                          title={dis ? 'يتعارض مع حجز موجود أو يتجاوز منتصف الليل' : undefined}
                          aria-pressed={sel}
                          className={optionBtn(sel, dis, 'py-3 text-[11px] flex-col gap-0.5')}
                        >
                          <span>{durationLabel(d)}</span>
                          <span className={[
                            'text-[9px] font-mono',
                            sel ? 'text-emerald-400/70' : 'text-white/25',
                          ].join(' ')}>
                            {price} د.أ
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              </div>
            </div>

            {/* ── Legend ── */}
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[10px] text-white/25">
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-white/[0.05] border border-white/[0.08]" aria-hidden />
                متاح
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-red-950/40 border border-red-800/50" aria-hidden />
                محجوز
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-white/[0.02] border border-white/[0.04] opacity-30" aria-hidden />
                منتهي
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm border border-white/[0.08] [background-image:repeating-linear-gradient(45deg,transparent,transparent_2px,rgba(255,255,255,0.06)_2px,rgba(255,255,255,0.06)_4px)]" aria-hidden />
                مغلق
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-sm bg-emerald-500/25 border border-emerald-400/60" aria-hidden />
                محدد
              </span>
            </div>
          </>
        )}

        {/* ── Price summary ── */}
        {canSubmit && actualStartStr && actualEndStr && (
          <div className="flex items-center justify-between px-4 py-3.5 rounded-xl bg-[#0d0f0e] border border-white/[0.05]">
            <div>
              <p className="text-[9px] font-bold text-white/20 tracking-widest uppercase mb-0.5">
                إجمالي الحجز
              </p>
              <p className="text-[11px] text-white/30 font-mono">
                {formatTime12(actualStartMins)} - {formatTime12(actualEndMins)}
              </p>
            </div>
            <div className="flex items-baseline gap-1">
              <span className="text-[30px] font-bold text-[#f0efe8] leading-none tracking-tight">
                {total.toFixed(2)}
              </span>
              <span className="text-[13px] font-bold text-emerald-500">د.أ</span>
            </div>
          </div>
        )}

        {/* ── JIT full-name capture (only when missing) ── */}
        {needsName && (
          <div className="rounded-xl border border-white/[0.05] bg-[#0d0f0e] px-4 py-3.5">
            <p className="text-[11px] text-white/35 mb-2.5 leading-relaxed">
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

        {/* ── Guest fields — name / phone / SMS consent ── */}
        {/* Gate on !authLoading so the block never flashes during the /auth/me
            in-flight window: a returning user is null until the probe resolves. */}
        {!authLoading && isGuest && (
          <div className="rounded-xl border border-white/[0.08] bg-[#0d0f0e] px-4 py-4 flex flex-col gap-4">
            <p className="text-[11px] font-bold text-white/30 tracking-widest uppercase">
              بياناتك
            </p>

            {/* Name. This block renders only inside the guest path (outer
                `{isGuest && …}` gate), so a returning user with a stored name
                never reaches it — the field is already hidden for them. TS narrows
                `user` to null here, which is exactly the "(!user || !full_name)"
                contract: the field shows for a guest / new-no-name account only. */}
            <div className="flex flex-col gap-1.5">
              <label htmlFor="guest-name" className="text-[11px] font-bold text-white/40 tracking-wide">
                الاسم الكامل <span className="text-emerald-500">*</span>
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
                  'w-full rounded-xl px-4 py-2.5 bg-[#121413] text-[13px] text-[#f0efe8]',
                  'border transition-all duration-150 focus:outline-none',
                  'placeholder:text-white/20 disabled:opacity-50',
                  guestNameTouched && !isValidFullName(guestName)
                    ? 'border-red-500/50 focus:ring-1 focus:ring-red-500/30'
                    : 'border-white/[0.09] hover:border-white/[0.18] focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15',
                ].join(' ')}
              />
              {guestNameTouched && !isValidFullName(guestName) && (
                <span className="text-[11px] text-red-400/80">الاسم يجب أن يكون بين حرفين و100 حرف</span>
              )}
            </div>

            {/* Phone */}
            <div className="flex flex-col gap-1.5">
              <label htmlFor="guest-phone" className="text-[11px] font-bold text-white/40 tracking-wide">
                رقم الجوال <span className="text-emerald-500">*</span>
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
                  'w-full rounded-xl px-4 py-2.5 bg-[#121413] text-[13px] text-[#f0efe8] font-mono',
                  'border transition-all duration-150 focus:outline-none',
                  'placeholder:text-white/20 disabled:opacity-50',
                  guestPhoneTouched && guestPhoneError
                    ? 'border-red-500/50 focus:ring-1 focus:ring-red-500/30'
                    : 'border-white/[0.09] hover:border-white/[0.18] focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15',
                ].join(' ')}
              />
              {guestPhoneTouched && guestPhoneError && (
                <span className="text-[11px] text-red-400/80">{guestPhoneError}</span>
              )}
              {guestPhoneTouched && !guestPhoneError && guestPhone.trim() && (
                <span className="text-[11px] text-emerald-500/70 font-mono">
                  {normalizePhone(guestPhone).e164}
                </span>
              )}
            </div>

            {/* SMS consent */}
            <label className="flex items-start gap-3 cursor-pointer select-none group">
              <div className="relative mt-0.5 flex-shrink-0">
                <input
                  type="checkbox"
                  checked={smsConsent}
                  disabled={submitting}
                  onChange={(e) => {
                    setSmsConsent(e.target.checked);
                    setConsentAt(e.target.checked ? new Date().toISOString() : null);
                  }}
                  className="sr-only"
                />
                <div className={[
                  'w-4 h-4 rounded border transition-all duration-150',
                  smsConsent
                    ? 'bg-emerald-500 border-emerald-500'
                    : 'bg-transparent border-white/25 group-hover:border-white/40',
                ].join(' ')}>
                  {smsConsent && (
                    <svg viewBox="0 0 12 12" fill="none" className="w-full h-full p-0.5" aria-hidden>
                      <path d="M2 6l3 3 5-5" stroke="white" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  )}
                </div>
              </div>
              <span className="text-[11px] text-white/40 leading-relaxed group-hover:text-white/55 transition-colors duration-150">
                أوافق على حفظ رقم جوالي وتقديمه لصاحب الملعب للتواصل معي بعد تأكيد الحجز
              </span>
            </label>
          </div>
        )}

        {/* ── API error ── */}
        {apiError && (
          <div
            role="alert"
            className="rounded-xl px-4 py-3 text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] leading-relaxed"
          >
            {apiError}
          </div>
        )}

        {/* ── Confirm button ── */}
        {/* While auth is resolving, show a neutral, non-actionable loading state
            rather than an active/disabled state derived from incomplete user data. */}
        <button
          type="button"
          onClick={handleSubmit}
          disabled={authLoading || !canSubmit}
          className={[
            'flex items-center justify-center gap-2.5 w-full py-3.5 rounded-xl mb-1',
            'text-[13px] font-bold tracking-wide',
            'transition-all duration-200 active:scale-[0.98]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
            'focus-visible:ring-offset-2 focus-visible:ring-offset-[#141715]',
            !authLoading && canSubmit
              ? 'bg-gradient-to-r from-green-600 to-emerald-500 text-white ' +
                'shadow-[0_4px_20px_rgba(16,185,129,0.22)] hover:shadow-[0_4px_28px_rgba(16,185,129,0.38)]'
              : 'bg-white/[0.04] text-white/20 border border-white/[0.05] cursor-not-allowed',
          ].join(' ')}
        >
          {authLoading ? (
            <div className="w-4 h-4 rounded-full border-2 border-white/25 border-t-white/60 animate-spin" />
          ) : submitting ? (
            <>
              <div className="w-4 h-4 rounded-full border-2 border-white/25 border-t-white animate-spin" />
              جاري الحجز...
            </>
          ) : (
            'تأكيد الحجز'
          )}
        </button>

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
