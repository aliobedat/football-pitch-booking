package handlers

// Session cookie plumbing. The access + refresh JWTs are delivered EXCLUSIVELY
// as httpOnly cookies — they never appear in a response body and are never
// readable by client JavaScript. This replaces the former localStorage token
// strategy and is the locked-in decision recorded in PROJECT_HANDOFF.md.
//
// The role/expiry companions are intentionally NOT httpOnly: they carry no
// secret (the signed JWT in the httpOnly cookie is the real credential) and
// exist only for client-side UX and the Next.js edge route guard (proxy.ts).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/config"
)

const (
	cookieAccess  = "malaab_access"  // httpOnly: the access JWT
	cookieRefresh = "malaab_refresh" // httpOnly: the opaque refresh token
	cookieRole    = "malaab_role"    // readable: role, for the edge guard
	cookieExpiry  = "malaab_expiry"  // readable: access-token expiry (unix secs)
	cookieCSRF    = "malaab_csrf"    // readable: double-submit CSRF token

	// refreshCookiePath scopes the refresh token to the auth endpoints that
	// actually consume it (refresh + logout), so the long-lived secret is not
	// transmitted on every API request.
	refreshCookiePath = "/api/v1/auth"
)

// issueSessionCookies writes the httpOnly access + refresh cookies plus their
// readable role/expiry companions. SameSite=Strict (plus Secure in production)
// gives CSRF resistance at the transport layer; the double-submit CSRF token
// layers an explicit defence on top of that.
func issueSessionCookies(c *gin.Context, cfg *config.Config, accessToken, rawRefresh, csrfToken, role string) {
	secure := cfg.AppEnv == "production"
	accessMaxAge := int(cfg.JWT.AccessExpiry.Seconds())
	refreshMaxAge := int(cfg.JWT.RefreshExpiry.Seconds())

	c.SetSameSite(http.SameSiteStrictMode)

	// httpOnly cookies — the JWTs never touch JavaScript.
	c.SetCookie(cookieAccess, accessToken, accessMaxAge, "/", "", secure, true)
	c.SetCookie(cookieRefresh, rawRefresh, refreshMaxAge, refreshCookiePath, "", secure, true)

	// Readable companions (httpOnly=false) — non-secret, UX + edge guard only.
	c.SetCookie(cookieRole, role, refreshMaxAge, "/", "", secure, false)
	expiresAt := strconv.FormatInt(time.Now().Add(cfg.JWT.AccessExpiry).Unix(), 10)
	c.SetCookie(cookieExpiry, expiresAt, accessMaxAge, "/", "", secure, false)

	// Readable CSRF token for the double-submit pattern. httpOnly MUST be false:
	// the SPA reads it and echoes it back in the X-CSRF-Token header, which
	// middleware.RequireCSRF matches against this cookie. An attacker on another
	// origin can neither read this cookie nor set the matching custom header.
	c.SetCookie(cookieCSRF, csrfToken, refreshMaxAge, "/", "", secure, false)
}

// newCSRFToken returns a high-entropy token for the double-submit CSRF cookie.
func newCSRFToken() (string, error) {
	b := make([]byte, 32) // 256 bits of entropy
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("newCSRFToken: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// clearSessionCookies expires every session cookie. Called on logout and when a
// refresh is rejected, so a dead session leaves nothing behind in the browser.
// The path of each delete MUST match the path it was set with, or the browser
// keeps the original cookie.
func clearSessionCookies(c *gin.Context, cfg *config.Config) {
	secure := cfg.AppEnv == "production"
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(cookieAccess, "", -1, "/", "", secure, true)
	c.SetCookie(cookieRefresh, "", -1, refreshCookiePath, "", secure, true)
	c.SetCookie(cookieRole, "", -1, "/", "", secure, false)
	c.SetCookie(cookieExpiry, "", -1, "/", "", secure, false)
	c.SetCookie(cookieCSRF, "", -1, "/", "", secure, false)
}
