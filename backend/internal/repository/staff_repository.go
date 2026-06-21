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
	// ErrStaffUserNotFound — no user exists for the supplied phone. Staff must
	// have registered (logged in once) before they can be provisioned. Maps to 404.
	ErrStaffUserNotFound = errors.New("staff: no registered user for that phone")
	// ErrCannotBindPrivileged — the target is an owner/admin (or the inviting
	// owner themselves); they cannot be demoted into a staff binding. Maps to 422.
	ErrCannotBindPrivileged = errors.New("staff: target user cannot be assigned as staff")
	// ErrStaffBindingNotFound — no staff binding for that user under this owner
	// (never bound, already revoked, or belongs to a different owner). Maps to 404.
	ErrStaffBindingNotFound = errors.New("staff: no binding for that user under this owner")
)

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
	// atomically: it verifies the owner owns EVERY pitchID, finds the target user by
	// E.164 phone, promotes them to the `staff` role, and inserts a binding per
	// pitch (idempotent — ON CONFLICT DO NOTHING, so re-inviting to add pitches is
	// safe). Ownership check + promotion + inserts run in one transaction so a
	// partial provision is impossible. Returns the user's full binding set.
	CreateStaffBindings(ctx context.Context, ownerID int, pitchIDs []int, phoneE164 string) (*StaffMember, error)

	// ListStaffForOwner returns every staff member the owner has provisioned, each
	// grouped with all of their bound pitches.
	ListStaffForOwner(ctx context.Context, ownerID int) ([]StaffMember, error)

	// RevokeStaff removes a staff member the owner provisioned: it deletes ALL of
	// their bindings under this owner and demotes the user back to `player` (only if
	// no bindings remain anywhere), in one transaction. Strictly owner-scoped — an
	// owner can only revoke staff bound to THEM (staff.owner_id = ownerID); a foreign
	// or non-existent binding yields ErrStaffBindingNotFound (→404) and writes nothing.
	RevokeStaff(ctx context.Context, ownerID, staffUserID int) error
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

func (r *staffRepo) CreateStaffBindings(ctx context.Context, ownerID int, pitchIDs []int, phoneE164 string) (*StaffMember, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. STRICT OWNERSHIP GUARD: the owner must own EVERY live pitch in the set.
	//    Counting distinct owned pitches against the distinct requested set rejects
	//    the whole request if even one pitch is foreign/deleted (all-or-nothing).
	var ownedCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(DISTINCT id) FROM pitches
		 WHERE id = ANY($1) AND owner_id = $2 AND deleted_at IS NULL`,
		pitchIDs, ownerID,
	).Scan(&ownedCount); err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: ownership check: %w", err)
	}
	if ownedCount != distinctCount(pitchIDs) {
		return nil, ErrPitchNotOwned
	}

	// 2. Resolve the target user by phone. They must already exist.
	var targetID int
	var role string
	err = tx.QueryRow(ctx,
		`SELECT id, role::text FROM users WHERE phone = $1`, phoneE164,
	).Scan(&targetID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStaffUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: lookup user: %w", err)
	}

	// 3. Eligible targets: a PLAYER (fresh promotion) OR a user already staff UNDER
	//    THIS OWNER (incremental pitch add). Reject self, owner/admin, and staff
	//    provisioned by a DIFFERENT owner — never silently re-home or demote.
	if targetID == ownerID {
		return nil, ErrCannotBindPrivileged
	}
	switch role {
	case auth.RolePlayer:
		// ok — will be promoted below.
	case auth.RoleStaff:
		var foreign bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM staff WHERE user_id = $1 AND owner_id <> $2)`,
			targetID, ownerID,
		).Scan(&foreign); err != nil {
			return nil, fmt.Errorf("CreateStaffBindings: foreign-owner check: %w", err)
		}
		if foreign {
			return nil, ErrCannotBindPrivileged
		}
	default: // owner / admin
		return nil, ErrCannotBindPrivileged
	}

	// 4. Insert one binding per pitch, idempotently (composite UNIQUE(user_id,
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

	// 5. Promote to staff (idempotent if already staff).
	if _, err := tx.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, targetID); err != nil {
		return nil, fmt.Errorf("CreateStaffBindings: promote: %w", err)
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

func (r *staffRepo) RevokeStaff(ctx context.Context, ownerID, staffUserID int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("RevokeStaff: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Delete the binding ONLY when it belongs to this owner. The owner_id
	//    predicate is the isolation guard: an owner can never revoke another owner's
	//    staff. No row deleted (foreign/absent binding) → ErrStaffBindingNotFound.
	ct, err := tx.Exec(ctx,
		`DELETE FROM staff WHERE user_id = $1 AND owner_id = $2`, staffUserID, ownerID)
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

func (r *staffRepo) ListStaffForOwner(ctx context.Context, ownerID int) ([]StaffMember, error) {
	// One row per (user, pitch); grouped into StaffMember in Go. Ordered so each
	// user's rows are contiguous (newest member first by their latest binding),
	// pitches within a member by name.
	rows, err := r.db.Query(ctx, `
		SELECT s.user_id, COALESCE(u.phone,''), COALESCE(u.full_name,''), s.pitch_id, p.name,
		       max(s.created_at) OVER (PARTITION BY s.user_id) AS member_recency
		FROM staff s
		JOIN users u   ON u.id = s.user_id
		JOIN pitches p ON p.id = s.pitch_id
		WHERE s.owner_id = $1
		ORDER BY member_recency DESC, s.user_id, p.name`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("ListStaffForOwner: %w", err)
	}
	defer rows.Close()

	out := []StaffMember{}
	idx := map[int]int{} // user_id → position in out
	for rows.Next() {
		var userID, pitchID int
		var phone, fullName, pitchName string
		var recency time.Time
		if err := rows.Scan(&userID, &phone, &fullName, &pitchID, &pitchName, &recency); err != nil {
			return nil, fmt.Errorf("ListStaffForOwner: scan: %w", err)
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
