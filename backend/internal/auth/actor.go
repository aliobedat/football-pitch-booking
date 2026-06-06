package auth

// Role constants for the authorization model. Stored in users.role and embedded
// in the JWT `rol` claim. `player` books pitches; `owner` is a tenant that owns
// pitches; `admin` is a superuser with unscoped access.
const (
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
	RolePlayer = "player"
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
