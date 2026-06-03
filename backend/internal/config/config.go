package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv     string
	ServerPort string
	DB         DBConfig
	JWT        JWTConfig      // ← NEW
	BcryptCost int            // ← NEW
	OTP        OTPConfig      // ← NEW (PART 3B)
	WhatsApp   WhatsAppConfig // ← NEW (PART 4)
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
			Pepper: otpPepper,
		},
		WhatsApp: loadWhatsAppConfig(),
		DB:       loadDBConfig(int32(maxConns), int32(minConns)),
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
