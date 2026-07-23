package config

import "testing"

// TestIsDemoEnv_ExplicitOnly proves "demo" is recognised only via exact
// (case-insensitive, trimmed) match, and every other value — including dev
// values and typos — is fail-closed to NOT demo.
func TestIsDemoEnv_ExplicitOnly(t *testing.T) {
	cases := []struct {
		appEnv string
		want   bool
	}{
		{"demo", true},
		{"Demo", true},
		{" DEMO ", true},
		{"", false},
		{"development", false},
		{"dev", false},
		{"local", false},
		{"test", false},
		{"production", false},
		{"demoo", false},
		{"demo-typo", false},
	}
	for _, tc := range cases {
		if got := IsDemoEnv(tc.appEnv); got != tc.want {
			t.Errorf("IsDemoEnv(%q) = %v, want %v", tc.appEnv, got, tc.want)
		}
	}
}

// TestDemoIsNotDev proves Demo was NOT merged into the developer allowlist —
// the whole point of the separate map. If this ever regresses, Demo would
// silently inherit relaxed cookies (SameSite=Lax, Secure=false) and the
// localhost DB fallback, exactly what the WO forbids.
func TestDemoIsNotDev(t *testing.T) {
	if IsDevEnv("demo") {
		t.Fatal("demo must NOT be a recognised dev environment — it must inherit production-grade security")
	}
	if IsDemoEnv("development") {
		t.Fatal("development must NOT be a recognised demo environment")
	}
}

// TestConfig_IsDemo mirrors IsDemoEnv through the Config method.
func TestConfig_IsDemo(t *testing.T) {
	c := &Config{AppEnv: "demo"}
	if !c.IsDemo() {
		t.Fatal("Config.IsDemo() = false for AppEnv=demo, want true")
	}
	if c.IsDev() {
		t.Fatal("Config.IsDev() = true for AppEnv=demo, want false (production-grade security)")
	}

	prod := &Config{AppEnv: ""}
	if prod.IsDemo() {
		t.Fatal("Config.IsDemo() = true for empty AppEnv, want false (fail-closed)")
	}
}

func demoSafeConfig() *Config {
	return &Config{
		Notification: NotificationConfig{
			OTPRoute:     "FAKE",
			BookingRoute: "log_only",
			DefaultRoute: "log_only",
		},
		PaidWhatsAppEnabled:          false,
		WhatsAppToSMSFallbackEnabled: false,
	}
}

// TestValidateDemoSafety_AcceptsDemoSafeCombination proves the exact posture
// the WO specifies (FAKE/log_only, no paid switches, no paid credentials)
// passes.
func TestValidateDemoSafety_AcceptsDemoSafeCombination(t *testing.T) {
	if err := ValidateDemoSafety(demoSafeConfig(), "FAKE"); err != nil {
		t.Fatalf("ValidateDemoSafety() = %v, want nil for a Demo-safe config", err)
	}
}

// TestValidateDemoSafety_RejectsUnsafeCombinations proves every unsafe
// deviation is refused (fail-closed), never silently corrected.
func TestValidateDemoSafety_RejectsUnsafeCombinations(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		mutate  func(*Config)
	}{
		{"paid active channel WHATSAPP", "WHATSAPP", func(c *Config) {}},
		{"paid active channel SMS", "SMS", func(c *Config) {}},
		{"OTP routed to twilio_sms", "FAKE", func(c *Config) { c.Notification.OTPRoute = "twilio_sms" }},
		{"booking routed live", "FAKE", func(c *Config) { c.Notification.BookingRoute = "WHATSAPP" }},
		{"default route not log_only", "FAKE", func(c *Config) { c.Notification.DefaultRoute = "twilio_sms" }},
		{"paid WhatsApp enabled", "FAKE", func(c *Config) { c.PaidWhatsAppEnabled = true }},
		{"SMS fallback enabled", "FAKE", func(c *Config) { c.WhatsAppToSMSFallbackEnabled = true }},
		{"Twilio credentials configured", "FAKE", func(c *Config) {
			c.Twilio = TwilioConfig{AccountSID: "AC1", AuthToken: "tok", FromNumber: "+1555"}
		}},
		{"Infobip credentials configured", "FAKE", func(c *Config) {
			c.Infobip = InfobipConfig{BaseURL: "https://x.infobip.com", APIKey: "k", Sender: "+1555"}
		}},
		{"Meta WhatsApp token configured", "FAKE", func(c *Config) { c.WhatsApp.Token = "tok" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := demoSafeConfig()
			tc.mutate(cfg)
			if err := ValidateDemoSafety(cfg, tc.channel); err == nil {
				t.Fatalf("ValidateDemoSafety() = nil, want a refusal for %q", tc.name)
			}
		})
	}
}

// TestValidateDemoCookieSafety_AcceptsCorrectConfiguration proves both the
// bare-host and leading-dot forms of the Demo domain are accepted alongside
// the exact Demo prefix.
func TestValidateDemoCookieSafety_AcceptsCorrectConfiguration(t *testing.T) {
	cases := []string{"demo.marmajo.com", ".demo.marmajo.com"}
	for _, domain := range cases {
		cfg := &Config{CookieNamePrefix: "malaab_demo_", CookieDomain: domain}
		if err := ValidateDemoCookieSafety(cfg); err != nil {
			t.Errorf("ValidateDemoCookieSafety(prefix=malaab_demo_, domain=%q) = %v, want nil", domain, err)
		}
	}
}

// TestValidateDemoCookieSafety_RejectsMisconfiguration proves every unsafe
// combination refuses to boot: missing prefix, the Production default
// prefix, missing domain, the Production parent domain, and any domain
// outside the Demo subtree.
func TestValidateDemoCookieSafety_RejectsMisconfiguration(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		domain string
	}{
		{"missing prefix", "", "demo.marmajo.com"},
		{"production default prefix", "malaab_", "demo.marmajo.com"},
		{"missing domain", "malaab_demo_", ""},
		{"production parent domain", "malaab_demo_", ".marmajo.com"},
		{"production parent domain no dot", "malaab_demo_", "marmajo.com"},
		{"outside demo subtree - sibling subdomain", "malaab_demo_", "admin.marmajo.com"},
		{"outside demo subtree - unrelated domain", "malaab_demo_", "example.com"},
		{"deeper demo subdomain not the bare value", "malaab_demo_", "api.demo.marmajo.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{CookieNamePrefix: tc.prefix, CookieDomain: tc.domain}
			if err := ValidateDemoCookieSafety(cfg); err == nil {
				t.Fatalf("ValidateDemoCookieSafety(prefix=%q, domain=%q) = nil, want a refusal", tc.prefix, tc.domain)
			}
		})
	}
}

// TestCookieNamePrefix_DefaultsToProductionName proves an unset
// COOKIE_NAME_PREFIX resolves to the historical "malaab_" prefix via Load(),
// not an empty string — required env vars for Load() are stubbed here.
func TestCookieNamePrefix_DefaultsToProductionName(t *testing.T) {
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("OTP_HMAC_PEPPER", "0123456789012345")
	t.Setenv("CLOUDINARY_CLOUD_NAME", "x")
	t.Setenv("CLOUDINARY_API_KEY", "x")
	t.Setenv("CLOUDINARY_API_SECRET", "x")
	t.Setenv("APP_ENV", "test")
	t.Setenv("DB_USER", "x")
	t.Setenv("DB_PASSWORD", "x")
	t.Setenv("DB_NAME", "x")

	cfg := Load()
	if cfg.CookieNamePrefix != "malaab_" {
		t.Fatalf("CookieNamePrefix = %q, want the unchanged default %q", cfg.CookieNamePrefix, "malaab_")
	}
}
