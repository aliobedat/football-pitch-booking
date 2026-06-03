package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CSRF double-submit cookie constants. The cookie is set (readable) by the auth
// handlers (handlers.cookieCSRF); the client echoes it back in this header.
const (
	csrfCookieName = "malaab_csrf"
	csrfHeaderName = "X-CSRF-Token"
)

// RequireCSRF enforces the double-submit-cookie CSRF defence on state-changing,
// cookie-authenticated requests.
//
// Exemptions (both genuinely CSRF-immune):
//   - Safe methods (GET/HEAD/OPTIONS/TRACE) — they must not mutate state.
//   - Requests carrying an Authorization: Bearer header — a programmatic client
//     or test. A cross-site attacker cannot set a custom Authorization header
//     (it forces a CORS preflight our allowlist rejects), so ambient cookies
//     alone can never produce a Bearer request.
//
// For everything else the X-CSRF-Token header must equal the malaab_csrf cookie
// (compared in constant time). A cross-site attacker can make the browser send
// the cookie automatically, but can neither read its value nor set the matching
// custom header — so the two cannot be made to agree from another origin.
//
// Chain this AFTER RequireAuth on protected routes; by the time CSRF is checked
// the session is already known to be valid.
func RequireCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) || hasBearerToken(c) {
			c.Next()
			return
		}

		cookie, err := c.Cookie(csrfCookieName)
		header := c.GetHeader(csrfHeaderName)
		if err != nil || cookie == "" || header == "" ||
			subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "csrf_failed",
				"message": "missing or invalid CSRF token",
			})
			return
		}

		c.Next()
	}
}

// isSafeMethod reports whether the HTTP method is read-only per RFC 7231 and so
// exempt from CSRF protection.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// hasBearerToken reports whether the request authenticates via an
// Authorization: Bearer header (vs. the session cookie).
func hasBearerToken(c *gin.Context) bool {
	header := c.GetHeader("Authorization")
	return header != "" && strings.HasPrefix(strings.ToLower(header), "bearer ")
}
