package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// devEnvs is the explicit allowlist of APP_ENV values that enable developer
// conveniences: Gin DebugMode, relaxed cookie security (SameSite=Lax, Secure=
// false) and the localhost-only DB fallback. The gating is FAIL-CLOSED — ANY
// other value, including empty, unset, or a typo, is treated as production and
// gets the secure path. Local dev MUST therefore set APP_ENV to one of these.
var devEnvs = map[string]bool{
	"development": true,
	"local":       true,
	"dev":         true,
	"test":        true,
}

// IsDevEnv reports whether appEnv is a recognised developer environment.
// Unknown/empty values are NOT dev → production behaviour (fail-closed).
func IsDevEnv(appEnv string) bool {
	return devEnvs[strings.ToLower(strings.TrimSpace(appEnv))]
}

// IsDev reports whether this config is running in a developer environment.
func (c *Config) IsDev() bool { return IsDevEnv(c.AppEnv) }

type Config struct {
	AppEnv       string
	CookieDomain string
	ServerPort   string
	// BookingOTPRequired gates whether the PLAYER booking flow requires an OTP.
	// MVP default: false (booking works with name + JO phone only, no code).
	// FAIL-OPEN BY DESIGN — a deliberate, scoped inversion of this stack's usual
	// fail-closed posture: the whole point is to UNBLOCK booking, so an
	// absent/unparseable BOOKING_OTP_REQUIRED resolves to false (not required).
	// Only the exact string "true" turns the OTP requirement back on. Owner/staff/
	// admin login is unaffected — this flag gates the booking caller only.
	BookingOTPRequired bool
	DB           DBConfig
	JWT          JWTConfig      // ← NEW
	BcryptCost   int            // ← NEW
	OTP          OTPConfig      // ← NEW (PART 3B)
	WhatsApp     WhatsAppConfig // ← NEW (PART 4)
	// WhatsAppProvider selects the WhatsApp delivery provider: "meta" (default) or
	// "infobip" (Gate 2 / PR-1). Empty/unset → meta, the fallback-safe provider.
	WhatsAppProvider string
	Infobip          InfobipConfig // ← NEW (Gate 2 / PR-1)
	Cloudinary       CloudinaryConfig
	Twilio           TwilioConfig       // ← NEW (MVP auth channel)
	Notification     NotificationConfig // ← NEW (type-based routing)
}

// TwilioConfig holds the Twilio Programmable SMS credentials for the closed-beta
// auth channel (OTP-over-SMS). All three are needed to send; they are OPTIONAL at
// load time (a FAKE/dev deployment needs none), but a PARTIAL configuration fails
// fast (see loadTwilioConfig) and the routing safety check fails boot if OTP is
// routed to Twilio while it is unconfigured. The AuthToken is a secret, never
// hardcoded and never logged.
type TwilioConfig struct {
	AccountSID string // TWILIO_ACCOUNT_SID
	AuthToken  string // TWILIO_AUTH_TOKEN — secret
	FromNumber string // TWILIO_FROM_NUMBER — E.164 trial sender number
}

// Configured reports whether all three Twilio credentials are present.
func (c TwilioConfig) Configured() bool {
	return c.AccountSID != "" && c.AuthToken != "" && c.FromNumber != ""
}

// NotificationConfig is the config-driven routing policy: which registered
// channel/sink each message type resolves to. Defaults encode the closed-beta
// policy (OTP → Twilio SMS; booking events → log only). Changing the policy
// post-incorporation is an env edit, no code change. The OTP route MUST resolve
// to a real delivery adapter — enforced by a startup assertion, not here.
type NotificationConfig struct {
	OTPRoute     string // NOTIFY_OTP_ROUTE      (default twilio_sms)
	BookingRoute string // NOTIFY_BOOKING_ROUTE  (default log_only) — all booking kinds
	DefaultRoute string // NOTIFY_DEFAULT_ROUTE  (default log_only) — fail-safe for unknown kinds
}

// CloudinaryConfig holds the credentials and pinned upload target for
// backend-signed direct uploads of pitch images (browser → Cloudinary). The API
// SECRET is server-side only and is NEVER sent to the client — the backend uses
// it solely to sign upload params and to destroy replaced assets.
//
// All three credentials are REQUIRED: the service fails fast on boot if any is
// missing (see Load), consistent with the JWT/OTP startup assertions. CloudName
// and APIKey are non-secret (they reach the browser in the signed payload);
// APISecret is secret.
//
// UploadPreset and Folder are pinned into the signed params so a leaked signature
// cannot redirect an upload to a different preset or folder.
type CloudinaryConfig struct {
	CloudName    string // CLOUDINARY_CLOUD_NAME  — non-secret
	APIKey       string // CLOUDINARY_API_KEY     — non-secret
	APISecret    string // CLOUDINARY_API_SECRET  — SECRET, backend-only
	UploadPreset string // CLOUDINARY_UPLOAD_PRESET — signed preset (default malaeb_pitches)
	Folder       string // CLOUDINARY_UPLOAD_FOLDER — pinned folder (default malaeb/pitches)
}

// WhatsAppConfig holds Meta WhatsApp Cloud API credentials plus the names of the
// pre-approved message templates the WhatsApp adapter renders against.
//
// These values are OPTIONAL at load time: deployments running the FAKE or SMS
// channel need not set them, so we never panic when they are absent (unlike the
// hard-required DB/JWT/OTP secrets). The WhatsApp adapter validates the values it
// actually needs when it is constructed and at send time — a missing template,
// for example, is treated as a delivery failure that triggers the SMS fallback.
//
// Credentials come exclusively from the environment and are never hardcoded.
type WhatsAppConfig struct {
	Token   string // WHATSAPP_TOKEN — Meta Cloud API bearer token
	PhoneID string // WHATSAPP_PHONE_ID — sender phone-number id
	// WABAID is the WhatsApp Business Account id the daily send-quota guard
	// (GATE 2) keys its per-UTC-day counter on (WHATSAPP_WABA_ID). It is DISTINCT
	// from PhoneID (the sender phone-number id used for outbound Cloud API calls):
	// the unverified-tier messaging limit is enforced at the WABA level. Optional
	// at load time like the other WhatsApp settings; an empty value means the
	// quota guard buckets under an empty key — acceptable only for FAKE/SMS
	// deployments that never route booking notifications through WhatsApp.
	WABAID     string
	APIBaseURL string // WHATSAPP_API_BASE_URL — Graph API base (default https://graph.facebook.com)
	APIVersion string // WHATSAPP_API_VERSION — Graph API version (default v21.0)
	// WebhookVerifyToken is the token Meta echoes during the status-webhook
	// subscription handshake (WHATSAPP_WEBHOOK_VERIFY_TOKEN). Optional: when
	// empty the GET verification endpoint rejects all handshakes. It is NOT a
	// credential for outbound calls — only the inbound webhook uses it.
	WebhookVerifyToken string
	Templates          WhatsAppTemplates
}

// WhatsAppTemplates names the approved templates, one per outbound message kind.
type WhatsAppTemplates struct {
	Language         string // WHATSAPP_TEMPLATE_LANG — BCP-47 code (default en)
	OTP              string // WHATSAPP_OTP_TEMPLATE — AUTHENTICATION-category template
	BookingConfirmed string // WHATSAPP_BOOKING_CONFIRMED_TEMPLATE — UTILITY-category template
	BookingCancelled string // WHATSAPP_BOOKING_CANCELLED_TEMPLATE — UTILITY-category template
	BookingReminder  string // WHATSAPP_BOOKING_REMINDER_TEMPLATE — UTILITY-category template (PART 7)
}

// InfobipConfig holds the Infobip WhatsApp credentials and the names of the
// pre-approved templates the Infobip adapter renders against (Gate 2 / PR-1). It is
// DISTINCT from WhatsAppConfig (Meta): the two providers do not share credentials
// or template ids.
//
// These values are OPTIONAL at load time (a meta/FAKE/SMS deployment needs none).
// When WHATSAPP_PROVIDER=infobip is selected, the adapter validates the values it
// needs at construction; a missing base url / api key / sender is a fail-closed
// startup error. The APIKey is a SECRET: it comes only from the environment, is
// never hardcoded, and is never logged.
type InfobipConfig struct {
	BaseURL   string // INFOBIP_BASE_URL — account base URL, e.g. https://xxxxx.api.infobip.com
	APIKey    string // INFOBIP_API_KEY — SECRET, server-only ("App <key>" auth)
	Sender    string // INFOBIP_WHATSAPP_SENDER — registered WhatsApp sender number/id
	Templates InfobipTemplates
}

// Configured reports whether the three required Infobip values are all present.
func (c InfobipConfig) Configured() bool {
	return c.BaseURL != "" && c.APIKey != "" && c.Sender != ""
}

// InfobipTemplates names the approved Infobip templates, one per outbound message
// kind (parity with the Meta adapter's supported kinds).
type InfobipTemplates struct {
	Language         string // INFOBIP_TEMPLATE_LANG — BCP-47 code (default en)
	OTP              string // INFOBIP_OTP_TEMPLATE — AUTHENTICATION-category template
	BookingConfirmed string // INFOBIP_BOOKING_CONFIRMED_TEMPLATE — UTILITY-category template
	BookingCancelled string // INFOBIP_BOOKING_CANCELLED_TEMPLATE — UTILITY-category template
	BookingReminder  string // INFOBIP_BOOKING_REMINDER_TEMPLATE — UTILITY-category template
}

// OTPConfig holds the configuration for the phone-first OTP flow.
type OTPConfig struct {
	// Pepper is the server-side HMAC secret used to key the digest of every
	// stored one-time code (see internal/otp.NewHMACHasher). It MUST come from
	// the environment and is never hardcoded — a leaked code store is useless
	// without it.
	Pepper string

	// GlobalDailyCap is the platform-wide ceiling on OTP sends per rolling day —
	// the global circuit breaker from the anti-AIT rate-limit work. It is aligned
	// with the Twilio TRIAL daily ceiling (~50/day) so the closed beta cannot blow
	// past the provider limit. Raise it post-incorporation when off the trial.
	GlobalDailyCap int
}

type DBConfig struct {
	// URL takes full precedence when set. Passed directly to pgx so that
	// URL-encoded passwords and connection parameters are handled natively.
	// Falls back to the individual fields below when empty.
	URL      string
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	MaxConns int32
	MinConns int32
}

// JWTConfig holds all JWT-related configuration.
type JWTConfig struct {
	Secret        string
	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
}

func (d DBConfig) DSN() string {
	if d.URL != "" {
		return enforceSSLMode(d.URL)
	}
	// Keyword DSN built ONLY for the dev localhost fallback (see loadDBConfig),
	// where a local Postgres without TLS is acceptable. A cloud database always
	// arrives via DATABASE_URL, which goes through enforceSSLMode above.
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		d.Host, d.Port, d.User, d.Password, d.Name,
	)
}

// enforceSSLMode guarantees a non-local DATABASE_URL connects over TLS. A
// localhost target may run without TLS (left untouched); any other host with a
// missing or weaker-than-require sslmode is upgraded to sslmode=require — we
// never silently talk to a cloud database in cleartext.
func enforceSSLMode(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL // malformed — let pgx surface the parse error
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return rawURL
	}
	q := u.Query()
	switch q.Get("sslmode") {
	case "require", "verify-ca", "verify-full":
		// already at or above the minimum
	default:
		q.Set("sslmode", "require")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func Load() *Config {
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "20"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "5"))
	bcryptCost, _ := strconv.Atoi(getEnv("BCRYPT_COST", "12"))

	accessExpiry, err := time.ParseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m"))
	if err != nil {
		panic("CONFIG: JWT_ACCESS_EXPIRY is not a valid duration (e.g. '15m', '1h')")
	}

	refreshExpiry, err := time.ParseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h"))
	if err != nil {
		panic("CONFIG: JWT_REFRESH_EXPIRY is not a valid duration (e.g. '168h')")
	}

	jwtSecret := mustGetEnv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		panic("CONFIG: JWT_SECRET must be at least 32 characters long")
	}

	if bcryptCost < 10 || bcryptCost > 31 {
		panic("CONFIG: BCRYPT_COST must be between 10 and 31")
	}

	// OTP_HMAC_PEPPER keys the digest of every stored one-time code. It is a
	// server-side secret — required, never defaulted, and held to a minimum
	// length so a weak/empty value fails loudly at startup.
	otpPepper := mustGetEnv("OTP_HMAC_PEPPER")
	if len(otpPepper) < 16 {
		panic("CONFIG: OTP_HMAC_PEPPER must be at least 16 characters long")
	}

	// Global OTP daily ceiling — aligned with the Twilio trial limit by default.
	otpGlobalDailyCap, err := strconv.Atoi(getEnv("OTP_GLOBAL_DAILY_CAP", "50"))
	if err != nil || otpGlobalDailyCap < 1 {
		panic("CONFIG: OTP_GLOBAL_DAILY_CAP must be a positive integer")
	}

	return &Config{
		// FAIL-CLOSED: no dev default. An unset/empty APP_ENV is NOT a dev value
		// (see IsDevEnv) and therefore inherits the secure production path. Local
		// dev must set APP_ENV=development explicitly.
		AppEnv: getEnv("APP_ENV", ""),
		// FAIL-OPEN (scoped): only an explicit "true" requires booking OTP; anything
		// else — unset, "false", or a typo — leaves booking OTP NOT required so the
		// player flow is never blocked by a misread flag.
		BookingOTPRequired: getEnv("BOOKING_OTP_REQUIRED", "false") == "true",
		// Optional. Empty = host-only cookies (dev/localhost). In production set to
		// the parent domain with a leading dot (e.g. ".malaebjo.com") so cookies set
		// by the API host are readable across sibling subdomains (first-party,
		// cross-subdomain). No panic — empty is a valid, safe default.
		CookieDomain: getEnv("COOKIE_DOMAIN", ""),
		ServerPort:   getEnv("PORT", getEnv("SERVER_PORT", "8080")),
		BcryptCost:   bcryptCost,
		JWT: JWTConfig{
			Secret:        jwtSecret,
			AccessExpiry:  accessExpiry,
			RefreshExpiry: refreshExpiry,
		},
		OTP: OTPConfig{
			Pepper:         otpPepper,
			GlobalDailyCap: otpGlobalDailyCap,
		},
		WhatsApp:         loadWhatsAppConfig(),
		WhatsAppProvider: getEnv("WHATSAPP_PROVIDER", "meta"),
		Infobip:          loadInfobipConfig(),
		Cloudinary:       loadCloudinaryConfig(),
		Twilio:           loadTwilioConfig(),
		Notification:     loadNotificationConfig(),
		DB:               loadDBConfig(int32(maxConns), int32(minConns), IsDevEnv(getEnv("APP_ENV", ""))),
	}
}

// loadTwilioConfig reads the optional Twilio credentials. Absence is fine (FAKE/
// dev). But a PARTIAL configuration — some credentials set, others missing — is a
// deployment mistake that would silently disable SMS, so it fails fast, matching
// the loud-failure convention of the other secrets.
func loadTwilioConfig() TwilioConfig {
	c := TwilioConfig{
		AccountSID: getEnv("TWILIO_ACCOUNT_SID", ""),
		AuthToken:  getEnv("TWILIO_AUTH_TOKEN", ""),
		FromNumber: getEnv("TWILIO_FROM_NUMBER", ""),
	}
	set := 0
	for _, v := range []string{c.AccountSID, c.AuthToken, c.FromNumber} {
		if v != "" {
			set++
		}
	}
	if set != 0 && set != 3 {
		panic("CONFIG: TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN and TWILIO_FROM_NUMBER must all be set together")
	}
	return c
}

// loadNotificationConfig reads the config-driven routing policy, defaulting to the
// closed-beta policy: OTP over Twilio SMS, booking events to the log-only sink.
func loadNotificationConfig() NotificationConfig {
	return NotificationConfig{
		OTPRoute:     getEnv("NOTIFY_OTP_ROUTE", "twilio_sms"),
		BookingRoute: getEnv("NOTIFY_BOOKING_ROUTE", "log_only"),
		DefaultRoute: getEnv("NOTIFY_DEFAULT_ROUTE", "log_only"),
	}
}

// loadCloudinaryConfig reads the Cloudinary credentials for signed direct image
// uploads. The three credentials are REQUIRED — a missing value panics at boot
// (mustGetEnv), matching the JWT/OTP startup assertions, so a deploy can never
// run with image upload half-configured. The preset and folder default to the
// operator-provisioned signed preset and its pinned folder.
func loadCloudinaryConfig() CloudinaryConfig {
	return CloudinaryConfig{
		CloudName:    mustGetEnv("CLOUDINARY_CLOUD_NAME"),
		APIKey:       mustGetEnv("CLOUDINARY_API_KEY"),
		APISecret:    mustGetEnv("CLOUDINARY_API_SECRET"),
		UploadPreset: getEnv("CLOUDINARY_UPLOAD_PRESET", "malaeb_pitches"),
		Folder:       getEnv("CLOUDINARY_UPLOAD_FOLDER", "malaeb/pitches"),
	}
}

// loadWhatsAppConfig reads the optional Meta WhatsApp Cloud API settings from the
// environment. Nothing here is required — absence is normal for FAKE/SMS
// deployments — so we default the endpoint coordinates and leave credentials and
// template names empty when unset.
func loadWhatsAppConfig() WhatsAppConfig {
	return WhatsAppConfig{
		Token:              getEnv("WHATSAPP_TOKEN", ""),
		PhoneID:            getEnv("WHATSAPP_PHONE_ID", ""),
		WABAID:             getEnv("WHATSAPP_WABA_ID", ""),
		APIBaseURL:         getEnv("WHATSAPP_API_BASE_URL", "https://graph.facebook.com"),
		APIVersion:         getEnv("WHATSAPP_API_VERSION", "v21.0"),
		WebhookVerifyToken: getEnv("WHATSAPP_WEBHOOK_VERIFY_TOKEN", ""),
		Templates: WhatsAppTemplates{
			Language:         getEnv("WHATSAPP_TEMPLATE_LANG", "en"),
			OTP:              getEnv("WHATSAPP_OTP_TEMPLATE", ""),
			BookingConfirmed: getEnv("WHATSAPP_BOOKING_CONFIRMED_TEMPLATE", ""),
			BookingCancelled: getEnv("WHATSAPP_BOOKING_CANCELLED_TEMPLATE", ""),
			BookingReminder:  getEnv("WHATSAPP_BOOKING_REMINDER_TEMPLATE", ""),
		},
	}
}

// loadInfobipConfig reads the optional Infobip WhatsApp settings from the
// environment. Nothing here is required at load time — absence is normal for a
// meta/FAKE/SMS deployment — so credentials and template names default to empty.
// The adapter validates what it needs at construction; main fails closed when
// provider=infobip but the required values are missing.
func loadInfobipConfig() InfobipConfig {
	return InfobipConfig{
		BaseURL: getEnv("INFOBIP_BASE_URL", ""),
		APIKey:  getEnv("INFOBIP_API_KEY", ""),
		Sender:  getEnv("INFOBIP_WHATSAPP_SENDER", ""),
		Templates: InfobipTemplates{
			Language:         getEnv("INFOBIP_TEMPLATE_LANG", "en"),
			OTP:              getEnv("INFOBIP_OTP_TEMPLATE", ""),
			BookingConfirmed: getEnv("INFOBIP_BOOKING_CONFIRMED_TEMPLATE", ""),
			BookingCancelled: getEnv("INFOBIP_BOOKING_CANCELLED_TEMPLATE", ""),
			BookingReminder:  getEnv("INFOBIP_BOOKING_REMINDER_TEMPLATE", ""),
		},
	}
}

func loadDBConfig(maxConns, minConns int32, isDev bool) DBConfig {
	if url := getEnv("DATABASE_URL", ""); url != "" {
		return DBConfig{URL: url, MaxConns: maxConns, MinConns: minConns}
	}

	// No DATABASE_URL. We NEVER silently assemble a fallback cloud DSN: in
	// production this is fatal, refusing to boot rather than guessing connection
	// parameters and risking an insecure/wrong target.
	if !isDev {
		panic("FATAL: DATABASE_URL is required in production — no insecure fallback DSN is built. " +
			"Set DATABASE_URL (sslmode=require enforced) or APP_ENV to a dev value for the localhost fallback")
	}

	// Dev-only fallback, and ONLY to a localhost database (where sslmode=disable
	// is acceptable). A non-local DB_HOST in dev still requires DATABASE_URL so
	// TLS enforcement (enforceSSLMode) is never bypassed.
	host := getEnv("DB_HOST", "localhost")
	switch host {
	case "localhost", "127.0.0.1", "::1":
	default:
		panic("FATAL: the dev DB fallback is localhost-only — set DATABASE_URL for a non-local database")
	}
	return DBConfig{
		Host:     host,
		Port:     getEnv("DB_PORT", "5432"),
		User:     mustGetEnv("DB_USER"),
		Password: mustGetEnv("DB_PASSWORD"),
		Name:     mustGetEnv("DB_NAME"),
		MaxConns: maxConns,
		MinConns: minConns,
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func mustGetEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		panic(fmt.Sprintf("FATAL: required environment variable '%s' is not set", key))
	}
	return val
}
