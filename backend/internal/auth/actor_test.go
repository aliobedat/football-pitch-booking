package auth

import "testing"

// OwnerScopeFilter is the single owner-scoping primitive (Dashboard PR 2 flag).
// These assert the structural contract every owner-scoped query relies on:
// admins are unscoped ("TRUE", no args); owners get a parameterised predicate
// bound to their own id at the requested placeholder index.
func TestOwnerScopeFilter_Owner(t *testing.T) {
	a := Actor{UserID: 42, Role: RoleOwner}
	clause, args := a.OwnerScopeFilter("p.owner_id", 3)
	if clause != "p.owner_id = $3" {
		t.Fatalf("clause = %q, want %q", clause, "p.owner_id = $3")
	}
	if len(args) != 1 || args[0] != 42 {
		t.Fatalf("args = %v, want [42]", args)
	}
}

func TestOwnerScopeFilter_AdminUnscoped(t *testing.T) {
	for _, role := range []string{RoleAdmin} {
		a := Actor{UserID: 1, Role: role}
		clause, args := a.OwnerScopeFilter("p.owner_id", 1)
		if clause != "TRUE" {
			t.Fatalf("role %s: clause = %q, want TRUE (unscoped)", role, clause)
		}
		if args != nil {
			t.Fatalf("role %s: args = %v, want nil", role, args)
		}
	}
}

func TestOwnerScopeFilter_StaffIsScopedNotUnscoped(t *testing.T) {
	// Defensive: a staff/player actor must NOT be treated as unscoped — only admin
	// gets "TRUE". (Staff never reach owner-scoped read queries, but the primitive
	// must fail safe regardless.)
	a := Actor{UserID: 9, Role: RoleStaff}
	clause, args := a.OwnerScopeFilter("p.owner_id", 1)
	if clause == "TRUE" || len(args) != 1 {
		t.Fatalf("staff got unscoped filter (clause=%q args=%v); must be scoped", clause, args)
	}
}
