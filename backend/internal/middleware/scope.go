package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
)

// ContextKeyScope holds the DB-resolved auth.Scope injected by ResolveScope.
const ContextKeyScope = "malaab.scope"

// StaffScopeResolver is the seam ResolveScope uses to look up a staff member's
// binding. Backed by repository.StaffRepository in production; faked in tests.
// (gin.Context satisfies context.Context, so the handler passes `c` directly.)
type StaffScopeResolver interface {
	StaffBinding(ctx context.Context, userID int) (pitchID int, ownerID int, found bool, err error)
}

// ResolveScope is the CENTRAL scope guard. Chained after RequireAuth inside the
// protected group, it resolves each actor's scope from the DB once per request
// and injects it into the context — scope is deliberately NOT in the JWT, so a
// rebind/revoke takes effect on the very next request.
//
//   - staff: load the single pitch binding. A staff user with NO binding is
//     rejected (403) — an un-provisioned staff account can do nothing.
//   - everyone else: an empty scope (owners are scoped to owned pitches in the
//     data layer; admins are unscoped; players carry no pitch scope).
//
// This is authorization plumbing, not the finance gate: finance/analytics routes
// additionally hard-reject staff via RequireRole("owner","admin").
func ResolveScope(resolver StaffScopeResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetUserRole(c)

		if role != auth.RoleStaff {
			c.Set(ContextKeyScope, auth.Scope{})
			c.Next()
			return
		}

		userID := GetUserID(c)
		pitchID, ownerID, found, err := resolver.StaffBinding(c, userID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "scope_resolution_failed", "message": "could not resolve your access scope",
			})
			return
		}
		if !found {
			// Provisioned-but-unbound staff (or a stale promotion): deny entirely.
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "staff_unprovisioned", "message": "no pitch is assigned to your account",
			})
			return
		}

		c.Set(ContextKeyScope, auth.Scope{BoundPitchID: pitchID, ProvisionedBy: ownerID})
		c.Next()
	}
}

// GetScope returns the DB-resolved scope injected by ResolveScope, or the zero
// scope if the guard did not run.
func GetScope(c *gin.Context) auth.Scope {
	if v, ok := c.Get(ContextKeyScope); ok {
		if s, ok := v.(auth.Scope); ok {
			return s
		}
	}
	return auth.Scope{}
}
