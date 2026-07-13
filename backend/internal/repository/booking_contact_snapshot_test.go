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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
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

// TestContactSnapshot_PlayerNameLocationAndCompositePitch (T2 + {{1}}/{{3}}
// sourcing) proves GetBookingContact resolves the confirmation fields from real
// rows: player name from the users profile, location from the venue neighbourhood,
// and the pitch display name via the shared pitchDisplayNameExpr — bare for a
// single-pitch venue (collapse rule), "venue — label" once the venue is multi-pitch.
func TestContactSnapshot_PlayerNameLocationAndCompositePitch(t *testing.T) {
	e := newContactEnv(t)
	ctx := context.Background()

	// (1) Single-pitch venue → collapse to a bare name; player name + location resolve.
	start1 := e.futureAt(150)
	b1, err := e.repo.CreateBooking(ctx, models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start1, EndTime: start1.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create booking 1: %v", err)
	}
	c1, err := e.repo.GetBookingContact(ctx, b1.ID)
	if err != nil {
		t.Fatalf("GetBookingContact 1: %v", err)
	}
	if c1.PlayerName != "CS Player" {
		t.Errorf("PlayerName = %q, want %q (users.full_name)", c1.PlayerName, "CS Player")
	}
	if c1.Location != "Amman" {
		t.Errorf("Location = %q, want %q (venue neighbourhood)", c1.Location, "Amman")
	}
	if strings.Contains(c1.PitchName, " — ") {
		t.Errorf("single-pitch venue must collapse to a bare name; got composite %q", c1.PitchName)
	}

	// (2) Make the venue MULTI-pitch: move a second pitch (same owner, invariant
	// preserved) under this venue and label the booked pitch. Now the display name
	// must take the composite "venue — label" form.
	var venueID int64
	if err := e.pool.QueryRow(ctx, `SELECT venue_id FROM pitches WHERE id=$1`, e.pitchID).Scan(&venueID); err != nil {
		t.Fatalf("read venue_id: %v", err)
	}
	var venueName string
	if err := e.pool.QueryRow(ctx, `SELECT name FROM venues WHERE id=$1`, venueID).Scan(&venueName); err != nil {
		t.Fatalf("read venue name: %v", err)
	}
	p2, err := (&data.PitchModel{DB: e.pool}).CreatePitch(ctx, data.CreatePitchRequest{
		Name: "CS Pitch Sibling", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(e.ownerID),
	})
	if err != nil {
		t.Fatalf("seed sibling pitch: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = e.pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, p2.ID)
		_, _ = e.pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, p2.ID)
		_, _ = e.pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, p2.ID)
	})
	if _, err := e.pool.Exec(ctx, `UPDATE pitches SET venue_id=$1 WHERE id=$2`, venueID, p2.ID); err != nil {
		t.Fatalf("reassign sibling to venue: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE pitches SET label='Court 1' WHERE id=$1`, e.pitchID); err != nil {
		t.Fatalf("label booked pitch: %v", err)
	}

	start2 := e.futureAt(200)
	b2, err := e.repo.CreateBooking(ctx, models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start2, EndTime: start2.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create booking 2: %v", err)
	}
	c2, err := e.repo.GetBookingContact(ctx, b2.ID)
	if err != nil {
		t.Fatalf("GetBookingContact 2: %v", err)
	}
	if want := venueName + " — Court 1"; c2.PitchName != want {
		t.Errorf("multi-pitch venue must render composite; PitchName = %q, want %q", c2.PitchName, want)
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

// TestContactSnapshot_ListAgreesWithDetail proves the owner/admin listing
// (GetAllBookings) resolves user_phone with the same snapshot-first COALESCE as
// GetBookingContact: frozen snapshot wins over a later profile edit, and a
// pre-030 row (NULL snapshot) falls back to the live users.phone.
func TestContactSnapshot_ListAgreesWithDetail(t *testing.T) {
	e := newContactEnv(t)
	ctx := context.Background()
	start := e.futureAt(160)

	b, err := e.repo.CreateBooking(ctx, models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	listPhone := func(label string) string {
		t.Helper()
		all, err := e.repo.GetAllBookings(ctx, auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, nil, BookingFilter{})
		if err != nil {
			t.Fatalf("%s: GetAllBookings: %v", label, err)
		}
		for _, ab := range all {
			if int64(ab.ID) == b.ID {
				return ab.UserPhone
			}
		}
		t.Fatalf("%s: booking %d not in owner listing", label, b.ID)
		return ""
	}

	// Snapshot wins: edit the profile phone after booking, list must keep the
	// frozen value. The new number takes its own UniqueSuffix — mutating a digit
	// of the fixture phone can collide with a sibling fixture in a full-suite
	// run (the process-wide counter hands out adjacent suffixes → 23505).
	newPhone := fmt.Sprintf("+96293%06d", testutil.UniqueSuffix()%1_000_000)
	if _, err := e.pool.Exec(ctx, `UPDATE users SET phone = $1 WHERE id = $2`, newPhone, e.playerID); err != nil {
		t.Fatalf("update player phone: %v", err)
	}
	if got := listPhone("post-edit"); got != e.phone {
		t.Errorf("list user_phone = %q, want frozen snapshot %q (not the edited %q)", got, e.phone, newPhone)
	}

	// Pre-030 fallback: clear the snapshot, list must fall back to the live users.phone.
	if _, err := e.pool.Exec(ctx,
		`UPDATE bookings SET contact_name = NULL, contact_phone = NULL WHERE id = $1`, b.ID); err != nil {
		t.Fatalf("clear snapshot: %v", err)
	}
	if got := listPhone("null-snapshot"); got != newPhone {
		t.Errorf("fallback list user_phone = %q, want users.phone %q", got, newPhone)
	}
}
