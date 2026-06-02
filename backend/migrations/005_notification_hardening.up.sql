-- ================================================================
-- Migration 005 (UP) — Notification hardening (PART 6)
--
-- Makes notification delivery production-grade:
--   * users.opt_out — an explicit consent WITHDRAWAL flag. Distinct from
--     opt_in (the AUTHENTICATION/OTP consent grant): opt_out, once true,
--     STRICTLY blocks every outbound message to the user regardless of kind
--     (checked at the app layer by the NotificationService opt-out gate).
--   * notification_jobs — a Postgres-backed outbox/queue. Every async dispatch
--     is persisted here and drained by a background worker with exponential
--     backoff; permanent failures terminate in a dead-letter state. No external
--     broker (Redis/RabbitMQ) is introduced — pure Postgres + Go.
--   * message_deliveries — per-provider-message delivery status, updated from
--     the Meta Cloud API status webhooks (sent/delivered/read/failed).
--
-- Paired with 005_notification_hardening.down.sql. Idempotent where Postgres
-- allows (IF NOT EXISTS); run exactly once forward.
-- ================================================================


-- ────────────────────────────────────────────────────────────────
-- 1. users.opt_out — explicit consent withdrawal.
--    Default FALSE: existing users have not opted out. The NotificationService
--    refuses ALL message kinds (OTP + booking events) for a user whose
--    opt_out is true. Withdrawing consent (POST /notifications/opt-out) sets
--    this true and clears opt_in in the same write.
-- ────────────────────────────────────────────────────────────────
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS opt_out BOOLEAN NOT NULL DEFAULT FALSE;


-- ────────────────────────────────────────────────────────────────
-- 2. notification_jobs — the durable outbox/queue.
--    envelope holds the full channel-agnostic OutboundMessage (recipient,
--    kind, typed payload) as JSON; recipient/kind are denormalised columns for
--    observability and indexing. The worker claims due jobs
--    (status='pending' AND next_attempt_at <= now) with FOR UPDATE SKIP LOCKED,
--    so multiple workers never process the same job.
--
--    status lifecycle:
--      pending     — awaiting its (back-off) attempt time
--      processing  — claimed by a worker, in flight
--      succeeded   — accepted by the provider
--      dead_letter — exhausted max_attempts (permanent delivery failure)
--      blocked     — refused permanently for policy/validation reasons
--                    (recipient opted out, malformed message) — never retried
-- ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notification_jobs (
    id                  BIGSERIAL    PRIMARY KEY,
    recipient           TEXT         NOT NULL,
    kind                TEXT         NOT NULL,
    envelope            JSONB        NOT NULL,
    status              TEXT         NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'succeeded', 'dead_letter', 'blocked')),
    attempts            INT          NOT NULL DEFAULT 0,
    max_attempts        INT          NOT NULL DEFAULT 5,
    next_attempt_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_error          TEXT,
    provider_message_id TEXT,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- The claim query always filters by (status, next_attempt_at); this partial
-- index keeps it cheap as succeeded/dead-lettered rows accumulate.
CREATE INDEX IF NOT EXISTS idx_notification_jobs_due
    ON notification_jobs (next_attempt_at)
    WHERE status = 'pending';


-- ────────────────────────────────────────────────────────────────
-- 3. message_deliveries — provider delivery status, keyed by the upstream
--    message id. A row is born when the worker successfully hands a message to
--    the provider (status='sent') and is advanced by the Cloud API status
--    webhook (delivered/read) or marked failed. Keyed UNIQUE on
--    provider_message_id so webhook updates UPSERT idempotently — even if the
--    status callback races ahead of the worker's own record.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS message_deliveries (
    id                  BIGSERIAL    PRIMARY KEY,
    provider_message_id TEXT         NOT NULL UNIQUE,
    job_id              BIGINT       REFERENCES notification_jobs (id) ON DELETE SET NULL,
    recipient           TEXT,
    status              TEXT         NOT NULL
        CHECK (status IN ('sent', 'delivered', 'read', 'failed')),
    error_code          INT,
    error_title         TEXT,
    raw                 JSONB,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
