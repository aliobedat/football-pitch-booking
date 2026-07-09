-- ═══════════════════════════════════════════════════════════════════════════
-- Malaeb — CANONICAL SCHEMA BASELINE (scratch/test DBs)
--
-- Regenerated 2026-07-09 from the LIVE production schema (schema-only, no
-- data) via WO-SCHEMA-DRIFT-RECONCILIATION:
--
--     pg_dump --schema-only --no-owner --no-privileges "$DATABASE_URL"
--
-- This file IS the full current schema: migrations 002–032 (and the historical
-- out-of-band ALTERs) are already baked in. Do NOT replay backend/migrations/*
-- on a scratch built from this file — they remain in the repo as history and
-- as the manual-apply path for PRODUCTION only.
--
-- MAINTENANCE RULE: whenever a new migration is applied to production,
-- regenerate this file with the command above so scratch/test DBs stay
-- faithful. (No schema_migrations ledger exists; live is ground truth.)
--
-- Scratch recipe (Neon): CREATE DATABASE scratch_<name>; then
-- ALTER DATABASE scratch_<name> SET search_path TO public; load this file;
-- connect via the UNPOOLED host (drop "-pooler" from the hostname) — the
-- pooler resets session defaults and rejects startup options.
-- ═══════════════════════════════════════════════════════════════════════════

--
-- PostgreSQL database dump
--

\restrict Z4HayVqQ7NBc88J4NJwB9UwVfWeuk3lWAmDVHq6SzUN67OXrfv4lnbiNecZCFjX

-- Dumped from database version 17.10 (21f7c76)
-- Dumped by pg_dump version 18.4

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: btree_gist; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;


--
-- Name: EXTENSION btree_gist; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION btree_gist IS 'support for indexing common datatypes in GiST';


--
-- Name: booking_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.booking_status AS ENUM (
    'pending',
    'confirmed',
    'rejected',
    'completed',
    'cancelled',
    'no_show'
);


--
-- Name: payment_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.payment_status AS ENUM (
    'unpaid',
    'paid_cash'
);


--
-- Name: pitch_format; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.pitch_format AS ENUM (
    'خماسي',
    'سباعي'
);


--
-- Name: pitch_surface; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.pitch_surface AS ENUM (
    'artificial_grass',
    'natural_grass',
    'futsal_court'
);


--
-- Name: user_role; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.user_role AS ENUM (
    'player',
    'owner',
    'admin',
    'staff'
);


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: booking_idempotency_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.booking_idempotency_keys (
    id bigint NOT NULL,
    idem_key text NOT NULL,
    user_id integer NOT NULL,
    endpoint text NOT NULL,
    fingerprint text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    booking_id bigint,
    response jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT booking_idempotency_keys_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'completed'::text])))
);


--
-- Name: booking_idempotency_keys_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.booking_idempotency_keys_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: booking_idempotency_keys_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.booking_idempotency_keys_id_seq OWNED BY public.booking_idempotency_keys.id;


--
-- Name: bookings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.bookings (
    id integer NOT NULL,
    pitch_id integer NOT NULL,
    player_id integer,
    booking_range tstzrange NOT NULL,
    status public.booking_status DEFAULT 'pending'::public.booking_status NOT NULL,
    total_price numeric(10,3) NOT NULL,
    notes text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    payment_status public.payment_status DEFAULT 'unpaid'::public.payment_status NOT NULL,
    reminder_sent boolean DEFAULT false NOT NULL,
    source text NOT NULL,
    guest_name text,
    guest_phone text,
    recurrence_group_id uuid,
    attendance character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    customer_id bigint,
    contact_name text,
    contact_phone text,
    amount_paid numeric(10,3),
    CONSTRAINT bookings_attendance_check CHECK (((attendance)::text = ANY ((ARRAY['pending'::character varying, 'checked_in'::character varying, 'no_show'::character varying])::text[]))),
    CONSTRAINT bookings_contact_phone_e164_chk CHECK (((contact_phone IS NULL) OR (contact_phone ~ '^\+[1-9][0-9]{1,14}$'::text))),
    CONSTRAINT bookings_named_guest_chk CHECK (((source <> ALL (ARRAY['manual'::text, 'academy'::text])) OR (guest_name IS NOT NULL))),
    CONSTRAINT bookings_source_chk CHECK ((source = ANY (ARRAY['player'::text, 'academy'::text, 'block'::text, 'manual'::text]))),
    CONSTRAINT bookings_source_player_chk CHECK ((((source = ANY (ARRAY['block'::text, 'manual'::text, 'academy'::text])) AND (player_id IS NULL)) OR ((source = 'player'::text) AND (player_id IS NOT NULL)))),
    CONSTRAINT bookings_total_price_check CHECK ((total_price >= (0)::numeric)),
    CONSTRAINT chk_amount_paid_bounds CHECK (((amount_paid IS NULL) OR ((amount_paid >= (0)::numeric) AND (amount_paid <= total_price)))),
    CONSTRAINT chk_min_duration CHECK (((upper(booking_range) - lower(booking_range)) >= '01:00:00'::interval)),
    CONSTRAINT chk_valid_range CHECK (((NOT isempty(booking_range)) AND (lower(booking_range) IS NOT NULL) AND (upper(booking_range) IS NOT NULL)))
);


--
-- Name: bookings_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.bookings_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: bookings_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.bookings_id_seq OWNED BY public.bookings.id;


--
-- Name: customers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.customers (
    id bigint NOT NULL,
    owner_id integer NOT NULL,
    player_id integer,
    name text,
    phone text NOT NULL,
    notes text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT customers_phone_e164_chk CHECK ((phone ~ '^\+[1-9][0-9]{1,14}$'::text))
);


--
-- Name: customers_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.customers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: customers_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.customers_id_seq OWNED BY public.customers.id;


--
-- Name: expenses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.expenses (
    id bigint NOT NULL,
    owner_id integer NOT NULL,
    pitch_id integer,
    category text NOT NULL,
    amount numeric(10,3) NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    note text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    idempotency_key text,
    CONSTRAINT expenses_amount_check CHECK ((amount >= (0)::numeric)),
    CONSTRAINT expenses_category_chk CHECK ((category = ANY (ARRAY['Electricity'::text, 'Staff'::text, 'Water'::text, 'Maintenance'::text, 'Marketing'::text, 'Other'::text])))
);


--
-- Name: expenses_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.expenses_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: expenses_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.expenses_id_seq OWNED BY public.expenses.id;


--
-- Name: message_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.message_deliveries (
    id bigint NOT NULL,
    provider_message_id text NOT NULL,
    job_id bigint,
    recipient text,
    status text NOT NULL,
    error_code integer,
    error_title text,
    raw jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT message_deliveries_status_check CHECK ((status = ANY (ARRAY['sent'::text, 'delivered'::text, 'read'::text, 'failed'::text])))
);


--
-- Name: message_deliveries_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.message_deliveries_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: message_deliveries_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.message_deliveries_id_seq OWNED BY public.message_deliveries.id;


--
-- Name: notification_jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notification_jobs (
    id bigint NOT NULL,
    recipient text NOT NULL,
    kind text NOT NULL,
    envelope jsonb NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text,
    provider_message_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT notification_jobs_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processing'::text, 'succeeded'::text, 'dead_letter'::text, 'blocked'::text])))
);


--
-- Name: notification_jobs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notification_jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: notification_jobs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notification_jobs_id_seq OWNED BY public.notification_jobs.id;


--
-- Name: operating_hours; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.operating_hours (
    id bigint NOT NULL,
    pitch_id integer NOT NULL,
    weekday smallint NOT NULL,
    open_time time without time zone NOT NULL,
    close_time time without time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operating_hours_weekday_check CHECK (((weekday >= 0) AND (weekday <= 6)))
);


--
-- Name: operating_hours_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.operating_hours_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: operating_hours_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.operating_hours_id_seq OWNED BY public.operating_hours.id;


--
-- Name: otp_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.otp_codes (
    phone text NOT NULL,
    code_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: otp_rate_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.otp_rate_events (
    id bigint NOT NULL,
    bucket_key text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: otp_rate_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.otp_rate_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: otp_rate_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.otp_rate_events_id_seq OWNED BY public.otp_rate_events.id;


--
-- Name: pitch_audit_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pitch_audit_log (
    id bigint NOT NULL,
    pitch_id integer NOT NULL,
    actor_id integer,
    actor_role character varying(20) NOT NULL,
    action text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    detail jsonb
);


--
-- Name: pitch_audit_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.pitch_audit_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: pitch_audit_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.pitch_audit_log_id_seq OWNED BY public.pitch_audit_log.id;


--
-- Name: pitches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pitches (
    id integer NOT NULL,
    owner_id integer NOT NULL,
    name character varying(150) NOT NULL,
    neighborhood character varying(100) NOT NULL,
    surface public.pitch_surface NOT NULL,
    format public.pitch_format NOT NULL,
    price_per_hour integer NOT NULL,
    rating numeric(2,1) DEFAULT 0.0 NOT NULL,
    review_count integer DEFAULT 0 NOT NULL,
    is_featured boolean DEFAULT false NOT NULL,
    amenities text[] DEFAULT '{}'::text[] NOT NULL,
    pitch_hue character varying(20) DEFAULT '#141715'::character varying NOT NULL,
    is_active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    latitude double precision DEFAULT 0,
    longitude double precision DEFAULT 0 NOT NULL,
    deleted_at timestamp with time zone,
    description text DEFAULT ''::text NOT NULL,
    image_url text DEFAULT ''::text NOT NULL,
    image_public_id text DEFAULT ''::text NOT NULL,
    maps_url text,
    CONSTRAINT pitches_price_per_hour_check CHECK ((price_per_hour > 0))
);


--
-- Name: pitches_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.pitches_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: pitches_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.pitches_id_seq OWNED BY public.pitches.id;


--
-- Name: refresh_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.refresh_tokens (
    id integer NOT NULL,
    user_id integer NOT NULL,
    token_hash text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    revoked boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: refresh_tokens_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.refresh_tokens_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: refresh_tokens_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.refresh_tokens_id_seq OWNED BY public.refresh_tokens.id;


--
-- Name: reviews; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reviews (
    id integer NOT NULL,
    pitch_id integer NOT NULL,
    player_id integer NOT NULL,
    booking_id integer NOT NULL,
    rating smallint NOT NULL,
    comment text,
    is_flagged boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT reviews_comment_check CHECK ((char_length(comment) <= 1000)),
    CONSTRAINT reviews_rating_check CHECK (((rating >= 1) AND (rating <= 5)))
);


--
-- Name: reviews_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.reviews ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.reviews_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: staff; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.staff (
    id integer NOT NULL,
    user_id integer NOT NULL,
    pitch_id integer NOT NULL,
    owner_id integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: staff_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.staff_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: staff_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.staff_id_seq OWNED BY public.staff.id;


--
-- Name: status_transitions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.status_transitions (
    id integer NOT NULL,
    booking_id integer NOT NULL,
    from_status public.booking_status,
    to_status public.booking_status NOT NULL,
    actor_id integer,
    actor_role character varying(20) NOT NULL,
    reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: status_transitions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.status_transitions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: status_transitions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.status_transitions_id_seq OWNED BY public.status_transitions.id;


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id integer NOT NULL,
    full_name character varying(100),
    email character varying(255),
    phone character varying(20),
    role public.user_role NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    phone_verified boolean DEFAULT false NOT NULL,
    opt_in boolean DEFAULT false NOT NULL,
    opt_out boolean DEFAULT false NOT NULL,
    last_booking_at timestamp with time zone,
    booking_count integer DEFAULT 0 NOT NULL,
    phone_verified_at timestamp with time zone,
    password_hash text,
    CONSTRAINT users_phone_e164_chk CHECK (((phone IS NULL) OR ((phone)::text ~ '^\+[1-9][0-9]{1,14}$'::text)))
);


--
-- Name: users_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.users_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: users_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;


--
-- Name: waba_daily_sends; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.waba_daily_sends (
    waba_id text NOT NULL,
    send_date date NOT NULL,
    count integer DEFAULT 0 NOT NULL
);


--
-- Name: booking_idempotency_keys id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.booking_idempotency_keys ALTER COLUMN id SET DEFAULT nextval('public.booking_idempotency_keys_id_seq'::regclass);


--
-- Name: bookings id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings ALTER COLUMN id SET DEFAULT nextval('public.bookings_id_seq'::regclass);


--
-- Name: customers id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.customers ALTER COLUMN id SET DEFAULT nextval('public.customers_id_seq'::regclass);


--
-- Name: expenses id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.expenses ALTER COLUMN id SET DEFAULT nextval('public.expenses_id_seq'::regclass);


--
-- Name: message_deliveries id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message_deliveries ALTER COLUMN id SET DEFAULT nextval('public.message_deliveries_id_seq'::regclass);


--
-- Name: notification_jobs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_jobs ALTER COLUMN id SET DEFAULT nextval('public.notification_jobs_id_seq'::regclass);


--
-- Name: operating_hours id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operating_hours ALTER COLUMN id SET DEFAULT nextval('public.operating_hours_id_seq'::regclass);


--
-- Name: otp_rate_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.otp_rate_events ALTER COLUMN id SET DEFAULT nextval('public.otp_rate_events_id_seq'::regclass);


--
-- Name: pitch_audit_log id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pitch_audit_log ALTER COLUMN id SET DEFAULT nextval('public.pitch_audit_log_id_seq'::regclass);


--
-- Name: pitches id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pitches ALTER COLUMN id SET DEFAULT nextval('public.pitches_id_seq'::regclass);


--
-- Name: refresh_tokens id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens ALTER COLUMN id SET DEFAULT nextval('public.refresh_tokens_id_seq'::regclass);


--
-- Name: staff id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.staff ALTER COLUMN id SET DEFAULT nextval('public.staff_id_seq'::regclass);


--
-- Name: status_transitions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.status_transitions ALTER COLUMN id SET DEFAULT nextval('public.status_transitions_id_seq'::regclass);


--
-- Name: users id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);


--
-- Name: booking_idempotency_keys booking_idempotency_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.booking_idempotency_keys
    ADD CONSTRAINT booking_idempotency_keys_pkey PRIMARY KEY (id);


--
-- Name: booking_idempotency_keys booking_idempotency_user_key_uniq; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.booking_idempotency_keys
    ADD CONSTRAINT booking_idempotency_user_key_uniq UNIQUE (user_id, idem_key);


--
-- Name: bookings bookings_pitch_id_booking_range_excl; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings
    ADD CONSTRAINT bookings_pitch_id_booking_range_excl EXCLUDE USING gist (pitch_id WITH =, booking_range WITH &&) WHERE ((status <> 'cancelled'::public.booking_status));


--
-- Name: bookings bookings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings
    ADD CONSTRAINT bookings_pkey PRIMARY KEY (id);


--
-- Name: customers customers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.customers
    ADD CONSTRAINT customers_pkey PRIMARY KEY (id);


--
-- Name: expenses expenses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.expenses
    ADD CONSTRAINT expenses_pkey PRIMARY KEY (id);


--
-- Name: message_deliveries message_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message_deliveries
    ADD CONSTRAINT message_deliveries_pkey PRIMARY KEY (id);


--
-- Name: message_deliveries message_deliveries_provider_message_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message_deliveries
    ADD CONSTRAINT message_deliveries_provider_message_id_key UNIQUE (provider_message_id);


--
-- Name: notification_jobs notification_jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_jobs
    ADD CONSTRAINT notification_jobs_pkey PRIMARY KEY (id);


--
-- Name: operating_hours operating_hours_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operating_hours
    ADD CONSTRAINT operating_hours_pkey PRIMARY KEY (id);


--
-- Name: otp_codes otp_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.otp_codes
    ADD CONSTRAINT otp_codes_pkey PRIMARY KEY (phone);


--
-- Name: otp_rate_events otp_rate_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.otp_rate_events
    ADD CONSTRAINT otp_rate_events_pkey PRIMARY KEY (id);


--
-- Name: pitch_audit_log pitch_audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pitch_audit_log
    ADD CONSTRAINT pitch_audit_log_pkey PRIMARY KEY (id);


--
-- Name: pitches pitches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pitches
    ADD CONSTRAINT pitches_pkey PRIMARY KEY (id);


--
-- Name: refresh_tokens refresh_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_pkey PRIMARY KEY (id);


--
-- Name: refresh_tokens refresh_tokens_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_token_hash_key UNIQUE (token_hash);


--
-- Name: reviews reviews_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT reviews_pkey PRIMARY KEY (id);


--
-- Name: staff staff_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.staff
    ADD CONSTRAINT staff_pkey PRIMARY KEY (id);


--
-- Name: staff staff_user_pitch_uq; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.staff
    ADD CONSTRAINT staff_user_pitch_uq UNIQUE (user_id, pitch_id);


--
-- Name: status_transitions status_transitions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.status_transitions
    ADD CONSTRAINT status_transitions_pkey PRIMARY KEY (id);


--
-- Name: bookings uq_booking_triple; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings
    ADD CONSTRAINT uq_booking_triple UNIQUE (id, player_id, pitch_id);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: waba_daily_sends waba_daily_sends_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.waba_daily_sends
    ADD CONSTRAINT waba_daily_sends_pkey PRIMARY KEY (waba_id, send_date);


--
-- Name: idx_booking_idempotency_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_booking_idempotency_expires_at ON public.booking_idempotency_keys USING btree (expires_at);


--
-- Name: idx_bookings_customer_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_customer_id ON public.bookings USING btree (customer_id);


--
-- Name: idx_bookings_pitch; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_pitch ON public.bookings USING btree (pitch_id);


--
-- Name: idx_bookings_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_player ON public.bookings USING btree (player_id);


--
-- Name: idx_bookings_player_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_player_id ON public.bookings USING btree (player_id);


--
-- Name: idx_bookings_player_pitch; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_player_pitch ON public.bookings USING btree (player_id, pitch_id);


--
-- Name: idx_bookings_range; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_range ON public.bookings USING gist (pitch_id, booking_range);


--
-- Name: idx_bookings_recurrence_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_recurrence_group ON public.bookings USING btree (recurrence_group_id) WHERE (recurrence_group_id IS NOT NULL);


--
-- Name: idx_bookings_reminder_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_bookings_reminder_due ON public.bookings USING btree (lower(booking_range)) WHERE ((status = 'confirmed'::public.booking_status) AND (reminder_sent = false));


--
-- Name: idx_customers_owner_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_customers_owner_id ON public.customers USING btree (owner_id);


--
-- Name: idx_customers_player_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_customers_player_id ON public.customers USING btree (player_id);


--
-- Name: idx_expenses_owner_idem; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_expenses_owner_idem ON public.expenses USING btree (owner_id, idempotency_key) WHERE (idempotency_key IS NOT NULL);


--
-- Name: idx_expenses_owner_occurred; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expenses_owner_occurred ON public.expenses USING btree (owner_id, occurred_at) WHERE (deleted_at IS NULL);


--
-- Name: idx_expenses_owner_pitch; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expenses_owner_pitch ON public.expenses USING btree (owner_id, pitch_id) WHERE (deleted_at IS NULL);


--
-- Name: idx_notification_jobs_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_notification_jobs_due ON public.notification_jobs USING btree (next_attempt_at) WHERE (status = 'pending'::text);


--
-- Name: idx_operating_hours_pitch_weekday; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_operating_hours_pitch_weekday ON public.operating_hours USING btree (pitch_id, weekday);


--
-- Name: idx_otp_rate_events_bucket_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_otp_rate_events_bucket_time ON public.otp_rate_events USING btree (bucket_key, created_at);


--
-- Name: idx_pitch_audit_log_pitch; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pitch_audit_log_pitch ON public.pitch_audit_log USING btree (pitch_id);


--
-- Name: idx_pitches_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pitches_active ON public.pitches USING btree (id) WHERE (deleted_at IS NULL);


--
-- Name: idx_pitches_featured; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pitches_featured ON public.pitches USING btree (is_featured DESC, price_per_hour);


--
-- Name: idx_pitches_owner; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pitches_owner ON public.pitches USING btree (owner_id);


--
-- Name: idx_pitches_owner_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pitches_owner_id ON public.pitches USING btree (owner_id);


--
-- Name: idx_refresh_tokens_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_refresh_tokens_user ON public.refresh_tokens USING btree (user_id);


--
-- Name: idx_reviews_pitch_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reviews_pitch_id ON public.reviews USING btree (pitch_id) WHERE (deleted_at IS NULL);


--
-- Name: idx_staff_owner_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_staff_owner_id ON public.staff USING btree (owner_id);


--
-- Name: idx_staff_pitch_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_staff_pitch_id ON public.staff USING btree (pitch_id);


--
-- Name: idx_staff_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_staff_user_id ON public.staff USING btree (user_id);


--
-- Name: idx_status_transitions_booking; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_status_transitions_booking ON public.status_transitions USING btree (booking_id);


--
-- Name: idx_users_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_email ON public.users USING btree (email);


--
-- Name: idx_users_phone_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_users_phone_unique ON public.users USING btree (phone);


--
-- Name: uq_customers_owner_phone; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_customers_owner_phone ON public.customers USING btree (owner_id, phone);


--
-- Name: uq_review_player_pitch; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_review_player_pitch ON public.reviews USING btree (player_id, pitch_id) WHERE (deleted_at IS NULL);


--
-- Name: bookings bookings_customer_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings
    ADD CONSTRAINT bookings_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(id) ON DELETE SET NULL;


--
-- Name: bookings bookings_pitch_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings
    ADD CONSTRAINT bookings_pitch_id_fkey FOREIGN KEY (pitch_id) REFERENCES public.pitches(id) ON DELETE RESTRICT;


--
-- Name: bookings bookings_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bookings
    ADD CONSTRAINT bookings_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.users(id) ON DELETE RESTRICT;


--
-- Name: customers customers_owner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.customers
    ADD CONSTRAINT customers_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: customers customers_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.customers
    ADD CONSTRAINT customers_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: expenses expenses_owner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.expenses
    ADD CONSTRAINT expenses_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.users(id) ON DELETE RESTRICT;


--
-- Name: expenses expenses_pitch_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.expenses
    ADD CONSTRAINT expenses_pitch_id_fkey FOREIGN KEY (pitch_id) REFERENCES public.pitches(id) ON DELETE SET NULL;


--
-- Name: reviews fk_reviews_booking_triple; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reviews
    ADD CONSTRAINT fk_reviews_booking_triple FOREIGN KEY (booking_id, player_id, pitch_id) REFERENCES public.bookings(id, player_id, pitch_id) ON DELETE RESTRICT;


--
-- Name: message_deliveries message_deliveries_job_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message_deliveries
    ADD CONSTRAINT message_deliveries_job_id_fkey FOREIGN KEY (job_id) REFERENCES public.notification_jobs(id) ON DELETE SET NULL;


--
-- Name: operating_hours operating_hours_pitch_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operating_hours
    ADD CONSTRAINT operating_hours_pitch_id_fkey FOREIGN KEY (pitch_id) REFERENCES public.pitches(id) ON DELETE CASCADE;


--
-- Name: pitch_audit_log pitch_audit_log_actor_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pitch_audit_log
    ADD CONSTRAINT pitch_audit_log_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: pitches pitches_owner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pitches
    ADD CONSTRAINT pitches_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.users(id) ON DELETE RESTRICT;


--
-- Name: refresh_tokens refresh_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: staff staff_owner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.staff
    ADD CONSTRAINT staff_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: staff staff_pitch_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.staff
    ADD CONSTRAINT staff_pitch_id_fkey FOREIGN KEY (pitch_id) REFERENCES public.pitches(id) ON DELETE CASCADE;


--
-- Name: staff staff_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.staff
    ADD CONSTRAINT staff_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: status_transitions status_transitions_actor_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.status_transitions
    ADD CONSTRAINT status_transitions_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: status_transitions status_transitions_booking_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.status_transitions
    ADD CONSTRAINT status_transitions_booking_id_fkey FOREIGN KEY (booking_id) REFERENCES public.bookings(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict Z4HayVqQ7NBc88J4NJwB9UwVfWeuk3lWAmDVHq6SzUN67OXrfv4lnbiNecZCFjX

