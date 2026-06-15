package repository

// StaffRepository is the persistence seam for the owner-provisioned `staff` role
// (Dashboard PR 2). It owns the staff→pitch binding and the strict ownership
// invariant: an owner may bind a staff member ONLY to a pitch they actually own.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint (23505)
// violation — the UNIQUE(user_id) cap that enforces one pitch per staff member.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Sentinel errors so handlers can map precisely to HTTP status codes.
var (
	// ErrPitchNotOwned — the owner does not own the target pitch (or it is
	// soft-deleted). The core ownership guard; maps to 403.
	ErrPitchNotOwned = errors.New("staff: owner does not own the target pitch")
	// ErrStaffUserNotFound — no user exists for the supplied phone. Staff must
	// have registered (logged in once) before they can be provisioned. Maps to 404.
	ErrStaffUserNotFound = errors.New("staff: no registered user for that phone")
	// ErrStaffAlreadyBound — the target user is already bound to a pitch (V1 caps
	// a staff member to a single pitch). Maps to 409.
	ErrStaffAlreadyBound = errors.New("staff: user is already assigned to a pitch")
	// ErrCannotBindPrivileged — the target is an owner/admin (or the inviting
	// owner themselves); they cannot be demoted into a staff binding. Maps to 422.
	ErrCannotBindPrivileged = errors.New("staff: target user cannot be assigned as staff")
)

// StaffBinding is one staff→pitch assignment.
type StaffBinding struct {
	ID       int    `json:"id"`
	UserID   int    `json:"user_id"`
	PitchID  int    `json:"pitch_id"`
	OwnerID  int    `json:"owner_id"`
	Phone    string `json:"phone"`
	FullName string `json:"full_name"`
}

// StaffRepository persists staff bindings.
type StaffRepository interface {
	// StaffBinding resolves the single pitch (+ provisioning owner) a staff user
	// is bound to. `found` is false (no error) when the user has no binding. Read
	// on every staff request by the scope guard.
	StaffBinding(ctx context.Context, userID int) (pitchID int, ownerID int, found bool, err error)

	// CreateStaffBinding provisions a staff member atomically: it verifies the
	// owner owns pitchID, finds the target user by E.164 phone, promotes them to
	// the `staff` role, and inserts the binding. The ownership check and the
	// promotion happen in one transaction so a partial provision is impossible.
	CreateStaffBinding(ctx context.Context, ownerID, pitchID int, phoneE164 string) (*StaffBinding, error)

	// ListStaffForOwner returns every staff member the owner has provisioned.
	ListStaffForOwner(ctx context.Context, ownerID int) ([]StaffBinding, error)
}

type staffRepo struct {
	db *pgxpool.Pool
}

// NewStaffRepository constructs a Postgres-backed StaffRepository.
func NewStaffRepository(db *pgxpool.Pool) StaffRepository {
	return &staffRepo{db: db}
}

func (r *staffRepo) StaffBinding(ctx context.Context, userID int) (int, int, bool, error) {
	var pitchID, ownerID int
	err := r.db.QueryRow(ctx,
		`SELECT pitch_id, owner_id FROM staff WHERE user_id = $1`, userID,
	).Scan(&pitchID, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("StaffBinding: %w", err)
	}
	return pitchID, ownerID, true, nil
}

func (r *staffRepo) CreateStaffBinding(ctx context.Context, ownerID, pitchID int, phoneE164 string) (*StaffBinding, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("CreateStaffBinding: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. STRICT OWNERSHIP GUARD: the owner must actually own this (live) pitch.
	var ownsIt bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pitches WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL)`,
		pitchID, ownerID,
	).Scan(&ownsIt); err != nil {
		return nil, fmt.Errorf("CreateStaffBinding: ownership check: %w", err)
	}
	if !ownsIt {
		return nil, ErrPitchNotOwned
	}

	// 2. Resolve the target user by phone. They must already exist.
	var targetID int
	var role, fullName string
	err = tx.QueryRow(ctx,
		`SELECT id, role::text, COALESCE(full_name,'') FROM users WHERE phone = $1`, phoneE164,
	).Scan(&targetID, &role, &fullName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStaffUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("CreateStaffBinding: lookup user: %w", err)
	}

	// 3. PROMOTE ONLY A PLAYER. Reject self-binding and any non-player role
	//    (owner/admin/already-staff) — no silent demotion, no re-binding.
	if targetID == ownerID || role != auth.RolePlayer {
		return nil, ErrCannotBindPrivileged
	}

	// 4. Insert the binding; UNIQUE(user_id) enforces the single-pitch-per-staff
	//    cap. A duplicate surfaces as ErrStaffAlreadyBound.
	var bindingID int
	err = tx.QueryRow(ctx,
		`INSERT INTO staff (user_id, pitch_id, owner_id) VALUES ($1, $2, $3) RETURNING id`,
		targetID, pitchID, ownerID,
	).Scan(&bindingID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrStaffAlreadyBound
		}
		return nil, fmt.Errorf("CreateStaffBinding: insert: %w", err)
	}

	// 5. Promote to staff (idempotent if already staff).
	if _, err := tx.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, targetID); err != nil {
		return nil, fmt.Errorf("CreateStaffBinding: promote: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("CreateStaffBinding: commit: %w", err)
	}

	return &StaffBinding{
		ID: bindingID, UserID: targetID, PitchID: pitchID, OwnerID: ownerID,
		Phone: phoneE164, FullName: fullName,
	}, nil
}

func (r *staffRepo) ListStaffForOwner(ctx context.Context, ownerID int) ([]StaffBinding, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.user_id, s.pitch_id, s.owner_id, COALESCE(u.phone,''), COALESCE(u.full_name,'')
		FROM staff s JOIN users u ON u.id = s.user_id
		WHERE s.owner_id = $1
		ORDER BY s.created_at DESC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("ListStaffForOwner: %w", err)
	}
	defer rows.Close()

	var out []StaffBinding
	for rows.Next() {
		var b StaffBinding
		if err := rows.Scan(&b.ID, &b.UserID, &b.PitchID, &b.OwnerID, &b.Phone, &b.FullName); err != nil {
			return nil, fmt.Errorf("ListStaffForOwner: scan: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
