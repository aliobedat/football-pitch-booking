package repository

// Integration tests for the booking contact snapshot (delta B / migration 030)
// against a live database. They prove:
//   - a fresh player booking freezes the player's phone onto the row, and a LATER
//     profile phone edit does NOT re-point the booking's contact (immutability);
//   - a PRE-030 row (NULL contact_phone) still resolves owner-facing contact via
//     the COALESCE fallback to the live users.phone in GetBookingContact.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set (requires migration 030
// applied), matching the other repository integration suites.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run ContactSnapshot

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type contactEnv struct {
	pool     *pgxpool.Pool
	repo     BookingRepository
	ownerID  int64
	playerID int64
	pitchID  int64
	phone    string
}

func newContactEnv(t *testing.T) *contactEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping contact snapshot integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}

	suffix := testutil.UniqueSuffix() % 1_000_000
	mk := func(name, prefix, role string) (int64, string) {
		ph := fmt.Sprintf("+962%s%06d", prefix, suffix)
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, ph, role).Scan(&id); err != nil {
			pool.Close()
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id, ph
	}
	ownerID, _ := mk("CS Owner", "90", "owner")
	playerID, playerPhone := mk("CS Player", "79", "player")

	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "CS Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	e := &contactEnv{
		pool: pool, repo: NewBookingRepository(pool),
		ownerID: ownerID, playerID: playerID, pitchID: int64(p.ID), phone: playerPhone,
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerID, playerID})
		pool.Close()
	})
	return e
}

func (e *contactEnv) futureAt(hoursFromNow int) time.Time {
	return time.Now().In(timeutil.Amman()).Add(time.Duration(hoursFromNow) * time.Hour).Truncate(time.Minute)
}

// TestContactSnapshot_FrozenAtCreate_SurvivesProfileEdit proves delta B: the
// booking's contact phone is the value at create time, even after the user later
// changes their profile phone.
func TestContactSnapshot_FrozenAtCreate_SurvivesProfileEdit(t *testing.T) {
	e := newContactEnv(t)
	ctx := context.Background()
	start := e.futureAt(120)

	b, err := e.repo.CreateBooking(ctx, models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// The player edits their profile phone AFTER booking.
	newPhone := e.phone[:len(e.phone)-1] + "9" // mutate last digit
	if _, err := e.pool.Exec(ctx, `UPDATE users SET phone = $1 WHERE id = $2`, newPhone, e.playerID); err != nil {
		t.Fatalf("update player phone: %v", err)
	}

	contact, err := e.repo.GetBookingContact(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetBookingContact: %v", err)
	}
	if contact.Phone != e.phone {
		t.Errorf("contact phone = %q, want frozen snapshot %q (not the edited %q)", contact.Phone, e.phone, newPhone)
	}
}

// TestContactSnapshot_PreMigrationRow_FallsBackToUsersPhone proves the fallback:
// a row with NULL contact_phone (the pre-030 shape) still resolves the recipient
// via COALESCE(b.contact_phone, u.phone) in GetBookingContact.
func TestContactSnapshot_PreMigrationRow_FallsBackToUsersPhone(t *testing.T) {
	e := newContactEnv(t)
	ctx := context.Background()
	start := e.futureAt(140)

	b, err := e.repo.CreateBooking(ctx, models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Simulate a pre-030 row: clear the snapshot so only the live users join can
	// resolve the contact.
	if _, err := e.pool.Exec(ctx,
		`UPDATE bookings SET contact_name = NULL, contact_phone = NULL WHERE id = $1`, b.ID); err != nil {
		t.Fatalf("clear snapshot: %v", err)
	}

	contact, err := e.repo.GetBookingContact(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetBookingContact: %v", err)
	}
	if contact.Phone != e.phone {
		t.Errorf("fallback contact phone = %q, want users.phone %q", contact.Phone, e.phone)
	}
}
