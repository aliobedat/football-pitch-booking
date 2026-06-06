package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv       string
	ServerPort   string
	DB           DBConfig
	JWT          JWTConfig      // ← NEW
	BcryptCost   int            // ← NEW
	OTP          OTPConfig      // ← NEW (PART 3B)
	WhatsApp     WhatsAppConfig // ← NEW (PART 4)
	Cloudinary   CloudinaryConfig
	Twilio       TwilioConfig       // ← NEW (MVP auth channel)
	Notification NotificationConfig // ← NEW (type-based routing)
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
	Token      string // WHATSAPP_TOKEN — Meta Cloud API bearer token
	PhoneID    string // WHATSAPP_PHONE_ID — sender phone-number id
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
		return d.URL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		d.Host, d.Port, d.User, d.Password, d.Name,
	)
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
		AppEnv:     getEnv("APP_ENV", "development"),
		ServerPort: getEnv("PORT", getEnv("SERVER_PORT", "8080")),
		BcryptCost: bcryptCost,
		JWT: JWTConfig{
			Secret:        jwtSecret,
			AccessExpiry:  accessExpiry,
			RefreshExpiry: refreshExpiry,
		},
		OTP: OTPConfig{
			Pepper:         otpPepper,
			GlobalDailyCap: otpGlobalDailyCap,
		},
		WhatsApp:     loadWhatsAppConfig(),
		Cloudinary:   loadCloudinaryConfig(),
		Twilio:       loadTwilioConfig(),
		Notification: loadNotificationConfig(),
		DB:           loadDBConfig(int32(maxConns), int32(minConns)),
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

func loadDBConfig(maxConns, minConns int32) DBConfig {
	if url := getEnv("DATABASE_URL", ""); url != "" {
		return DBConfig{URL: url, MaxConns: maxConns, MinConns: minConns}
	}
	return DBConfig{
		Host:     mustGetEnv("DB_HOST"),
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
