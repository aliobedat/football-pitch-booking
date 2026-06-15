package auth

import "fmt"

// Role constants for the authorization model. Stored in users.role and embedded
// in the JWT `rol` claim. `player` books pitches; `owner` is a tenant that owns
// pitches; `admin` is a superuser with unscoped access.
const (
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
	RolePlayer = "player"
	// RoleStaff is an owner-provisioned operator bound to a single pitch (V1).
	// A staff member operates the day-to-day of their bound pitch but is
	// categorically barred from finance/analytics (enforced server-side).
	RoleStaff = "staff"
)

// Actor is the authenticated principal behind a request, derived solely from the
// validated JWT claims. It is threaded into the data layer so ownership scoping
// is enforced in SQL rather than only in handlers.
type Actor struct {
	UserID int
	Role   string
}

// IsAdmin reports whether the actor has unscoped (superuser) access.
func (a Actor) IsAdmin() bool { return a.Role == RoleAdmin }

// OwnerScope returns the user id an ownership predicate must match, or 0 for an
// admin. Zero is the established data-layer convention meaning "no ownership
// filter" — admins bypass the predicate and see/modify every row.
func (a Actor) OwnerScope() int {
	if a.IsAdmin() {
		return 0
	}
	return a.UserID
}

// OwnerScopeFilter returns a SQL WHERE-fragment scoping `col` to the rows this
// actor owns, plus the positional args to bind (placeholders begin at startIdx).
// For an admin it returns ("TRUE", nil) — unscoped.
//
// This is the ONE canonical owner-scoping primitive: a new owner-scoped query
// composes this helper instead of hand-writing `AND owner_id = $n`, so ownership
// isolation is structural by construction — a developer cannot silently omit the
// predicate and leak another owner's rows. Example:
//
//	clause, sargs := actor.OwnerScopeFilter("p.owner_id", len(args)+1)
//	wheres = append(wheres, clause)
//	args = append(args, sargs...)
func (a Actor) OwnerScopeFilter(col string, startIdx int) (clause string, args []any) {
	if a.IsAdmin() {
		return "TRUE", nil
	}
	return fmt.Sprintf("%s = $%d", col, startIdx), []any{a.UserID}
}
