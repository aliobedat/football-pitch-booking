package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
)

// Context keys for values injected by RequireAuth.
// Using string constants with a namespaced prefix prevents collisions with
// Gin's own keys and any third-party middleware.
const (
	ContextKeyUserID = "malaab.user_id"
	ContextKeyRole   = "malaab.role"
	ContextKeyActor  = "malaab.actor"
)

// RequireAuth validates the Bearer JWT in the Authorization header.
// On success it injects user_id and role into the Gin context.
// On failure it aborts the request with a precise, non-leaking error.
//
// Usage:
//
//	protected := v1.Group("/").Use(middleware.RequireAuth(jwtManager))
func RequireAuth(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, ok := extractToken(c)
		if !ok {
			abortUnauthorized(c, "missing_token", "a valid session cookie or bearer token is required")
			return
		}

		claims, err := jwtManager.ValidateAccessToken(tokenString)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrTokenExpired):
				abortUnauthorized(c, "token_expired", "access token has expired, please refresh")
			case errors.Is(err, auth.ErrTokenMalformed):
				abortUnauthorized(c, "token_malformed", "token format is invalid")
			default:
				abortUnauthorized(c, "token_invalid", "token could not be verified")
			}
			return
		}

		// Inject validated identity into context for downstream handlers. The
		// typed Actor is the canonical handle used for ownership scoping; the
		// individual id/role keys are retained for existing callers.
		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyRole, claims.Role)
		c.Set(ContextKeyActor, auth.Actor{UserID: claims.UserID, Role: claims.Role})

		c.Next()
	}
}

// RequireRole returns a middleware that permits access only to users whose role
// is in the provided list. Must be chained after RequireAuth.
//
// Usage:
//
//	ownerOnly := middleware.RequireRole("owner")
//	adminOrOwner := middleware.RequireRole("admin", "owner")
func RequireRole(roles ...string) gin.HandlerFunc {
	// Build a set for O(1) lookup
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}

	return func(c *gin.Context) {
		role, exists := c.Get(ContextKeyRole)
		if !exists {
			// RequireAuth was not chained before RequireRole — programming error
			abortUnauthorized(c, "unauthenticated", "authentication is required")
			return
		}

		roleStr, ok := role.(string)
		if !ok {
			abortForbidden(c)
			return
		}

		if _, permitted := allowed[roleStr]; !permitted {
			abortForbidden(c)
			return
		}

		c.Next()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Context getter helpers
// Expose typed getters so handlers never use raw string keys.
// ─────────────────────────────────────────────────────────────────────────────

// GetUserID retrieves the authenticated user's ID from the Gin context.
// Returns 0 if RequireAuth has not run (should never happen on protected routes).
func GetUserID(c *gin.Context) int {
	id, _ := c.Get(ContextKeyUserID)
	userID, _ := id.(int)
	return userID
}

// GetUserRole retrieves the authenticated user's role from the Gin context.
func GetUserRole(c *gin.Context) string {
	role, _ := c.Get(ContextKeyRole)
	r, _ := role.(string)
	return r
}

// GetActor retrieves the authenticated principal from the Gin context. It is the
// handle handlers pass into the data layer for ownership scoping. Falls back to
// assembling one from the id/role keys so it is robust even if only those were
// set. Returns a zero Actor (UserID 0, empty Role) if RequireAuth has not run.
func GetActor(c *gin.Context) auth.Actor {
	if v, ok := c.Get(ContextKeyActor); ok {
		if a, ok := v.(auth.Actor); ok {
			return a
		}
	}
	return auth.Actor{UserID: GetUserID(c), Role: GetUserRole(c)}
}

// ─────────────────────────────────────────────────────────────────────────────
// Private helpers
// ─────────────────────────────────────────────────────────────────────────────

// accessCookieName is the httpOnly cookie carrying the access JWT for browser
// clients. It mirrors handlers.cookieAccess (duplicated, not imported, because
// handlers depends on middleware — importing it back would be a cycle).
const accessCookieName = "malaab_access"

// extractToken pulls the access token from the session cookie first (the browser
// path), then falls back to the Authorization header for non-browser API clients
// and tests. Cookie-first keeps the httpOnly-cookie migration transparent to
// handlers while preserving header auth for programmatic callers.
func extractToken(c *gin.Context) (string, bool) {
	if cookie, err := c.Cookie(accessCookieName); err == nil && cookie != "" {
		return cookie, true
	}
	return extractBearerToken(c)
}

// extractBearerToken parses the Authorization header and returns the raw token.
// Returns false if the header is absent or not in "Bearer <token>" format.
func extractBearerToken(c *gin.Context) (string, bool) {
	header := c.GetHeader("Authorization")
	if header == "" {
		return "", false
	}

	// Must be exactly "Bearer <token>" — two parts, first must be "Bearer"
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
		return "", false
	}

	return parts[1], true
}

func abortUnauthorized(c *gin.Context, code, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error":   code,
		"message": message,
	})
}

func abortForbidden(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error":   "forbidden",
		"message": "you do not have permission to access this resource",
	})
}