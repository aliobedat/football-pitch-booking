package handlers

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/config"
)

func init() { gin.SetMode(gin.TestMode) }

func testCookieCfg() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			AccessExpiry:  15 * time.Minute,
			RefreshExpiry: 168 * time.Hour,
		},
	}
}

func issueOnce(cfg *config.Config) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	issueSessionCookies(c, cfg, "access-jwt", "refresh-token", "csrf-token", "player")
	return rec
}

// TestCookiePrefix_ProductionDefaultUnchanged proves the historical malaab_*
// names are issued when COOKIE_NAME_PREFIX is left at its "malaab_" default —
// Production's behaviour must be byte-for-byte unchanged.
func TestCookiePrefix_ProductionDefaultUnchanged(t *testing.T) {
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })
	SetCookieNamePrefix("malaab_")

	rec := issueOnce(testCookieCfg())
	for _, name := range []string{"malaab_access", "malaab_refresh", "malaab_role", "malaab_expiry", "malaab_csrf"} {
		if cookieByName(rec, name) == nil {
			t.Fatalf("expected Production cookie %q, got none of that name", name)
		}
	}
}

// TestCookiePrefix_DemoResolvesDistinctNames proves Demo's configured prefix
// produces cookie names that never collide with Production's, satisfying the
// isolation requirement even when both share a parent registrable domain.
func TestCookiePrefix_DemoResolvesDistinctNames(t *testing.T) {
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })
	SetCookieNamePrefix("malaab_demo_")

	rec := issueOnce(testCookieCfg())
	for _, name := range []string{"malaab_demo_access", "malaab_demo_refresh", "malaab_demo_role", "malaab_demo_expiry", "malaab_demo_csrf"} {
		if cookieByName(rec, name) == nil {
			t.Fatalf("expected Demo cookie %q, got none of that name", name)
		}
	}
	// None of the Production names should appear — proves no collision/overwrite.
	for _, prodName := range []string{"malaab_access", "malaab_refresh", "malaab_role", "malaab_expiry", "malaab_csrf"} {
		if cookieByName(rec, prodName) != nil {
			t.Fatalf("Demo response must not also set Production cookie %q", prodName)
		}
	}
}

// TestCookiePrefix_ClearMatchesIssuedNames proves logout clears exactly the
// cookies that were issued under the currently configured prefix — a stale
// clear (mismatched name/domain) would leave a dead session cookie behind.
func TestCookiePrefix_ClearMatchesIssuedNames(t *testing.T) {
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })
	SetCookieNamePrefix("malaab_demo_")

	cfg := testCookieCfg()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	clearSessionCookies(c, cfg)

	ck := cookieByName(rec, "malaab_demo_access")
	if ck == nil {
		t.Fatalf("expected a clearing Set-Cookie for malaab_demo_access")
	}
	if ck.MaxAge >= 0 {
		t.Fatalf("clearing cookie malaab_demo_access must have MaxAge < 0, got %d", ck.MaxAge)
	}
}
