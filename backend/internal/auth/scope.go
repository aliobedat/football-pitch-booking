package auth

// Scope is the DB-resolved authorization scope injected per request by
// middleware.ResolveScope. It deliberately lives OUTSIDE the JWT (the token
// carries only { sub, role, exp }): scope can be revoked/rebound in the DB and
// must take effect on the very next request, which a baked-in token claim could
// not guarantee.
//
// For a `staff` actor, BoundPitchID is the single pitch (V1) they may operate and
// ProvisionedBy is the owner who bound them. For every other role the zero value
// applies (owners are scoped to their owned pitches in the data layer; admins are
// unscoped).
type Scope struct {
	BoundPitchID  int
	ProvisionedBy int
}

// IsStaffBound reports whether this scope carries a concrete staff→pitch binding.
func (s Scope) IsStaffBound() bool { return s.BoundPitchID > 0 }
