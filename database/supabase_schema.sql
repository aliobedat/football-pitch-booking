-- ================================================================
-- FOOTBALL PITCH BOOKING PLATFORM — SUPABASE SCHEMA
-- Single combined script: original design + all migrations applied.
-- Run once in the Supabase SQL Editor (or psql) on a clean database.
-- ================================================================


-- ────────────────────────────────────────────────────────────────
-- EXTENSIONS
-- btree_gist is required for the EXCLUDE anti-double-booking
-- constraint, which mixes an integer column (pitch_id) with a
-- range operator (&&) in the same GiST index.
-- ────────────────────────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS btree_gist;


-- ────────────────────────────────────────────────────────────────
-- ENUMS
-- ────────────────────────────────────────────────────────────────
CREATE TYPE user_role      AS ENUM ('player', 'owner');
CREATE TYPE booking_status AS ENUM ('pending', 'confirmed', 'cancelled');


-- ────────────────────────────────────────────────────────────────
-- TABLE: users
-- Unified table for players and pitch owners.
-- Role is enforced at the application layer (JWT claim).
-- ────────────────────────────────────────────────────────────────
CREATE TABLE users (
    id            SERIAL        PRIMARY KEY,
    full_name     VARCHAR(100)  NOT NULL,
    email         VARCHAR(255)  NOT NULL UNIQUE,
    phone         VARCHAR(20),
    password_hash TEXT          NOT NULL,
    role          user_role     NOT NULL,
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- Fast lookup on the login path
CREATE INDEX idx_users_email ON users (email);


-- ────────────────────────────────────────────────────────────────
-- TABLE: refresh_tokens
-- Stores the SHA-256 hash of each issued refresh token.
-- One-time-use: the token is marked revoked on first consumption,
-- preventing replay attacks even if a token is intercepted.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE refresh_tokens (
    id          SERIAL       PRIMARY KEY,
    user_id     INT          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  TEXT         NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ  NOT NULL,
    revoked     BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id);
-- Hash lookup must be fast — it runs on every API request that uses a refresh token
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens (token_hash);


-- ────────────────────────────────────────────────────────────────
-- TABLE: pitches
-- Owned by a user with role = 'owner'.
-- surface / format are free-form TEXT (not enums) to allow the
-- owner dashboard to add new types without a migration.
-- amenities is a PostgreSQL TEXT array.
-- pitch_hue is a CSS rgba() string used for the UI diagram fill.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE pitches (
    id             SERIAL         PRIMARY KEY,
    owner_id       INT            REFERENCES users (id) ON DELETE RESTRICT,
    name           VARCHAR(150)   NOT NULL,
    neighborhood   VARCHAR(100)   NOT NULL DEFAULT '',
    surface        TEXT           NOT NULL DEFAULT 'artificial_grass',
    format         TEXT           NOT NULL DEFAULT 'خماسي',
    price_per_hour NUMERIC(10, 3) NOT NULL CHECK (price_per_hour > 0),
    description    TEXT           NOT NULL DEFAULT '',
    image_url      TEXT           NOT NULL DEFAULT '',
    rating         NUMERIC(4, 2)  NOT NULL DEFAULT 0.00,
    review_count   INT            NOT NULL DEFAULT 0,
    is_featured    BOOLEAN        NOT NULL DEFAULT FALSE,
    amenities      TEXT[]         NOT NULL DEFAULT '{}',
    pitch_hue      TEXT           NOT NULL DEFAULT 'rgba(16,71,50,0.65)',
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_pitches_owner    ON pitches (owner_id);
CREATE INDEX idx_pitches_featured ON pitches (is_featured);


-- ────────────────────────────────────────────────────────────────
-- TABLE: bookings
-- Uses start_time / end_time (TIMESTAMPTZ) rather than a tsrange
-- column so that plain timestamp comparisons work without casting.
--
-- The EXCLUDE constraint is the race-condition-proof double-booking
-- guard: PostgreSQL enforces it at commit time inside any concurrent
-- transaction, making application-level locking unnecessary.
-- Cancelled bookings are excluded from the overlap check so that a
-- cancelled slot can be re-booked immediately.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE bookings (
    id          SERIAL         PRIMARY KEY,
    pitch_id    INT            NOT NULL REFERENCES pitches (id) ON DELETE RESTRICT,
    user_id     INT            NOT NULL REFERENCES users   (id) ON DELETE RESTRICT,
    start_time  TIMESTAMPTZ    NOT NULL,
    end_time    TIMESTAMPTZ    NOT NULL,
    status      booking_status NOT NULL DEFAULT 'pending',
    total_price NUMERIC(10, 3) NOT NULL CHECK (total_price >= 0),
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),

    -- Reject reversed or zero-length intervals at write time
    CONSTRAINT chk_booking_times
        CHECK (end_time > start_time),

    -- Enforce the business rule: minimum 1-hour booking
    CONSTRAINT chk_min_duration
        CHECK (end_time - start_time >= INTERVAL '1 hour'),

    -- Anti-double-booking: no two active bookings for the same pitch
    -- may overlap. tstzrange is used because the columns are TIMESTAMPTZ.
    -- btree_gist allows mixing the integer pitch_id with the range &&.
    EXCLUDE USING GIST (
        pitch_id                               WITH =,
        tstzrange(start_time, end_time, '[)')  WITH &&
    ) WHERE (status <> 'cancelled')
);

CREATE INDEX idx_bookings_pitch  ON bookings (pitch_id);
CREATE INDEX idx_bookings_user   ON bookings (user_id);
CREATE INDEX idx_bookings_status ON bookings (status);

-- B-tree index for the availability query:
--   WHERE pitch_id = $1 AND start_time < $day_end AND end_time > $day_start
CREATE INDEX idx_bookings_times  ON bookings (pitch_id, start_time, end_time);
