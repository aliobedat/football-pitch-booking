package auth

// Scope is the DB-resolved authorization scope injected per request by
// middleware.ResolveScope. It deliberately lives OUTSIDE the JWT (the token
// carries only { sub, role, exp }): scope can be revoked/rebound in the DB and
// must take effect on the very next request, which a baked-in token claim could
// not guarantee.
//
// For a `staff` actor, BoundPitchIDs is the set of pitches they may operate (1:N —
// an owner may staff one guard across several pitches of the same complex) and
// ProvisionedBy is the owner who bound them. For every other role the zero value
// applies (owners are scoped to their owned pitches in the data layer; admins are
// unscoped).
type Scope struct {
	BoundPitchIDs []int
	ProvisionedBy int
}

// IsStaffBound reports whether this scope carries at least one staff→pitch binding.
func (s Scope) IsStaffBound() bool { return len(s.BoundPitchIDs) > 0 }
