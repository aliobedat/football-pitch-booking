-- ============================================================
-- EXTENSIONS
-- btree_gist is required for EXCLUDE constraints on non-geometric
-- types (like tsrange combined with integer columns)
-- ============================================================
CREATE EXTENSION IF NOT EXISTS btree_gist;


-- ============================================================
-- ENUMS
-- ============================================================
CREATE TYPE user_role    AS ENUM ('player', 'owner');
CREATE TYPE pitch_surface AS ENUM ('natural_grass', 'artificial_turf', 'futsal_court');
CREATE TYPE booking_status AS ENUM ('pending', 'confirmed', 'cancelled');


-- ============================================================
-- TABLE: users
-- Unified table for both Players and Pitch Owners.
-- ============================================================
CREATE TABLE users (
    id              SERIAL          PRIMARY KEY,
    full_name       VARCHAR(100)    NOT NULL,
    email           VARCHAR(255)    NOT NULL UNIQUE,
    phone           VARCHAR(20),
    password_hash   TEXT            NOT NULL,
    role            user_role       NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

-- Fast lookup by email (login path)
CREATE INDEX idx_users_email ON users (email);


-- ============================================================
-- TABLE: pitches
-- Owned by a user with role = 'owner'.
-- ============================================================
CREATE TABLE pitches (
    id              SERIAL          PRIMARY KEY,
    owner_id        INT             NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name            VARCHAR(150)    NOT NULL,
    description     TEXT,
    surface_type    pitch_surface   NOT NULL DEFAULT 'artificial_turf',
    capacity        SMALLINT        NOT NULL CHECK (capacity > 0),  -- max players
    price_per_hour  NUMERIC(10, 3)  NOT NULL CHECK (price_per_hour > 0), -- JOD
    latitude        DECIMAL(9, 6),
    longitude       DECIMAL(9, 6),
    city            VARCHAR(100),
    address         TEXT,
    is_active       BOOLEAN         NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_pitches_owner   ON pitches (owner_id);
CREATE INDEX idx_pitches_city    ON pitches (city);
CREATE INDEX idx_pitches_active  ON pitches (is_active);


-- ============================================================
-- TABLE: bookings
-- Core table. The EXCLUDE constraint is the anti-double-booking engine.
-- ============================================================
CREATE TABLE bookings (
    id              SERIAL          PRIMARY KEY,
    pitch_id        INT             NOT NULL REFERENCES pitches (id) ON DELETE RESTRICT,
    player_id       INT             NOT NULL REFERENCES users (id)  ON DELETE RESTRICT,

    -- tsrange stores [start_time, end_time) — lower inclusive, upper exclusive
    -- This models time slots correctly: a 19:00-21:00 booking does NOT
    -- conflict with a 21:00-23:00 booking.
    booking_range   TSRANGE         NOT NULL,

    status          booking_status  NOT NULL DEFAULT 'pending',
    total_price     NUMERIC(10, 3)  NOT NULL CHECK (total_price >= 0),
    notes           TEXT,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    -- -------------------------------------------------------
    -- CONSTRAINT: Prevent empty or reversed time ranges
    -- -------------------------------------------------------
    CONSTRAINT chk_valid_range
        CHECK (
            NOT isempty(booking_range)
            AND lower(booking_range) IS NOT NULL
            AND upper(booking_range) IS NOT NULL
        ),

    -- -------------------------------------------------------
    -- CONSTRAINT: Minimum booking duration — 1 hour
    -- -------------------------------------------------------
    CONSTRAINT chk_min_duration
        CHECK (
            upper(booking_range) - lower(booking_range) >= INTERVAL '1 hour'
        ),

    -- -------------------------------------------------------
    -- CONSTRAINT: No overlapping bookings for the same pitch.
    -- Uses GiST index under the hood — enforced at commit time,
    -- making it race-condition-proof even under concurrent transactions.
    -- Cancelled bookings are excluded from the conflict check.
    -- -------------------------------------------------------
    EXCLUDE USING GIST (
        pitch_id    WITH =,
        booking_range WITH &&
    ) WHERE (status <> 'cancelled')
);

-- Standard query indexes
CREATE INDEX idx_bookings_pitch    ON bookings (pitch_id);
CREATE INDEX idx_bookings_player   ON bookings (player_id);
CREATE INDEX idx_bookings_status   ON bookings (status);

-- GiST index to accelerate range queries (availability checks)
CREATE INDEX idx_bookings_range_gist
    ON bookings USING GIST (pitch_id, booking_range);