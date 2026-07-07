package repository

// WO-ENSUREBOOKINGUSER — Layer 1 regression proof against a REAL database.
// Proves the unauthenticated no-OTP booking upsert can never yield or mutate a
// privileged account, exercising the actual SQL (FOR UPDATE read, allowlist,
// guarded ON CONFLICT). Benign player behavior (create, JIT name) and the
// fresh-phone concurrency path are covered too.
//
// Gated on PITCH_SCOPING_TEST_DATABASE_URL (house convention). The acceptance
// run for this WO sets it to the dev DB so the suite RUNS (zero skips) — a
// silent skip is a failed gate. Every seeded/created row is cleaned up by phone.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run BookingSession -v

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/models"
)

func bookingAuthPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping booking-session escalation DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ephemeralPhone returns a unique JO mobile and registers deletion of any user
// (and its refresh tokens) created for it.
func ephemeralPhone(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	phone := fmt.Sprintf("+96279%07d", time.Now().UnixNano()%10_000_000)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cctx, `DELETE FROM refresh_tokens WHERE user_id IN (SELECT id FROM users WHERE phone=$1)`, phone)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE phone = $1`, phone)
	})
	return phone
}

// seedUser inserts a users row with the given role/name and returns its id.
func seedUser(t *testing.T, pool *pgxpool.Pool, phone, role, fullName string) int {
	t.Helper()
	var id int
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (phone, role, full_name) VALUES ($1, $2::user_role, NULLIF($3,'')) RETURNING id`,
		phone, role, fullName,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed %s user: %v", role, err)
	}
	return id
}

type userSnapshot struct {
	role      string
	fullName  string
	updatedAt time.Time
}

func snapshotUser(t *testing.T, pool *pgxpool.Pool, phone string) userSnapshot {
	t.Helper()
	var s userSnapshot
	err := pool.QueryRow(context.Background(),
		`SELECT role::text, COALESCE(full_name,''), updated_at FROM users WHERE phone=$1`, phone,
	).Scan(&s.role, &s.fullName, &s.updatedAt)
	if err != nil {
		t.Fatalf("snapshot %s: %v", phone, err)
	}
	return s
}

// ── a/b: privileged phones are refused with the row byte-identical after ───────
func TestBookingSession_PrivilegedPhone_RefusedAndUnchanged(t *testing.T) {
	pool := bookingAuthPool(t)
	repo := NewAuthRepository(pool)

	for _, role := range []string{"owner", "admin", "staff"} {
		t.Run(role, func(t *testing.T) {
			phone := ephemeralPhone(t, pool)
			seedUser(t, pool, phone, role, "Legit "+role)
			before := snapshotUser(t, pool, phone)

			// The attacker knows the phone and tries to establish a booking session,
			// even supplying a name to try to force a write.
			u, err := repo.EnsureBookingUser(context.Background(), phone, "Attacker Name")
			if !errors.Is(err, ErrPrivilegedPhoneBookingRefused) {
				t.Fatalf("err = %v (user=%+v), want ErrPrivilegedPhoneBookingRefused", err, u)
			}
			if u != nil {
				t.Fatalf("a user was returned on refusal: %+v", u)
			}

			after := snapshotUser(t, pool, phone)
			if after.role != before.role {
				t.Fatalf("role mutated: %q → %q", before.role, after.role)
			}
			if after.fullName != before.fullName {
				t.Fatalf("full_name mutated: %q → %q", before.fullName, after.fullName)
			}
			// Byte-identical: NOT even an updated_at bump on refusal.
			if !after.updatedAt.Equal(before.updatedAt) {
				t.Fatalf("updated_at bumped on refusal: %v → %v", before.updatedAt, after.updatedAt)
			}
		})
	}
}

// ── c: a brand-new phone becomes a player ──────────────────────────────────────
func TestBookingSession_NewPhone_CreatesPlayer(t *testing.T) {
	pool := bookingAuthPool(t)
	repo := NewAuthRepository(pool)
	phone := ephemeralPhone(t, pool)

	u, err := repo.EnsureBookingUser(context.Background(), phone, "لاعب جديد")
	if err != nil {
		t.Fatalf("new phone: %v", err)
	}
	if u.Role != models.RolePlayer {
		t.Fatalf("role = %q, want player", u.Role)
	}
	if u.Phone != phone || u.FullName != "لاعب جديد" {
		t.Fatalf("unexpected user: %+v", u)
	}
	// The booking path must never verify the phone.
	var verified bool
	if err := pool.QueryRow(context.Background(),
		`SELECT phone_verified FROM users WHERE phone=$1`, phone).Scan(&verified); err != nil {
		t.Fatalf("read phone_verified: %v", err)
	}
	if verified {
		t.Fatalf("phone_verified = true, want false (no OTP was checked)")
	}
}

// ── d: an existing player keeps working; JIT name capture is not regressed ─────
func TestBookingSession_ExistingPlayer_JITNameNoOverwrite(t *testing.T) {
	pool := bookingAuthPool(t)
	repo := NewAuthRepository(pool)
	phone := ephemeralPhone(t, pool)
	seedUser(t, pool, phone, "player", "") // player with no name yet

	// First booking captures the name.
	u, err := repo.EnsureBookingUser(context.Background(), phone, "علي")
	if err != nil {
		t.Fatalf("existing player: %v", err)
	}
	if u.Role != models.RolePlayer || u.FullName != "علي" {
		t.Fatalf("JIT name capture failed: %+v", u)
	}
	// A later booking must NOT overwrite the captured name.
	u2, err := repo.EnsureBookingUser(context.Background(), phone, "اسم آخر")
	if err != nil {
		t.Fatalf("second booking: %v", err)
	}
	if u2.FullName != "علي" {
		t.Fatalf("name overwritten: %q, want unchanged 'علي'", u2.FullName)
	}
}

// ── e: concurrent fresh-phone calls → exactly one row, all consistent ──────────
func TestBookingSession_ConcurrentFreshPhone_NoDuplicate(t *testing.T) {
	pool := bookingAuthPool(t)
	repo := NewAuthRepository(pool)
	phone := ephemeralPhone(t, pool)

	const n = 8
	var wg sync.WaitGroup
	ids := make([]int, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			u, err := repo.EnsureBookingUser(context.Background(), phone, "سباق")
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = u.ID
			if u.Role != models.RolePlayer {
				errs[i] = fmt.Errorf("role = %q, want player", u.Role)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	// All callers must observe the same single user id.
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("divergent ids: %v", ids)
		}
	}
	// Exactly one row exists for the phone.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE phone=$1`, phone).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want exactly 1", count)
	}
}
