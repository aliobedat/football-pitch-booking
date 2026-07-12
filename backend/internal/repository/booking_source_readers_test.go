package repository

// Integration tests proving the Phase 2 reader changes: a seeded BLOCK row is
// excluded from (or relabeled in) every player-semantics path, while remaining
// occupied for availability. Live-DB, SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run SourceReaders

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type readersEnv struct {
	pool        *pgxpool.Pool
	repo        BookingRepository
	reviews     ReviewRepository
	reminder    ReminderRepository
	ownerID     int64
	playerID    int64
	playerPhone string
	pitchID     int64
}

func newReadersEnv(t *testing.T) *readersEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping source readers integration test")
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
	testutil.AssertSchemaBaseline(t, pool)

	suffix := testutil.UniqueSuffix() % 1_000_000
	mk := func(name, prefix, role string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, fmt.Sprintf("+962%s%06d", prefix, suffix), role).Scan(&id); err != nil {
			pool.Close()
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	ownerID := mk("RD Owner", "88", "owner")
	playerID := mk("RD Player", "89", "player")
	playerPhone := fmt.Sprintf("+96289%06d", suffix)

	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "RD Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	e := &readersEnv{
		pool: pool, repo: NewBookingRepository(pool),
		reviews: NewReviewRepository(pool), reminder: NewReminderRepository(pool),
		ownerID: ownerID, playerID: playerID, playerPhone: playerPhone, pitchID: int64(p.ID),
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM reviews WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM venues WHERE owner_id = $1`, ownerID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerID, playerID})
		pool.Close()
	})
	return e
}

// seed inserts a bookings row directly. player nil → NULL player_id (block).
func (e *readersEnv) seed(source string, player *int64, start time.Time, dur time.Duration, status string) int64 {
	var id int64
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), 30, $5::booking_status, $6)
		RETURNING id
	`, e.pitchID, player, start, start.Add(dur), status, source).Scan(&id); err != nil {
		panic(fmt.Sprintf("seed booking (%s): %v", source, err))
	}
	return id
}

// ── #2 GetBookedSlots: a block is OCCUPIED (counts as unavailable) ───────────

func TestSourceReaders_GetBookedSlotsIncludesBlock(t *testing.T) {
	e := newReadersEnv(t)
	// Fixed Amman-zone instant: GetBookedSlots resolves the civil day from the
	// date's OWN zone, so a now()-derived UTC instant lands one day off when
	// run between 00:00–03:00 Amman. An Amman-zone literal is unambiguous.
	day := time.Date(2032, 3, 10, 0, 0, 0, 0, timeutil.Amman())
	blockStart := time.Date(2032, 3, 10, 18, 0, 0, 0, timeutil.Amman())
	e.seed("block", nil, blockStart, time.Hour, "confirmed")

	slots, err := e.repo.GetBookedSlots(context.Background(), int(e.pitchID), day)
	if err != nil {
		t.Fatalf("GetBookedSlots: %v", err)
	}
	found := false
	for _, s := range slots {
		if s.StartTime.Equal(blockStart) {
			found = true
		}
	}
	if !found {
		t.Fatalf("block slot at %s not present in booked slots — blocks must count as occupied", blockStart)
	}
}

// ── #3 GetAllBookings: block is relabeled (source=block, no player, no phone) ─

func TestSourceReaders_GetAllBookingsRelabelsBlock(t *testing.T) {
	e := newReadersEnv(t)
	blockID := e.seed("block", nil, time.Now().UTC().Add(72*time.Hour), time.Hour, "confirmed")

	all, err := e.repo.GetAllBookings(context.Background(), auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, nil, BookingFilter{})
	if err != nil {
		t.Fatalf("GetAllBookings: %v", err)
	}
	var blk *models.AdminBooking
	for i := range all {
		if all[i].ID == blockID {
			blk = &all[i]
		}
	}
	if blk == nil {
		t.Fatalf("block %d not returned by GetAllBookings (owner should see their pitch's blocks)", blockID)
	}
	if blk.Source != models.SourceBlock {
		t.Errorf("source = %q, want block", blk.Source)
	}
	if blk.PlayerID != nil {
		t.Errorf("player_id = %v, want nil for a block", *blk.PlayerID)
	}
	if blk.UserPhone != "" || blk.UserName != "" {
		t.Errorf("user fields = (%q,%q), want empty for a block", blk.UserName, blk.UserPhone)
	}
}

// ── #4 ClaimDueReminders: a block in the next 24h is NOT reminded ────────────

func TestSourceReaders_ReminderSkipsBlock(t *testing.T) {
	e := newReadersEnv(t)
	now := time.Now().UTC()
	// Player booking and block both start within the 24h window, at different hours.
	e.seed("player", &e.playerID, now.Add(2*time.Hour), time.Hour, "confirmed")
	blockID := e.seed("block", nil, now.Add(5*time.Hour), time.Hour, "confirmed")

	// ClaimDueReminders is GLOBAL (it scans every due booking in the window),
	// so a shared scratch DB can contain due strays from other fixtures or
	// panic-killed runs. Assert only on THIS env's recipient phone — the global
	// claim count is not ours to pin down.
	var mine int
	_, err := e.reminder.ClaimDueReminders(context.Background(), now, 24*time.Hour, 100,
		func(d DueReminder) (ReminderJob, error) {
			if d.Phone == e.playerPhone {
				mine++
			}
			return ReminderJob{Recipient: d.Phone, Kind: "booking_reminder", Envelope: []byte("{}")}, nil
		})
	if err != nil {
		t.Fatalf("ClaimDueReminders: %v", err)
	}
	if mine != 1 {
		t.Fatalf("claimed %d reminders for this env's player, want 1 (player only — the block must be skipped)", mine)
	}
	// The block row must remain un-reminded.
	var reminded bool
	if err := e.pool.QueryRow(context.Background(),
		`SELECT reminder_sent FROM bookings WHERE id = $1`, blockID).Scan(&reminded); err != nil {
		t.Fatalf("read block reminder_sent: %v", err)
	}
	if reminded {
		t.Fatalf("block %d was marked reminder_sent — a block must never be reminded", blockID)
	}
}

// ── #5 CheckEligibility: a block never confers review eligibility ────────────

func TestSourceReaders_EligibilityIgnoresBlock(t *testing.T) {
	e := newReadersEnv(t)
	// A past, ended block on the pitch — the only booking on it.
	e.seed("block", nil, time.Now().UTC().Add(-3*time.Hour), time.Hour, "confirmed")

	elig, err := e.reviews.CheckEligibility(context.Background(), e.playerID, e.pitchID)
	if err != nil {
		t.Fatalf("CheckEligibility: %v", err)
	}
	if elig.Eligible {
		t.Fatalf("player is eligible off a block-only pitch — a block must not qualify for review")
	}

	// Sanity: a real past player booking DOES confer eligibility (query still works).
	e.seed("player", &e.playerID, time.Now().UTC().Add(-2*time.Hour), time.Hour, "confirmed")
	elig2, err := e.reviews.CheckEligibility(context.Background(), e.playerID, e.pitchID)
	if err != nil {
		t.Fatalf("CheckEligibility (player): %v", err)
	}
	if !elig2.Eligible {
		t.Fatalf("player with a past booking should be eligible")
	}
}

// ── #6 CreateReview composite FK: a review cannot reference a block ──────────

func TestSourceReaders_ReviewCannotReferenceBlock(t *testing.T) {
	e := newReadersEnv(t)
	blockID := e.seed("block", nil, time.Now().UTC().Add(-3*time.Hour), time.Hour, "confirmed")

	comment := "should never persist"
	_, err := e.reviews.CreateReview(context.Background(), models.CreateReviewRequest{
		PitchID:             e.pitchID,
		PlayerID:            e.playerID,
		QualifyingBookingID: blockID,
		Rating:              5,
		Comment:             &comment,
	})
	if !errors.Is(err, ErrReviewBookingInvalid) {
		t.Fatalf("review referencing a block: err = %v, want ErrReviewBookingInvalid (composite FK rejects it)", err)
	}
}

// ── WO-COMPOSITE-B2C: booking reads carry the composite display name ─────────

func TestSourceReaders_CompositeBookingNames(t *testing.T) {
	e := newReadersEnv(t)
	ctx := context.Background()

	// 1:1 venue (collapse): the player's booking shows the bare name — the
	// venue is named after its lone pitch, so the collapse rule yields it.
	first := e.seed("player", &e.playerID, time.Now().UTC().Add(48*time.Hour), time.Hour, "confirmed")
	nameOfUser := func(bookingID int64) string {
		t.Helper()
		list, err := e.repo.GetUserBookings(ctx, e.playerID)
		if err != nil {
			t.Fatalf("GetUserBookings: %v", err)
		}
		for _, b := range list {
			if b.ID == bookingID {
				return b.PitchName
			}
		}
		t.Fatalf("booking %d not in GetUserBookings", bookingID)
		return ""
	}
	nameOfAdmin := func(bookingID int64) string {
		t.Helper()
		all, err := e.repo.GetAllBookings(ctx, auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, nil, BookingFilter{})
		if err != nil {
			t.Fatalf("GetAllBookings: %v", err)
		}
		for _, b := range all {
			if b.ID == bookingID {
				return b.PitchName
			}
		}
		t.Fatalf("booking %d not in GetAllBookings", bookingID)
		return ""
	}
	if got := nameOfUser(first); got != "RD Pitch" {
		t.Errorf("1:1 GetUserBookings name = %q, want bare %q (collapse rule)", got, "RD Pitch")
	}
	if got := nameOfAdmin(first); got != "RD Pitch" {
		t.Errorf("1:1 GetAllBookings name = %q, want bare %q (collapse rule)", got, "RD Pitch")
	}

	// Grow the venue to two pitches: display names become composite at read
	// time — for the NEW pitch's booking AND retroactively for the first
	// (the first pitch gets the auto «ملعب ١» label).
	var venueID int64
	if err := e.pool.QueryRow(ctx, `SELECT venue_id FROM pitches WHERE id = $1`, e.pitchID).Scan(&venueID); err != nil {
		t.Fatalf("resolve venue: %v", err)
	}
	model := &data.PitchModel{DB: e.pool}
	sibling, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "ملعب ٢", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(e.ownerID),
		VenueID: &venueID, Label: "ملعب ٢",
	})
	if err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM bookings WHERE pitch_id = $1`, sibling.ID)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM pitches WHERE id = $1`, sibling.ID)
	})

	second := int64(0)
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), 30, 'confirmed', 'player')
		RETURNING id
	`, sibling.ID, e.playerID,
		time.Now().UTC().Add(72*time.Hour), time.Now().UTC().Add(73*time.Hour)).Scan(&second); err != nil {
		t.Fatalf("seed sibling booking: %v", err)
	}

	if got := nameOfUser(second); got != "RD Pitch — ملعب ٢" {
		t.Errorf("multi GetUserBookings name = %q, want %q", got, "RD Pitch — ملعب ٢")
	}
	if got := nameOfAdmin(second); got != "RD Pitch — ملعب ٢" {
		t.Errorf("multi GetAllBookings name = %q, want %q", got, "RD Pitch — ملعب ٢")
	}
	// The FIRST booking's display follows at read time (auto «ملعب ١»).
	if got := nameOfUser(first); got != "RD Pitch — ملعب ١" {
		t.Errorf("retro GetUserBookings name = %q, want %q", got, "RD Pitch — ملعب ١")
	}
}
