package repository

// StaffRepository is the persistence seam for the owner-provisioned `staff` role
// (Dashboard PR 2). It owns the staff→pitch binding and the strict ownership
// invariant: an owner may bind a staff member ONLY to a pitch they actually own.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// Sentinel errors so handlers can map precisely to HTTP status codes.
var (
	// ErrPitchNotOwned — the owner does not own one of the target pitches (or it is
	// soft-deleted). The core ownership guard; maps to 403.
	ErrPitchNotOwned = errors.New("staff: owner does not own the target pitch")
	// ErrStaffUserNotFound — no user exists for the supplied phone. Retained for
	// API compatibility; the onboarding flow now CREATES the user instead of
	// rejecting an unregistered phone, so this is no longer returned by the create
	// path. Maps to 404 where still referenced.
	ErrStaffUserNotFound = errors.New("staff: no registered user for that phone")
	// ErrCannotBindPrivileged — the target is an owner/admin (or the inviting
	// owner themselves); they cannot be demoted into a staff binding. Maps to 422.
	ErrCannotBindPrivileged = errors.New("staff: target user cannot be assigned as staff")
	// ErrStaffForeignOwner — the target is already staff provisioned by a DIFFERENT
	// owner and the caller is not allowed to re-home them. Maps to 403.
	ErrStaffForeignOwner = errors.New("staff: target is staff under another owner")
	// ErrPasswordRequired — a password is mandatory for this onboarding case (a
	// brand-new user, or promoting a player who has no password yet) but none was
	// supplied. Maps to 422.
	ErrPasswordRequired = errors.New("staff: a password is required to provision this user")
	// ErrStaffBindingNotFound — no staff binding for that user under this owner
	// (never bound, already revoked, or belongs to a different owner). Maps to 404.
	ErrStaffBindingNotFound = errors.New("staff: no binding for that user under this owner")
)

// StaffProvision carries the optional onboarding inputs for CreateStaffBindings.
// The handler computes PasswordHash (bcrypt) before calling — the repository never
// sees plaintext. Empty strings mean "not provided" (leave existing value).
type StaffProvision struct {
	FullName     string // optional; saved/updated when non-empty
	PasswordHash string // optional bcrypt hash; set/reset when non-empty
}

// StaffPitch is one pitch a staff member is bound to (with its display name).
type StaffPitch struct {
	PitchID   int    `json:"pitch_id"`
	PitchName string `json:"pitch_name"`
}

// StaffMember is one provisioned guard and the SET of pitches they operate (1:N).
// It groups the underlying per-pitch staff rows by user for the owner-facing list
// and the invite response.
type StaffMember struct {
	UserID   int          `json:"user_id"`
	OwnerID  int          `json:"owner_id"`
	Phone    string       `json:"phone"`
	FullName string       `json:"full_name"`
	Pitches  []StaffPitch `json:"pitches"`
}

// StaffRepository persists staff bindings.
type StaffRepository interface {
	// StaffBindings resolves EVERY pitch (+ provisioning owner) a staff user is
	// bound to (1:N). `found` is false (no error) when the user has no bindings.
	// Read on every staff request by the scope guard.
	StaffBindings(ctx context.Context, userID int) (pitchIDs []int, ownerID int, found bool, err error)

	// CreateStaffBindings provisions a staff member across one or more pitches
	// atomically: it validates the pitch set against the ACTOR's authority, finds
	// the target user by E.164 phone, promotes them to the `staff` role, and inserts
	// a binding per pitch (idempotent — ON CONFLICT DO NOTHING). Authority:
	//   - owner: must own EVERY pitch in the set; the binding is scoped to the owner.
	//   - admin: may bind to ANY live pitch, but the binding still carries a single
	//     owner_id (single-owner invariant), so all selected pitches must be live and
	//     share ONE owner — that owner becomes the binding's owner_id (NOT the admin).
	// A foreign/deleted/mixed-owner set yields ErrPitchNotOwned. All steps run in one
	// transaction so a partial provision is impossible. Returns the full binding set.
	//
	// Onboarding (role-aware ensure, all in-tx, target row locked FOR UPDATE before
	// any write):
	//   - phone not in users     → CREATE the user as staff (prov.PasswordHash required)
	//   - existing player        → promote to staff (password required iff they have
	//                              none yet); set/reset password + full_name if provided
	//   - existing staff         → re-bind (password optional; reset if provided)
	//   - existing owner/admin    → ErrCannotBindPrivileged (no role/password/bind writes)
	//   - staff under another owner (caller not admin) → ErrStaffForeignOwner
	//   - missing required password → ErrPasswordRequired
	CreateStaffBindings(ctx context.Context, actor auth.Actor, pitchIDs []int, phoneE164 string, prov StaffProvision) (*StaffMember, error)

	// ListStaff returns provisioned staff grouped with their bound pitches, scoped to
	// the actor: an owner sees only their own bindings; an admin sees ALL bindings
	// across every owner (admin-bypass via Actor.OwnerScopeFilter).
	ListStaff(ctx context.Context, actor auth.Actor) ([]StaffMember, error)

	// RevokeStaff removes a staff member's bindings and demotes them back to `player`
	// (only if no bindings remain anywhere), in one transaction. Scoped to the actor:
	//   - owner: may revoke ONLY staff bound to THEM (staff.owner_id = owner.id).
	//   - admin: may revoke ANY staff binding (admin-bypass via OwnerScopeFilter).
	// A foreign/non-existent binding (for an owner) yields ErrStaffBindingNotFound
	// (→404) and writes nothing.
	RevokeStaff(ctx context.Context, actor auth.Actor, staffUserID int) error
}

type staffRepo struct {
	db *pgxpool.Pool
}

// NewStaffRepository constructs a Postgres-backed StaffRepository.
func NewStaffRepository(db *pgxpool.Pool) StaffRepository {
	return &staffRepo{db: db}
}

func (r *staffRepo) StaffBindings(ctx context.Context, userID int) ([]int, int, bool, error) {
	rows, err := r.db.Query(ctx,
		`SELECT pitch_id, owner_id FROM staff WHERE user_id = $1 ORDER BY pitch_id`, userID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("StaffBindings: %w", err)
	}
	defer rows.Close()

	var pitchIDs []int
	var ownerID int
	for rows.Next() {
		var pid, oid int
		if err := rows.Scan(&pid, &oid); err != nil {
			return nil, 0, false, fmt.Errorf("StaffBindings: scan: %w", err)
		}
		pitchIDs = append(pitchIDs, pid)
		ownerID = oid // single-owner invariant (a staff user is bound under one owner)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("StaffBindings: %w", err)
	}
	if len(pitchIDs) == 0 {
		return nil, 0, false, nil
	}
	return pitchIDs, ownerID, true, nil
}

func (r *staffRepo) CreateStaffBindings(ctx context.Context, actor auth.Actor, pitchIDs []int, phoneE164 string, prov StaffProvision) (*StaffMember, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. AUTHORITY + OWNER RESOLUTION. An owner must own every live pitch; an admin
	//    may bind to any live pitch but the binding still carries one real owner (the
	//    pitches' shared owner — never the admin). Either path rejects a foreign/
	//    deleted/mixed-owner set with ErrPitchNotOwned. The resolved ownerID is the
	//    binding's owner_id everywhere below, preserving the single-owner invariant.
	ownerID, err := resolveBindingOwnerTx(ctx, tx, actor, pitchIDs)
	if err != nil {
		return nil, err
	}

	// 2. Lock + read the target by phone (role-aware ensure). FOR UPDATE serialises
	//    against a concurrent role/password change so the decision below cannot race
	//    a write. A missing row means a brand-new user to create.
	var (
		targetID    int
		role        string
		hasPassword bool
		exists      bool
	)
	err = tx.QueryRow(ctx,
		`SELECT id, role::text, password_hash IS NOT NULL FROM users WHERE phone = $1 FOR UPDATE`,
		phoneE164,
	).Scan(&targetID, &role, &hasPassword)
	switch {
	case err == nil:
		exists = true
	case errors.Is(err, pgx.ErrNoRows):
		exists = false
	default:
		return nil, fmt.Errorf("CreateStaffBindings: lookup user: %w", err)
	}

	// 3. Decide eligibility + password requirement BEFORE any write (fail closed).
	if exists {
		if targetID == ownerID {
			return nil, ErrCannotBindPrivileged // an owner cannot enroll themselves
		}
		switch role {
		case auth.RolePlayer:
			// Promotion: a password is mandatory unless they already have one.
			if !hasPassword && prov.PasswordHash == "" {
				return nil, ErrPasswordRequired
			}
		case auth.RoleStaff:
			// Already staff: re-bind. Refuse if provisioned by a DIFFERENT owner
			// (never silently re-home). Password optional.
			var foreign bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM staff WHERE user_id = $1 AND owner_id <> $2)`,
				targetID, ownerID,
			).Scan(&foreign); err != nil {
				return nil, fmt.Errorf("CreateStaffBindings: foreign-owner check: %w", err)
			}
			if foreign {
				return nil, ErrStaffForeignOwner
			}
		default: // owner / admin (or any unknown role) → refuse, touch nothing.
			return nil, ErrCannotBindPrivileged
		}
	} else {
		// Brand-new user: a password is always required (they must be able to log in).
		if prov.PasswordHash == "" {
			return nil, ErrPasswordRequired
		}
	}

	// 4. Ensure the user row: create a fresh staff user, or update the existing
	//    player/staff. full_name + password_hash are written only when provided
	//    (NULLIF coalesces empty → keep current). role lands on 'staff' either way.
	if exists {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET
				role          = 'staff',
				full_name     = COALESCE(NULLIF($2,''), full_name),
				password_hash = COALESCE(NULLIF($3,''), password_hash),
				updated_at    = NOW()
			WHERE id = $1
		`, targetID, prov.FullName, prov.PasswordHash); err != nil {
			return nil, fmt.Errorf("CreateStaffBindings: update user: %w", err)
		}
	} else {
		// Race-safe insert: a concurrent create of the same phone trips users.phone
		// UNIQUE → the tx fails and rolls back (no orphan), which is fail-closed.
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (phone, role, full_name, password_hash, opt_in)
			VALUES ($1, 'staff', NULLIF($2,''), $3, FALSE)
			RETURNING id
		`, phoneE164, prov.FullName, prov.PasswordHash).Scan(&targetID); err != nil {
			return nil, fmt.Errorf("CreateStaffBindings: create user: %w", err)
		}
	}

	// 5. Insert one binding per pitch, idempotently (composite UNIQUE(user_id,
	//    pitch_id) backs ON CONFLICT DO NOTHING) so re-inviting to add pitches never
	//    errors on an existing one.
	for _, pid := range pitchIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO staff (user_id, pitch_id, owner_id) VALUES ($1, $2, $3)
			 ON CONFLICT (user_id, pitch_id) DO NOTHING`,
			targetID, pid, ownerID,
		); err != nil {
			return nil, fmt.Errorf("CreateStaffBindings: insert pitch %d: %w", pid, err)
		}
	}

	member, err := loadStaffMemberTx(ctx, tx, ownerID, targetID)
	if err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: reload: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: commit: %w", err)
	}
	return member, nil
}

// resolveBindingOwnerTx validates pitchIDs against the actor's authority and
// returns the owner_id the resulting staff binding must carry:
//   - owner: must own EVERY live pitch in the set; returns the owner's id.
//   - admin: may bind to any live pitch, but a staff binding still carries ONE
//     owner (single-owner invariant), so all selected pitches must be live and
//     share a single owner; returns THAT owner's id (never the admin's).
//
// A foreign, soft-deleted, non-existent, or mixed-owner set yields ErrPitchNotOwned.
// This is the staff-path application of the canonical admin-bypass convention
// (Actor.IsAdmin); owner isolation is unchanged.
func resolveBindingOwnerTx(ctx context.Context, tx pgx.Tx, actor auth.Actor, pitchIDs []int) (int, error) {
	if actor.IsAdmin() {
		// Distinct owners across the requested LIVE pitches.
		rows, err := tx.Query(ctx,
			`SELECT DISTINCT owner_id FROM pitches
			 WHERE id = ANY($1) AND deleted_at IS NULL`, pitchIDs)
		if err != nil {
			return 0, fmt.Errorf("resolveBindingOwner: owners: %w", err)
		}
		defer rows.Close()
		var owners []int
		for rows.Next() {
			var oid int
			if err := rows.Scan(&oid); err != nil {
				return 0, fmt.Errorf("resolveBindingOwner: scan: %w", err)
			}
			owners = append(owners, oid)
		}
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("resolveBindingOwner: rows: %w", err)
		}
		// Must resolve to exactly one owner (rejects mixed-owner and all-missing sets)
		// AND every requested pitch must be live (rejects a set with any deleted/
		// non-existent id, matching the owner path's all-or-nothing semantics).
		if len(owners) != 1 {
			return 0, ErrPitchNotOwned
		}
		var liveCount int
		if err := tx.QueryRow(ctx,
			`SELECT count(DISTINCT id) FROM pitches
			 WHERE id = ANY($1) AND deleted_at IS NULL`, pitchIDs,
		).Scan(&liveCount); err != nil {
			return 0, fmt.Errorf("resolveBindingOwner: live count: %w", err)
		}
		if liveCount != distinctCount(pitchIDs) {
			return 0, ErrPitchNotOwned
		}
		return owners[0], nil
	}

	// Owner path: STRICT — must own every live pitch in the set (all-or-nothing).
	var ownedCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(DISTINCT id) FROM pitches
		 WHERE id = ANY($1) AND owner_id = $2 AND deleted_at IS NULL`,
		pitchIDs, actor.UserID,
	).Scan(&ownedCount); err != nil {
		return 0, fmt.Errorf("resolveBindingOwner: ownership check: %w", err)
	}
	if ownedCount != distinctCount(pitchIDs) {
		return 0, ErrPitchNotOwned
	}
	return actor.UserID, nil
}

// distinctCount returns the number of distinct ids in xs.
func distinctCount(xs []int) int {
	seen := make(map[int]struct{}, len(xs))
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

// loadStaffMemberTx reads one staff member (under ownerID) grouped with all their
// pitch bindings + names, inside the caller's tx.
func loadStaffMemberTx(ctx context.Context, tx pgx.Tx, ownerID, userID int) (*StaffMember, error) {
	rows, err := tx.Query(ctx, `
		SELECT u.id, COALESCE(u.phone,''), COALESCE(u.full_name,''), s.pitch_id, p.name
		FROM staff s
		JOIN users u   ON u.id = s.user_id
		JOIN pitches p ON p.id = s.pitch_id
		WHERE s.user_id = $1 AND s.owner_id = $2
		ORDER BY p.name`, userID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := &StaffMember{UserID: userID, OwnerID: ownerID}
	for rows.Next() {
		var sp StaffPitch
		if err := rows.Scan(&m.UserID, &m.Phone, &m.FullName, &sp.PitchID, &sp.PitchName); err != nil {
			return nil, err
		}
		m.Pitches = append(m.Pitches, sp)
	}
	return m, rows.Err()
}

func (r *staffRepo) RevokeStaff(ctx context.Context, actor auth.Actor, staffUserID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("RevokeStaff: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Delete the binding under the actor's scope. OwnerScopeFilter yields the
	//    owner-isolation predicate (owner_id = owner.id) for an owner — an owner can
	//    never revoke another owner's staff — and "TRUE" for an admin (revoke ANY
	//    binding). No row deleted (foreign/absent for an owner) → ErrStaffBindingNotFound.
	clause, sargs := actor.OwnerScopeFilter("owner_id", 2)
	args := append([]any{staffUserID}, sargs...)
	ct, err := tx.Exec(ctx,
		`DELETE FROM staff WHERE user_id = $1 AND `+clause, args...)
	if err != nil {
		return fmt.Errorf("RevokeStaff: delete binding: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrStaffBindingNotFound
	}

	// 2. Demote back to player ONLY if no bindings remain anywhere (this owner's are
	//    now gone; the NOT EXISTS guards the future multi-owner case so we never strip
	//    the staff role while another binding still relies on it). Scoped to
	//    role='staff' so we never clobber a role that changed out from under us.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'player'
		 WHERE id = $1 AND role = 'staff'
		   AND NOT EXISTS (SELECT 1 FROM staff WHERE user_id = $1)`, staffUserID); err != nil {
		return fmt.Errorf("RevokeStaff: demote: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("RevokeStaff: commit: %w", err)
	}
	return nil
}

func (r *staffRepo) ListStaff(ctx context.Context, actor auth.Actor) ([]StaffMember, error) {
	// One row per (user, pitch); grouped into StaffMember in Go. Ordered so each
	// user's rows are contiguous (newest member first by their latest binding),
	// pitches within a member by name. OwnerScopeFilter scopes to the owner's own
	// bindings, or "TRUE" for an admin (every owner's staff). owner_id is selected
	// per row so admin results carry each binding's real owner.
	clause, args := actor.OwnerScopeFilter("s.owner_id", 1)
	rows, err := r.db.Query(ctx, `
		SELECT s.user_id, s.owner_id, COALESCE(u.phone,''), COALESCE(u.full_name,''), s.pitch_id, p.name,
		       max(s.created_at) OVER (PARTITION BY s.user_id) AS member_recency
		FROM staff s
		JOIN users u   ON u.id = s.user_id
		JOIN pitches p ON p.id = s.pitch_id
		WHERE `+clause+`
		ORDER BY member_recency DESC, s.user_id, p.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("ListStaff: %w", err)
	}
	defer rows.Close()

	out := []StaffMember{}
	idx := map[int]int{} // user_id → position in out
	for rows.Next() {
		var userID, ownerID, pitchID int
		var phone, fullName, pitchName string
		var recency time.Time
		if err := rows.Scan(&userID, &ownerID, &phone, &fullName, &pitchID, &pitchName, &recency); err != nil {
			return nil, fmt.Errorf("ListStaff: scan: %w", err)
		}
		i, ok := idx[userID]
		if !ok {
			out = append(out, StaffMember{UserID: userID, OwnerID: ownerID, Phone: phone, FullName: fullName})
			i = len(out) - 1
			idx[userID] = i
		}
		out[i].Pitches = append(out[i].Pitches, StaffPitch{PitchID: pitchID, PitchName: pitchName})
	}
	return out, rows.Err()
}
