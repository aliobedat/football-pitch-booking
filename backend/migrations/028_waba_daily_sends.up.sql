-- 028_waba_daily_sends.up.sql  (WhatsApp unverified-tier quota guard — GATE 2)
--
-- A per-WABA, per-UTC-day counter of WhatsApp BUSINESS-INITIATED notification sends
-- (booking_confirmed / booking_cancelled / booking_reminder). It exists to keep an
-- unverified Meta Business Portfolio under its ~250/day messaging ceiling: the send
-- path reserves a slot (atomic increment) before each gated WhatsApp send, warns as
-- it nears the cap, and refuses (→ SMS fallback) once the cap is reached.
--
-- OTP (AUTHENTICATION-category) is DELIBERATELY NOT counted here — it bypasses the
-- guard entirely so login is never blocked by booking-notification volume.
--
-- Day boundary is the UTC calendar day. This is a NEW bucket convention, but it does
-- not collide with anything: the only other time-window logic (OTP rate limits) uses
-- rolling windows, and no migration buckets by ::date. Meta's real limit is a rolling
-- 24h window; a UTC calendar day is a deliberately conservative approximation (it can
-- only refuse earlier, never later).

CREATE TABLE IF NOT EXISTS waba_daily_sends (
    -- The WhatsApp Business Account id the send is billed against (from config).
    waba_id   TEXT NOT NULL,
    -- The UTC calendar day this bucket counts (supplied by the app clock).
    send_date DATE NOT NULL,
    -- Number of gated WhatsApp sends reserved for (waba_id, send_date). Includes
    -- refused-over-cap attempts by design (the upsert increments unconditionally),
    -- so it doubles as a "demand vs cap" signal.
    count     INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (waba_id, send_date)
);
