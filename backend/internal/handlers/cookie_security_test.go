package handlers

// §5.5 — cookieSecurity() MUST fail closed. Production cookie semantics
// (SameSite=None + Secure) are the DEFAULT; only an explicit, recognised dev
// APP_ENV relaxes to SameSite=Lax + insecure. Any unknown/empty/typo'd value
// inherits the secure path. A regression here silently downgrades production
// cookies (cross-site session cookies dropped, or Secure stripped), so this is a
// HIGH-severity invariant. Pure unit test — no DB, runs in the default suite.

import (
	"net/http"
	"testing"

	"github.com/ali/football-pitch-api/internal/config"
)

func TestCookieSecurity_FailClosed(t *testing.T) {
	cases := []struct {
		appEnv     string
		wantDev    bool // expected IsDev()
		wantSame   http.SameSite
		wantSecure bool
	}{
		// Recognised dev values (IsDevEnv lower-cases + trims) → relaxed.
		{"development", true, http.SameSiteLaxMode, false},
		{"local", true, http.SameSiteLaxMode, false},
		{"dev", true, http.SameSiteLaxMode, false},
		{"test", true, http.SameSiteLaxMode, false},
		{"  Development  ", true, http.SameSiteLaxMode, false}, // trimmed + case-folded
		{"TEST", true, http.SameSiteLaxMode, false},

		// Everything else → PRODUCTION (None + Secure). These are the fail-closed
		// cases: a wrong value must NEVER yield relaxed cookies.
		{"", false, http.SameSiteNoneMode, true},          // unset/empty
		{"production", false, http.SameSiteNoneMode, true}, // the real prod value
		{"prod", false, http.SameSiteNoneMode, true},       // not in the allowlist
		{"staging", false, http.SameSiteNoneMode, true},
		{"developmentx", false, http.SameSiteNoneMode, true}, // typo
		{"devel", false, http.SameSiteNoneMode, true},        // near-miss
		{"production ", false, http.SameSiteNoneMode, true},   // trims to "production"
	}

	for _, tc := range cases {
		t.Run("env="+tc.appEnv, func(t *testing.T) {
			cfg := &config.Config{AppEnv: tc.appEnv}

			if got := cfg.IsDev(); got != tc.wantDev {
				t.Fatalf("IsDev(%q) = %v, want %v", tc.appEnv, got, tc.wantDev)
			}

			same, secure := cookieSecurity(cfg)
			if same != tc.wantSame || secure != tc.wantSecure {
				t.Fatalf("cookieSecurity(%q) = (SameSite=%v, Secure=%v), want (SameSite=%v, Secure=%v)",
					tc.appEnv, same, secure, tc.wantSame, tc.wantSecure)
			}

			// The nightmare regression, asserted explicitly: a non-dev env must
			// never emit an insecure cookie.
			if !tc.wantDev && !secure {
				t.Fatalf("FAIL-OPEN: APP_ENV=%q produced Secure=false — production cookie downgrade", tc.appEnv)
			}
		})
	}
}
